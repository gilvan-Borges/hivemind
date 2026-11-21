package workerx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
)

func initWorkerTestRepo(t *testing.T) (string, gitx.Local) {
	t.Helper()
	dir := t.TempDir()
	g := gitx.Local{}
	ctx := context.Background()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "hivemind@example.com"},
		{"config", "user.name", "HiveMind"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := g.Run(ctx, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(dir, "pkg", "api"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "pkg", "api", "handler.go"), []byte("package api\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "pkg", "ui"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "pkg", "ui", "view.ts"), []byte("export {};\n"), 0o644)
	_, _ = g.Run(ctx, dir, "add", ".")
	_, _ = g.Run(ctx, dir, "commit", "-m", "init")
	return dir, g
}

func TestValidatePostHocDiff_EnforcesOwnedPathsAndPathsec(t *testing.T) {
	dir, g := initWorkerTestRepo(t)
	ctx := context.Background()

	// Write allowed file, disallowed out-of-slice file, and sensitive .env file.
	_ = os.WriteFile(filepath.Join(dir, "pkg", "api", "handler.go"), []byte("package api\nfunc OK() {}\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "pkg", "ui", "view.ts"), []byte("export const dirty = true;\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=fake\n"), 0o600)

	res, err := ValidatePostHocDiff(ctx, g, dir, []string{"pkg/api/**"})
	if err != nil {
		t.Fatalf("ValidatePostHocDiff: %v", err)
	}
	if res.Passed() {
		t.Fatal("expected post-hoc guard to reject .env and pkg/ui/view.ts")
	}
	if len(res.Violations) != 2 {
		t.Fatalf("expected 2 violations, got %#v", res.Violations)
	}

	if err := RevertViolations(ctx, g, dir, res.Violations); err != nil {
		t.Fatalf("RevertViolations: %v", err)
	}
	resAfter, err := ValidatePostHocDiff(ctx, g, dir, []string{"pkg/api/**"})
	if err != nil {
		t.Fatalf("ValidatePostHocDiff after revert: %v", err)
	}
	if !resAfter.Passed() {
		t.Fatalf("expected post-hoc guard to pass after reverting violations, got %#v", resAfter.Violations)
	}
}
