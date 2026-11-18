// Package skillx carrega o catálogo de skills do projeto (.agents/skills)
// para que o Chefe escolha as diretrizes certas ao delegar tarefas à equipe.
package skillx

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// SkillsDir é o catálogo de skills relativo à raiz do workspace.
const SkillsDir = ".agents/skills"

const (
	maxDescriptionBytes = 240
	// DefaultBodyBytes limita o corpo de cada skill injetada no prompt do executor.
	DefaultBodyBytes = 4 * 1024
	// DefaultTotalBytes limita o total de diretrizes de skills por agente.
	DefaultTotalBytes = 12 * 1024
)

// Skill é uma entrada do catálogo, lida de <root>/.agents/skills/<name>/SKILL.md.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
}

// Load lê <root>/.agents/skills/*/SKILL.md e devolve o catálogo ordenado por nome.
// Retorna nil quando o diretório não existe (catálogo vazio é estado normal).
func Load(root string) []Skill {
	entries, err := os.ReadDir(filepath.Join(root, SkillsDir))
	if err != nil {
		return nil
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(e.Name()))
		if name == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, SkillsDir, e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		out = append(out, Skill{
			Name:        name,
			Description: ExtractDescription(string(data)),
			Path:        filepath.ToSlash(filepath.Join(SkillsDir, e.Name(), "SKILL.md")),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ExtractDescription devolve a primeira frase/parágrafo curto do SKILL.md (sem títulos).
func ExtractDescription(md string) string {
	var parts []string
	for _, ln := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
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
	if len(parts) == 0 {
		return ""
	}
	return strx.TruncateBytes(strings.Join(parts, " "), maxDescriptionBytes, "…")
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

// Names devolve os nomes das skills na ordem recebida.
func Names(skills []Skill) []string {
	out := make([]string, 0, len(skills))
	for _, s := range skills {
		out = append(out, s.Name)
	}
	return out
}

// CatalogBlock formata o catálogo para o prompt do Chefe (uma linha por skill).
func CatalogBlock(catalog []Skill) string {
	if len(catalog) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SKILLS DO PROJETO (catálogo em %s):\n", SkillsDir)
	for _, s := range catalog {
		if s.Description == "" {
			fmt.Fprintf(&b, "- `%s` — sem descrição; leia `%s` antes de decidir.\n", s.Name, s.Path)
			continue
		}
		fmt.Fprintf(&b, "- `%s` — %s\n", s.Name, s.Description)
	}
	return b.String()
}

// GuidanceBlock monta o bloco de diretrizes das skills escolhidas para o prompt
// dos executores (dev/QA). Cada corpo é truncado em maxPerSkill bytes e o
// conjunto em maxTotal bytes (valores <= 0 usam os padrões), mantendo o consumo
// de tokens sob controle.
func GuidanceBlock(root string, skills []Skill, maxPerSkill, maxTotal int) string {
	if len(skills) == 0 {
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
	for _, s := range skills {
		remaining := maxTotal - total
		if remaining <= 0 {
			break
		}
		limit := maxPerSkill
		if limit > remaining {
			limit = remaining
		}
		body := strings.TrimSpace(s.ReadBody(root, limit))
		if body == "" {
			continue
		}
		fmt.Fprintf(&b, "\n\n[Skill `%s` selecionada pelo Chefe — siga estas diretrizes]\n%s\n(fonte: %s)", s.Name, body, s.Path)
		total += len(body)
	}
	return b.String()
}

// ReadBody devolve o conteúdo do SKILL.md truncado em maxBytes.
func (s Skill) ReadBody(root string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = DefaultBodyBytes
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(s.Path)))
	if err != nil {
		return ""
	}
	return strx.TruncateBytes(string(data), maxBytes, "\n…(skill truncada)")
}
