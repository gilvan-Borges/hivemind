package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnquotePath(t *testing.T) {
	cases := map[string]string{
		`"a\303\247\303\243o.ts"`: "ação.ts",
		`"com espaco.txt"`:        "com espaco.txt",
		`"tab\there"`:             "tab\there",
		`plain.go`:                "plain.go",
		`"quote\"inside"`:         `quote"inside`,
	}
	for in, want := range cases {
		if got := UnquotePath(in); got != want {
			t.Fatalf("UnquotePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateBranchNameRejectsInjection(t *testing.T) {
	for _, bad := range []string{"", "-f", "--force", "a..b", "a b", "a~b", "a^b", "a:b", "a?b", "a*b", "a[b", `a\b`, "a@{b}", "a.lock", "a."} {
		if err := ValidateBranchName(bad); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
	for _, good := range []string{"main", "feat/acentuação", "fix/issue-123", "hivemind/proj-1/dev_1"} {
		if err := ValidateBranchName(good); err != nil {
			t.Fatalf("expected %q to be valid: %v", good, err)
		}
	}
}

func TestCommitAllExcludesHivemindState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não está instalado")
	}
	repo := t.TempDir()
	ctx := context.Background()
	g := Local{}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "HiveMind"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := g.Run(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("oi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".hivemind", "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".hivemind", "sessions", "s1.json"), []byte(`{"secret":"token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, repo, "add", "README.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, repo, "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "novo.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	committed, err := g.CommitAll(ctx, repo, "feat: novo")
	if err != nil || !committed {
		t.Fatalf("CommitAll = %v, %v", committed, err)
	}
	tracked, err := g.Run(ctx, repo, "ls-files")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tracked, ".hivemind") {
		t.Fatalf(".hivemind must never be committed:\n%s", tracked)
	}
	if !strings.Contains(tracked, "novo.txt") {
		t.Fatalf("expected novo.txt committed:\n%s", tracked)
	}
}

// Regression: .hivemind/ already in .gitignore + directory on disk used to make
// `git add … :(exclude).hivemind` exit 1 ("paths are ignored").
func TestCommitAllWithExistingHivemindIgnore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não está instalado")
	}
	repo := t.TempDir()
	ctx := context.Background()
	g := Local{}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "HiveMind"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := g.Run(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".hivemind/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("oi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".hivemind", "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".hivemind", "sessions", "s1.json"), []byte(`{"secret":"token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, repo, "add", "README.md", ".gitignore"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, repo, "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "novo.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	committed, err := g.CommitAll(ctx, repo, "feat: novo")
	if err != nil || !committed {
		t.Fatalf("CommitAll = %v, %v", committed, err)
	}
	tracked, err := g.Run(ctx, repo, "ls-files")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tracked, ".hivemind") {
		t.Fatalf(".hivemind must never be committed:\n%s", tracked)
	}
	if !strings.Contains(tracked, "novo.txt") {
		t.Fatalf("expected novo.txt committed:\n%s", tracked)
	}
}
