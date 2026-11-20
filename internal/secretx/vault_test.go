package secretx

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestValidateNameAndMemoryVault(t *testing.T) {
	for _, bad := range []string{"", " ", "a/b", "a b", "../x", strings.Repeat("a", 97)} {
		if err := ValidateName(bad); err == nil {
			t.Fatalf("expected invalid name %q to fail", bad)
		}
	}
	v := NewMemoryVault()
	if ok, _ := v.Available(); !ok {
		t.Fatal("expected memory vault available")
	}
	if err := v.Set("gh_token", "super-secret-value"); err != nil {
		t.Fatal(err)
	}
	got, err := v.Get("gh_token")
	if err != nil || got != "super-secret-value" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	names, err := v.ListNames()
	if err != nil || !slices.Equal(names, []string{"gh_token"}) {
		t.Fatalf("names=%v err=%v", names, err)
	}
	if err := v.Delete("gh_token"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Get("gh_token"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound, got %v", err)
	}
	v.SetUnavailable("locked")
	if err := v.Set("k", "v"); !errors.Is(err, ErrVaultUnavailable) {
		t.Fatalf("expected ErrVaultUnavailable, got %v", err)
	}
}

func TestWindowsCredentialManagerAndDPAPIRoundTrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Credential Manager + DPAPI test is Windows-only")
	}
	v := NewOSVault()
	if ok, reason := v.Available(); !ok {
		t.Fatalf("Windows vault unavailable: %s", reason)
	}

	key := fmt.Sprintf("test_phase2_%d", time.Now().UnixNano())
	longKey := key + "_large"
	t.Cleanup(func() {
		_ = v.Delete(key)
		_ = v.Delete(longKey)
	})

	secretVal := "hm_secret_tok_987654321"
	if err := v.Set(key, secretVal); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	got, err := v.Get(key)
	if err != nil || got != secretVal {
		t.Fatalf("Get mismatch: got=%q err=%v", got, err)
	}

	// Large blob (> maxCredBlobBytes) exercises DPAPI file-backed fallback.
	largeVal := strings.Repeat("A", 3500)
	if err := v.Set(longKey, largeVal); err != nil {
		t.Fatalf("Set large failed: %v", err)
	}
	gotLarge, err := v.Get(longKey)
	if err != nil || gotLarge != largeVal {
		t.Fatalf("Get large mismatch: len=%d err=%v", len(gotLarge), err)
	}

	names, err := v.ListNames()
	if err != nil {
		t.Fatalf("ListNames failed: %v", err)
	}
	if !slices.Contains(names, key) || !slices.Contains(names, longKey) {
		t.Fatalf("expected %q and %q in %v", key, longKey, names)
	}

	if err := v.Delete(key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := v.Get(key); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("expected ErrSecretNotFound after delete, got %v", err)
	}
}
