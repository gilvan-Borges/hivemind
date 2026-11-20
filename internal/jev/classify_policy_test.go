package jev

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClassifyEnabledDefaultsOnAndMigrates(t *testing.T) {
	if !DefaultPolicy().ClassifyEnabled {
		t.Fatal("classificador deveria nascer ligado")
	}
	missing, err := LoadPolicyFrom(filepath.Join(t.TempDir(), "ausente.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !missing.ClassifyEnabled {
		t.Fatal("arquivo ausente deveria usar o padrão ligado")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, []byte("{\"version\":1,\"enabled\":false}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	never, err := LoadPolicyFrom(path)
	if err != nil || !never.ClassifyEnabled {
		t.Fatalf("quem nunca configurou deveria ficar ligado: %+v %v", never, err)
	}

	if err := os.WriteFile(path, []byte("{\"version\":1,\"classifyEnabled\":false}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	off, err := LoadPolicyFrom(path)
	if err != nil || off.ClassifyEnabled {
		t.Fatalf("false explícito deveria continuar desligado: %+v %v", off, err)
	}

	if err := os.WriteFile(path, []byte("{\"classifyEnabled\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	on, err := LoadPolicyFrom(path)
	if err != nil || !on.ClassifyEnabled {
		t.Fatalf("true explícito: %+v %v", on, err)
	}

	saved := DefaultPolicy()
	saved.ClassifyEnabled = false
	if err := SavePolicyTo(path, saved); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"classifyEnabled": false`) {
		t.Fatalf("gravação omitiu o false: %s", raw)
	}
	again, err := LoadPolicyFrom(path)
	if err != nil || again.ClassifyEnabled {
		t.Fatalf("releitura do false: %+v %v", again, err)
	}
}
