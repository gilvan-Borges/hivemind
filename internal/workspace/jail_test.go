package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveStaysInside(t *testing.T) {
	root := t.TempDir()
	got, err := Resolve(root, filepath.Join("src", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "src", "main.go")
	if resolvedRoot, err := filepath.EvalSymlinks(root); err == nil {
		want = filepath.Join(resolvedRoot, "src", "main.go")
	}
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if _, err := Resolve(root, filepath.Join("..", "secret.txt")); err == nil {
		t.Fatal("expected escape to fail")
	}
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if _, err := Resolve(root, outside); err == nil {
		t.Fatal("expected absolute escape to fail")
	}
}

func TestResolveSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink indisponível neste ambiente: %v", err)
	}
	if _, err := Resolve(root, filepath.Join("escape", "file.txt")); err == nil {
		t.Fatal("expected symlink escape to fail")
	}
}

func TestResolveDotGitAndHivemindProtection(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, blocked := range []string{
		filepath.Join(".git", "config"),
		filepath.Join(".GIT", "hooks", "post-checkout"),
		filepath.Join(".Git.", "config"),
		filepath.Join("GIT~1", "config"),
		filepath.Join(".hivemind", "team.yaml"),
		filepath.Join(".HIVEMIND", "sessions", "abc.json"),
	} {
		if _, err := Resolve(root, blocked); err == nil {
			t.Fatalf("expected %q to be blocked", blocked)
		}
	}

	// Read-only memory access is allowed only when explicitly opted in.
	if _, err := ResolveWithOptions(root, filepath.Join(".hivemind", "MEMORY.md"), Options{AllowMemoryRead: true}); err != nil {
		t.Fatalf("expected MEMORY.md read allowed with option: %v", err)
	}
	if _, err := ResolveWithOptions(root, filepath.Join(".hivemind", "team.yaml"), Options{AllowMemoryRead: true}); err == nil {
		t.Fatal("team.yaml must remain blocked even with AllowMemoryRead")
	}
	if _, err := ResolveWithOptions(root, filepath.Join(".hivemind", "team.yaml"), Options{AllowHivemind: true}); err != nil {
		t.Fatalf("IDE AllowHivemind should allow team.yaml: %v", err)
	}
}
