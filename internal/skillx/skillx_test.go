package skillx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, SkillsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadParsesNameAndDescription(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "code-review", "# Code Review\n\nRevisa PRs com foco em segurança.\nSegunda linha ignorada.\n\n# Título posterior\nCorpo que não entra na descrição.")
	writeSkill(t, root, "git-flow", "# Git Flow\n\nFluxo de branches da equipe.")

	catalog := Load(root)
	if len(catalog) != 2 {
		t.Fatalf("esperava 2 skills, veio %d", len(catalog))
	}
	if catalog[0].Name != "code-review" || catalog[1].Name != "git-flow" {
		t.Fatalf("ordem inesperada: %v", Names(catalog))
	}
	if !strings.Contains(catalog[0].Description, "Revisa PRs") {
		t.Fatalf("descrição errada: %q", catalog[0].Description)
	}
	if strings.Contains(catalog[0].Description, "Corpo") {
		t.Fatalf("descrição deveria parar nas primeiras linhas: %q", catalog[0].Description)
	}
	if catalog[0].Path != ".agents/skills/code-review/SKILL.md" {
		t.Fatalf("path relativo errado: %q", catalog[0].Path)
	}
}

func TestLoadMissingDirIsEmpty(t *testing.T) {
	if got := Load(t.TempDir()); got != nil {
		t.Fatalf("esperava catálogo vazio, veio %v", Names(got))
	}
}

func TestMatchCaseInsensitiveUniqueOrdered(t *testing.T) {
	catalog := []Skill{
		{Name: "code-review"},
		{Name: "git-flow"},
	}
	got := Match(catalog, []string{"  Git-Flow ", "inexistente", "git-flow", "CODE-REVIEW"})
	if len(got) != 2 {
		t.Fatalf("esperava 2 matches, veio %v", Names(got))
	}
	if got[0].Name != "git-flow" || got[1].Name != "code-review" {
		t.Fatalf("ordem/dedup errados: %v", Names(got))
	}
}

func TestCatalogBlockListsSkills(t *testing.T) {
	catalog := []Skill{
		{Name: "code-review", Description: "Revisa PRs com foco em segurança."},
		{Name: "git-flow", Description: ""},
	}
	block := CatalogBlock(catalog)
	if !strings.Contains(block, "- `code-review` — Revisa PRs") {
		t.Fatalf("bloco sem a skill descrita: %q", block)
	}
	if !strings.Contains(block, "`git-flow` — sem descrição") {
		t.Fatalf("bloco sem fallback para skill sem descrição: %q", block)
	}
}

func TestGuidanceBlockTruncatesAndCaps(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("a \n", 4*1024) // ~8KB
	writeSkill(t, root, "big", "# Big\n\n"+big)
	writeSkill(t, root, "small", "# Small\n\nDiretriz curta.")

	catalog := Load(root)
	if len(catalog) != 2 {
		t.Fatalf("esperava 2 skills, veio %d", len(catalog))
	}

	got := GuidanceBlock(root, catalog, 1024, 2048)
	if got == "" {
		t.Fatal("bloco vazio")
	}
	if len(got) > 2048+400 { // margem para cabeçalhos dos blocos
		t.Fatalf("bloco passou do teto total: %d bytes", len(got))
	}
	if !strings.Contains(got, "skill `big`") && !strings.Contains(got, "[Skill `big`") {
		t.Fatal("skill big não apareceu no bloco")
	}
	if !strings.Contains(got, "skill truncada") {
		t.Fatal("corpo grande deveria ter sido truncado")
	}
}
