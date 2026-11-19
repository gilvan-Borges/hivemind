package idecore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseBuildProblemsMultiStack(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "internal", "app"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "internal", "app", "main.go"), []byte("package app\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "src", "view.ts"), []byte("export {};\n"), 0o644)

	raw := "\x1b[31m# github.com/acme/demo/internal/app\x1b[0m\n" +
		"./internal/app/main.go:42:10: undefined: bogusVar\n" +
		"src/view.ts(18,5): error TS2339: Property 'foo' does not exist on type 'Bar'.\n" +
		"src/view.ts:22:3 - warning TS6133: 'x' is declared but its value is never read.\n" +
		"[ERROR] /" + filepath.ToSlash(filepath.Join(root, "src", "App.java")) + ":[11,8] cannot find symbol\n" +
		"error[E0425]: cannot find value `z` in this scope\n" +
		" --> src/lib.rs:9:12\n" +
		`  File "` + filepath.Join(root, "script.py") + `", line 7, in <module>` + "\n" +
		"NameError: name 'missing' is not defined\n"

	got := ParseBuildProblems(raw, root)
	if len(got) != 6 {
		t.Fatalf("expected 6 parsed problems, got %d: %+v", len(got), got)
	}

	if got[0].Source != "go" || got[0].File != "internal/app/main.go" || got[0].Line != 42 || got[0].Col != 10 {
		t.Fatalf("go problem mismatch: %+v", got[0])
	}
	if got[1].Source != "tsc" || got[1].File != "src/view.ts" || got[1].Line != 18 || got[1].Col != 5 || got[1].Code != "TS2339" {
		t.Fatalf("tsc paren problem mismatch: %+v", got[1])
	}
	if got[2].Source != "tsc" || got[2].Severity != "warning" || got[2].Line != 22 {
		t.Fatalf("tsc colon warning mismatch: %+v", got[2])
	}
	if got[3].Source != "maven" || got[3].File != "src/App.java" || got[3].Line != 11 || got[3].Col != 8 {
		t.Fatalf("maven problem mismatch: %+v", got[3])
	}
	if got[4].Source != "rust" || got[4].File != "src/lib.rs" || got[4].Code != "E0425" || got[4].Line != 9 {
		t.Fatalf("rust problem mismatch: %+v", got[4])
	}
	if got[5].Source != "python" || got[5].File != "script.py" || got[5].Line != 7 {
		t.Fatalf("python problem mismatch: %+v", got[5])
	}
}
