// Package skillx carrega os catálogos de skills do projeto (.agents/skills)
// e nativos da IDE (%LOCALAPPDATA%\HiveMind\skills) para que o Chefe escolha
// as diretrizes certas ao delegar tarefas à equipe.
package skillx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// SkillsDir é o catálogo principal de skills relativo à raiz do workspace do projeto.
const SkillsDir = ".agents/skills"

// HiveMindSkillsDir permite que o usuário também guarde skills no projeto dentro de .hivemind/skills/<nome>/SKILL.md
// (no estilo .claude/skills).
const HiveMindSkillsDir = ".hivemind/skills"

const (
	maxDescriptionBytes = 240
	// DefaultCatalogBudgetBytes limita o tamanho total do catálogo de skills injetado
	// no prompt de triagem para evitar explosão de tokens em workspaces com muitas skills
	// (padrão Codex skills_context_budget + progressive disclosure).
	DefaultCatalogBudgetBytes = 4 * 1024
	// DefaultBodyBytes limita o corpo de cada skill injetada no prompt do executor.
	DefaultBodyBytes = 4 * 1024
	// DefaultTotalBytes limita o total de diretrizes de skills por agente.
	DefaultTotalBytes = 12 * 1024
)

// Skill é uma entrada de catálogo, lida de <root>/SKILL.md.
// Para skills de projeto, Path é relativo à raiz do workspace;
// para skills nativas da IDE, Path é relativo ao diretório nativo.
type Skill struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Path        string    `json:"path"`
	Source      string    `json:"source,omitempty"` // "project" | "native"
	UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	// Enabled é false só para skills nativas desligadas (opt-in). Skills de projeto nascem ligadas.
	Enabled bool `json:"enabled"`
	// Triggers são keywords declaradas no frontmatter (`triggers:`). Quando
	// declaradas, o corpo completo só entra no prompt quando uma keyword casa
	// com o objetivo da execução; sem match, o guidance injeta apenas um stub
	// de 1 linha. Skills sem triggers mantêm o comportamento de corpo completo.
	Triggers []string `json:"triggers,omitempty"`
}

// HashBody is the SHA-256 hex the IDE stores when a person reviews a skill.
func HashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

type skillCacheEntry struct {
	stamp  string
	hash   string
	skills []Skill
}

var skillCache sync.Map

// Load lê <root>/.agents/skills/*/SKILL.md e <root>/.hivemind/skills/*/SKILL.md
// e devolve o catálogo do projeto, ordenado por nome e sem duplicatas.
func Load(root string) []Skill {
	out := loadDirCached(filepath.Join(root, SkillsDir), SkillsDir, "project")
	seen := make(map[string]bool, len(out))
	for _, s := range out {
		seen[s.Name] = true
	}
	for _, s := range loadDirCached(filepath.Join(root, filepath.FromSlash(HiveMindSkillsDir)), HiveMindSkillsDir, "project") {
		if !seen[s.Name] {
			seen[s.Name] = true
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LoadNative lê <dir>/<name>/SKILL.md, onde dir é a raiz do catálogo nativo
// da IDE (ex.: %LOCALAPPDATA%\HiveMind\skills). Retorna nil se inexistente.
func LoadNative(dir string) []Skill {
	out := loadDirCached(dir, "", "native")
	out = applySkillState(dir, out)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func loadDirCached(dir, relBase, source string) []Skill {
	stamp := skillDirStamp(dir)
	key := source + "\x00" + relBase + "\x00" + dir
	if v, ok := skillCache.Load(key); ok {
		e := v.(*skillCacheEntry)
		// shortcut: mtime+size iguais não relêem o SKILL.md; o hash do corpo só é recalculado quando o mtime muda. Para pegar um write que restaura o mtime antigo, hashear em toda chamada.
		if e.stamp == stamp {
			return cloneSkills(e.skills)
		}
	}
	skills, hash := loadDir(dir, relBase, source)
	if v, ok := skillCache.Load(key); ok {
		e := v.(*skillCacheEntry)
		if e.hash != "" && e.hash == hash {
			fresh := cloneSkills(skills)
			skillCache.Store(key, &skillCacheEntry{stamp: stamp, hash: hash, skills: fresh})
			return cloneSkills(fresh)
		}
	}
	stored := cloneSkills(skills)
	skillCache.Store(key, &skillCacheEntry{stamp: stamp, hash: hash, skills: stored})
	return cloneSkills(stored)
}

func skillDirStamp(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "missing"
	}
	var b strings.Builder
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s|%d|%d\n", e.Name(), info.Size(), info.ModTime().UnixNano())
	}
	if b.Len() == 0 {
		return "empty"
	}
	return b.String()
}

func cloneSkills(in []Skill) []Skill {
	if in == nil {
		return nil
	}
	out := make([]Skill, len(in))
	copy(out, in)
	return out
}

func loadDir(dir, relBase, source string) ([]Skill, string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ""
	}
	var out []Skill
	var parts []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(e.Name()))
		if name == "" {
			continue
		}
		abs := filepath.Join(dir, e.Name(), "SKILL.md")
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(e.Name(), "SKILL.md"))
		if relBase != "" {
			rel = relBase + "/" + rel
		}
		parts = append(parts, name+"\n"+HashBody(data))
		out = append(out, Skill{
			Name:        name,
			Description: ExtractDescription(string(data)),
			Path:        rel,
			Source:      source,
			UpdatedAt:   info.ModTime(),
			Enabled:     true,
			Triggers:     ExtractTriggers(string(data)),
		})
	}
	sort.Strings(parts)
	return out, HashBody([]byte(strings.Join(parts, "\n")))
}

// ExtractDescription devolve a primeira frase/parágrafo curto do corpo do SKILL.md (sem títulos),
// usando o campo YAML `description:` do frontmatter como fallback quando o corpo não tem parágrafo.
func ExtractDescription(md string) string {
	body := stripFrontmatter(md)
	var parts []string
	for _, ln := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			if len(parts) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(t, "#") {
			continue
		}
		parts = append(parts, t)
		if len(parts) >= 2 {
			break
		}
	}
	if len(parts) > 0 {
		return strx.TruncateBytes(strings.Join(parts, " "), maxDescriptionBytes, "…")
	}
	if fmDesc := extractFrontmatterDescription(md); fmDesc != "" {
		return strx.TruncateBytes(fmDesc, maxDescriptionBytes, "…")
	}
	return ""
}

func extractFrontmatterDescription(md string) string {
	s := strings.ReplaceAll(md, "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return ""
	}
	rest := s[4:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return ""
	}
	fm := rest[:idx]
	lines := strings.Split(fm, "\n")
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		low := strings.ToLower(trimmed)
		if !strings.HasPrefix(low, "description:") {
			continue
		}
		val := strings.TrimSpace(trimmed[len("description:"):])
		if val == ">-" || val == ">" || val == "|-" || val == "|" {
			var block []string
			for j := i + 1; j < len(lines); j++ {
				ln := lines[j]
				if len(ln) == 0 {
					continue
				}
				if ln[0] != ' ' && ln[0] != '\t' {
					break
				}
				if t := strings.TrimSpace(ln); t != "" {
					block = append(block, t)
				}
			}
			return strings.TrimSpace(strings.Join(block, " "))
		}
		val = strings.Trim(val, `"'`)
		return strings.TrimSpace(val)
	}
	return ""
}

func stripFrontmatter(md string) string {
	s := strings.ReplaceAll(md, "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return md
	}
	rest := s[4:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return md
	}
	after := rest[idx+4:]
	if strings.HasPrefix(after, "\n") {
		after = after[1:]
	}
	return after
}

// ExtractTriggers devolve as keywords declaradas no frontmatter `triggers:` do
// SKILL.md. Aceita lista inline (triggers: ["a", "b"]) e bullets
// (triggers:\n  - a\n  - b). Retorna nil quando o frontmatter não declara
// triggers — skills sem triggers mantêm injeção de corpo completo.
func ExtractTriggers(md string) []string {
	s := strings.ReplaceAll(md, "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return nil
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(raw string) {
		t := strings.ToLower(strings.Trim(strings.TrimSpace(raw), "\"'`"))
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	inTriggers := false
	for _, ln := range strings.Split(rest[:end], "\n") {
		t := strings.TrimSpace(ln)
		low := strings.ToLower(t)
		if !inTriggers {
			if strings.HasPrefix(low, "triggers:") {
				inTriggers = true
				inline := strings.TrimSpace(t[len("triggers:"):])
				if inline != "" {
					for _, part := range strings.Split(strings.Trim(inline, "[]"), ",") {
						add(part)
					}
					return out
				}
			}
			continue
		}
		if strings.HasPrefix(t, "-") {
			add(t[1:])
			continue
		}
		if t == "" {
			continue
		}
		break // próximo campo do frontmatter encerra a lista
	}
	return out
}

// MatchTrigger reports whether any declared trigger appears in text
// (case-insensitive substring match, o modelo de keyword routing). Skills sem
// triggers nunca casam — o chamador decide o fallback (corpo completo).
func MatchTrigger(s Skill, text string) bool {
	if len(s.Triggers) == 0 {
		return false
	}
	low := strings.ToLower(text)
	for _, tr := range s.Triggers {
		if tr != "" && strings.Contains(low, tr) {
			return true
		}
	}
	return false
}

// Match devolve as skills do catálogo cujos nomes foram pedidos (case-insensitive),
// na ordem pedida e sem duplicatas. Nomes desconhecidos são ignorados.
func Match(catalog []Skill, names []string) []Skill {
	if len(catalog) == 0 || len(names) == 0 {
		return nil
	}
	byName := make(map[string]Skill, len(catalog))
	for _, s := range catalog {
		byName[s.Name] = s
	}
	var out []Skill
	seen := make(map[string]bool)
	for _, raw := range names {
		n := strings.ToLower(strings.TrimSpace(raw))
		if n == "" || seen[n] {
			continue
		}
		s, ok := byName[n]
		if !ok {
			continue
		}
		seen[n] = true
		out = append(out, s)
	}
	return out
}

// Find devolve a skill do catálogo com o nome exato (case-insensitive) ou nil.
func Find(catalog []Skill, name string) *Skill {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, s := range catalog {
		if s.Name == n {
			cp := s
			return &cp
		}
	}
	return nil
}

// Names devolve os nomes das skills na ordem recebida.
func Names(skills []Skill) []string {
	out := make([]string, 0, len(skills))
	for _, s := range skills {
		out = append(out, s.Name)
	}
	return out
}

func day(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// CatalogBlock formata o catálogo combinado para o prompt do Chefe respeitando
// DefaultCatalogBudgetBytes (progressive disclosure + orçamento de contexto de skills).
func CatalogBlock(project, native []Skill) string {
	return CatalogBlockWithBudget(project, native, DefaultCatalogBudgetBytes)
}

// CatalogBlockWithBudget formata o catálogo de skills dentro de maxBytes.
// Se o catálogo completo exceder o orçamento, reduz progressivamente o tamanho das
// descrições (80 bytes), depois omite descrições (mantendo nome + caminho) e, se
// ainda exceder, trunca entradas excedentes com aviso explícito.
func CatalogBlockWithBudget(project, native []Skill, maxBytes int) string {
	if len(project) == 0 && len(native) == 0 {
		return ""
	}
	if maxBytes <= 0 {
		maxBytes = DefaultCatalogBudgetBytes
	}
	if full := renderCatalogBlock(project, native, 0, true); len(full) <= maxBytes {
		return full
	}
	if compact := renderCatalogBlock(project, native, 80, true); len(compact) <= maxBytes {
		return compact
	}
	minimal := renderCatalogBlock(project, native, 0, false)
	if len(minimal) <= maxBytes {
		return minimal
	}
	notice := "\n[Algumas skills foram omitidas para respeitar o orçamento de contexto de skills.]\n"
	limit := maxBytes - len(notice)
	if limit < 64 {
		limit = 64
	}
	return strx.TruncateBytes(minimal, limit, notice)
}

// ProgressiveDisclosureHint retorna diretrizes enxutas de higiene de contexto para
// uso econômico de skills (carregar somente o SKILL.md aplicável e referências diretas).
func ProgressiveDisclosureHint() string {
	return "HIGIENE DE CONTEXTO PARA SKILLS (PROGRESSIVE DISCLOSURE):\n" +
		"- Carregue apenas o SKILL.md das skills realmente aplicáveis à tarefa; não pré-carregue pastas references/, scripts/ ou assets/ não relacionadas.\n" +
		"- Evite perseguir referências em cascata: abra somente arquivos linkados diretamente pelo SKILL.md quando necessário, resolvendo caminhos relativos a partir do diretório do SKILL.md.\n"
}

func renderCatalogBlock(project, native []Skill, descLimit int, includeDesc bool) string {
	var b strings.Builder
	if len(project) > 0 {
		fmt.Fprintf(&b, "SKILLS DO PROJETO (%s):\n", SkillsDir)
		for _, s := range project {
			writeCatalogLineWithLimit(&b, s, descLimit, includeDesc)
		}
	}
	var optional []Skill
	if len(native) > 0 {
		b.WriteString("SKILLS NATIVAS DA IDE (gerenciadas pelo painel Skills do app, mais fáceis de manter atualizadas):\n")
		for _, s := range native {
			if !s.Enabled {
				optional = append(optional, s)
				continue
			}
			writeCatalogLineWithLimit(&b, s, descLimit, includeDesc)
		}
	}
	if len(optional) > 0 {
		b.WriteString("SKILLS OPCIONAIS DESLIGADAS (não selecione, a menos que o usuário tenha pedido explicitamente esta skill pelo nome):\n")
		for _, s := range optional {
			writeCatalogLineWithLimit(&b, s, descLimit, includeDesc)
		}
	}
	return b.String()
}

func writeCatalogLineWithLimit(b *strings.Builder, s Skill, descLimit int, includeDesc bool) {
	if !includeDesc {
		fmt.Fprintf(b, "- `%s` (`%s`)\n", s.Name, s.Path)
		return
	}
	desc := s.Description
	if descLimit > 0 && len(desc) > descLimit {
		desc = strx.TruncateBytes(desc, descLimit, "…")
	}
	if desc == "" {
		if d := day(s.UpdatedAt); d != "" {
			fmt.Fprintf(b, "- `%s` — sem descrição; leia `%s` antes de decidir. (atualizada em %s)\n", s.Name, s.Path, d)
			return
		}
		fmt.Fprintf(b, "- `%s` — sem descrição; leia `%s` antes de decidir.\n", s.Name, s.Path)
		return
	}
	if d := day(s.UpdatedAt); d != "" {
		fmt.Fprintf(b, "- `%s` — %s (atualizada em %s)\n", s.Name, desc, d)
		return
	}
	fmt.Fprintf(b, "- `%s` — %s\n", s.Name, desc)
}

// GuidanceBlock monta o bloco de diretrizes das skills escolhidas para o prompt
// dos executores (dev/QA). Skills de projeto são lidas de projectRoot e nativas
// de nativeRoot. Cada corpo é truncado em maxPerSkill bytes e o conjunto em
// maxTotal bytes (valores <= 0 usam os padrões), mantendo o consumo sob controle.
//
// Skills que declaram `triggers:` no frontmatter seguem injeção condicional:
// o corpo completo entra apenas quando uma keyword casa com o objetivo da
// execução; sem match, o bloco carrega só um stub de 1 linha apontando o
// caminho da skill, poupando tokens por turno. Skills sem triggers mantêm o
// corpo completo (comportamento anterior).
func GuidanceBlock(objective, projectRoot, nativeRoot string, project, native []Skill, maxPerSkill, maxTotal int) string {
	if len(project) == 0 && len(native) == 0 {
		return ""
	}
	if maxPerSkill <= 0 {
		maxPerSkill = DefaultBodyBytes
	}
	if maxTotal <= 0 {
		maxTotal = DefaultTotalBytes
	}
	var b strings.Builder
	total := 0
	appendBody := func(root string, s Skill) bool {
		if len(s.Triggers) > 0 && !MatchTrigger(s, objective) {
			desc := s.Description
			if desc == "" {
				desc = "guia declarado por esta skill"
			}
			fmt.Fprintf(&b, "\n\n[Skill `%s` disponível — %s. Não injetada porque o objetivo não casa com os triggers; leia `%s` com read_file apenas se a tarefa passar a exigir.]", s.Name, desc, s.Path)
			return true
		}
		remaining := maxTotal - total
		if remaining <= 0 {
			return false
		}
		limit := maxPerSkill
		if limit > remaining {
			limit = remaining
		}
		body := strings.TrimSpace(s.ReadBody(root, limit))
		if body == "" {
			return true
		}
		fmt.Fprintf(&b, "\n\n[Skill `%s` selecionada pelo Chefe — siga estas diretrizes]\n%s\n(fonte: %s)", s.Name, body, s.Path)
		total += len(body)
		return true
	}
	for _, s := range project {
		if !appendBody(projectRoot, s) {
			break
		}
	}
	for _, s := range native {
		if !appendBody(nativeRoot, s) {
			break
		}
	}
	return b.String()
}

// ReadBody devolve o conteúdo do SKILL.md truncado em maxBytes.
func (s Skill) ReadBody(root string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = DefaultBodyBytes
	}
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(s.Path)))
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+8))
	if err != nil {
		return ""
	}
	return strx.TruncateBytes(string(data), maxBytes, "\n…(skill truncada)")
}
