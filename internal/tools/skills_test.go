package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func writeSKILLMD(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadSkillToolResolvesProjectAndNative(t *testing.T) {
	projectRoot := t.TempDir()
	nativeRoot := t.TempDir()
	writeSKILLMD(t, filepath.Join(projectRoot, skillx.SkillsDir), "code-review", "# Code Review\n\nDiretriz completa do projeto.")
	writeSKILLMD(t, nativeRoot, "sdd-discovery", "# SDD\n\nRoteiro completo da entrevista.")

	project := skillx.Load(projectRoot)
	native := skillx.LoadNative(nativeRoot)
	if len(project) != 1 || len(native) != 1 {
		t.Fatalf("catálogos: project=%d native=%d", len(project), len(native))
	}
	lookup := NewSkillLookup(projectRoot, nativeRoot, project, native)

	role, err := ForRole(team.RoleDeveloper, Deps{Root: projectRoot, SkillLookup: lookup})
	if err != nil {
		t.Fatal(err)
	}
	var hasReadSkill bool
	for _, tl := range role {
		if tl.Name() == "read_skill" {
			hasReadSkill = true
		}
	}
	if !hasReadSkill {
		t.Fatal("dev com SkillLookup deveria receber read_skill")
	}

	// Sem lookup, a tool não é exposta.
	bare, err := ForRole(team.RoleQA, Deps{Root: projectRoot})
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range bare {
		if tl.Name() == "read_skill" {
			t.Fatal("sem SkillLookup, read_skill não deveria ser exposta")
		}
	}

	d := Deps{Root: projectRoot, SkillLookup: lookup}

	res, err := d.readSkill(readSkillArgs{Name: "code-review"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Diretriz completa do projeto.") || !strings.Contains(res.Text, "(fonte:") {
		t.Fatalf("corpo do projeto ausente: %q", res.Text)
	}

	res, err = d.readSkill(readSkillArgs{Name: "SDD-Discovery"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Roteiro completo da entrevista.") {
		t.Fatalf("corpo nativo ausente: %q", res.Text)
	}

	if _, err = d.readSkill(readSkillArgs{Name: "inexistente"}); err == nil {
		t.Fatal("skill inexistente deveria falhar")
	}
	if _, err = d.readSkill(readSkillArgs{Name: "   "}); err == nil {
		t.Fatal("nome vazio deveria falhar")
	}
}

func TestReadSkillPreferredNativeOverridesSameNamedProjectSkill(t *testing.T) {
	projectRoot := t.TempDir()
	nativeRoot := t.TempDir()
	writeSKILLMD(t, filepath.Join(projectRoot, skillx.SkillsDir), "shared", "# Shared\n\nVersão do projeto.")
	writeSKILLMD(t, nativeRoot, "shared", "# Shared\n\nVersão nativa aprovada.")

	project := skillx.Load(projectRoot)
	native := skillx.LoadNative(nativeRoot)
	lookup := NewSkillLookupWithPreferredNative(projectRoot, nativeRoot, project, native, native)

	d := Deps{Root: projectRoot, SkillLookup: lookup}
	res, err := d.readSkill(readSkillArgs{Name: "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "Versão nativa aprovada.") {
		t.Fatalf("esperava versão nativa preferida, veio: %q", res.Text)
	}
}
