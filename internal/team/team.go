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

	// Permission modes inspired by Claude Code / Codex autonomy levels.
	PermissionGoverned   = "governed"    // Default: strict worktree/PR/review governance & confirmations
	PermissionAutoEdit   = "auto_edit"   // Local edits & allowlisted shell run without prompts; main merge still asks
	PermissionFullAccess = "full_access" // Full autonomy & permissions granted by the user in settings

	// Skill target scopes: restrict a skill to specific specialist roles or apply to everyone.
	SkillTargetAll   = "all"
	SkillTargetChief = "chief"
	SkillTargetDev   = "dev"
	SkillTargetQA    = "qa"
)

// SkillAssignment pins a skill (native or project) to specific specialist roles.
// Roles may contain "all" or any combination of "chief", "dev", and "qa".
type SkillAssignment struct {
	Name   string   `yaml:"name" json:"name"`
	Source string   `yaml:"source,omitempty" json:"source,omitempty"` // "native" | "project" | ""
	Roles  []string `yaml:"roles,omitempty" json:"roles,omitempty"`
}

// File is the on-disk project configuration. Several teams can share one workspace.
type File struct {
	Version  int      `yaml:"version" json:"version"`
	Active   string   `yaml:"active" json:"active"`
	Settings Settings `yaml:"settings" json:"settings"`
	Teams    []Team   `yaml:"teams" json:"teams"`
}

// AutoReviewSettings configures automatic background PR review on the IDE Flow tab.
// Automatic reviews NEVER merge pull requests (enforced as a hard tool gate).
type AutoReviewSettings struct {
	Enabled             bool   `yaml:"enabled" json:"enabled"`
	PollIntervalSeconds int    `yaml:"poll_interval_seconds,omitempty" json:"poll_interval_seconds,omitempty"`
	RunTests            *bool  `yaml:"run_tests,omitempty" json:"run_tests,omitempty"`
	ReviewAction        string `yaml:"review_action,omitempty" json:"review_action,omitempty"` // "full_review" | "comment_only"
	IncludeDrafts       bool   `yaml:"include_drafts,omitempty" json:"include_drafts,omitempty"`
	MaxPRsPerHour       int    `yaml:"max_prs_per_hour,omitempty" json:"max_prs_per_hour,omitempty"`
	MaxTokensPerPR      int64  `yaml:"max_tokens_per_pr,omitempty" json:"max_tokens_per_pr,omitempty"`
	Model               string `yaml:"model,omitempty" json:"model,omitempty"`
}

// EffectivePollIntervalSeconds returns the bounded polling interval (default 120s, min 60s, max 3600s).
func (a AutoReviewSettings) EffectivePollIntervalSeconds() int {
	if a.PollIntervalSeconds <= 0 {
		return 120
	}
	if a.PollIntervalSeconds < 60 {
		return 60
	}
	if a.PollIntervalSeconds > 3600 {
		return 3600
	}
	return a.PollIntervalSeconds
}

// EffectiveRunTests reports whether automatic reviews may execute project tests in an isolated worktree (default true).
func (a AutoReviewSettings) EffectiveRunTests() bool {
	if a.RunTests == nil {
		return true
	}
	return *a.RunTests
}

// EffectiveReviewAction normalizes the review action ("full_review" or "comment_only").
func (a AutoReviewSettings) EffectiveReviewAction() string {
	switch strings.ToLower(strings.TrimSpace(a.ReviewAction)) {
	case "comment_only", "comment", "comments":
		return "comment_only"
	default:
		return "full_review"
	}
}

// EffectiveMaxPRsPerHour returns the hourly PR cap (default 6, range 1..30).
func (a AutoReviewSettings) EffectiveMaxPRsPerHour() int {
	if a.MaxPRsPerHour <= 0 {
		return 6
	}
	if a.MaxPRsPerHour > 30 {
		return 30
	}
	return a.MaxPRsPerHour
}

// EffectiveMaxTokensPerPR returns the per-PR token budget (default 600,000 when unset).
func (a AutoReviewSettings) EffectiveMaxTokensPerPR(fallback int64) int64 {
	if a.MaxTokensPerPR > 0 {
		return a.MaxTokensPerPR
	}
	if fallback > 0 {
		return fallback
	}
	return 600000
}

// Settings apply to every team in the project.
type Settings struct {
	PermissionMode           string             `yaml:"permission_mode,omitempty" json:"permission_mode,omitempty"`
	JiraEnabled              bool               `yaml:"jira_enabled" json:"jira_enabled"`
	JiraProject              string             `yaml:"jira_project,omitempty" json:"jira_project,omitempty"`
	Worktrees                *bool              `yaml:"worktrees,omitempty" json:"worktrees,omitempty"`
	AutoCommit               bool               `yaml:"auto_commit" json:"auto_commit"`
	AutoPR                   bool               `yaml:"auto_pr" json:"auto_pr"`
	HomologBranch            string             `yaml:"homolog_branch,omitempty" json:"homolog_branch,omitempty"`
	ProtectedBranches        []string           `yaml:"protected_branches,omitempty" json:"protected_branches,omitempty"`
	RequireMainMergeApproval *bool              `yaml:"require_main_merge_approval,omitempty" json:"require_main_merge_approval,omitempty"`
	MaxQALoops               int                `yaml:"max_qa_loops" json:"max_qa_loops"`
	MaxDynamicDevs           int                `yaml:"max_dynamic_devs,omitempty" json:"max_dynamic_devs,omitempty"`
	MaxLLMCalls              int                `yaml:"max_llm_calls" json:"max_llm_calls"`
	MaxDevCalls              int                `yaml:"max_dev_calls,omitempty" json:"max_dev_calls,omitempty"`
	MaxDevExploreCalls       int                `yaml:"max_dev_explore_calls,omitempty" json:"max_dev_explore_calls,omitempty"`
	MaxDevInputTokens        int64              `yaml:"max_dev_input_tokens,omitempty" json:"max_dev_input_tokens,omitempty"`
	MaxTokens                int64              `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
	InTurnKeepToolResults    int                `yaml:"in_turn_keep_tool_results,omitempty" json:"in_turn_keep_tool_results,omitempty"`
	ReviewModel              string             `yaml:"review_model,omitempty" json:"review_model,omitempty"`
	InlineModel              string             `yaml:"inline_model,omitempty" json:"inline_model,omitempty"`
	AutoReview               AutoReviewSettings `yaml:"auto_review,omitempty" json:"auto_review,omitempty"`
	BaseBranch               string             `yaml:"base_branch" json:"base_branch"`
	ShellAllowlist           []string           `yaml:"shell_allowlist,omitempty" json:"shell_allowlist,omitempty"`
	ConfirmShell             *bool              `yaml:"confirm_shell,omitempty" json:"confirm_shell,omitempty"`
	SkillAssignments         []SkillAssignment  `yaml:"skill_assignments,omitempty" json:"skill_assignments,omitempty"`
}

// DefaultSettings returns the recommended governance defaults:
// isolated worktree ON, Chief/Architect auto-commit ON, auto-PR ON, and governed permissions.
func DefaultSettings() Settings {
	wt := true
	confirm := true
	reqMain := true
	return Settings{
		PermissionMode:           PermissionGoverned,
		JiraEnabled:              true,
		Worktrees:                &wt,
		AutoCommit:               true,
		AutoPR:                   true,
		HomologBranch:            "homolog",
		ProtectedBranches:        []string{"main", "master", "production"},
		RequireMainMergeApproval: &reqMain,
		MaxQALoops:               3,
		MaxDynamicDevs:           5,
		MaxDevCalls:              80,
		MaxDevExploreCalls:       40,
		MaxDevInputTokens:        1_500_000,
		BaseBranch:               "main",
		ShellAllowlist:           append([]string(nil), DefaultAllowlist...),
		ConfirmShell:             &confirm,
	}
}

// NormalizePermissionMode canonicalizes user-provided permission mode strings.
func NormalizePermissionMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case PermissionFullAccess, "full", "full-access", "fullaccess", "bypass", "bypass_permissions", "total", "yolo":
		return PermissionFullAccess
	case PermissionAutoEdit, "auto-edit", "autoedit", "accept_edits", "acceptedits":
		return PermissionAutoEdit
	default:
		return PermissionGoverned
	}
}

// EffectiveMaxDynamicDevs returns how many parallel dev agents the Chief/Architect
// may dynamically spawn for a large task (default 5, range 1..6).
func (s Settings) EffectiveMaxDynamicDevs() int {
	if s.MaxDynamicDevs <= 0 {
		return 5
	}
	if s.MaxDynamicDevs > 6 {
		return 6
	}
	return s.MaxDynamicDevs
}

// EffectiveMaxDevCalls returns the hard per-round LLM call cap for a single
// developer agent (default 80, range 20..300). The orchestrator aborts the run
// with a clear error when a dev exceeds it without finishing, preventing
// token-burning loops like endless read/search spirals.
func (s Settings) EffectiveMaxDevCalls() int {
	if s.MaxDevCalls <= 0 {
		return 80
	}
	if s.MaxDevCalls < 20 {
		return 20
	}
	if s.MaxDevCalls > 300 {
		return 300
	}
	return s.MaxDevCalls
}

// EffectiveMaxDevExploreCalls returns the hard cap of consecutive read/search
// calls a dev may make before editing ANY file (default 40, range 8..120).
func (s Settings) EffectiveMaxDevExploreCalls() int {
	if s.MaxDevExploreCalls <= 0 {
		return 40
	}
	if s.MaxDevExploreCalls < 8 {
		return 8
	}
	if s.MaxDevExploreCalls > 120 {
		return 120
	}
	return s.MaxDevExploreCalls
}

// EffectiveMaxDevInputTokens returns the per-round input-token budget of a dev
// (default 1,500,000, range 200,000..10,000,000).
func (s Settings) EffectiveMaxDevInputTokens() int64 {
	if s.MaxDevInputTokens <= 0 {
		return 1_500_000
	}
	if s.MaxDevInputTokens < 200_000 {
		return 200_000
	}
	if s.MaxDevInputTokens > 10_000_000 {
		return 10_000_000
	}
	return s.MaxDevInputTokens
}

// EffectiveInTurnKeepToolResults returns how many recent tool outputs stay verbatim
// inside the active turn before older large tool outputs are compacted on the fly
// (default 6, range 2..20).
func (s Settings) EffectiveInTurnKeepToolResults() int {
	if s.InTurnKeepToolResults <= 0 {
		return 12
	}
	if s.InTurnKeepToolResults < 2 {
		return 2
	}
	if s.InTurnKeepToolResults > 20 {
		return 20
	}
	return s.InTurnKeepToolResults
}

// EffectivePermissionMode returns the normalized permission mode (default "governed").
func (s Settings) EffectivePermissionMode() string {
	return NormalizePermissionMode(s.PermissionMode)
}

// IsFullAccess reports whether the user granted full unrestricted access in settings.
func (s Settings) IsFullAccess() bool {
	return s.EffectivePermissionMode() == PermissionFullAccess
}

// EffectiveAllowlist returns "*" when FullAccess is enabled, or the configured allowlist.
func (s Settings) EffectiveAllowlist() []string {
	if s.IsFullAccess() {
		return []string{"*"}
	}
	if len(s.ShellAllowlist) == 0 {
		return append([]string(nil), DefaultAllowlist...)
	}
	return s.ShellAllowlist
}

// NeedsShellConfirm reports whether dangerous shell commands must ask for human confirmation.
func (s Settings) NeedsShellConfirm() bool {
	mode := s.EffectivePermissionMode()
	if mode == PermissionFullAccess || mode == PermissionAutoEdit {
		return false
	}
	if s.ConfirmShell != nil {
		return *s.ConfirmShell
	}
	return true
}

// IsHomologBranch reports whether branch is a homologation/staging target where the Chief
// may merge automatically once a reviewer has approved the PR.
func (s Settings) IsHomologBranch(branch string) bool {
	b := strings.ToLower(strings.TrimSpace(branch))
	if b == "" {
		return false
	}
	h := strings.ToLower(strings.TrimSpace(s.HomologBranch))
	if h == "" {
		h = "homolog"
	}
	return b == h || b == "homolog" || b == "homologacao" || b == "staging"
}

// IsProtectedBranch reports whether branch is main/master/production requiring explicit user permission.
func (s Settings) IsProtectedBranch(branch string) bool {
	b := strings.ToLower(strings.TrimSpace(branch))
	if b == "" {
		return true
	}
	list := s.ProtectedBranches
	if len(list) == 0 {
		list = []string{"main", "master", "production", "prod"}
	}
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), b) {
			return true
		}
	}
	return false
}

// NeedsMergePermission reports whether merging a PR into baseBranch requires explicit user approval.
// In full_access mode, user pre-authorized all operations. On homolog, Chief can merge once reviewer approved.
// On main (or any non-homolog branch when require_main_merge_approval is true), user permission is required.
func (s Settings) NeedsMergePermission(baseBranch string) bool {
	if s.IsFullAccess() {
		return false
	}
	if s.IsHomologBranch(baseBranch) {
		return false
	}
	if s.IsProtectedBranch(baseBranch) {
		if s.RequireMainMergeApproval != nil {
			return *s.RequireMainMergeApproval
		}
		return true
	}
	if s.RequireMainMergeApproval != nil {
		return *s.RequireMainMergeApproval
	}
	return true
}

// Team is one named group of agents.
type Team struct {
	Name        string  `yaml:"name" json:"name"`
	Description string  `yaml:"description,omitempty" json:"description,omitempty"`
	Agents      []Agent `yaml:"agents" json:"agents"`
}

// Agent is one specialist. Each agent picks its own model.
type Agent struct {
	Name         string `yaml:"name" json:"name"`
	Role         string `yaml:"role" json:"role"`
	Provider     string `yaml:"provider" json:"provider"`
	Model        string `yaml:"model" json:"model"`
	Instructions string `yaml:"instructions,omitempty" json:"instructions,omitempty"`
}

// Normalize fills defaults and canonical role names.
func (f *File) Normalize() {
	if f.Version == 0 {
		f.Version = 1
	}
	f.Settings.PermissionMode = NormalizePermissionMode(f.Settings.PermissionMode)
	if f.Settings.Worktrees == nil {
		wt := true
		f.Settings.Worktrees = &wt
	}
	if strings.TrimSpace(f.Settings.HomologBranch) == "" {
		f.Settings.HomologBranch = "homolog"
	}
	if len(f.Settings.ProtectedBranches) == 0 {
		f.Settings.ProtectedBranches = []string{"main", "master", "production"}
	}
	if f.Settings.RequireMainMergeApproval == nil {
		reqMain := true
		f.Settings.RequireMainMergeApproval = &reqMain
	}
	if f.Settings.MaxQALoops <= 0 {
		f.Settings.MaxQALoops = 3
	}
	if f.Settings.MaxDynamicDevs <= 0 {
		f.Settings.MaxDynamicDevs = 5
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
	f.Settings.SkillAssignments = NormalizeSkillAssignments(f.Settings.SkillAssignments)
	for i := range f.Teams {
		f.Teams[i].Name = strings.TrimSpace(f.Teams[i].Name)
		for j := range f.Teams[i].Agents {
			a := &f.Teams[i].Agents[j]
			a.Name = strings.TrimSpace(a.Name)
			a.Role = NormalizeRole(a.Role)
			a.Provider = NormalizeProvider(a.Provider, a.Model)
			a.Model = strings.TrimSpace(a.Model)
			if a.Provider == "jev" && (a.Model == "" || strings.EqualFold(a.Model, "jev") || strings.EqualFold(a.Model, "jev:auto") || strings.EqualFold(a.Model, "jev/auto")) {
				a.Model = "auto"
			}
			if strings.TrimSpace(a.Instructions) == "" {
				a.Instructions = DefaultInstructions(a.Role)
			}
		}
	}
	if f.Active == "" && len(f.Teams) == 1 {
		f.Active = f.Teams[0].Name
	}
}

// NormalizeSkillTarget canonicalizes a skill assignment role target.
func NormalizeSkillTarget(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case SkillTargetAll, "todos", "*":
		return SkillTargetAll
	case SkillTargetChief, "chefe", RoleArchitect, RoleProjectManager, RoleTechLead, "arquiteto", "pm":
		return SkillTargetChief
	case SkillTargetDev, RoleDeveloper, "desenvolvedor", "devs":
		return SkillTargetDev
	case SkillTargetQA, "revisor":
		return SkillTargetQA
	default:
		return ""
	}
}

// NormalizeSkillAssignments deduplicates, normalizes names/sources/roles, and drops empty entries.
func NormalizeSkillAssignments(in []SkillAssignment) []SkillAssignment {
	if len(in) == 0 {
		return nil
	}
	var out []SkillAssignment
	seenIdx := make(map[string]int, len(in))
	for _, item := range in {
		name := strings.ToLower(strings.TrimSpace(item.Name))
		if name == "" {
			continue
		}
		src := strings.ToLower(strings.TrimSpace(item.Source))
		if src != "native" && src != "project" {
			src = ""
		}
		roleSet := make(map[string]bool, 4)
		for _, r := range item.Roles {
			if norm := NormalizeSkillTarget(r); norm != "" {
				roleSet[norm] = true
			}
		}
		var roles []string
		if roleSet[SkillTargetAll] || (roleSet[SkillTargetChief] && roleSet[SkillTargetDev] && roleSet[SkillTargetQA]) {
			roles = []string{SkillTargetAll}
		} else {
			for _, candidate := range []string{SkillTargetChief, SkillTargetDev, SkillTargetQA} {
				if roleSet[candidate] {
					roles = append(roles, candidate)
				}
			}
		}
		if len(roles) == 0 {
			continue
		}
		key := src + ":" + name
		if idx, exists := seenIdx[key]; exists {
			out[idx].Roles = roles
			continue
		}
		seenIdx[key] = len(out)
		out = append(out, SkillAssignment{
			Name:   name,
			Source: src,
			Roles:  roles,
		})
	}
	return out
}

// SkillsForTarget returns all skill assignments that apply to target ("chief", "dev", or "qa").
func (s Settings) SkillsForTarget(target string) []SkillAssignment {
	norm := NormalizeSkillTarget(target)
	if norm == "" {
		return nil
	}
	var out []SkillAssignment
	for _, a := range s.SkillAssignments {
		for _, r := range a.Roles {
			nr := NormalizeSkillTarget(r)
			if nr == SkillTargetAll || nr == norm {
				out = append(out, a)
				break
			}
		}
	}
	return out
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
		if strings.EqualFold(strings.TrimSpace(a.Name), "jev") {
			return fmt.Errorf("nome de agente %q é reservado para o roteador Jev", a.Name)
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

// ValidateForRun requires a Chief (architect or project_manager), at least one
// developer (base template that can dynamically scale up to MaxDynamicDevs), and one QA.
// Optional roles such as tech_lead remain supported when present in team.yaml.
func (t Team) ValidateForRun() error {
	if err := t.Validate(); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, a := range t.Agents {
		counts[NormalizeRole(a.Role)]++
	}
	if counts[RoleArchitect] == 0 && counts[RoleProjectManager] == 0 {
		return fmt.Errorf("o time %s precisa de um Chefe com papel %s ou %s", t.Name, RoleArchitect, RoleProjectManager)
	}
	if counts[RoleDeveloper] == 0 {
		return fmt.Errorf("o time %s precisa de ao menos um agente com papel %s", t.Name, RoleDeveloper)
	}
	if counts[RoleQA] == 0 {
		return fmt.Errorf("o time %s precisa de um agente com papel %s", t.Name, RoleQA)
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

// NormalizeProvider maps aliases and empty provider strings to the canonical
// provider id used by modelx.New.
func NormalizeProvider(provider, model string) string {
	rawProv := strings.ToLower(strings.TrimSpace(provider))
	if i := strings.IndexByte(rawProv, '@'); i > 0 {
		rawProv = rawProv[:i]
	}
	switch rawProv {
	case "agy", "antigravity":
		return "agy"
	case "opencode", "opencode-go", "opencode_go", "opencode-zen", "zen":
		return "opencode"
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
	case "openrouter":
		return "openrouter"
	case "cursor":
		return "cursor"
	case "jev":
		return "jev"
	case "":
		return inferProvider(model)
	default:
		return rawProv
	}
}

func inferProvider(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.IndexByte(m, '@'); i > 0 {
		prefix := m[:i]
		switch prefix {
		case "agy", "antigravity":
			return "agy"
		case "openrouter":
			return "openrouter"
		case "opencode", "opencode-go":
			return "opencode"
		case "cursor":
			return "cursor"
		case "gemini":
			return "gemini"
		case "anthropic":
			return "anthropic"
		case "openai":
			return "openai"
		}
	}
	switch {
	case m == "jev" || strings.HasPrefix(m, "jev:") || strings.HasPrefix(m, "jev/"):
		return "jev"
	case strings.HasPrefix(m, "openrouter:") || strings.HasPrefix(m, "openrouter/"):
		return "openrouter"
	case m == "cursor" || strings.HasPrefix(m, "cursor:") || strings.HasPrefix(m, "cursor/"):
		return "cursor"
	case m == "agy" || strings.HasPrefix(m, "agy:"):
		return "agy"
	case m == "opencode" || m == "opencode-go" ||
		strings.HasPrefix(m, "opencode:") || strings.HasPrefix(m, "opencode/") ||
		strings.HasPrefix(m, "opencode-go:") || strings.HasPrefix(m, "opencode-go/") ||
		strings.HasPrefix(m, "zen:"):
		return "opencode"
	case strings.Contains(m, "/"):
		// Vendor/model IDs like "anthropic/claude-sonnet-4-5" come from OpenRouter.
		return "openrouter"
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
	"git", "gh", "go", "gofmt", "python", "python3", "pytest", "pip",
	"node", "npm", "npx", "pnpm", "yarn", "bun",
	"cargo", "rustc", "mvn", "gradle", "dotnet", "make", "cmake",
	"echo", "printf", "ls", "dir", "cat", "type", "pwd",
	"rg", "find",
}
