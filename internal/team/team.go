// Package team loads and validates the project team stored in .hivemind/team.yaml.
package team

import (
	"fmt"
	"strings"
)

const (
	RoleProjectManager = "project_manager"
	RoleTechLead       = "tech_lead"
	RoleArchitect      = "architect"
	RoleDeveloper      = "developer"
	RoleQA             = "qa"
)

// File is the on-disk project configuration. Several teams can share one workspace.
type File struct {
	Version  int      `yaml:"version"`
	Active   string   `yaml:"active"`
	Settings Settings `yaml:"settings"`
	Teams    []Team   `yaml:"teams"`
}

// Settings apply to every team in the project.
type Settings struct {
	JiraEnabled    bool     `yaml:"jira_enabled"`
	JiraProject    string   `yaml:"jira_project,omitempty"`
	Worktrees      *bool    `yaml:"worktrees,omitempty"`
	AutoCommit     bool     `yaml:"auto_commit"`
	AutoPR         bool     `yaml:"auto_pr"`
	MaxQALoops     int      `yaml:"max_qa_loops"`
	MaxLLMCalls    int      `yaml:"max_llm_calls"`
	BaseBranch     string   `yaml:"base_branch"`
	ShellAllowlist []string `yaml:"shell_allowlist,omitempty"`
	ConfirmShell   *bool    `yaml:"confirm_shell,omitempty"`
}

// Team is one named group of agents.
type Team struct {
	Name        string  `yaml:"name"`
	Description string  `yaml:"description,omitempty"`
	Agents      []Agent `yaml:"agents"`
}

// Agent is one specialist. Each agent picks its own model.
type Agent struct {
	Name         string `yaml:"name"`
	Role         string `yaml:"role"`
	Provider     string `yaml:"provider"`
	Model        string `yaml:"model"`
	Instructions string `yaml:"instructions,omitempty"`
}

// Normalize fills defaults and canonical role names.
func (f *File) Normalize() {
	if f.Version == 0 {
		f.Version = 1
	}
	if f.Settings.MaxQALoops <= 0 {
		f.Settings.MaxQALoops = 3
	}
	if strings.TrimSpace(f.Settings.BaseBranch) == "" {
		f.Settings.BaseBranch = "main"
	}
	if f.Settings.ConfirmShell == nil {
		yes := true
		f.Settings.ConfirmShell = &yes
	}
	if len(f.Settings.ShellAllowlist) == 0 {
		f.Settings.ShellAllowlist = append([]string(nil), DefaultAllowlist...)
	}
	for i := range f.Teams {
		f.Teams[i].Name = strings.TrimSpace(f.Teams[i].Name)
		for j := range f.Teams[i].Agents {
			a := &f.Teams[i].Agents[j]
			a.Name = strings.TrimSpace(a.Name)
			a.Role = NormalizeRole(a.Role)
			a.Provider = NormalizeProvider(a.Provider, a.Model)
			a.Model = strings.TrimSpace(a.Model)
			if strings.TrimSpace(a.Instructions) == "" {
				a.Instructions = DefaultInstructions(a.Role)
			}
		}
	}
	if f.Active == "" && len(f.Teams) == 1 {
		f.Active = f.Teams[0].Name
	}
}

// ActiveTeam returns the selected team.
func (f File) ActiveTeam(name string) (Team, error) {
	if name == "" {
		name = f.Active
	}
	for _, t := range f.Teams {
		if t.Name == name {
			return t, nil
		}
	}
	if name == "" {
		return Team{}, fmt.Errorf("nenhum time ativo; rode `hivemind team create`")
	}
	return Team{}, fmt.Errorf("time %q não encontrado", name)
}

// Validate checks names, roles and models. It does not require a full squad,
// so a team can be edited incrementally.
func (t Team) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("o time precisa de um nome")
	}
	seen := map[string]struct{}{}
	for _, a := range t.Agents {
		if a.Name == "" {
			return fmt.Errorf("agente sem nome no time %s", t.Name)
		}
		if _, ok := seen[a.Name]; ok {
			return fmt.Errorf("nome de agente duplicado: %s", a.Name)
		}
		seen[a.Name] = struct{}{}
		if NormalizeRole(a.Role) == "" {
			return fmt.Errorf("papel desconhecido %q no agente %s", a.Role, a.Name)
		}
		if strings.TrimSpace(a.Model) == "" {
			return fmt.Errorf("agente %s precisa de um modelo", a.Name)
		}
	}
	return nil
}

// ValidateForRun requires one coordinator, one tech lead, one architect,
// at least one developer and one QA so the pipeline can execute.
func (t Team) ValidateForRun() error {
	if err := t.Validate(); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, a := range t.Agents {
		counts[NormalizeRole(a.Role)]++
	}
	required := []string{RoleProjectManager, RoleTechLead, RoleArchitect, RoleDeveloper, RoleQA}
	for _, role := range required {
		if counts[role] == 0 {
			return fmt.Errorf("o time %s precisa de um agente com papel %s", t.Name, role)
		}
	}
	return nil
}

// ByRole returns agents of role in file order.
func (t Team) ByRole(role string) []Agent {
	role = NormalizeRole(role)
	var out []Agent
	for _, a := range t.Agents {
		if NormalizeRole(a.Role) == role {
			out = append(out, a)
		}
	}
	return out
}

// NormalizeRole accepts English identifiers and a few CLI aliases.
func NormalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case RoleProjectManager, "pm", "gerente", "project-manager", "projectmanager":
		return RoleProjectManager
	case RoleTechLead, "tech", "lead", "tech-lead", "techlead":
		return RoleTechLead
	case RoleArchitect, "arq":
		return RoleArchitect
	case RoleDeveloper, "dev", "desenvolvedor":
		return RoleDeveloper
	case RoleQA, "quality", "teste", "test":
		return RoleQA
	default:
		return ""
	}
}

// NormalizeProvider picks a provider from an explicit value or the model id.
func NormalizeProvider(provider, model string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "agy", "antigravity":
		return "agy"
	case "gemini", "google":
		return "gemini"
	case "vertex":
		return "vertex"
	case "anthropic", "claude":
		return "anthropic"
	case "openai", "chatgpt":
		return "openai"
	case "openai-responses", "openai_responses":
		return "openai-responses"
	case "":
		return inferProvider(model)
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

func inferProvider(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case m == "agy" || strings.HasPrefix(m, "agy:"):
		return "agy"
	case strings.Contains(m, "claude"):
		return "anthropic"
	case strings.HasPrefix(m, "gpt-"), strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"), strings.HasPrefix(m, "chatgpt"):
		return "openai"
	default:
		return "gemini"
	}
}

// DefaultAllowlist is the set of executables the shell tool may run without
// being treated as unknown. Dangerous flag combinations still need confirmation.
var DefaultAllowlist = []string{
	"git", "go", "gofmt", "python", "python3", "pytest", "pip",
	"node", "npm", "npx", "pnpm", "yarn", "bun",
	"cargo", "rustc", "mvn", "gradle", "dotnet", "make", "cmake",
	"echo", "printf", "ls", "dir", "cat", "type", "pwd",
	"rg", "find",
}
