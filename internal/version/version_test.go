package version

import (
	"os"
	"strings"
	"testing"
)

// TestVersionMatchesVersionFile keeps the Go default in sync with the single
// source of truth: the VERSION file at the repository root. A bump no arquivo
// sem o default correspondente (ou vice-versa) falha aqui.
func TestVersionMatchesVersionFile(t *testing.T) {
	raw, err := os.ReadFile("../../VERSION")
	if err != nil {
		t.Fatalf("ler ../../VERSION: %v", err)
	}
	fileVersion := strings.TrimSpace(string(raw))
	if fileVersion == "" {
		t.Fatal("arquivo VERSION vazio")
	}
	if Version != fileVersion {
		t.Fatalf("internal/version.Version = %q, arquivo VERSION = %q (mantenha os dois iguais ou injete via -ldflags no build)", Version, fileVersion)
	}
}

func TestStringFormats(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, BuildDate
	defer func() { Version, Commit, BuildDate = oldV, oldC, oldD }()

	Version, Commit, BuildDate = "0.2.0", "dev", ""
	if got := String(); got != "0.2.0" {
		t.Fatalf("sem injeção esperava %q, veio %q", "0.2.0", got)
	}
	if IsInjected() {
		t.Fatal("commit dev não pode contar como injetado")
	}

	Version, Commit, BuildDate = "0.2.0", "abc1234", "2026-11-21T12:00:00Z"
	want := "0.2.0 (abc1234, 2026-11-21T12:00:00Z)"
	if got := String(); got != want {
		t.Fatalf("esperava %q, veio %q", want, got)
	}
	if !IsInjected() {
		t.Fatal("commit real deveria contar como injetado")
	}

	Version, Commit, BuildDate = "0.2.0", "abc1234", ""
	want = "0.2.0 (abc1234)"
	if got := String(); got != want {
		t.Fatalf("esperava %q, veio %q", want, got)
	}
}
