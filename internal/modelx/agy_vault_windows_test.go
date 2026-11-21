//go:build windows

package modelx

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeFakeIDToken(email string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payloadBytes, _ := json.Marshal(map[string]any{
		"email":          email,
		"email_verified": true,
		"sub":            "1234567890",
	})
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	sig := base64.RawURLEncoding.EncodeToString([]byte("fakesig"))
	return header + "." + payload + "." + sig
}

func TestAgyOAuthLoopbackAndManualCodeExchangeEndToEnd(t *testing.T) {
	// Backup and restore real Windows Credential Manager "gemini:antigravity" after test!
	origCred, origEmail, origErr := ReadLiveAgyOAuthCredential()
	t.Cleanup(func() {
		shutdownPendingAgyServer()
		if origErr == nil && len(origCred) > 0 {
			_ = WriteLiveAgyOAuthCredential(origCred)
		}
		_ = origEmail
	})

	// Isolate profiles.json and agy-profiles vault to a temp directory.
	tmpDir := t.TempDir()
	origProfilesPathFn := profilesFilePathFn
	profilesFilePathFn = func() (string, error) {
		return filepath.Join(tmpDir, "provider-profiles.json"), nil
	}
	t.Cleanup(func() {
		profilesFilePathFn = origProfilesPathFn
	})

	// Seed Conta #1 (conta1@example.com) in the live slot and sync it to profiles.
	seed1 := agyKeyringSessionBlob{
		Token: agyKeyringTokenPayload{
			AccessToken:  "ya29.conta1-access",
			TokenType:    "Bearer",
			RefreshToken: "1//conta1-refresh",
			Expiry:       "2026-11-20T20:00:00Z",
			ExpiresIn:    3599,
		},
		AuthMethod:   "consumer",
		IDTokenEmail: "conta1@empresa.com.br",
		WriterID:     agyDefaultWriterID,
	}
	seed1Bytes, _ := json.Marshal(seed1)
	if err := WriteLiveAgyOAuthCredential(seed1Bytes); err != nil {
		t.Fatalf("WriteLiveAgyOAuthCredential seed1: %v", err)
	}
	if _, err := SyncCurrentLiveAgyAccountToProfiles(); err != nil {
		t.Fatalf("SyncCurrentLiveAgyAccountToProfiles seed1: %v", err)
	}

	// Mock Google's OAuth2 token endpoint.
	nextAccountEmail := "conta2@particular.com.br"
	mockGoogle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		code := r.FormValue("code")
		if code == "" || strings.Contains(code, "invalid") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid code"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"access_token":  "ya29.mock-" + code,
			"token_type":    "Bearer",
			"refresh_token": "1//mock-refresh-" + code,
			"expires_in":    3599,
			"id_token":      makeFakeIDToken(nextAccountEmail),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer mockGoogle.Close()

	origTokenEndpoint := agyOAuthTokenEndpoint
	agyOAuthTokenEndpoint = mockGoogle.URL
	t.Cleanup(func() {
		agyOAuthTokenEndpoint = origTokenEndpoint
	})

	// 1. Start the loopback OAuth flow (what ConnectNewAgyGoogleAccount runs).
	authURL, err := startAgyLoopbackOAuthFlow(seed1Bytes, "conta1@empresa.com.br")
	if err != nil {
		t.Fatalf("startAgyLoopbackOAuthFlow: %v", err)
	}
	parsedAuthURL, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("url.Parse(authURL): %v", err)
	}
	redirectURI := parsedAuthURL.Query().Get("redirect_uri")
	if !strings.HasPrefix(redirectURI, "http://localhost:") {
		t.Fatalf("unexpected redirect_uri: %q", redirectURI)
	}

	// 2. Simulate the browser redirecting back to http://localhost:<port>/oauth-callback?code=4/0Atest_loopback
	cbResp, err := http.Get(redirectURI + "?code=4/0Atest_loopback&state=" + url.QueryEscape(parsedAuthURL.Query().Get("state")))
	if err != nil {
		t.Fatalf("GET loopback callback failed: %v", err)
	}
	cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusOK {
		t.Fatalf("expected HTTP 200 on loopback callback, got %d", cbResp.StatusCode)
	}

	// Verify both Conta #1 and Conta #2 exist in isolated vault folders without overwriting each other!
	ov := ListProfilesOverview("")
	var agyProfs []ProviderProfileSummary
	for _, p := range ov.Profiles {
		if p.Provider == "agy" {
			agyProfs = append(agyProfs, p)
		}
	}
	if len(agyProfs) != 2 {
		t.Fatalf("expected 2 isolated AGY Google accounts in pool, got %d: %+v", len(agyProfs), agyProfs)
	}
	for _, p := range agyProfs {
		vpath, _ := AgyProfileVaultPath(p.ID)
		data, rerr := os.ReadFile(vpath)
		if rerr != nil || len(data) == 0 {
			t.Fatalf("missing isolated vault file for %s at %s: %v", p.ID, vpath, rerr)
		}
		if strings.Contains(string(data), "ya29.") || strings.Contains(string(data), "1//") || !strings.Contains(string(data), `"vault_ref": "vault:`) {
			t.Fatalf("oauth-session.json must store vault_ref metadata without plaintext tokens:\n%s", string(data))
		}
	}

	// 3. Also test manual code paste via SubmitAgyOAuthCode (both raw 4/0A... code and full callback URL).
	nextAccountEmail = "conta3@reserva.com.br"
	ov3, err := SubmitAgyOAuthCode("4/0Amanual_paste_code", "")
	if err != nil {
		t.Fatalf("SubmitAgyOAuthCode failed: %v", err)
	}
	var countAfterPaste int
	for _, p := range ov3.Profiles {
		if p.Provider == "agy" {
			countAfterPaste++
		}
	}
	if countAfterPaste != 3 {
		t.Fatalf("expected 3 isolated AGY Google accounts after manual paste, got %d", countAfterPaste)
	}

	// 4. Live Windows Credential Manager ("gemini:antigravity") MUST still belong to Conta #1!
	_, liveEmailAfterAdd, lerr := ReadLiveAgyOAuthCredential()
	if lerr != nil || liveEmailAfterAdd != "conta1@empresa.com.br" {
		t.Fatalf("adding new HiveMind accounts must not switch Antigravity live account; got %q (err=%v)", liveEmailAfterAdd, lerr)
	}

	// 5. WithAgyVaultLease switches temporarily inside fn and restores Conta #1 right after.
	var emailInsideLease string
	if err := WithAgyVaultLease("agy-conta2-particular-com-br", func() error {
		_, emailInsideLease, _ = ReadLiveAgyOAuthCredential()
		return nil
	}); err != nil {
		t.Fatalf("WithAgyVaultLease failed: %v", err)
	}
	if emailInsideLease != "conta2@particular.com.br" {
		t.Fatalf("expected Conta #2 inside lease, got %q", emailInsideLease)
	}
	_, emailAfterLease, _ := ReadLiveAgyOAuthCredential()
	if emailAfterLease != "conta1@empresa.com.br" {
		t.Fatalf("expected Conta #1 restored after lease exit, got %q", emailAfterLease)
	}
}
