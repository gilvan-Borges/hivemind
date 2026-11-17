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
		t.Fatal(err)
	}
	if _, err := Resolve(root, filepath.Join("escape", "file.txt")); err == nil {
		t.Fatal("expected symlink escape to fail")
	}
}

func TestResolveDotGit(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(root, filepath.Join(".git", "config")); err == nil {
		t.Fatal("expected .git to be blocked")
	}
}
