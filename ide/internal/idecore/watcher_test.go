package idecore

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func waitForBatch(t *testing.T, batches chan FileChangeEvent, timeout time.Duration) FileChangeEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case b := <-batches:
			if len(b.Changed) > 0 || b.Overflow || b.GitChanged {
				return b
			}
		case <-deadline:
			t.Fatal("nenhum lote de mudanças chegou no prazo")
			return FileChangeEvent{}
		}
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

func TestFileWatcherReportsFileChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("espera eventos reais de filesystem")
	}
	root := t.TempDir()

	batches := make(chan FileChangeEvent, 16)
	var mu sync.Mutex
	var got []FileChangeEvent
	fw, err := NewFileWatcher(root, func(ev FileChangeEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
		batches <- ev
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := fw.Start(); err != nil {
		t.Fatal(err)
	}
	defer fw.Close()

	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	batch := waitForBatch(t, batches, 5*time.Second)
	if !containsPath(batch.Changed, "main.go") {
		t.Fatalf("lote %v não contém main.go", batch.Changed)
	}

	if err := os.WriteFile(target, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	batch = waitForBatch(t, batches, 5*time.Second)
	if !containsPath(batch.Changed, "main.go") {
		t.Fatalf("lote %v não contém main.go na segunda escrita", batch.Changed)
	}
}

func TestFileWatcherSkipsIgnoredDirs(t *testing.T) {
	if testing.Short() {
		t.Skip("espera eventos reais de filesystem")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".hivemind", "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}

	batches := make(chan FileChangeEvent, 16)
	fw, err := NewFileWatcher(root, func(ev FileChangeEvent) { batches <- ev })
	if err != nil {
		t.Fatal(err)
	}
	if err := fw.Start(); err != nil {
		t.Fatal(err)
	}
	defer fw.Close()

	if err := os.WriteFile(filepath.Join(root, "node_modules", "pkg.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hivemind", "sessions", "s.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "a.go"), []byte("package internal"), 0o644); err != nil {
		t.Fatal(err)
	}

	batch := waitForBatch(t, batches, 5*time.Second)
	for _, p := range batch.Changed {
		if isIgnoredRelPath(p) {
			t.Fatalf("evento vazou de diretório ignorado: %q em %v", p, batch.Changed)
		}
	}
	if !containsPath(batch.Changed, "internal/a.go") {
		t.Fatalf("lote %v deveria conter internal/a.go", batch.Changed)
	}
}

func TestFileWatcherWatchesNewSubdirectories(t *testing.T) {
	if testing.Short() {
		t.Skip("espera eventos reais de filesystem")
	}
	root := t.TempDir()

	batches := make(chan FileChangeEvent, 16)
	fw, err := NewFileWatcher(root, func(ev FileChangeEvent) { batches <- ev })
	if err != nil {
		t.Fatal(err)
	}
	if err := fw.Start(); err != nil {
		t.Fatal(err)
	}
	defer fw.Close()

	// pasta criada DEPOIS do Start deve ganhar watch e reportar mudanças
	if err := os.MkdirAll(filepath.Join(root, "cmd", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	// dar tempo do watchTree assíncrono registrar a pasta nova
	time.Sleep(200 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(root, "cmd", "app", "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	batch := waitForBatch(t, batches, 5*time.Second)
	if !containsPath(batch.Changed, "cmd/app/main.go") {
		t.Fatalf("lote %v deveria conter cmd/app/main.go", batch.Changed)
	}
}

func TestIsIgnoredRelPath(t *testing.T) {
	cases := map[string]bool{
		".hivemind/team.yaml":       true,
		".hivemind/sessions/a.json": true,
		"node_modules/pkg/index.js": true,
		"internal/idecore/watch.go": false,
		"cmd/app/main.go":           false,
	}
	for rel, want := range cases {
		if got := isIgnoredRelPath(rel); got != want {
			t.Fatalf("isIgnoredRelPath(%q) = %v, queria %v", rel, got, want)
		}
	}
}

func TestIsWatchedGitStateRel(t *testing.T) {
	cases := map[string]bool{
		".git/HEAD":                   true,
		".git/index":                  true,
		".git/MERGE_HEAD":             true,
		".git/REBASE_HEAD":            true,
		"sub/repo/.git/HEAD":          true,
		"sub/repo/.git/index":         true,
		".git/index.lock":             false,
		".git/objects/ab/1234":        false,
		"src/main.go":                 false,
	}
	for rel, want := range cases {
		if got := isWatchedGitStateRel(rel); got != want {
			t.Fatalf("isWatchedGitStateRel(%q) = %v, queria %v", rel, got, want)
		}
	}
}

func TestFileWatcherDetectsGitHEADAndIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("espera eventos reais de filesystem")
	}
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	objDir := filepath.Join(gitDir, "objects", "ab")
	if err := os.MkdirAll(objDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	batches := make(chan FileChangeEvent, 16)
	fw, err := NewFileWatcher(root, func(ev FileChangeEvent) { batches <- ev })
	if err != nil {
		t.Fatal(err)
	}
	if err := fw.Start(); err != nil {
		t.Fatal(err)
	}
	defer fw.Close()

	// Object writes must never produce events.
	if err := os.WriteFile(filepath.Join(objDir, "deadbeef"), []byte("obj"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Switching HEAD must set GitChanged=true without polluting Changed slice.
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/feat/test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	batch := waitForBatch(t, batches, 5*time.Second)
	if !batch.GitChanged {
		t.Fatalf("expected GitChanged=true on .git/HEAD update, got %+v", batch)
	}
	for _, p := range batch.Changed {
		if p == ".git/HEAD" || isIgnoredRelPath(p) {
			t.Fatalf("did not expect git internal path %q in Changed: %v", p, batch.Changed)
		}
	}
}
