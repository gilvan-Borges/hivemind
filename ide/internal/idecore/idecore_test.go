package idecore

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
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

func TestFastCurrentBranch(t *testing.T) {
	t.Run("BranchRef", func(t *testing.T) {
		dir := t.TempDir()
		gitDir := filepath.Join(dir, ".git")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/feature/x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := FastCurrentBranch(dir); got != "feature/x" {
			t.Fatalf("expected feature/x, got %q", got)
		}
	})

	t.Run("DetachedHash", func(t *testing.T) {
		dir := t.TempDir()
		gitDir := filepath.Join(dir, ".git")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("0123456789abcdef0123456789abcdef01234567\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := FastCurrentBranch(dir); got != "0123456" {
			t.Fatalf("expected 0123456, got %q", got)
		}
	})

	t.Run("NonGitDir", func(t *testing.T) {
		dir := t.TempDir()
		if got := FastCurrentBranch(dir); got != "" {
			t.Fatalf("expected empty branch, got %q", got)
		}
	})
}

func TestListDirAndFileIndex(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "src", "app"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "README.md"), []byte("# app"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("package main"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "src", "app", "service.go"), []byte("package app"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "node_modules", "pkg", "index.js"), []byte(""), 0o644)

	nodes, err := ListDir(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 root entries (src + README.md, ignoring node_modules), got %+v", nodes)
	}
	if !nodes[0].IsDir || nodes[0].Name != "src" || !nodes[0].HasChildren || len(nodes[0].Children) != 0 {
		t.Fatalf("expected shallow dir node with HasChildren=true and empty Children, got %+v", nodes[0])
	}
	if _, err := ListDir(dir, "../escape"); err == nil {
		t.Fatal("expected traversal rejection")
	}

	idx := NewFileIndex()
	if err := idx.Refresh(dir); err != nil {
		t.Fatal(err)
	}
	// Searching "app" should rank "src/app/service.go" (only path match) and ignore node_modules.
	res := idx.Search(dir, "index.js", 10)
	if len(res) != 0 {
		t.Fatalf("expected node_modules ignored, got %+v", res)
	}
	// Create a file whose basename starts with "app" to test basename prefix ranking ahead of directory match.
	_ = os.WriteFile(filepath.Join(dir, "src", "app_config.yaml"), []byte("x: 1"), 0o644)
	_ = idx.Refresh(dir)
	ranked := idx.Search(dir, "app", 10)
	if len(ranked) != 2 || ranked[0] != "src/app_config.yaml" {
		t.Fatalf("expected basename prefix ranked first, got %+v", ranked)
	}
}

func TestDecodeImagesAndAgyMultimodalPrompt(t *testing.T) {
	pngBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	dataURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)

	atts, err := DecodeImageInputs([]ImageInput{{
		Name:       "print.png",
		DataBase64: dataURL,
	}})
	if err != nil || len(atts) != 1 || atts[0].MIMEType != "image/png" {
		t.Fatalf("unexpected DecodeImageInputs result: %+v err=%v", atts, err)
	}

	dir := t.TempDir()
	req := &model.LLMRequest{
		Contents: []*genai.Content{{
			Role: "user",
			Parts: []*genai.Part{
				{Text: "o que há nesta foto?"},
				{InlineData: &genai.Blob{MIMEType: "image/png", Data: pngBytes}},
			},
		}},
	}
	saved, err := modelx.SaveRequestImages(dir, req)
	if err != nil || len(saved) != 1 {
		t.Fatalf("SaveRequestImages failed: %+v err=%v", saved, err)
	}
	prompt := modelx.BuildAgyPromptWithImages(req, nil, saved)
	if !strings.Contains(prompt, "imagem_1.png") || strings.Contains(prompt, "[conteúdo binário omitido]") {
		t.Fatalf("expected saved image referenced in agy prompt:\n%s", prompt)
	}
}

func TestRunWorkspaceCommand(t *testing.T) {
	dir := t.TempDir()
	res, err := RunWorkspaceCommand(context.Background(), dir, "go version")
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Output, "go version") {
		t.Fatalf("unexpected command output: %+v", res)
	}
}

func TestSessionsTokenAggregationAndRestore(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store, err := sessionx.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(ctx, &session.CreateRequest{
		AppName: sessionx.AppName,
		UserID:  sessionx.UserID,
		State: map[string]any{
			"objective": "[Contexto do Projeto]\nStack: Go\n[Objetivo do Usuário]\nRefatorar handler HTTP",
			"jira_key":  "MEI-42",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := created.Session
	_ = store.AppendEvent(ctx, sess, &session.Event{
		ID:     "e1",
		Author: "user",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{Role: "user", Parts: []*genai.Part{{Text: "[Objetivo do Usuário]\nRefatorar handler HTTP"}}},
		},
	})
	_ = store.AppendEvent(ctx, sess, &session.Event{
		ID:     "e2",
		Author: "chefe",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "Plano pronto."}}},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     1200,
				CandidatesTokenCount: 300,
				TotalTokenCount:      1500,
			},
		},
	})
	_ = store.AppendEvent(ctx, sess, &session.Event{
		ID:     "e3",
		Author: "dev",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "Handler atualizado."}}},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     2000,
				CandidatesTokenCount: 500,
				TotalTokenCount:      2500,
			},
		},
	})

	summary, err := ListWorkspaceSessions(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(summary.Sessions))
	}
	s0 := summary.Sessions[0]
	if s0.Title != "Refatorar handler HTTP" || s0.InputTokens != 3200 || s0.OutputTokens != 800 || s0.TotalTokens != 4000 {
		t.Fatalf("unexpected session summary: %+v", s0)
	}
	if summary.ProjectTotalTokens != 4000 {
		t.Fatalf("expected project total 4000, got %d", summary.ProjectTotalTokens)
	}

	detail, err := LoadSessionDetail(ctx, dir, sess.ID())
	if err != nil {
		t.Fatal(err)
	}
	if detail.TotalTokens != 4000 || detail.AgentUsage["chefe"].TotalTokens != 1500 || detail.AgentUsage["dev"].TotalTokens != 2500 {
		t.Fatalf("unexpected session detail usage: %+v", detail)
	}
	if len(detail.Messages) != 3 {
		t.Fatalf("expected 3 restored messages, got %d", len(detail.Messages))
	}

	if err := DeleteWorkspaceSession(ctx, dir, sess.ID()); err != nil {
		t.Fatal(err)
	}
	after, _ := ListWorkspaceSessions(ctx, dir)
	if len(after.Sessions) != 0 {
		t.Fatalf("expected 0 sessions after delete, got %d", len(after.Sessions))
	}
}

func TestExplorerCRUDAndGlobalTextSearch(t *testing.T) {
	dir := t.TempDir()
	if err := CreateWorkspaceDir(dir, "pkg/api"); err != nil {
		t.Fatal(err)
	}
	if err := CreateWorkspaceFile(dir, "pkg/api/server.go"); err != nil {
		t.Fatal(err)
	}
	if err := WriteWorkspaceFile(dir, "pkg/api/server.go", "package api\nfunc StartServer() {}\n"); err != nil {
		t.Fatal(err)
	}

	hits, err := SearchWorkspaceText(dir, "startserver", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].RelPath != "pkg/api/server.go" || hits[0].Line != 2 {
		t.Fatalf("unexpected text search hits: %+v", hits)
	}

	if err := RenameWorkspacePath(dir, "pkg/api/server.go", "pkg/api/http.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWorkspaceFile(dir, "pkg/api/http.go"); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}
	if err := DeleteWorkspacePath(dir, "pkg"); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateTeamAgentModelAndWorkspaceMemory(t *testing.T) {
	dir := t.TempDir()
	_, err := BuildTeamFromWizard(dir, WizardInput{
		TeamName:     "squad",
		ChiefRole:    "architect",
		DevCount:     1,
		DefaultModel: "agy:gemini-3.8-flash-high",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := UpdateTeamAgentModel(dir, "squad", "qa", "agy:gemini-4-hydrogen-high")
	if err != nil {
		t.Fatal(err)
	}
	var qaModel string
	for _, ag := range updated.Teams[0].Agents {
		if ag.Name == "qa" {
			qaModel = ag.Model
		}
	}
	if qaModel != "agy:gemini-4-hydrogen-high" {
		t.Fatalf("expected updated qa model, got %q", qaModel)
	}

	rel, err := EnsureWorkspaceMemory(dir)
	if err != nil || rel != ".hivemind/MEMORY.md" {
		t.Fatalf("EnsureWorkspaceMemory failed: rel=%q err=%v", rel, err)
	}
	snippet := loadWorkspaceMemorySnippet(dir)
	if !strings.Contains(snippet, "Memória Compartilhada do Time") {
		t.Fatalf("expected memory snippet, got %q", snippet)
	}
	if err := DeleteWorkspacePath(dir, ".hivemind"); err == nil {
		t.Fatal("expected deleting .hivemind root to be blocked")
	}
	if err := DeleteWorkspacePath(dir, ".git"); err == nil {
		t.Fatal("expected deleting .git to be blocked")
	}
}

func TestExpandAtFileMentionsAndSafety(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "pkg"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "pkg", "hello.go"), []byte("package pkg\nfunc Hello() {}\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, ".hivemind"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, ".hivemind", "team.yaml"), []byte("secret: 1\n"), 0o600)

	got := expandAtFileMentions(dir, "Revise @pkg/hello.go e ignore dev@empresa.com e @.hivemind/team.yaml")
	if !strings.Contains(got, "func Hello()") {
		t.Fatalf("expected @pkg/hello.go content expanded, got %q", got)
	}
	if strings.Contains(got, "secret: 1") {
		t.Fatalf("team.yaml must not be expanded by @mention: %q", got)
	}
}

func TestUntrackedDiffDiscardAndSyncIntegration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não instalado")
	}
	repo := t.TempDir()
	ctx := context.Background()
	g := gitx.Local{}
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "HiveMind"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := g.Run(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o644)
	_, _ = g.Run(ctx, repo, "add", "README.md")
	_, _ = g.Run(ctx, repo, "commit", "-m", "init")

	// 1. Untracked file must appear in WorkspaceDiff and be cleaned by DiscardWorkspaceChanges.
	_ = os.WriteFile(filepath.Join(repo, "novo.go"), []byte("package main\n"), 0o644)
	diffs, err := WorkspaceDiff(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 || diffs[0].Path != "novo.go" || diffs[0].Added != 1 {
		t.Fatalf("expected untracked novo.go in diff, got %+v", diffs)
	}
	if err := DiscardWorkspaceChanges(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "novo.go")); !os.IsNotExist(err) {
		t.Fatal("expected DiscardWorkspaceChanges to clean untracked novo.go")
	}

	// 2. Changes merged into hivemind/integracao worktree must sync into workspace working tree.
	devWT, err := g.EnsureWorktree(ctx, repo, "dev_1", "hivemind/t/dev_1")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(devWT, "endpoint.go"), []byte("package main\nfunc Health() {}\n"), 0o644)
	if _, err := g.CommitAll(ctx, devWT, "feat: endpoint"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.Integrate(ctx, repo, []string{"hivemind/t/dev_1"}); err != nil {
		t.Fatal(err)
	}
	synced, err := SyncIntegrationToWorkspace(ctx, repo)
	if err != nil || synced != 1 {
		t.Fatalf("expected 1 file synced from integration worktree, got synced=%d err=%v", synced, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "endpoint.go")); err != nil {
		t.Fatalf("expected endpoint.go synced into workspace: %v", err)
	}
	afterDiffs, err := WorkspaceDiff(ctx, repo)
	if err != nil || len(afterDiffs) != 1 || afterDiffs[0].Path != "endpoint.go" {
		t.Fatalf("expected synced endpoint.go visible in WorkspaceDiff, got %+v err=%v", afterDiffs, err)
	}
}
