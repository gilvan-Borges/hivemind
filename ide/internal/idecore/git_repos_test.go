package idecore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func initTestRepoWithFile(t *testing.T, dir, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, string(out))
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@hivemind.local")
	run("config", "user.name", "HiveMind Test")
	full := filepath.Join(dir, filename)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir parent %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	run("add", ".")
	run("commit", "-m", "init commit")
}

func TestDiscoverGitRepos_RootAndNestedSubRepo(t *testing.T) {
	root := t.TempDir()
	initTestRepoWithFile(t, root, "README.md", "root\n")
	nested := filepath.Join(root, "HiveMind-App")
	initTestRepoWithFile(t, nested, "app/Main.kt", "fun main() {}\n")

	// Ignore nested repo inside ignored directories like node_modules.
	ignoredRepo := filepath.Join(root, "node_modules", "some-dep")
	initTestRepoWithFile(t, ignoredRepo, "index.js", "module.exports = 1;\n")

	// Add an unstaged modification in root and an untracked file in nested repo.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("root updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "app", "NewScreen.kt"), []byte("class NewScreen\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repos, err := DiscoverGitRepos(ctx, root)
	if err != nil {
		t.Fatalf("DiscoverGitRepos failed: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos (root + HiveMind-App), got %d: %+v", len(repos), repos)
	}

	if !repos[0].IsRoot || repos[0].RelPath != "" || repos[0].Branch != "main" {
		t.Fatalf("unexpected root repo entry: %+v", repos[0])
	}
	if len(repos[0].Diffs) != 1 || repos[0].Diffs[0].Path != "README.md" {
		t.Fatalf("unexpected root repo diffs: %+v", repos[0].Diffs)
	}

	if repos[1].IsRoot || repos[1].RelPath != "HiveMind-App" || repos[1].Name != "HiveMind-App" || repos[1].Branch != "main" {
		t.Fatalf("unexpected nested repo entry: %+v", repos[1])
	}
	if len(repos[1].Diffs) != 1 || repos[1].Diffs[0].Path != "app/NewScreen.kt" {
		t.Fatalf("unexpected nested repo diffs: %+v", repos[1])
	}
}

func TestDiscoverGitRepos_ContainerWorkspaceWithoutRootGit(t *testing.T) {
	container := t.TempDir()
	projA := filepath.Join(container, "backend")
	projB := filepath.Join(container, "mobile", "android")
	initTestRepoWithFile(t, projA, "main.go", "package main\n")
	initTestRepoWithFile(t, projB, "build.gradle.kts", "plugins {}\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repos, err := DiscoverGitRepos(ctx, container)
	if err != nil {
		t.Fatalf("DiscoverGitRepos failed: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 nested repos, got %d: %+v", len(repos), repos)
	}
	if repos[0].RelPath != "backend" || repos[1].RelPath != "mobile/android" {
		t.Fatalf("unexpected repo order/paths: %+v", repos)
	}
	if repos[0].IsRoot || repos[1].IsRoot {
		t.Fatalf("neither nested repo should have IsRoot=true")
	}
}
