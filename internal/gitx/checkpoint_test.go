package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func initCheckpointRepo(t *testing.T) (string, Local) {
	t.Helper()
	dir := t.TempDir()
	g := Local{}
	ctx := context.Background()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "hivemind@example.com"},
		{"config", "user.name", "HiveMind"},
		{"config", "commit.gpgsign", "false"},
		{"config", "core.autocrlf", "false"},
	} {
		if _, err := g.Run(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\nconst V = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, dir, "add", "app.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Run(ctx, dir, "commit", "-m", "initial"); err != nil {
		t.Fatal(err)
	}
	return dir, g
}

func TestWorktreeCheckpointCreateListAndRollback(t *testing.T) {
	dir, g := initCheckpointRepo(t)
	ctx := context.Background()

	// Modify tracked file before checkpoint to verify dirty stash-snapshot preservation.
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\nconst V = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp, err := CreateCheckpoint(ctx, g, dir, "run-1", "antes do dev")
	if err != nil {
		t.Fatalf("CreateCheckpoint failed: %v", err)
	}
	if !cp.HasDiff {
		t.Fatal("expected HasDiff=true when dirty modifications existed before checkpoint")
	}

	// Simulate broken dev edits + untracked junk after checkpoint.
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("BROKEN\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.txt"), []byte("junk\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cps, err := ListCheckpoints(ctx, g, dir)
	if err != nil || len(cps) != 1 || cps[0].ID != cp.ID {
		t.Fatalf("ListCheckpoints unexpected: %#v err=%v", cps, err)
	}

	if err := RollbackCheckpoint(ctx, g, dir, cp.ID); err != nil {
		t.Fatalf("RollbackCheckpoint failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "app.go"))
	if err != nil || string(got) != "package app\nconst V = 2\n" {
		t.Fatalf("expected app.go rolled back to V=2, got %q err=%v", string(got), err)
	}
	if _, err := os.Stat(filepath.Join(dir, "junk.txt")); !os.IsNotExist(err) {
		t.Fatal("expected junk.txt removed during rollback clean")
	}
	if err := DeleteCheckpoint(ctx, g, dir, cp.ID); err != nil {
		t.Fatalf("DeleteCheckpoint failed: %v", err)
	}
}
