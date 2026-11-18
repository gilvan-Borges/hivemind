package idecore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestRecentProjectsMRUAndFilterMissing(t *testing.T) {
	tmp := t.TempDir()
	recentFile := filepath.Join(tmp, "HiveMind", "ide-recent.txt")
	projA := filepath.Join(tmp, "proj-a")
	projB := filepath.Join(tmp, "proj-b")
	missing := filepath.Join(tmp, "proj-missing")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := RecordRecentProject(recentFile, projA); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordRecentProject(recentFile, projB); err != nil {
		t.Fatal(err)
	}
	got, err := RecordRecentProject(recentFile, projA)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != projA || got[1] != projB {
		t.Fatalf("unexpected MRU order: %#v", got)
	}

	// Append a missing directory directly and ensure LoadRecentProjects filters it out.
	if err := os.WriteFile(recentFile, []byte(projA+"\n"+missing+"\n"+projB+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded := LoadRecentProjects(recentFile)
	if len(loaded) != 2 || loaded[0] != projA || loaded[1] != projB {
		t.Fatalf("expected missing dir filtered, got %#v", loaded)
	}
}

func TestDetectStack(t *testing.T) {
	t.Run("Go", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s := DetectStack(dir)
		if !strings.Contains(s.Summary, "Go") || s.TestHint != "go test ./..." {
			t.Fatalf("unexpected Go stack: %+v", s)
		}
		if !strings.Contains(s.FormatForChief(), "Stack detectada: Go") {
			t.Fatalf("missing chief context: %s", s.FormatForChief())
		}
	})

	t.Run("JavaMavenAndAngular", func(t *testing.T) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "pom.xml"), []byte("<project/>"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "angular.json"), []byte("{}"), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"@angular/core":"18.0.0"}}`), 0o644)
		s := DetectStack(dir)
		if !strings.Contains(s.Summary, "Java / Maven") || !strings.Contains(s.Summary, "Angular") {
			t.Fatalf("expected Java / Maven + Angular, got %+v", s)
		}
	})
}

func TestExplorerPathSafetyAndReadWrite(t *testing.T) {
	dir := t.TempDir()
	if err := WriteWorkspaceFile(dir, "hello.txt", "olá hivemind"); err != nil {
		t.Fatal(err)
	}
	got, err := ReadWorkspaceFile(dir, "hello.txt")
	if err != nil || got != "olá hivemind" {
		t.Fatalf("read back failed: got=%q err=%v", got, err)
	}
	if _, err := ResolveSafe(dir, filepath.Join("..", "outside.txt")); err == nil {
		t.Fatal("expected traversal error")
	}
	tree, err := ListFileTree(dir, 3)
	if err != nil || len(tree) != 1 || tree[0].Name != "hello.txt" {
		t.Fatalf("unexpected tree: %+v err=%v", tree, err)
	}
	matches := SearchFiles(dir, "hel", 10)
	if len(matches) != 1 || matches[0] != "hello.txt" {
		t.Fatalf("unexpected search results: %+v", matches)
	}
}

func TestParseUnifiedDiff(t *testing.T) {
	raw := strings.Join([]string{
		"diff --git a/main.go b/main.go",
		"index 1111111..2222222 100644",
		"--- a/main.go",
		"+++ b/main.go",
		"@@ -1,3 +1,4 @@",
		" package main",
		"-func old() {}",
		"+func newFn() {}",
		"+func extra() {}",
	}, "\n")

	diffs := ParseUnifiedDiff(raw)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 file diff, got %d", len(diffs))
	}
	d := diffs[0]
	if d.Path != "main.go" || d.Added != 2 || d.Removed != 1 {
		t.Fatalf("unexpected diff stats: %+v", d)
	}
}

func TestBuildTeamFromWizardDoesNotModifyGitignore(t *testing.T) {
	dir := t.TempDir()
	gitignore := filepath.Join(dir, ".gitignore")
	original := "bin/\n"
	if err := os.WriteFile(gitignore, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := BuildTeamFromWizard(dir, WizardInput{
		TeamName:     "alpha",
		ChiefRole:    "architect",
		DevCount:     2,
		DefaultModel: "agy:gemini-4-hydrogen-medium",
		AgentModels: map[string]string{
			"qa": "agy:claude-sonnet-5-5-medium",
		},
		Worktrees:  true,
		AutoCommit: true,
		AutoPR:     false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Active != "alpha" {
		t.Fatalf("expected active team alpha, got %s", cfg.Active)
	}
	t0, err := cfg.ActiveTeam("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if err := t0.ValidateForRun(); err != nil {
		t.Fatalf("wizard team should be runnable: %v", err)
	}
	if len(t0.ByRole(team.RoleDeveloper)) != 2 {
		t.Fatalf("expected 2 devs, got %d", len(t0.ByRole(team.RoleDeveloper)))
	}
	after, err := os.ReadFile(gitignore)
	if err != nil || string(after) != original {
		t.Fatalf(".gitignore must not be modified by init; got %q", string(after))
	}
}

func TestParseAgyModelsOutput(t *testing.T) {
	raw := "Fetching available models...\ngemini-4-hydrogen-medium\tGemini 4 Hydrogen (Medium)\nclaude-sonnet-5-5-medium\tClaude Sonnet 5.5 (Medium)\n"
	models := modelx.ParseAgyModelsOutput(raw)
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Slug != "gemini-4-hydrogen-medium" || models[0].Name != "Gemini 4 Hydrogen (Medium)" {
		t.Fatalf("unexpected model[0]: %+v", models[0])
	}
}
