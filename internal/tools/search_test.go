package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestSearchTextLiteralAndRegex(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("api/ClienteController.java", "public class ClienteController {\n  public PageResponse<ClienteResponse> list() { return null; }\n}\n")
	mustWrite("web/src/routes.ts", "export const routes = [\n  { path: '/clientes', role: 'SUPER_ADMIN' },\n];\n")
	mustWrite("web/src/routes.spec.ts", "test('clientes route requires SUPER_ADMIN', () => {});\n")
	// Ignored directories must NEVER appear in results.
	mustWrite(".git/config", "PageResponse SUPER_ADMIN\n")
	mustWrite(".hivemind/MEMORY.md", "PageResponse SUPER_ADMIN\n")
	mustWrite(".worktrees/agent-1/ClienteController.java", "PageResponse SUPER_ADMIN\n")
	mustWrite("node_modules/pkg/index.js", "PageResponse SUPER_ADMIN\n")
	// Binary file must be skipped.
	mustWrite("assets/logo.bin", "PNG\x00\x01\x02PageResponse SUPER_ADMIN")

	// 1. Literal case-insensitive search.
	out, err := runSearchText(context.Background(), root, searchTextArgs{Query: "pageresponse"})
	if err != nil {
		t.Fatalf("literal search failed: %v", err)
	}
	if !strings.Contains(out, "api/ClienteController.java:2:") {
		t.Fatalf("expected match in api/ClienteController.java:2, got:\n%s", out)
	}
	for _, forbidden := range []string{".git/", ".hivemind/", ".worktrees/", "node_modules/", "logo.bin"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("ignored/binary path %q leaked into search_text output:\n%s", forbidden, out)
		}
	}

	// 2. Regex + glob filter.
	out, err = runSearchText(context.Background(), root, searchTextArgs{
		Query: `SUPER_[A-Z]+`,
		Regex: true,
		Glob:  "*.spec.ts",
	})
	if err != nil {
		t.Fatalf("regex+glob search failed: %v", err)
	}
	if !strings.Contains(out, "web/src/routes.spec.ts:1:") {
		t.Fatalf("expected match in routes.spec.ts, got:\n%s", out)
	}
	if strings.Contains(out, "web/src/routes.ts:") {
		t.Fatalf("glob *.spec.ts should not match routes.ts:\n%s", out)
	}

	// 3. Path restriction (directory and single file).
	out, err = runSearchText(context.Background(), root, searchTextArgs{
		Query: "SUPER_ADMIN",
		Path:  "api",
	})
	if err != nil {
		t.Fatalf("restricted path search failed: %v", err)
	}
	if !strings.Contains(out, "Nenhuma ocorrência encontrada") {
		t.Fatalf("expected no matches under api/, got:\n%s", out)
	}

	out, err = runSearchText(context.Background(), root, searchTextArgs{
		Query: "PageResponse",
		Path:  "api/ClienteController.java",
	})
	if err != nil {
		t.Fatalf("single-file path search failed: %v", err)
	}
	if !strings.Contains(out, "api/ClienteController.java:2:") {
		t.Fatalf("expected match in single-file search, got:\n%s", out)
	}
}

func TestSearchTextRejectsPathEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("TOP_SECRET_TOKEN\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runSearchText(context.Background(), root, searchTextArgs{
		Query: "TOP_SECRET_TOKEN",
		Path:  "../",
	}); err == nil {
		t.Fatal("expected traversal path ../ to be rejected")
	}

	// Symlink inside root pointing outside workspace must not be read.
	linkPath := filepath.Join(root, "escape.txt")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), linkPath); err == nil {
		out, err := runSearchText(context.Background(), root, searchTextArgs{Query: "TOP_SECRET_TOKEN"})
		if err != nil {
			t.Fatalf("unexpected error scanning workspace with symlink: %v", err)
		}
		if !strings.Contains(out, "Nenhuma ocorrência encontrada") || strings.Contains(out, "escape.txt") {
			t.Fatalf("symlink outside workspace must not be searched:\n%s", out)
		}
	}
}

func TestFindFilesGlobAndSubstring(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"web/e2e/public-routes.spec.ts",
		"web/e2e/auth.spec.ts",
		"specs/front/catraca/a11y-web.txt",
		".worktrees/other/ignored.spec.ts",
		"node_modules/lib/ignored.spec.ts",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("ok\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	out, err := runFindFiles(context.Background(), root, findFilesArgs{Pattern: "*.spec.ts"})
	if err != nil {
		t.Fatalf("find_files glob failed: %v", err)
	}
	if !strings.Contains(out, "web/e2e/public-routes.spec.ts") || !strings.Contains(out, "web/e2e/auth.spec.ts") {
		t.Fatalf("expected spec files in output:\n%s", out)
	}
	if strings.Contains(out, ".worktrees") || strings.Contains(out, "node_modules") {
		t.Fatalf("ignored dirs leaked into find_files:\n%s", out)
	}

	out, err = runFindFiles(context.Background(), root, findFilesArgs{Pattern: "a11y-web"})
	if err != nil {
		t.Fatalf("find_files substring failed: %v", err)
	}
	if !strings.Contains(out, "specs/front/catraca/a11y-web.txt") {
		t.Fatalf("expected a11y-web.txt in substring output:\n%s", out)
	}
}

func TestReadFileLineRange(t *testing.T) {
	root := t.TempDir()
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("linha-conteudo-%02d", i))
	}
	raw := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "sample.txt"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	// Backward compatibility: no start_line/end_line returns raw file contents.
	full, err := readFileRange(root, readFileArgs{Path: "sample.txt"})
	if err != nil {
		t.Fatalf("full read failed: %v", err)
	}
	if full != raw {
		t.Fatalf("expected exact full content for compatibility; len=%d want=%d", len(full), len(raw))
	}

	// Targeted range: lines 10..15.
	sliced, err := readFileRange(root, readFileArgs{Path: "sample.txt", StartLine: 10, EndLine: 15})
	if err != nil {
		t.Fatalf("range read failed: %v", err)
	}
	if !strings.Contains(sliced, "# sample.txt (linhas 10-15 de 50)") {
		t.Fatalf("missing header with line count, got:\n%s", sliced)
	}
	if !strings.Contains(sliced, "L10: linha-conteudo-10") || !strings.Contains(sliced, "L15: linha-conteudo-15") {
		t.Fatalf("missing expected lines 10..15, got:\n%s", sliced)
	}
	if strings.Contains(sliced, "L9:") || strings.Contains(sliced, "L16:") {
		t.Fatalf("unexpected lines outside range 10..15:\n%s", sliced)
	}

	// Out-of-bounds start_line returns clear error.
	if _, err := readFileRange(root, readFileArgs{Path: "sample.txt", StartLine: 99}); err == nil {
		t.Fatal("expected error for start_line > total lines")
	}
}

func TestAllRolesExposeSearchAndFindTools(t *testing.T) {
	gh := &githubx.REST{Token: "tok", Owner: "acme", Repo: "app"}
	deps := Deps{
		Root:     t.TempDir(),
		Settings: team.DefaultSettings(),
		GitHub:   gh,
	}
	roles := []string{
		team.RoleProjectManager,
		team.RoleTechLead,
		team.RoleArchitect,
		team.RoleDeveloper,
		team.RoleQA,
	}
	for _, role := range roles {
		list, err := ForRole(role, deps)
		if err != nil {
			t.Fatalf("ForRole(%s) failed: %v", role, err)
		}
		names := map[string]bool{}
		for _, tl := range list {
			names[tl.Name()] = true
		}
		if !names["search_text"] || !names["find_files"] {
			t.Fatalf("role %s missing search_text/find_files: %v", role, names)
		}
		if role == team.RoleProjectManager || role == team.RoleArchitect || role == team.RoleTechLead {
			if !names["github_pr_files"] {
				t.Fatalf("role %s missing github_pr_files", role)
			}
		}
	}
}
