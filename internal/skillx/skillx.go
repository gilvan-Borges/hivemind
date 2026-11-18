// Package skillx carrega os catálogos de skills do projeto (.agents/skills)
// e nativos da IDE (%LOCALAPPDATA%\HiveMind\skills) para que o Chefe escolha
// as diretrizes certas ao delegar tarefas à equipe.
package skillx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// SkillsDir é o catálogo de skills relativo à raiz do workspace do projeto.
const SkillsDir = ".agents/skills"

const (
	maxDescriptionBytes = 240
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
}

// Load lê <root>/.agents/skills/*/SKILL.md e devolve o catálogo do projeto,
// ordenado por nome. Retorna nil quando o diretório não existe.
func Load(root string) []Skill {
	out := loadDir(filepath.Join(root, SkillsDir), SkillsDir, "project")
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LoadNative lê <dir>/<name>/SKILL.md, onde dir é a raiz do catálogo nativo
// da IDE (ex.: %LOCALAPPDATA%\HiveMind\skills). Retorna nil se inexistente.
func LoadNative(dir string) []Skill {
	out := loadDir(dir, "", "native")
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func loadDir(dir, relBase, source string) []Skill {
	entries, err := os.ReadDir(dir)
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
		out = append(out, Skill{
			Name:        name,
			Description: ExtractDescription(string(data)),
			Path:        rel,
			Source:      source,
			UpdatedAt:   info.ModTime(),
		})
	}
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

// CatalogBlock formata o catálogo combinado para o prompt do Chefe:
// primeiro as skills do projeto, depois as nativas da IDE (com data de atualização).
func CatalogBlock(project, native []Skill) string {
	if len(project) == 0 && len(native) == 0 {
		return ""
	}
	var b strings.Builder
	if len(project) > 0 {
		fmt.Fprintf(&b, "SKILLS DO PROJETO (%s):\n", SkillsDir)
		for _, s := range project {
			writeCatalogLine(&b, s)
		}
	}
	if len(native) > 0 {
		b.WriteString("SKILLS NATIVAS DA IDE (gerenciadas pelo painel Skills do app, mais fáceis de manter atualizadas):\n")
		for _, s := range native {
			writeCatalogLine(&b, s)
		}
	}
	return b.String()
}

func writeCatalogLine(b *strings.Builder, s Skill) {
	if s.Description == "" {
		if d := day(s.UpdatedAt); d != "" {
			fmt.Fprintf(b, "- `%s` — sem descrição; leia `%s` antes de decidir. (atualizada em %s)\n", s.Name, s.Path, d)
			return
		}
		fmt.Fprintf(b, "- `%s` — sem descrição; leia `%s` antes de decidir.\n", s.Name, s.Path)
		return
	}
	if d := day(s.UpdatedAt); d != "" {
		fmt.Fprintf(b, "- `%s` — %s (atualizada em %s)\n", s.Name, s.Description, d)
		return
	}
	fmt.Fprintf(b, "- `%s` — %s\n", s.Name, s.Description)
}

// GuidanceBlock monta o bloco de diretrizes das skills escolhidas para o prompt
// dos executores (dev/QA). Skills de projeto são lidas de projectRoot e nativas
// de nativeRoot. Cada corpo é truncado em maxPerSkill bytes e o conjunto em
// maxTotal bytes (valores <= 0 usam os padrões), mantendo o consumo sob controle.
func GuidanceBlock(projectRoot, nativeRoot string, project, native []Skill, maxPerSkill, maxTotal int) string {
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
