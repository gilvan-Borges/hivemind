package skillx

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed seeds/*.md
var seedFS embed.FS

// ProductSkills são as skills de ferramenta semeadas no catálogo nativo.
// untangle-codebase entra no disco, mas nasce desligada.
var ProductSkills = []string{
	"ponytail",
	"improve-codebase-architecture",
	"untangle-codebase",
	"vps-segura",
	"revisar-e-testar",
}

const optionalSkill = "untangle-codebase"

type skillState struct {
	Disabled []string `json:"disabled"`
}

// UserSkillsDir é o catálogo nativo compartilhado pela IDE e pela CLI.
// No Windows segue %LOCALAPPDATA%\HiveMind\skills (o mesmo caminho de sempre).
// Sem essas variáveis, cai em UserConfigDir/HiveMind/skills.
func UserSkillsDir() string {
	if base := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); base != "" {
		return filepath.Join(base, "HiveMind", "skills")
	}
	if base := strings.TrimSpace(os.Getenv("USERPROFILE")); base != "" {
		return filepath.Join(base, "AppData", "Local", "HiveMind", "skills")
	}
	if base, err := os.UserConfigDir(); err == nil && strings.TrimSpace(base) != "" {
		return filepath.Join(base, "HiveMind", "skills")
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return filepath.Join("HiveMind", "skills")
	}
	return filepath.Join(home, ".hivemind", "skills")
}

// ProductSkillBody devolve o SKILL.md embutido no binário. O Cursor CLI não tem
// a ferramenta read_skill; a revisão cola este texto no prompt.
func ProductSkillBody(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		return "", false
	}
	body, err := seedFS.ReadFile("seeds/" + name + ".md")
	if err != nil {
		return "", false
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "", false
	}
	return text, true
}

// SeedProductSkills grava as skills de produto que ainda não existem.
// Uma SKILL.md já presente não é reescrita. O estado inicial desliga untangle-codebase
// só quando o arquivo de estado ainda não existe.
func SeedProductSkills(dir string) (seeded []string, err error) {
	if strings.TrimSpace(dir) == "" {
		dir = UserSkillsDir()
	}
	for _, name := range ProductSkills {
		body, readErr := seedFS.ReadFile("seeds/" + name + ".md")
		if readErr != nil {
			return seeded, fmt.Errorf("ler skill embutida %s: %w", name, readErr)
		}
		dest := filepath.Join(dir, name, "SKILL.md")
		if existing, readExistingErr := os.ReadFile(dest); readExistingErr == nil {
			// Upgrade legacy or outdated revisar-e-testar seeds automatically while preserving user edits on other skills.
			if name == "revisar-e-testar" {
				cur := string(existing)
				if strings.Contains(cur, "Não é: mobile, extensão") ||
					!strings.Contains(cur, "Ambiente da Worktree") ||
					(strings.Contains(cur, "Proibido escrever `Superfícies:") &&
						(!strings.Contains(cur, "Pós-merge: atualizar a task no Jira") ||
							!strings.Contains(cur, "Mudanças de Frontend / Visual") ||
							!strings.Contains(cur, "uv run pytest") ||
							!strings.Contains(cur, "-Dstyle.color=never"))) {
					_ = os.WriteFile(dest, body, 0o644)
				}
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return seeded, fmt.Errorf("semear skill %s: %w", name, err)
		}
		if err := os.WriteFile(dest, body, 0o644); err != nil {
			return seeded, fmt.Errorf("semear skill %s: %w", name, err)
		}
		seeded = append(seeded, name)
	}
	statePath := filepath.Join(dir, "skill-state.json")
	if _, statErr := os.Stat(statePath); os.IsNotExist(statErr) {
		if err := writeState(statePath, skillState{Disabled: []string{optionalSkill}}); err != nil {
			return seeded, err
		}
	}
	return seeded, nil
}

// SetSkillEnabled liga ou desliga uma skill nativa. Desligar tira a skill do
// catálogo que o Chefe escolhe sozinho; o nome continua disponível para pedido explícito.
func SetSkillEnabled(dir, name string, enabled bool) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return fmt.Errorf("nome de skill vazio")
	}
	if strings.TrimSpace(dir) == "" {
		dir = UserSkillsDir()
	}
	if _, err := os.Stat(filepath.Join(dir, name, "SKILL.md")); err != nil {
		if _, seedErr := SeedProductSkills(dir); seedErr != nil {
			return seedErr
		}
	}
	if _, err := os.Stat(filepath.Join(dir, name, "SKILL.md")); err != nil {
		return fmt.Errorf("skill %q não está no catálogo nativo", name)
	}
	st := readState(filepath.Join(dir, "skill-state.json"))
	next := make([]string, 0, len(st.Disabled))
	seen := map[string]bool{}
	for _, raw := range st.Disabled {
		n := strings.ToLower(strings.TrimSpace(raw))
		if n == "" || n == name || seen[n] {
			continue
		}
		seen[n] = true
		next = append(next, n)
	}
	if !enabled {
		next = append(next, name)
	}
	return writeState(filepath.Join(dir, "skill-state.json"), skillState{Disabled: next})
}

func applySkillState(dir string, skills []Skill) []Skill {
	disabled := map[string]bool{}
	for _, name := range readState(filepath.Join(dir, "skill-state.json")).Disabled {
		disabled[strings.ToLower(strings.TrimSpace(name))] = true
	}
	for i := range skills {
		skills[i].Enabled = !disabled[skills[i].Name]
	}
	return skills
}

func readState(path string) skillState {
	data, err := os.ReadFile(path)
	if err != nil {
		return skillState{}
	}
	var st skillState
	if json.Unmarshal(data, &st) != nil {
		return skillState{}
	}
	return st
}

func writeState(path string, st skillState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
