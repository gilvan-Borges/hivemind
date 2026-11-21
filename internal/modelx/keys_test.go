package modelx

import (
	"os"
	"strings"
	"testing"
)

func TestStoredAPIKeysRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	prev := keysFilePathFn
	keysFilePathFn = func() (string, error) {
		return tmp + "/api-keys.json", nil
	}
	t.Cleanup(func() { keysFilePathFn = prev })

	for _, name := range []string{"OPENROUTER_API_KEY", "OPENROUTER_BASE_URL", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GOOGLE_API_KEY"} {
		t.Setenv(name, "")
	}

	if got := EnvOrStored("OPENROUTER_API_KEY"); got != "" {
		t.Fatalf("expected empty before saving, got %q", got)
	}

	status, err := SaveStoredAPIKeys(map[string]string{
		"OPENROUTER_API_KEY":  "sk-or-v1-abcdefghijklmnop",
		"OPENROUTER_BASE_URL": "https://openrouter.ai/api/v1",
		"UNKNOWN_KEY":         "ignored",
	})
	if err != nil {
		t.Fatal(err)
	}
	if mask := status["OPENROUTER_API_KEY"]; mask == "" || len(mask) >= len("sk-or-v1-abcdefghijklmnop") {
		t.Fatalf("expected masked key, got %q", mask)
	}
	if _, ok := status["UNKNOWN_KEY"]; ok {
		t.Fatal("unsupported key must not be persisted")
	}
	if got := EnvOrStored("OPENROUTER_API_KEY"); got != "sk-or-v1-abcdefghijklmnop" {
		t.Fatalf("stored fallback failed: %q", got)
	}
	diskRaw, err := os.ReadFile(tmp + "/api-keys.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(diskRaw), "sk-or-v1-abcdefghijklmnop") || !strings.Contains(string(diskRaw), `"vault:modelx:apikey:OPENROUTER_API_KEY"`) {
		t.Fatalf("api-keys.json must store vault ref instead of plaintext key:\n%s", string(diskRaw))
	}
	if got := EnvOrStored("OPENROUTER_BASE_URL"); got != "https://openrouter.ai/api/v1" {
		t.Fatalf("stored base url failed: %q", got)
	}

	// Environment always wins.
	t.Setenv("OPENROUTER_API_KEY", "sk-or-v1-env-value")
	if got := EnvOrStored("OPENROUTER_API_KEY"); got != "sk-or-v1-env-value" {
		t.Fatalf("env must override stored key, got %q", got)
	}
	t.Setenv("OPENROUTER_API_KEY", "")

	// Empty value removes the stored entry.
	if _, err := SaveStoredAPIKeys(map[string]string{"OPENROUTER_API_KEY": ""}); err != nil {
		t.Fatal(err)
	}
	if got := EnvOrStored("OPENROUTER_API_KEY"); got != "" {
		t.Fatalf("expected removed key, got %q", got)
	}
}
