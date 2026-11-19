package idecore

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		SkillAssignments: []team.SkillAssignment{
			{Name: "qa-only", Source: "native", Roles: []string{"qa"}},
			{Name: "shared", Source: "project", Roles: []string{"all"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Settings.SkillAssignments) != 2 {
		t.Fatalf("expected 2 skill assignments, got %+v", cfg.Settings.SkillAssignments)
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

func TestSyncIntegrationRefusesDirtyWorkspace(t *testing.T) {
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
	readme := filepath.Join(repo, "README.md")
	if err := os.WriteFile(readme, []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = g.Run(ctx, repo, "add", "README.md")
	_, _ = g.Run(ctx, repo, "commit", "-m", "init")

	devWT, err := g.EnsureWorktree(ctx, repo, "dev_1", "hivemind/t/dev_1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devWT, "endpoint.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAll(ctx, devWT, "feat: endpoint"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.Integrate(ctx, repo, []string{"hivemind/t/dev_1"}); err != nil {
		t.Fatal(err)
	}

	// Local, uncommitted work in the user's tree: sync must refuse to touch it.
	if err := os.WriteFile(readme, []byte("alteração local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	synced, err := SyncIntegrationToWorkspace(ctx, repo)
	if !errors.Is(err, ErrWorkspaceDirty) {
		t.Fatalf("expected ErrWorkspaceDirty, got synced=%d err=%v", synced, err)
	}
	if synced != 0 {
		t.Fatalf("dirty workspace must not sync files, got %d", synced)
	}
	if data, _ := os.ReadFile(readme); string(data) != "alteração local\n" {
		t.Fatalf("local work was overwritten: %q", data)
	}
	if _, err := os.Stat(filepath.Join(repo, "endpoint.go")); !os.IsNotExist(err) {
		t.Fatal("integration file must not be copied into a dirty workspace")
	}
}

func TestWorkspaceFileUTF8ValidationAndAtomicWrite(t *testing.T) {
	dir := t.TempDir()

	// Latin-1 bytes are not valid UTF-8: opening must fail instead of corrupting on save.
	latin1 := filepath.Join(dir, "latin1.txt")
	if err := os.WriteFile(latin1, []byte{'c', 'a', 'f', 0xE9}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWorkspaceFile(dir, "latin1.txt"); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("expected UTF-8 error for latin1, got %v", err)
	}

	// UTF-16 LE with BOM must be rejected with an actionable message.
	utf16 := filepath.Join(dir, "utf16.txt")
	if err := os.WriteFile(utf16, []byte{0xFF, 0xFE, 'a', 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWorkspaceFile(dir, "utf16.txt"); err == nil || !strings.Contains(err.Error(), "UTF-16") {
		t.Fatalf("expected UTF-16 error, got %v", err)
	}

	// Binary content is rejected.
	bin := filepath.Join(dir, "bin.dat")
	if err := os.WriteFile(bin, []byte{0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWorkspaceFile(dir, "bin.dat"); err == nil || !strings.Contains(err.Error(), "binário") {
		t.Fatalf("expected binary error, got %v", err)
	}

	// Valid UTF-8 with accents and CRLF must round-trip byte-for-byte.
	text := "linha 1\r\nconfiguração: ação\r\n"
	if err := WriteWorkspaceFile(dir, "config/ação.txt", text); err != nil {
		t.Fatal(err)
	}
	got, err := ReadWorkspaceFile(dir, "config/ação.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got != text {
		t.Fatalf("round-trip mismatch:\n got=%q\nwant=%q", got, text)
	}
}

func TestQuotedGitPathsAreDecoded(t *testing.T) {
	raw := "diff --git \"a/a\\303\\247\\303\\243o.ts\" \"b/a\\303\\247\\303\\243o.ts\"\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ \"b/a\\303\\247\\303\\243o.ts\"\n" +
		"@@ -0,0 +1,1 @@\n" +
		"+export const ação = 1;\n"
	diffs := ParseUnifiedDiff(raw)
	if len(diffs) != 1 || diffs[0].Path != "ação.ts" {
		t.Fatalf("quoted path decoded = %+v", diffs)
	}
	status, staged := parsePorcelainStatus("?? \"a\\303\\247\\303\\243o.ts\"\n")
	if _, ok := status["ação.ts"]; !ok {
		t.Fatalf("quoted porcelain path not decoded: %+v (%v)", status, staged)
	}
}

func TestHasUserChangesIgnoresHivemind(t *testing.T) {
	if hasUserChanges("?? .hivemind/\n") {
		t.Fatal(".hivemind bookkeeping must not count as user work")
	}
	if !hasUserChanges(" M README.md\n") {
		t.Fatal("tracked modification must count as user work")
	}
	if !hasUserChanges("?? novo.txt\n") {
		t.Fatal("untracked file must count as user work")
	}
}

func TestHasUserChangesIgnoresHiveMindOnlyGitignore(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if _, err := runGit(ctx, dir, "init", "-b", "main"); err != nil {
		t.Skip("git indisponível")
	}
	_, _ = runGit(ctx, dir, "config", "user.name", "Test")
	_, _ = runGit(ctx, dir, "config", "user.email", "test@local")
	_, _ = runGit(ctx, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("bin/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = runGit(ctx, dir, "add", ".gitignore")
	_, _ = runGit(ctx, dir, "commit", "-m", "init")

	// HiveMind's EnsureHiveIgnore appends "# HiveMind\n.hivemind/\n": must NOT block sync.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("bin/\n\n# HiveMind\n.hivemind/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := runGit(ctx, dir, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		t.Fatal(err)
	}
	if hasUserChangesInWorkspace(ctx, dir, status) {
		t.Fatalf("HiveMind-only .gitignore edit must not be treated as dirty user work (status=%q)", status)
	}

	// User editing .gitignore with another entry MUST be treated as dirty.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("bin/\n*.secret\n\n# HiveMind\n.hivemind/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasUserChangesInWorkspace(ctx, dir, status) {
		t.Fatal("user edit in .gitignore must be treated as dirty")
	}
}

func TestParseGHPRList(t *testing.T) {
	raw := []byte(`[
		{
			"number": 42,
			"title": "feat: adiciona dashboard de metas",
			"body": "Resumo do PR",
			"url": "https://github.com/org/repo/pull/42",
			"state": "OPEN",
			"isDraft": true,
			"updatedAt": "2026-11-18T12:00:00Z",
			"headRefName": "feat/metas",
			"baseRefName": "homolog",
			"author": {"login": "gilvan-Borges"}
		},
		{
			"number": 43,
			"title": "fix: pendente minha revisao",
			"url": "https://github.com/org/repo/pull/43",
			"state": "OPEN",
			"author": {"login": "marcus"},
			"reviewRequests": [{"login": "gilvan-Borges"}],
			"reviews": [],
			"comments": []
		},
		{
			"number": 44,
			"title": "fix: ja aprovei esse PR",
			"url": "https://github.com/org/repo/pull/44",
			"state": "OPEN",
			"author": {"login": "marcus"},
			"reviewRequests": [{"login": "gilvan-Borges"}],
			"reviews": [{"state": "APPROVED", "author": {"login": "gilvan-Borges"}}],
			"comments": []
		},
		{
			"number": 45,
			"title": "fix: ja comentei nesse PR",
			"url": "https://github.com/org/repo/pull/45",
			"state": "OPEN",
			"author": {"login": "marcus"},
			"reviewRequests": [{"login": "gilvan-Borges"}],
			"reviews": [],
			"comments": [{"viewerDidAuthor": true, "author": {"login": "gilvan-Borges"}}]
		}
	]`)
	prs, err := ParseGHPRList(raw, "gilvan-Borges")
	if err != nil {
		t.Fatalf("ParseGHPRList error: %v", err)
	}
	if len(prs) != 4 {
		t.Fatalf("expected 4 PRs, got %d", len(prs))
	}
	if !prs[0].IsMine || prs[0].Number != 42 || !prs[0].IsDraft {
		t.Fatalf("unexpected PR 42: %+v", prs[0])
	}
	if prs[1].IsMine || !prs[1].ReviewRequested || prs[1].MyResponse != "" {
		t.Fatalf("unexpected PR 43 (pending review): %+v", prs[1])
	}
	if !prs[2].ReviewRequested || prs[2].MyResponse != "approved" {
		t.Fatalf("unexpected PR 44 (approved): %+v", prs[2])
	}
	if !prs[3].ReviewRequested || prs[3].MyResponse != "commented" {
		t.Fatalf("unexpected PR 45 (commented): %+v", prs[3])
	}
}

func TestPRFlowNeedsAttentionChronology(t *testing.T) {
	t1 := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	// Case 1 (exact PR #700 scenario): gilvan reproved at t1, joao commented after at t2 -> MUST return to "Para revisar" ("Nova resposta").
	resp, need, reason := computePRFlowState(
		"gilvan-Borges", false, false, false,
		[]prActivityReview{{Author: "gilvan-Borges", State: "CHANGES_REQUESTED", SubmittedAt: t1}},
		[]prActivityComment{{Author: "joaomarqu3ss", CreatedAt: t2}},
		time.Time{},
	)
	if resp != "changes_requested" || !need || reason != "Nova resposta" {
		t.Fatalf("expected PR #700 to bounce back with Nova resposta, got resp=%q need=%v reason=%q", resp, need, reason)
	}

	// Case 2: gilvan replies at t3 (after joao's t2) -> goes back to "Já respondidos".
	resp, need, reason = computePRFlowState(
		"gilvan-Borges", false, false, false,
		[]prActivityReview{{Author: "gilvan-Borges", State: "CHANGES_REQUESTED", SubmittedAt: t1}},
		[]prActivityComment{
			{Author: "joaomarqu3ss", CreatedAt: t2},
			{Author: "gilvan-Borges", CreatedAt: t3},
		},
		time.Time{},
	)
	if resp != "changes_requested" || need {
		t.Fatalf("expected PR to return to Já respondidos after gilvan replied last, got resp=%q need=%v reason=%q", resp, need, reason)
	}

	// Case 3: bot comment after gilvan's review must NOT falsely bounce PR back.
	_, need, _ = computePRFlowState(
		"gilvan-Borges", false, false, false,
		[]prActivityReview{{Author: "gilvan-Borges", State: "CHANGES_REQUESTED", SubmittedAt: t1}},
		[]prActivityComment{{Author: "github-actions[bot]", CreatedAt: t2}},
		time.Time{},
	)
	if need {
		t.Fatal("bot comment must not trigger needsAttention")
	}

	// Case 4: new commit pushed after CHANGES_REQUESTED -> "Novo commit".
	resp, need, reason = computePRFlowState(
		"gilvan-Borges", false, false, false,
		[]prActivityReview{{Author: "gilvan-Borges", State: "CHANGES_REQUESTED", SubmittedAt: t1}},
		nil,
		t2,
	)
	if resp != "changes_requested" || !need || reason != "Novo commit" {
		t.Fatalf("expected Novo commit attention, got resp=%q need=%v reason=%q", resp, need, reason)
	}

	// Case 5: already approved -> casual comment does NOT bounce back unless reviewRequested is true.
	resp, need, _ = computePRFlowState(
		"gilvan-Borges", false, false, false,
		[]prActivityReview{{Author: "gilvan-Borges", State: "APPROVED", SubmittedAt: t1}},
		[]prActivityComment{{Author: "joaomarqu3ss", CreatedAt: t2}},
		time.Time{},
	)
	if resp != "approved" || need {
		t.Fatalf("approved PR without re-review request should stay in Já respondidos, got resp=%q need=%v", resp, need)
	}
	resp, need, reason = computePRFlowState(
		"gilvan-Borges", false, true, true,
		[]prActivityReview{{Author: "gilvan-Borges", State: "APPROVED", SubmittedAt: t1}},
		nil,
		time.Time{},
	)
	if resp != "approved" || !need || reason != "Re-revisão" {
		t.Fatalf("re-requested review after approval must return as Re-revisão, got resp=%q need=%v reason=%q", resp, need, reason)
	}

	// Case 6: own PR (#699) receives reviewer comment after author's last commit -> Nova resposta.
	_, need, reason = computePRFlowState(
		"gilvan-Borges", true, false, false,
		nil,
		[]prActivityComment{{Author: "marcusrdrigues", CreatedAt: t2}},
		t1,
	)
	if !need || reason != "Nova resposta" {
		t.Fatalf("own PR with newer reviewer comment should flag Nova resposta, got need=%v reason=%q", need, reason)
	}

	// Case 7 (exact PR #699 waiting on Marcus): author committed & commented LAST -> leaves active Fluxo!
	_, need, reason = computePRFlowState(
		"gilvan-Borges", true, false, false,
		nil,
		[]prActivityComment{
			{Author: "marcusrdrigues", CreatedAt: t1},
			{Author: "gilvan-Borges", CreatedAt: t3},
		},
		t2,
	)
	if need {
		t.Fatalf("own PR where author replied last and is waiting for reviewer must leave active Fluxo, got need=%v reason=%q", need, reason)
	}

	// Case 8: own PR approved by Marcus with no pending re-review -> pops back as "Pronto p/ merge"!
	_, need, reason = computePRFlowState(
		"gilvan-Borges", true, false, false,
		[]prActivityReview{{Author: "marcusrdrigues", State: "APPROVED", SubmittedAt: t3}},
		[]prActivityComment{{Author: "gilvan-Borges", CreatedAt: t2}},
		t1,
	)
	if !need || reason != "Pronto p/ merge" {
		t.Fatalf("own PR approved by reviewer should return as Pronto p/ merge, got need=%v reason=%q", need, reason)
	}
}

func TestParseGitLogGraphAndPorcelainStatus(t *testing.T) {
	raw := "aaaa1111aaaa1111\x1faaaa111\x1fbbbb2222 cccc3333\x1fGilvan\x1f2 hours ago\x1fMerge pull request #6\x1fHEAD -> main, origin/main\x1e" +
		"bbbb2222bbbb2222\x1fbbbb222\x1fdddd4444\x1fGilvan\x1f3 hours ago\x1ffeat: skills panel\x1f\x1e"
	commits := ParseGitLogGraph(raw)
	if len(commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(commits))
	}
	if commits[0].Short != "aaaa111" || len(commits[0].Parents) != 2 || len(commits[0].Refs) != 2 {
		t.Fatalf("unexpected first commit: %+v", commits[0])
	}
	if commits[1].Parents[0] != "dddd4444" {
		t.Fatalf("unexpected second commit parents: %+v", commits[1])
	}

	st, staged := parsePorcelainStatus("M  ide/app.go\n M ide/main.go\nA  assets/logo.png\nD  old.txt\n")
	if st["ide/app.go"] != "M" || !staged["ide/app.go"] {
		t.Fatalf("expected staged M for ide/app.go, got status=%q staged=%v", st["ide/app.go"], staged["ide/app.go"])
	}
	if st["ide/main.go"] != "M" || staged["ide/main.go"] {
		t.Fatalf("expected unstaged M for ide/main.go, got status=%q staged=%v", st["ide/main.go"], staged["ide/main.go"])
	}
	if st["assets/logo.png"] != "A" || st["old.txt"] != "D" {
		t.Fatalf("unexpected statuses: %+v", st)
	}
}

func TestDeleteTeam(t *testing.T) {
	dir := t.TempDir()
	_, err := BuildTeamFromWizard(dir, WizardInput{TeamName: "alpha", DevCount: 1, DefaultModel: "agy"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildTeamFromWizard(dir, WizardInput{TeamName: "beta", DevCount: 1, DefaultModel: "agy"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := DeleteTeam(dir, "beta")
	if err != nil {
		t.Fatalf("DeleteTeam failed: %v", err)
	}
	if len(cfg.Teams) != 1 || cfg.Active != "alpha" {
		t.Fatalf("expected alpha active with 1 remaining team, got active=%q teams=%d", cfg.Active, len(cfg.Teams))
	}
	cfg, err = DeleteTeam(dir, "alpha")
	if err != nil {
		t.Fatalf("DeleteTeam last team failed: %v", err)
	}
	if len(cfg.Teams) != 0 || cfg.Active != "" {
		t.Fatalf("expected 0 teams and empty Active, got active=%q teams=%d", cfg.Active, len(cfg.Teams))
	}
}
