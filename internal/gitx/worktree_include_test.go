package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyWorktreeInclude_CopiesLiteralsAndBlocksSensitive(t *testing.T) {
	mainRepo := t.TempDir()
	wtDir := t.TempDir()

	_ = os.MkdirAll(filepath.Join(mainRepo, ".vscode"), 0o755)
	_ = os.WriteFile(filepath.Join(mainRepo, ".vscode", "settings.json"), []byte(`{"editor.tabSize":2}`), 0o644)
	_ = os.WriteFile(filepath.Join(mainRepo, ".nvmrc"), []byte("22.0.0\n"), 0o644)
	_ = os.WriteFile(filepath.Join(mainRepo, ".env"), []byte("SECRET_KEY=topsecret\n"), 0o600)
	_ = os.WriteFile(filepath.Join(mainRepo, "server.pem"), []byte("PRIVATE KEY"), 0o600)

	manifest := strings.Join([]string{
		"# comentário",
		".vscode/settings.json",
		".nvmrc",
		".env",
		"server.pem",
		"*.json",
		"../outside.txt",
		"missing.txt",
	}, "\n")
	if err := os.WriteFile(filepath.Join(mainRepo, WorktreeIncludeFile), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	rep, err := ApplyWorktreeInclude(mainRepo, wtDir)
	if err != nil {
		t.Fatalf("ApplyWorktreeInclude failed: %v", err)
	}
	if len(rep.Copied) != 2 {
		t.Fatalf("expected 2 copied paths (.vscode/settings.json, .nvmrc), got %#v (blocked=%#v)", rep.Copied, rep.Blocked)
	}
	if len(rep.Blocked) != 4 {
		t.Fatalf("expected 4 blocked paths (.env, server.pem, *.json, ../outside.txt), got %#v", rep.Blocked)
	}
	if len(rep.Skipped) != 1 {
		t.Fatalf("expected 1 skipped path (missing.txt), got %#v", rep.Skipped)
	}

	gotNVMRC, err := os.ReadFile(filepath.Join(wtDir, ".nvmrc"))
	if err != nil || string(gotNVMRC) != "22.0.0\n" {
		t.Fatalf("expected .nvmrc copied, got %q err=%v", string(gotNVMRC), err)
	}
	if _, err := os.Stat(filepath.Join(wtDir, ".env")); !os.IsNotExist(err) {
		t.Fatal(".env MUST never be copied into worktree")
	}
	if _, err := os.Stat(filepath.Join(wtDir, "server.pem")); !os.IsNotExist(err) {
		t.Fatal("server.pem MUST never be copied into worktree")
	}
}
