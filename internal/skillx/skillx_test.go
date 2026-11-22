package skillx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func writeNative(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
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
	if catalog[0].Source != "project" {
		t.Fatalf("source errado: %q", catalog[0].Source)
	}
	if !strings.Contains(catalog[0].Description, "Revisa PRs") {
		t.Fatalf("descrição errada: %q", catalog[0].Description)
	}
	if catalog[0].Path != ".agents/skills/code-review/SKILL.md" {
		t.Fatalf("path relativo errado: %q", catalog[0].Path)
	}
	if catalog[0].UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt deveria vir do mtime do arquivo")
	}
}

func TestLoadNativeReadsDirectChildren(t *testing.T) {
	root := t.TempDir()
	writeNative(t, root, "Apple-Design", "# Apple Design\n\nDiretrizes HIG da Apple.")

	catalog := LoadNative(root)
	if len(catalog) != 1 {
		t.Fatalf("esperava 1 skill nativa, veio %d", len(catalog))
	}
	if catalog[0].Name != "apple-design" {
		t.Fatalf("nome deveria ser lowercase: %q", catalog[0].Name)
	}
	if catalog[0].Source != "native" {
		t.Fatalf("source errado: %q", catalog[0].Source)
	}
	if catalog[0].Path != "Apple-Design/SKILL.md" {
		t.Fatalf("path nativo errado: %q", catalog[0].Path)
	}
}

func TestLoadMissingDirIsEmpty(t *testing.T) {
	if got := Load(t.TempDir()); got != nil {
		t.Fatalf("esperava catálogo vazio, veio %v", Names(got))
	}
	if got := LoadNative(t.TempDir()); got != nil {
		t.Fatalf("esperava catálogo nativo vazio, veio %v", Names(got))
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

func TestFindExactName(t *testing.T) {
	catalog := []Skill{{Name: "apple-design", Description: "HIG"}}
	if Find(catalog, "Apple-Design") == nil {
		t.Fatal("Find deveria achar case-insensitive")
	}
	if Find(catalog, "outra") != nil {
		t.Fatal("Find não deveria achar nome inexistente")
	}
}

func TestCatalogBlockCombinesSourcesWithDate(t *testing.T) {
	project := []Skill{
		{Name: "code-review", Description: "Revisa PRs com foco em segurança.", Source: "project", UpdatedAt: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)},
	}
	native := []Skill{
		{Name: "apple-design", Description: "Diretrizes HIG da Apple.", Source: "native", UpdatedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)},
		{Name: "git-flow", Description: "", Source: "native"},
	}
	block := CatalogBlock(project, native)
	if !strings.Contains(block, "SKILLS DO PROJETO") || !strings.Contains(block, "SKILLS NATIVAS DA IDE") {
		t.Fatalf("bloco sem as duas seções: %q", block)
	}
	if !strings.Contains(block, "- `code-review` — Revisa PRs com foco em segurança. (atualizada em 2026-11-01)") {
		t.Fatalf("linha do projeto errada: %q", block)
	}
	if !strings.Contains(block, "(atualizada em 2026-11-18)") {
		t.Fatalf("data da nativa ausente: %q", block)
	}
	if !strings.Contains(block, "`git-flow` — sem descrição") {
		t.Fatalf("fallback ausente: %q", block)
	}
	if CatalogBlock(nil, nil) != "" {
		t.Fatal("catálogo vazio deveria devolver bloco vazio")
	}
}

func TestExtractTriggersParsesInlineAndBulletLists(t *testing.T) {
	inline := "---\nname: test_inline\ntriggers: [\"cache\", 'Prompt Caching']\n---\nbody\n"
	got := ExtractTriggers(inline)
	if len(got) != 2 || got[0] != "cache" || got[1] != "prompt caching" {
		t.Fatalf("inline triggers = %v", got)
	}

	bullets := "---\nname: test_bullets\ntriggers:\n  - Docker\n  - compose\nother: 1\n---\nbody\n"
	got = ExtractTriggers(bullets)
	if len(got) != 2 || got[0] != "docker" || got[1] != "compose" {
		t.Fatalf("bullet triggers = %v", got)
	}

	noFront := "# apenas corpo\n"
	if ExtractTriggers(noFront) != nil {
		t.Fatal("sem frontmatter não pode declarar triggers")
	}
	untagged := "---\nname: x\ndescription: y\n---\ncorpo\n"
	if ExtractTriggers(untagged) != nil {
		t.Fatal("frontmatter sem triggers deve devolver nil (corpo completo)")
	}
}

func TestMatchTriggerIsCaseInsensitiveSubstring(t *testing.T) {
	s := Skill{Name: "x", Triggers: []string{"docker", "compose"}}
	if !MatchTrigger(s, "Corrigir o arquivo DOCKER-COMPOSE.yml da homologação") {
		t.Fatal("trigger deve casar case-insensitive como substring")
	}
	if MatchTrigger(s, "refatorar o módulo de autenticação") {
		t.Fatal("sem keyword no texto não pode casar")
	}
	if MatchTrigger(Skill{Name: "sem-triggers"}, "qualquer texto docker") {
		t.Fatal("skill sem triggers nunca casa por si só")
	}
}

func TestGuidanceBlockTriggersInjectStubInsteadOfBody(t *testing.T) {
	root := t.TempDir()
	writeSkill := func(name, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, SkillsDir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, SkillsDir, name, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill("docker", "---\nname: docker\ntriggers: [\"docker\", \"compose\"]\n---\nGuia de containers.\n\nDiretrizes longas de containers que nao devem entrar sem match.\n")
	writeSkill("plain", "Diretrizes gerais de edição que sempre entram.\n")

	catalog := Load(root)
	if len(catalog) != 2 {
		t.Fatalf("catálogo = %d skills", len(catalog))
	}

	// Sem match: skill com triggers vira stub de 1 linha; skill sem triggers mantém corpo.
	got := GuidanceBlock("refatorar autenticação", root, "", catalog, nil, 0, 0)
	if strings.Contains(got, "Diretrizes longas de containers") {
		t.Fatalf("corpo de skill com triggers não pode entrar sem match:\n%s", got)
	}
	if !strings.Contains(got, "[Skill `docker` disponível") {
		t.Fatalf("stub da skill com triggers ausente:\n%s", got)
	}
	if !strings.Contains(got, "Diretrizes gerais de edição") {
		t.Fatalf("skill sem triggers deve manter corpo completo:\n%s", got)
	}

	// Com match: corpo completo entra.
	got = GuidanceBlock("rodar os containers com docker compose", root, "", catalog, nil, 0, 0)
	if !strings.Contains(got, "Diretrizes longas de containers") {
		t.Fatalf("trigger casado deve injetar o corpo completo:\n%s", got)
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

	got := GuidanceBlock("", root, "", catalog, nil, 1024, 2048)
	if got == "" {
		t.Fatal("bloco vazio")
	}
	if len(got) > 2048+400 { // margem para cabeçalhos dos blocos
		t.Fatalf("bloco passou do teto total: %d bytes", len(got))
	}
	if !strings.Contains(got, "[Skill `big`") {
		t.Fatal("skill big não apareceu no bloco")
	}
	if !strings.Contains(got, "skill truncada") {
		t.Fatal("corpo grande deveria ter sido truncado")
	}
}

func TestGuidanceBlockMixesProjectAndNativeRoots(t *testing.T) {
	projectRoot := t.TempDir()
	nativeRoot := t.TempDir()
	writeSkill(t, projectRoot, "review", "# Review\n\nDiretriz do projeto.")
	writeNative(t, nativeRoot, "review", "# Review\n\nDiretriz NATIVA mais atual.")

	project := Load(projectRoot)
	native := LoadNative(nativeRoot)
	got := GuidanceBlock("", projectRoot, nativeRoot, project, native, 0, 0)
	if !strings.Contains(got, "Diretriz do projeto.") {
		t.Fatalf("skill do projeto ausente: %q", got)
	}
	if !strings.Contains(got, "Diretriz NATIVA mais atual.") {
		t.Fatalf("skill nativa ausente: %q", got)
	}
}

func TestLoadCacheInvalidatesOnMtimeAndHash(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "code-review", "# Code Review\n\nRevisa PRs com foco em segurança.")
	first := Load(root)
	if len(first) != 1 || !strings.Contains(first[0].Description, "Revisa PRs") {
		t.Fatalf("primeira carga: %+v", first)
	}
	key := "project\x00" + SkillsDir + "\x00" + filepath.Join(root, SkillsDir)
	v, ok := skillCache.Load(key)
	if !ok {
		t.Fatal("cache não guardou o catálogo")
	}
	hash := v.(*skillCacheEntry).hash
	if hash == "" || hash != HashBody([]byte("code-review\n"+HashBody(mustReadSkill(t, root, "code-review")))) {
		t.Fatalf("hash revisado ausente ou diferente: %s", hash)
	}

	path := filepath.Join(root, SkillsDir, "code-review", "SKILL.md")
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	second := Load(root)
	if second[0].Description != first[0].Description {
		t.Fatalf("mesmo corpo não pode mudar a descrição: %q", second[0].Description)
	}
	v, _ = skillCache.Load(key)
	if v.(*skillCacheEntry).hash != hash {
		t.Fatal("mtime sem mudança de conteúdo alterou o hash revisado")
	}

	writeSkill(t, root, "code-review", "# Code Review\n\nDescrição nova depois da edição.")
	third := Load(root)
	if !strings.Contains(third[0].Description, "Descrição nova") {
		t.Fatalf("edição não invalidou o cache: %q", third[0].Description)
	}
	v, _ = skillCache.Load(key)
	if v.(*skillCacheEntry).hash == hash {
		t.Fatal("hash revisado deveria mudar com o corpo")
	}
}

func mustReadSkill(t *testing.T, root, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, SkillsDir, name, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
