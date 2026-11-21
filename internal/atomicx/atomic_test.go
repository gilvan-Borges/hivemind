package atomicx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicCreateAndOverwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "state.json")
	if err := WriteFile(target, []byte(`{"v":1}`), 0o600); err != nil {
		t.Fatalf("initial WriteFile: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != `{"v":1}` {
		t.Fatalf("read after create: %q (%v)", string(got), err)
	}
	if err := WriteFile(target, []byte(`{"v":2}`), 0o600); err != nil {
		t.Fatalf("overwrite WriteFile: %v", err)
	}
	got, err = os.ReadFile(target)
	if err != nil || string(got) != `{"v":2}` {
		t.Fatalf("read after overwrite: %q (%v)", string(got), err)
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected no stray temp files, got %v (%v)", entries, err)
	}
}
