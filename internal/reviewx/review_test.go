package reviewx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoteOutsideWorkspaceIsRefused(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	if _, err := Add(root, "repo", LineNote{Path: filepath.Join("..", "fora.txt"), Side: "new", Line: 1, Body: "oi"}); err == nil {
		t.Fatal("caminho fora do workspace deveria ser recusado pelo jail")
	}
}

func TestSendBuildsPackageNotTranscript(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIVEMIND_DATA_DIR", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	note, err := Add(root, "repo", LineNote{Path: "main.go", Side: "new", Line: 1, HunkHeader: "@@ -0,0 +1 @@", Body: "ajuste o nome"})
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := Send(root, "repo", note.ID, "developer")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.FromRole != "review" || pkg.ToRole != "developer" || len(pkg.Facts) != 1 {
		t.Fatalf("pacote: %+v", pkg)
	}
	if !strings.Contains(pkg.Facts[0], "ajuste o nome") {
		t.Fatalf("fato sem o comentário: %s", pkg.Facts[0])
	}
	blob := pkg.Objective + strings.Join(pkg.Facts, "\n")
	if strings.Contains(blob, "transcript") {
		t.Fatalf("pacote carregou transcript: %s", blob)
	}
}

func TestNoteStaysOutOfGitStatus(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	t.Setenv("HIVEMIND_DATA_DIR", data)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v %s", strings.Join(args, " "), err, out)
		}
	}
	git("init")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "a.go")
	git("commit", "-m", "init")
	if _, err := Add(root, "repo", LineNote{Path: "a.go", Side: "old", Line: 1, Body: "nota local"}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("git status mostrou a nota: %s", out)
	}
}

func TestRepoKeysDoNotCollide(t *testing.T) {
	root := t.TempDir()
	data := t.TempDir()
	t.Setenv("HIVEMIND_DATA_DIR", data)
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(root, "github.com/acme_tools/app", LineNote{Path: "a.go", Side: "new", Line: 1, Body: "um"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(root, "github.com/acme/tools_app", LineNote{Path: "a.go", Side: "new", Line: 2, Body: "dois"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(data, "reviews"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("chaves distintas cairam no mesmo arquivo: %d", len(entries))
	}
}
