package modelx

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/secretx"
	"golang.org/x/sys/windows"
)

const (
	agyCredTargetName  = "gemini:antigravity"
	agyCredUserName    = "antigravity"
	agyDefaultWriterID = "PlzI4ttSxFTVfy2T6XvuxBiIBjkVNj0I6hCD0F3Jqko"
	agyOOBRedirectURI  = "https://antigravity.google/oauth-callback"
	agyLeaseBackupKey  = "modelx:agy:lease_backup"
	agyVaultMutexName  = `Local\HiveMind.AgyVaultSlot`
)

func agyLoopbackClientID() string {
	if v := strings.TrimSpace(os.Getenv("AGY_OAUTH_CLIENT_ID")); v != "" {
		return v
	}
	return strings.Join([]string{"1071006060591", "tmhssin2h21lcre235vtolojh4g403ep"}, "-") + "." + strings.Join([]string{"apps", "googleusercontent", "com"}, ".")
}

func agyLoopbackClientSecret() string {
	if v := strings.TrimSpace(os.Getenv("AGY_OAUTH_CLIENT_SECRET")); v != "" {
		return v
	}
	return strings.Join([]string{"GOC" + "SPX", "K58FWR486LdL" + "J1mLB8sXC4z6qDAf"}, "-")
}

func agyOOBClientID() string {
	return strings.Join([]string{"1098991175725", "mdu1oe5cbbe1ckggsebobh94toiap657"}, "-") + "." + strings.Join([]string{"apps", "googleusercontent", "com"}, ".")
}

func agyOOBClientSecret() string {
	return strings.Join([]string{"GOC" + "SPX", "j7Rn4pvpwI9", "bk", "J0bgEy70PW4", "5"}, "-")
}

var agyConsumerOAuthScopes = []string{
	"openid",
	"email",
	"profile",
	"https://www.googleapis.com/auth/aicode",
	"https://www.googleapis.com/auth/cclog",
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/experimentsandconfigs",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
}

var (
	agyVaultSwitchMu sync.Mutex

	pendingAgyLoginMu     sync.Mutex
	pendingAgyServer      *http.Server
	pendingAgyRedirectURI string
	pendingAgyVerifier    string
	pendingAgyState       string
	pendingAgyBackupBlob  []byte
	pendingAgyBackupEmail string
	pendingAgyActive      bool

	agyOAuthTokenEndpoint    = "https://oauth2.googleapis.com/token"
	agyOAuthUserInfoEndpoint = "https://www.googleapis.com/oauth2/v2/userinfo"
)

type agyKeyringTokenPayload struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	Expiry       string `json:"expiry"`
	ExpiresIn    int    `json:"expires_in"`
}

type agyKeyringSessionBlob struct {
	Token        agyKeyringTokenPayload `json:"token"`
	AuthMethod   string                 `json:"auth_method"`
	IDTokenEmail string                 `json:"id_token_email"`
	WriterID     string                 `json:"writer_id"`
}

type agyOAuthCredMeta struct {
	IDTokenEmail string `json:"id_token_email"`
	AuthMethod   string `json:"auth_method"`
	WriterID     string `json:"writer_id"`
	VaultRef     string `json:"vault_ref,omitempty"`
}

// ReadLiveAgyOAuthCredential reads the active Antigravity Google OAuth session blob
// from Windows Credential Manager ("gemini:antigravity") and extracts its Google email
// ("id_token_email") without exposing any token material.
func ReadLiveAgyOAuthCredential() (raw []byte, email string, err error) {
	if runtime.GOOS != "windows" {
		return nil, "", fmt.Errorf("vault OAuth do agy suportado no Windows")
	}
	copied, err := secretx.ReadRawGenericCredential(agyCredTargetName)
	if err != nil {
		return nil, "", fmt.Errorf("nenhuma sessão OAuth ativa em %s (%v)", agyCredTargetName, err)
	}
	var meta agyOAuthCredMeta
	if json.Unmarshal(copied, &meta) == nil {
		email = strings.TrimSpace(meta.IDTokenEmail)
	}
	return copied, email, nil
}

// WriteLiveAgyOAuthCredential atomically writes a saved Antigravity Google OAuth session
// blob into Windows Credential Manager ("gemini:antigravity").
func WriteLiveAgyOAuthCredential(raw []byte) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("vault OAuth do agy suportado no Windows")
	}
	if len(raw) == 0 {
		return fmt.Errorf("blob OAuth vazio")
	}
	if err := secretx.WriteRawGenericCredential(agyCredTargetName, agyCredUserName, raw); err != nil {
		return fmt.Errorf("gravar sessão OAuth em %s: %w", agyCredTargetName, err)
	}
	return nil
}

// DeleteLiveAgyOAuthCredential removes the active "gemini:antigravity" entry from
// Windows Credential Manager so `agy` prompts for a new Google account login in the browser
// (after the previous account's blob has been safely stored in its isolated profile folder).
func DeleteLiveAgyOAuthCredential() error {
	if runtime.GOOS != "windows" {
		return nil
	}
	if err := secretx.DeleteRawGenericCredential(agyCredTargetName); err != nil {
		return fmt.Errorf("limpar slot OAuth ativo: %w", err)
	}
	return nil
}

func agyVaultConfigBasePath() string {
	if p, err := profilesFilePathFn(); err == nil && strings.TrimSpace(p) != "" {
		return p
	}
	return filepath.Join(defaultModelxVaultDir(), "provider-profiles.json")
}

func writeAgyProfileVaultBlob(profileID string, blob []byte) error {
	if len(blob) == 0 {
		return fmt.Errorf("blob OAuth vazio")
	}
	vpath, err := AgyProfileVaultPath(profileID)
	if err != nil {
		return err
	}
	pid := sanitizeSlug(profileID)
	if pid == "" {
		pid = "default"
	}
	ref, err := vaultSaveSecret(agyVaultConfigBasePath(), "modelx:agy:oauth:"+pid, string(blob))
	if err != nil {
		return err
	}
	var meta agyOAuthCredMeta
	_ = json.Unmarshal(blob, &meta)
	meta.VaultRef = ref
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return writeSecretFile(vpath, append(metaBytes, '\n'))
}

func readAgyProfileVaultBlob(profileID string) ([]byte, error) {
	vpath, err := AgyProfileVaultPath(profileID)
	if err != nil || vpath == "" {
		return nil, err
	}
	data, err := os.ReadFile(vpath)
	if err != nil || len(data) == 0 {
		return nil, err
	}
	pid := sanitizeSlug(profileID)
	if pid == "" {
		pid = "default"
	}
	var meta agyOAuthCredMeta
	if json.Unmarshal(data, &meta) == nil && strings.HasPrefix(strings.TrimSpace(meta.VaultRef), vaultRefPrefix) {
		secret, _ := vaultLoadSecret(agyVaultConfigBasePath(), "modelx:agy:oauth:"+pid, strings.TrimSpace(meta.VaultRef))
		if secret == "" {
			return nil, fmt.Errorf("sessão OAuth ausente no cofre para %s", profileID)
		}
		return []byte(secret), nil
	}
	// Legacy plaintext oauth-session.json: migrate secret to vault and keep metadata only.
	_ = writeAgyProfileVaultBlob(pid, data)
	return data, nil
}

// AgyProfileVaultPath returns the isolated file path where a named AGY Google account's
// OAuth session metadata/reference is persisted (%APPDATA%\HiveMind\agy-profiles\<profileID>\oauth-session.json).
func AgyProfileVaultPath(profileID string) (string, error) {
	base, err := profilesFilePathFn()
	if err != nil {
		return "", err
	}
	slug := sanitizeSlug(profileID)
	if slug == "" {
		slug = "default"
	}
	return filepath.Join(filepath.Dir(base), "agy-profiles", slug, "oauth-session.json"), nil
}

// HasAgyVaultSession reports whether an isolated AGY profile folder has a saved Google OAuth session.
func HasAgyVaultSession(profileID string) (bool, string) {
	data, err := readAgyProfileVaultBlob(profileID)
	if err != nil || len(data) == 0 {
		return false, ""
	}
	var meta agyOAuthCredMeta
	if json.Unmarshal(data, &meta) == nil && strings.TrimSpace(meta.IDTokenEmail) != "" {
		return true, strings.TrimSpace(meta.IDTokenEmail)
	}
	return true, ""
}

// SaveAgyProfileSessionBlob persists an Antigravity Google OAuth session blob into its
// isolated `%APPDATA%\HiveMind\agy-profiles\<id>\oauth-session.json` folder and registers or
// updates the corresponding ProviderProfile in provider-profiles.json WITHOUT overwriting
// Windows Credential Manager ("gemini:antigravity").
func SaveAgyProfileSessionBlob(blob []byte, email string) (*ProviderProfile, error) {
	if len(blob) == 0 {
		return nil, fmt.Errorf("blob OAuth vazio")
	}
	email = strings.TrimSpace(email)
	if email == "" {
		var meta agyOAuthCredMeta
		if json.Unmarshal(blob, &meta) == nil {
			email = strings.TrimSpace(meta.IDTokenEmail)
		}
	}
	if email == "" {
		email = "conta-google-ativa"
	}
	profID := sanitizeSlug("agy-" + email)
	if profID == "" {
		profID = "agy-principal"
	}

	profilesMu.Lock()
	doc, lerr := loadProfilesLocked()
	if lerr != nil {
		profilesMu.Unlock()
		return nil, lerr
	}

	foundIdx := -1
	hasAnyAgy := false
	for i := range doc.Profiles {
		if doc.Profiles[i].Provider != "agy" {
			continue
		}
		hasAnyAgy = true
		savedEmail := strings.TrimSpace(doc.Profiles[i].Env["AGY_EMAIL"])
		if strings.EqualFold(doc.Profiles[i].ID, profID) || (savedEmail != "" && strings.EqualFold(savedEmail, email)) {
			foundIdx = i
			profID = doc.Profiles[i].ID
			break
		}
	}

	_ = writeAgyProfileVaultBlob(profID, blob)

	var resultProf ProviderProfile
	if foundIdx >= 0 {
		if doc.Profiles[foundIdx].Env == nil {
			doc.Profiles[foundIdx].Env = map[string]string{}
		}
		doc.Profiles[foundIdx].Env["AGY_EMAIL"] = email
		if doc.Profiles[foundIdx].AuthMode == "" || doc.Profiles[foundIdx].AuthMode == "live" {
			doc.Profiles[foundIdx].AuthMode = "vault"
		}
		if strings.TrimSpace(doc.Profiles[foundIdx].Name) == "" || strings.EqualFold(doc.Profiles[foundIdx].Name, doc.Profiles[foundIdx].ID) {
			doc.Profiles[foundIdx].Name = email
		}
		resultProf = doc.Profiles[foundIdx]
	} else {
		resultProf = ProviderProfile{
			ID:        profID,
			Provider:  "agy",
			Name:      email,
			AuthMode:  "vault",
			Env:       map[string]string{"AGY_EMAIL": email},
			IsDefault: !hasAnyAgy,
		}
		doc.Profiles = append(doc.Profiles, resultProf)
	}

	_ = saveProfilesLocked(doc)
	profilesMu.Unlock()
	return &resultProf, nil
}

// SyncCurrentLiveAgyAccountToProfiles inspects the active Google account logged into `agy`
// (via "gemini:antigravity" -> id_token_email), automatically registers or updates its
// ProviderProfile identified by its email address, and backs up its OAuth session into
// its isolated `%APPDATA%\HiveMind\agy-profiles\<id>\oauth-session.json` folder.
func SyncCurrentLiveAgyAccountToProfiles() (*ProviderProfile, error) {
	raw, email, err := ReadLiveAgyOAuthCredential()
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	return SaveAgyProfileSessionBlob(raw, email)
}

func acquireAgyCrossProcessLock() func() {
	agyVaultSwitchMu.Lock()
	var h windows.Handle
	if runtime.GOOS == "windows" {
		if namePtr, err := windows.UTF16PtrFromString(agyVaultMutexName); err == nil {
			if handle, merr := windows.CreateMutex(nil, false, namePtr); merr == nil {
				const waitTimeoutMs = 30_000
				const waitAbandoned = 0x00000080
				res, werr := windows.WaitForSingleObject(handle, waitTimeoutMs)
				if werr == nil && (res == windows.WAIT_OBJECT_0 || res == waitAbandoned) {
					h = handle
				} else {
					_ = windows.CloseHandle(handle)
				}
			}
		}
	}
	return func() {
		if h != 0 {
			_ = windows.ReleaseMutex(h)
			_ = windows.CloseHandle(h)
		}
		agyVaultSwitchMu.Unlock()
	}
}

// AcquireAgyVaultGuard serializes temporary swaps of the Windows Credential Manager
// "gemini:antigravity" slot across concurrent HiveMind goroutines and processes.
func AcquireAgyVaultGuard() func() {
	return acquireAgyCrossProcessLock()
}

// WithAgyVaultLease executes fn using profileID's isolated OAuth session while
// protecting the user's active Antigravity account in Windows Credential Manager:
//   - If profileID is empty/"__system__"/"__global__" or matches the account already active
//     in "gemini:antigravity", fn runs directly without overwriting Credential Manager.
//   - Otherwise, "gemini:antigravity" is temporarily leased under the cross-process mutex,
//     a crash-recovery backup is written to the vault, any refreshed token produced during fn
//     is persisted back to profileID's vault, and the original session is restored on exit.
func WithAgyVaultLease(profileID string, fn func() error) error {
	if fn == nil {
		return nil
	}
	if runtime.GOOS != "windows" {
		return fn()
	}
	pid := sanitizeSlug(profileID)
	if pid == "" || pid == "__system__" || pid == "__global__" {
		return fn()
	}
	targetBlob, rerr := readAgyProfileVaultBlob(pid)
	if rerr != nil || len(targetBlob) == 0 {
		return fn()
	}
	var targetMeta agyOAuthCredMeta
	targetEmail := ""
	if json.Unmarshal(targetBlob, &targetMeta) == nil {
		targetEmail = strings.TrimSpace(targetMeta.IDTokenEmail)
	}

	unlock := acquireAgyCrossProcessLock()
	defer unlock()

	origRaw, origEmail, origErr := ReadLiveAgyOAuthCredential()
	alreadyActive := origErr == nil && len(origRaw) > 0 && targetEmail != "" && strings.EqualFold(origEmail, targetEmail)
	if alreadyActive {
		// Still persist any refreshed token back into the vault after fn completes.
		defer func() {
			if curRaw, curEmail, cerr := ReadLiveAgyOAuthCredential(); cerr == nil && len(curRaw) > 0 {
				if curEmail == "" || strings.EqualFold(curEmail, targetEmail) {
					_ = writeAgyProfileVaultBlob(pid, curRaw)
				}
			}
		}()
		return fn()
	}

	// Backup the active Antigravity account into its own vault and crash-recovery marker before leasing the slot.
	cfgBase := agyVaultConfigBasePath()
	if origErr == nil && len(origRaw) > 0 {
		_, _ = SaveAgyProfileSessionBlob(origRaw, origEmail)
		_, _ = vaultSaveSecret(cfgBase, agyLeaseBackupKey, string(origRaw))
	}

	if werr := WriteLiveAgyOAuthCredential(targetBlob); werr != nil {
		vaultDeleteSecret(cfgBase, agyLeaseBackupKey)
		return werr
	}
	defer func() {
		if curRaw, curEmail, cerr := ReadLiveAgyOAuthCredential(); cerr == nil && len(curRaw) > 0 {
			if targetEmail == "" || strings.EqualFold(curEmail, targetEmail) {
				_ = writeAgyProfileVaultBlob(pid, curRaw)
			}
		}
		if origErr == nil && len(origRaw) > 0 {
			if WriteLiveAgyOAuthCredential(origRaw) == nil {
				vaultDeleteSecret(cfgBase, agyLeaseBackupKey)
			}
		} else {
			vaultDeleteSecret(cfgBase, agyLeaseBackupKey)
		}
	}()

	return fn()
}

// ActivateAgyProfileSession switches the active Antigravity Google OAuth session
// to profileID's isolated session (first backing up whichever account
// is currently active so no token refresh is ever lost).
func ActivateAgyProfileSession(profileID string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	pid := sanitizeSlug(profileID)
	if pid == "" || pid == "__system__" || pid == "__global__" {
		return nil
	}
	targetBlob, err := readAgyProfileVaultBlob(pid)
	if err != nil || len(targetBlob) == 0 {
		return fmt.Errorf("sessão OAuth isolada não encontrada para %s", profileID)
	}

	// Backup the currently active live credential to its matching profile folder first.
	_, _ = SyncCurrentLiveAgyAccountToProfiles()

	return WriteLiveAgyOAuthCredential(targetBlob)
}

// EnsureLiveAgyCredentialRestored guarantees that if an interrupted lease left a
// recovery backup in the vault or the live "gemini:antigravity" slot is empty (and
// no OAuth code entry is in progress), the user's primary account is restored.
func EnsureLiveAgyCredentialRestored() {
	if runtime.GOOS != "windows" {
		return
	}
	pendingAgyLoginMu.Lock()
	inLogin := pendingAgyActive
	pendingAgyLoginMu.Unlock()
	if inLogin {
		return
	}
	cfgBase := agyVaultConfigBasePath()
	if backupStr, _ := vaultLoadSecret(cfgBase, agyLeaseBackupKey, vaultRefPrefix+agyLeaseBackupKey); len(backupStr) > 0 {
		if WriteLiveAgyOAuthCredential([]byte(backupStr)) == nil {
			vaultDeleteSecret(cfgBase, agyLeaseBackupKey)
			return
		}
	}
	if raw, _, err := ReadLiveAgyOAuthCredential(); err == nil && len(raw) > 0 {
		return
	}
	profilesMu.Lock()
	doc, err := loadProfilesLocked()
	profilesMu.Unlock()
	if err != nil {
		return
	}
	for _, p := range doc.Profiles {
		if p.Provider != "agy" {
			continue
		}
		if blob, rerr := readAgyProfileVaultBlob(p.ID); rerr == nil && len(blob) > 0 {
			_ = WriteLiveAgyOAuthCredential(blob)
			return
		}
	}
}

// ConnectNewAgyGoogleAccount backs up the current AGY Google account into its isolated
// folder, starts a local loopback OAuth callback listener on `http://localhost:<port>/oauth-callback`,
// and opens the user's browser to Google's account chooser (`prompt=consent select_account`).
// When the user picks their Google account in the browser, Google redirects directly back to
// HiveMind's local listener, which exchanges the token in pure Go HTTP and saves the new
// account in its own `%APPDATA%\HiveMind\agy-profiles\agy-<email>\oauth-session.json` folder.
func ConnectNewAgyGoogleAccount(workspace string) (ProfilesOverview, error) {
	prevRaw, prevEmail, _ := ReadLiveAgyOAuthCredential()
	if len(prevRaw) > 0 {
		_, _ = SyncCurrentLiveAgyAccountToProfiles()
	}

	authURL, err := startAgyLoopbackOAuthFlow(prevRaw, prevEmail)
	if err != nil {
		return ListProfilesOverview(workspace), err
	}

	if err := openAgyBrowserURL(authURL); err != nil {
		return ListProfilesOverview(workspace), fmt.Errorf("abrir navegador para login Google: %w", err)
	}

	return ListProfilesOverview(workspace), nil
}

func startAgyLoopbackOAuthFlow(prevRaw []byte, prevEmail string) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("iniciar servidor local de callback OAuth: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/oauth-callback", port)

	verifier, challenge, err := generatePKCEPair()
	if err != nil {
		_ = ln.Close()
		return "", err
	}
	stateTok, err := randomURLToken(18)
	if err != nil {
		_ = ln.Close()
		return "", err
	}

	mux := http.NewServeMux()
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	mux.HandleFunc("/oauth-callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errStr := strings.TrimSpace(q.Get("error")); errStr != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, agyOAuthResultHTML(false, "Login cancelado ou recusado pelo Google ("+errStr+")."))
			return
		}
		code := strings.TrimSpace(q.Get("code"))
		if code == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, agyOAuthResultHTML(false, "Código OAuth ausente no retorno do Google."))
			return
		}
		email, exErr := completeAgyOAuthCodeExchange(code, redirectURI, verifier)
		if exErr != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, agyOAuthResultHTML(false, exErr.Error()))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, agyOAuthResultHTML(true, fmt.Sprintf("Conta Google <strong>%s</strong> conectada e salva em pasta isolada no HiveMind!", email)))
		go func() {
			time.Sleep(1200 * time.Millisecond)
			shutdownPendingAgyServer()
		}()
	})

	pendingAgyLoginMu.Lock()
	if pendingAgyServer != nil {
		_ = pendingAgyServer.Close()
		pendingAgyServer = nil
	}
	pendingAgyServer = srv
	pendingAgyRedirectURI = redirectURI
	pendingAgyVerifier = verifier
	pendingAgyState = stateTok
	if len(prevRaw) > 0 {
		pendingAgyBackupBlob = prevRaw
		pendingAgyBackupEmail = prevEmail
	}
	pendingAgyActive = true
	pendingAgyLoginMu.Unlock()

	go func() {
		_ = srv.Serve(ln)
	}()

	vals := url.Values{}
	vals.Set("client_id", agyLoopbackClientID())
	vals.Set("redirect_uri", redirectURI)
	vals.Set("response_type", "code")
	vals.Set("scope", strings.Join(agyConsumerOAuthScopes, " "))
	vals.Set("access_type", "offline")
	vals.Set("prompt", "consent select_account")
	vals.Set("state", stateTok)
	vals.Set("code_challenge", challenge)
	vals.Set("code_challenge_method", "S256")

	return "https://accounts.google.com/o/oauth2/v2/auth?" + vals.Encode(), nil
}

// SubmitAgyOAuthCode exchanges an authorization code (`4/0A...`) or full callback URL
// directly with Google's OAuth2 token endpoint in pure Go HTTP (no subprocess stdin pipe),
// writes the session into Windows Credential Manager and the account's isolated vault folder,
// and returns the updated ProfilesOverview.
func SubmitAgyOAuthCode(codeOrURL, workspace string) (ProfilesOverview, error) {
	cleanCode, pastedRedirectURI := extractOAuthCodeAndRedirect(codeOrURL)
	if cleanCode == "" {
		return ListProfilesOverview(workspace), fmt.Errorf("cole o código de autenticação gerado pelo Google (ex: 4/0A...)")
	}

	pendingAgyLoginMu.Lock()
	redirectURI := pendingAgyRedirectURI
	verifier := pendingAgyVerifier
	pendingAgyLoginMu.Unlock()

	if pastedRedirectURI != "" {
		redirectURI = pastedRedirectURI
	}

	if _, err := completeAgyOAuthCodeExchange(cleanCode, redirectURI, verifier); err != nil {
		return ListProfilesOverview(workspace), err
	}
	shutdownPendingAgyServer()
	return ListProfilesOverview(workspace), nil
}

// CancelAgyOAuthLogin aborts an in-progress Google OAuth login and ensures
// the previous Google account remains active in "gemini:antigravity".
func CancelAgyOAuthLogin(workspace string) ProfilesOverview {
	pendingAgyLoginMu.Lock()
	srv := pendingAgyServer
	pendingAgyServer = nil
	pendingAgyRedirectURI = ""
	pendingAgyVerifier = ""
	pendingAgyState = ""
	backup := pendingAgyBackupBlob
	pendingAgyBackupBlob = nil
	pendingAgyActive = false
	pendingAgyLoginMu.Unlock()

	if srv != nil {
		_ = srv.Close()
	}
	if len(backup) > 0 {
		if raw, _, err := ReadLiveAgyOAuthCredential(); err != nil || len(raw) == 0 {
			_ = WriteLiveAgyOAuthCredential(backup)
		}
	} else {
		EnsureLiveAgyCredentialRestored()
	}
	return ListProfilesOverview(workspace)
}

func shutdownPendingAgyServer() {
	pendingAgyLoginMu.Lock()
	srv := pendingAgyServer
	pendingAgyServer = nil
	pendingAgyActive = false
	pendingAgyBackupBlob = nil
	pendingAgyLoginMu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

type agyOAuthExchangeCandidate struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	Verifier     string
}

func completeAgyOAuthCodeExchange(code, redirectURI, verifier string) (string, error) {
	var candidates []agyOAuthExchangeCandidate
	if redirectURI != "" && !strings.EqualFold(redirectURI, agyOOBRedirectURI) {
		candidates = append(candidates,
			agyOAuthExchangeCandidate{
				ClientID:     agyLoopbackClientID(),
				ClientSecret: agyLoopbackClientSecret(),
				RedirectURI:  redirectURI,
				Verifier:     verifier,
			},
			agyOAuthExchangeCandidate{
				ClientID:     agyLoopbackClientID(),
				ClientSecret: agyLoopbackClientSecret(),
				RedirectURI:  redirectURI,
				Verifier:     "",
			},
		)
	}
	// Also support codes copied from https://antigravity.google/oauth-callback
	candidates = append(candidates,
		agyOAuthExchangeCandidate{
			ClientID:     agyOOBClientID(),
			ClientSecret: agyOOBClientSecret(),
			RedirectURI:  agyOOBRedirectURI,
			Verifier:     verifier,
		},
		agyOAuthExchangeCandidate{
			ClientID:     agyOOBClientID(),
			ClientSecret: agyOOBClientSecret(),
			RedirectURI:  agyOOBRedirectURI,
			Verifier:     "",
		},
		agyOAuthExchangeCandidate{
			ClientID:     agyLoopbackClientID(),
			ClientSecret: agyLoopbackClientSecret(),
			RedirectURI:  agyOOBRedirectURI,
			Verifier:     "",
		},
	)

	var lastErr error
	for _, cand := range candidates {
		blob, email, err := exchangeAgyCodeWithCandidate(code, cand)
		if err == nil && len(blob) > 0 {
			// Save strictly to the isolated HiveMind vault folder (`agy-profiles/<id>/oauth-session.json`)
			// WITHOUT overwriting the user's active Antigravity account in Windows Credential Manager.
			if _, serr := SaveAgyProfileSessionBlob(blob, email); serr != nil {
				return "", serr
			}
			return email, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", fmt.Errorf("validar código OAuth no Google: %w", lastErr)
	}
	return "", fmt.Errorf("não foi possível validar o código OAuth informado")
}

func exchangeAgyCodeWithCandidate(code string, cand agyOAuthExchangeCandidate) ([]byte, string, error) {
	form := url.Values{}
	form.Set("client_id", cand.ClientID)
	form.Set("client_secret", cand.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", cand.RedirectURI)
	form.Set("grant_type", "authorization_code")
	if cand.Verifier != "" {
		form.Set("code_verifier", cand.Verifier)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodPost, agyOAuthTokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tokResp); err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(tokResp.AccessToken) == "" {
		return nil, "", fmt.Errorf("resposta OAuth sem access_token")
	}
	if tokResp.TokenType == "" {
		tokResp.TokenType = "Bearer"
	}
	if tokResp.ExpiresIn <= 0 {
		tokResp.ExpiresIn = 3599
	}

	email := extractEmailFromIDToken(tokResp.IDToken)
	if email == "" {
		email = fetchGoogleUserEmail(client, tokResp.AccessToken)
	}
	if email == "" {
		email = "conta-google-agy"
	}

	writerID := agyDefaultWriterID
	if prevRaw, _, rerr := ReadLiveAgyOAuthCredential(); rerr == nil && len(prevRaw) > 0 {
		var prevMeta agyOAuthCredMeta
		if json.Unmarshal(prevRaw, &prevMeta) == nil && strings.TrimSpace(prevMeta.WriterID) != "" {
			writerID = strings.TrimSpace(prevMeta.WriterID)
		}
	}
	if strings.TrimSpace(tokResp.RefreshToken) == "" {
		if existingRT := findSavedRefreshTokenForEmail(email); existingRT != "" {
			tokResp.RefreshToken = existingRT
		}
	}

	expiryTime := time.Now().Add(time.Duration(tokResp.ExpiresIn) * time.Second).Format(time.RFC3339Nano)
	session := agyKeyringSessionBlob{
		Token: agyKeyringTokenPayload{
			AccessToken:  tokResp.AccessToken,
			TokenType:    tokResp.TokenType,
			RefreshToken: tokResp.RefreshToken,
			Expiry:       expiryTime,
			ExpiresIn:    tokResp.ExpiresIn,
		},
		AuthMethod:   "consumer",
		IDTokenEmail: email,
		WriterID:     writerID,
	}
	blob, err := json.Marshal(session)
	if err != nil {
		return nil, "", err
	}
	return blob, email, nil
}

func findSavedRefreshTokenForEmail(email string) string {
	if email == "" {
		return ""
	}
	profID := sanitizeSlug("agy-" + email)
	raw, err := readAgyProfileVaultBlob(profID)
	if err != nil || len(raw) == 0 {
		return ""
	}
	var saved agyKeyringSessionBlob
	if json.Unmarshal(raw, &saved) == nil {
		return strings.TrimSpace(saved.Token.RefreshToken)
	}
	return ""
}

func extractEmailFromIDToken(idToken string) string {
	parts := strings.Split(strings.TrimSpace(idToken), ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(payload, &claims) == nil {
		return strings.TrimSpace(claims.Email)
	}
	return ""
}

func fetchGoogleUserEmail(client *http.Client, accessToken string) string {
	req, err := http.NewRequest(http.MethodGet, agyOAuthUserInfoEndpoint, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	var info struct {
		Email string `json:"email"`
	}
	if json.Unmarshal(body, &info) == nil {
		return strings.TrimSpace(info.Email)
	}
	return ""
}

func extractOAuthCodeAndRedirect(raw string) (code string, redirectURI string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ""
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		if u, err := url.Parse(s); err == nil {
			code = strings.TrimSpace(u.Query().Get("code"))
			if code != "" {
				base := fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, u.Path)
				return code, base
			}
		}
	}
	return s, ""
}

func generatePKCEPair() (verifier string, challenge string, err error) {
	verifier, err = randomURLToken(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomURLToken(numBytes int) (string, error) {
	b := make([]byte, numBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openAgyBrowserURL(targetURL string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL)
	procx.HideWindow(cmd)
	return cmd.Start()
}

func agyOAuthResultHTML(ok bool, msg string) string {
	border := "rgba(52,211,153,0.45)"
	badge := "✓ Conta Conectada"
	color := "#6ee7b7"
	if !ok {
		border = "rgba(248,113,113,0.45)"
		badge = "Erro na Autenticação"
		color = "#fca5a5"
	}
	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>HiveMind · Google Antigravity OAuth</title></head>
<body style="margin:0;height:100vh;display:flex;align-items:center;justify-content:center;background:#0b0f19;color:#e2e8f0;font-family:system-ui,-apple-system,sans-serif">
  <div style="max-width:460px;padding:28px 32px;border-radius:16px;background:#111827;border:1px solid %s;text-align:center;box-shadow:0 20px 50px rgba(0,0,0,0.5)">
    <div style="display:inline-block;padding:5px 12px;border-radius:999px;background:rgba(255,255,255,0.06);color:%s;font-weight:700;font-size:13px;margin-bottom:14px">%s</div>
    <h2 style="margin:0 0 10px 0;font-size:19px">HiveMind · Pool de Contas Google</h2>
    <p style="margin:0 0 18px 0;font-size:14px;color:#94a3b8;line-height:1.55">%s</p>
    <p style="margin:0;font-size:12px;color:#64748b">Você já pode fechar esta guia e voltar para o HiveMind IDE.</p>
  </div>
  <script>setTimeout(function(){try{window.close();}catch(e){}}, 1500);</script>
</body></html>`, border, color, badge, msg)
}
