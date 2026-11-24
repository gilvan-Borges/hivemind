package skillx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedProductSkillsLeavesUntangleOff(t *testing.T) {
	dir := t.TempDir()
	seeded, err := SeedProductSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(seeded) != 5 {
		t.Fatalf("semeadas: %v", seeded)
	}
	vpsBody, err := os.ReadFile(filepath.Join(dir, "vps-segura", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(vpsBody), "StrictHostKeyChecking=yes") || strings.Contains(string(vpsBody), "StrictHostKeyChecking=no") {
		t.Fatal("vps-segura precisa manter a verificação do host")
	}
	if desc := Find(LoadNative(dir), "ponytail").Description; !strings.Contains(desc, "lite") {
		t.Fatalf("descrição do ponytail: %q", desc)
	}
	again, err := SeedProductSkills(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("segunda semeadura não pode reescrever: %v", again)
	}
	edited := filepath.Join(dir, "ponytail", "SKILL.md")
	if err := os.WriteFile(edited, []byte("# editada\n\nTexto da pessoa.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SeedProductSkills(dir); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(edited)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Texto da pessoa") {
		t.Fatal("edição do usuário foi sobrescrita")
	}
	catalog := LoadNative(dir)
	byName := map[string]Skill{}
	for _, s := range catalog {
		byName[s.Name] = s
	}
	if !byName["ponytail"].Enabled || !byName["improve-codebase-architecture"].Enabled || !byName["revisar-e-testar"].Enabled {
		t.Fatalf("skills leves deveriam estar ligadas: %+v", catalog)
	}
	if !strings.Contains(byName["revisar-e-testar"].Description, "mobile") {
		t.Fatalf("descrição de revisar-e-testar: %q", byName["revisar-e-testar"].Description)
	}
	if byName["untangle-codebase"].Enabled {
		t.Fatal("untangle-codebase deveria nascer desligada")
	}
	block := CatalogBlock(nil, catalog)
	if !strings.Contains(block, "OPCIONAIS DESLIGADAS") || !strings.Contains(block, "untangle-codebase") {
		t.Fatalf("catálogo:\n%s", block)
	}
	if err := SetSkillEnabled(dir, "untangle-codebase", true); err != nil {
		t.Fatal(err)
	}
	if !Find(LoadNative(dir), "untangle-codebase").Enabled {
		t.Fatal("ativar deveria ligar a skill")
	}
}

func TestExtractDescriptionSkipsFrontmatter(t *testing.T) {
	got := ExtractDescription("---\nname: ponytail\ndescription: ignorar\n---\n# Título\n\nModo lite no produto.\n")
	if !strings.Contains(got, "Modo lite") {
		t.Fatalf("descrição: %q", got)
	}
}
