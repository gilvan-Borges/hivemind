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

func TestResolveSpilloverScopedCarveOut(t *testing.T) {
	root := t.TempDir()
	spill := filepath.Join(".hivemind", "spillover", "run-shell-1.txt")
	if err := os.MkdirAll(filepath.Join(root, ".hivemind", "spillover"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, spill), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The spillover subtree resolves ONLY with the scoped option.
	if _, err := Resolve(root, spill); err == nil {
		t.Fatal("spillover must stay blocked without the option")
	}
	if _, err := ResolveWithOptions(root, spill, Options{AllowSpilloverRead: true}); err != nil {
		t.Fatalf("spillover read must be allowed with the option: %v", err)
	}

	// Everything else under .hivemind stays blocked even with the option.
	for _, blocked := range []string{
		filepath.Join(".hivemind", "team.yaml"),
		filepath.Join(".hivemind", "spillover-secret.txt"),
		filepath.Join(".hivemind", "spillovers", "x.txt"),
		filepath.Join(".hivemind", "spillover"),
	} {
		if _, err := ResolveWithOptions(root, blocked, Options{AllowSpilloverRead: true}); err == nil {
			t.Fatalf("expected %q to stay blocked with AllowSpilloverRead", blocked)
		}
	}

	// Dot-dot cannot smuggle the carve-out into another .hivemind path:
	// Clean runs before the segment loop.
	if _, err := ResolveWithOptions(root, filepath.Join(".hivemind", "spillover", "..", "team.yaml"), Options{AllowSpilloverRead: true}); err == nil {
		t.Fatal("dot-dot escape must stay blocked")
	}

	// A symlink inside spillover pointing back at protected config must not
	// resolve through the carve-out.
	if err := os.WriteFile(filepath.Join(root, ".hivemind", "team.yaml"), []byte("cfg"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, ".hivemind", "spillover", "escape.txt")
	if err := os.Symlink(filepath.Join(root, ".hivemind", "team.yaml"), link); err != nil {
		t.Skipf("symlink indisponível neste ambiente: %v", err)
	}
	if _, err := ResolveWithOptions(root, filepath.Join(".hivemind", "spillover", "escape.txt"), Options{AllowSpilloverRead: true}); err == nil {
		t.Fatal("symlink escape must stay blocked")
	}

	// Nested lookalikes do not inherit the carve-out.
	if err := os.MkdirAll(filepath.Join(root, "docs", ".hivemind", "spillover"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveWithOptions(root, filepath.Join("docs", ".hivemind", "spillover", "x.txt"), Options{AllowSpilloverRead: true}); err == nil {
		t.Fatal("nested .hivemind/spillover must stay blocked (carve-out is root-scoped)")
	}
}
