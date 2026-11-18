package idecore

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// WizardInput holds the interactive team builder choices from the IDE.
type WizardInput struct {
	TeamName       string            `json:"teamName"`
	ChiefRole      string            `json:"chiefRole"` // "architect" or "project_manager"
	DevCount       int               `json:"devCount"`
	MaxDynamicDevs int               `json:"maxDynamicDevs,omitempty"`
	DefaultModel   string            `json:"defaultModel"`
	AgentModels    map[string]string `json:"agentModels,omitempty"`
	PermissionMode string            `json:"permissionMode,omitempty"` // "governed", "auto_edit", "full_access"
	Worktrees      bool              `json:"worktrees"`
	AutoCommit     bool              `json:"autoCommit"`
	AutoPR         bool              `json:"autoPR"`
	JiraEnabled    bool              `json:"jiraEnabled"`
	JiraProject    string            `json:"jiraProject"`
	BaseBranch       string                 `json:"baseBranch"`
	HomologBranch    string                 `json:"homologBranch,omitempty"`
	MaxTokens        int64                  `json:"maxTokens"`
	SkillAssignments []team.SkillAssignment `json:"skillAssignments,omitempty"`
}

// BuildTeamFromWizard constructs a lean 3-agent team (Chefe + Dev + QA) by default
// with dynamic dev scaling (1..MaxDynamicDevs) controlled by the Chief/Architect,
// updating/creating .hivemind/team.yaml without modifying the project's root .gitignore.
func BuildTeamFromWizard(workspace string, in WizardInput) (team.File, error) {
	name := strings.TrimSpace(in.TeamName)
	if name == "" {
		name = "principal"
	}
	if in.DevCount < 1 {
		in.DevCount = 1
	}
	if in.DevCount > 6 {
		in.DevCount = 6
	}
	maxDyn := in.MaxDynamicDevs
	if maxDyn <= 0 {
		maxDyn = 5
	}
	if maxDyn > 6 {
		maxDyn = 6
	}
	defaultModel := strings.TrimSpace(in.DefaultModel)
	if defaultModel == "" {
		_, defaultModel = modelx.DefaultProviderModel()
	}

	pickModel := func(agentName string) string {
		if in.AgentModels != nil {
			if m := strings.TrimSpace(in.AgentModels[agentName]); m != "" {
				return m
			}
		}
		return defaultModel
	}

	chiefRole := team.RoleArchitect
	if strings.EqualFold(strings.TrimSpace(in.ChiefRole), team.RoleProjectManager) ||
		strings.Contains(strings.ToLower(in.ChiefRole), "gerente") {
		chiefRole = team.RoleProjectManager
	}

	agents := []team.Agent{
		{
			Name:     "chefe",
			Role:     chiefRole,
			Provider: team.NormalizeProvider("", pickModel("chefe")),
			Model:    pickModel("chefe"),
		},
	}
	for i := 1; i <= in.DevCount; i++ {
		devName := fmt.Sprintf("dev_%d", i)
		if in.DevCount == 1 {
			devName = "dev"
		}
		m := pickModel(devName)
		if m == defaultModel && devName != "dev" {
			m = pickModel("dev")
		}
		agents = append(agents, team.Agent{
			Name:     devName,
			Role:     team.RoleDeveloper,
			Provider: team.NormalizeProvider("", m),
			Model:    m,
		})
	}
	qaModel := pickModel("qa")
	agents = append(agents, team.Agent{
		Name:     "qa",
		Role:     team.RoleQA,
		Provider: team.NormalizeProvider("", qaModel),
		Model:    qaModel,
	})

	newTeam := team.Team{
		Name:   name,
		Agents: agents,
	}

	var file team.File
	if existing, err := team.Load(workspace); err == nil {
		file = existing
	} else {
		file.Settings = team.DefaultSettings()
	}
	if strings.TrimSpace(in.PermissionMode) != "" {
		file.Settings.PermissionMode = team.NormalizePermissionMode(in.PermissionMode)
	}
	file.Settings.MaxDynamicDevs = maxDyn
	wt := in.Worktrees
	file.Settings.Worktrees = &wt
	file.Settings.AutoCommit = in.AutoCommit
	file.Settings.AutoPR = in.AutoPR
	file.Settings.JiraEnabled = in.JiraEnabled
	file.Settings.JiraProject = strings.TrimSpace(in.JiraProject)
	if in.MaxTokens >= 0 {
		file.Settings.MaxTokens = in.MaxTokens
	}
	if strings.TrimSpace(in.BaseBranch) != "" {
		file.Settings.BaseBranch = strings.TrimSpace(in.BaseBranch)
	}
	if strings.TrimSpace(in.HomologBranch) != "" {
		file.Settings.HomologBranch = strings.TrimSpace(in.HomologBranch)
	}
	if in.SkillAssignments != nil {
		file.Settings.SkillAssignments = team.NormalizeSkillAssignments(in.SkillAssignments)
	}
	team.UpsertTeam(&file, newTeam)
	if err := team.Save(workspace, file); err != nil {
		return team.File{}, err
	}
	return file, nil
}

// SaveSkillAssignments persists per-role skill assignments into .hivemind/team.yaml.
func SaveSkillAssignments(workspace string, assignments []team.SkillAssignment) (team.File, error) {
	cfg, err := team.Load(workspace)
	if err != nil {
		return team.File{}, err
	}
	cfg.Settings.SkillAssignments = team.NormalizeSkillAssignments(assignments)
	if err := team.Save(workspace, cfg); err != nil {
		return team.File{}, err
	}
	return cfg, nil
}

// DeleteTeam removes teamName from .hivemind/team.yaml and saves the updated config.
func DeleteTeam(workspace, teamName string) (team.File, error) {
	cfg, err := team.Load(workspace)
	if err != nil {
		return team.File{}, err
	}
	if !team.RemoveTeam(&cfg, teamName) {
		return team.File{}, fmt.Errorf("equipe %q não encontrada", teamName)
	}
	if err := team.Save(workspace, cfg); err != nil {
		return team.File{}, err
	}
	return cfg, nil
}

// UpdateProjectPermissionMode updates settings.permission_mode ("governed", "auto_edit", "full_access") in .hivemind/team.yaml.
func UpdateProjectPermissionMode(workspace, permissionMode string) (team.File, error) {
	cfg, err := team.Load(workspace)
	if err != nil {
		return team.File{}, err
	}
	cfg.Settings.PermissionMode = team.NormalizePermissionMode(permissionMode)
	if err := team.Save(workspace, cfg); err != nil {
		return team.File{}, err
	}
	return cfg, nil
}

// LaneEvent wraps an orchestrator.Event with the IDE lane ID and team name.
type LaneEvent struct {
	LaneID string `json:"laneId"`
	Team   string `json:"team"`
	Event  orchestrator.Event `json:"event"`
}

// SessionManager tracks concurrent team runs across parallel IDE lanes.
type SessionManager struct {
	mu        sync.Mutex
	cancels   map[string]context.CancelFunc
	approvals map[string]chan bool
	Emit      func(LaneEvent)
}

// NewSessionManager creates a SessionManager.
func NewSessionManager(emit func(LaneEvent)) *SessionManager {
	return &SessionManager{
		cancels:   make(map[string]context.CancelFunc),
		approvals: make(map[string]chan bool),
		Emit:      emit,
	}
}

// ResolveApproval delivers the user's yes/no decision to a pending lane approval gate
// (such as merging an approved PR into main or running a protected command).
func (m *SessionManager) ResolveApproval(laneID string, approved bool) bool {
	m.mu.Lock()
	ch, ok := m.approvals[laneID]
	if ok {
		delete(m.approvals, laneID)
	}
	m.mu.Unlock()
	if ok && ch != nil {
		select {
		case ch <- approved:
		default:
		}
		return true
	}
	return false
}

// ImageInput carries a base64 or data-URL encoded image from the IDE chat.
type ImageInput struct {
	Name       string `json:"name"`
	MIMEType   string `json:"mimeType"`
	DataBase64 string `json:"dataBase64"`
}

// DecodeImageInputs converts frontend ImageInput items into orchestrator.Attachment blobs.
func DecodeImageInputs(inputs []ImageInput) ([]orchestrator.Attachment, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([]orchestrator.Attachment, 0, len(inputs))
	for i, in := range inputs {
		raw := strings.TrimSpace(in.DataBase64)
		mime := strings.TrimSpace(in.MIMEType)
		if strings.HasPrefix(raw, "data:") {
			if comma := strings.IndexByte(raw, ','); comma > 0 {
				header := raw[5:comma]
				if semi := strings.IndexByte(header, ';'); semi > 0 && mime == "" {
					mime = header[:semi]
				}
				raw = raw[comma+1:]
			}
		}
		if raw == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("imagem %d inválida: %w", i+1, err)
		}
		if mime == "" {
			mime = "image/png"
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = fmt.Sprintf("imagem_%d.png", i+1)
		}
		out = append(out, orchestrator.Attachment{
			Name:     name,
			MIMEType: mime,
			Data:     decoded,
		})
	}
	return out, nil
}

// UpdateTeamAgentModel updates the model of a specific agent inside teamName in .hivemind/team.yaml.
func UpdateTeamAgentModel(workspace, teamName, agentName, modelSpec string) (team.File, error) {
	cfg, err := team.Load(workspace)
	if err != nil {
		return team.File{}, err
	}
	spec := strings.TrimSpace(modelSpec)
	if spec == "" {
		return team.File{}, fmt.Errorf("informe o modelo")
	}
	updated := false
	isDynamicDev := strings.HasPrefix(strings.ToLower(strings.TrimSpace(agentName)), "dev_")
	for ti := range cfg.Teams {
		if teamName != "" && !strings.EqualFold(cfg.Teams[ti].Name, teamName) {
			continue
		}
		for ai := range cfg.Teams[ti].Agents {
			if strings.EqualFold(cfg.Teams[ti].Agents[ai].Name, agentName) {
				cfg.Teams[ti].Agents[ai].Model = spec
				cfg.Teams[ti].Agents[ai].Provider = team.NormalizeProvider("", spec)
				updated = true
			}
		}
		if !updated && isDynamicDev {
			for ai := range cfg.Teams[ti].Agents {
				if team.NormalizeRole(cfg.Teams[ti].Agents[ai].Role) == team.RoleDeveloper {
					cfg.Teams[ti].Agents[ai].Model = spec
					cfg.Teams[ti].Agents[ai].Provider = team.NormalizeProvider("", spec)
					updated = true
					break
				}
			}
		}
	}
	if !updated {
		return team.File{}, fmt.Errorf("agente %s não encontrado na equipe %s", agentName, teamName)
	}
	if err := team.Save(workspace, cfg); err != nil {
		return team.File{}, err
	}
	return cfg, nil
}

// EnsureWorkspaceMemory creates .hivemind/MEMORY.md with starter guidance if missing and returns its relative path.
func EnsureWorkspaceMemory(workspace string) (string, error) {
	rel := ".hivemind/MEMORY.md"
	target, err := ResolveSafe(workspace, rel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		starter := "# Memória Compartilhada do Time (HiveMind)\n\n" +
			"Anote aqui decisões arquiteturais, convenções de código, comandos específicos e restrições do projeto.\n" +
			"Todos os agentes da equipe recebem este contexto automaticamente sem precisar repetir no chat.\n\n" +
			"## Convenções & Arquitetura\n- \n\n" +
			"## Comandos & Ambiente\n- \n"
		if err := os.WriteFile(target, []byte(starter), 0o600); err != nil {
			return "", err
		}
	}
	return rel, nil
}

func loadWorkspaceMemorySnippet(workspaceDir string) string {
	target, err := ResolveSafe(workspaceDir, ".hivemind/MEMORY.md")
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return ""
	}
	txt := strings.TrimSpace(string(data))
	if txt == "" {
		return ""
	}
	txt = strx.TruncateBytes(txt, 4096, "\n…")
	return "\n[Memória Compartilhada do Time (.hivemind/MEMORY.md)]\n" + txt + "\n"
}

func expandAtFileMentions(workspaceRoot, userText string) string {
	if !strings.Contains(userText, "@") {
		return ""
	}
	seen := map[string]bool{}
	var b strings.Builder
	count := 0
	rs := []rune(userText)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '@' {
			continue
		}
		if i > 0 {
			prev := rs[i-1]
			if (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') || (prev >= '0' && prev <= '9') || prev == '_' {
				continue
			}
		}
		j := i + 1
		for j < len(rs) {
			c := rs[j]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' || c == '/' || c == '\\' {
				j++
			} else {
				break
			}
		}
		if j <= i+1 {
			continue
		}
		// Skip Ctrl+L inline selection snippets (@file:L10-L20 followed by code fence).
		if j < len(rs) && rs[j] == ':' {
			continue
		}
		candidate := strings.TrimRight(string(rs[i+1:j]), ".,")
		i = j - 1
		if candidate == "" || seen[candidate] {
			continue
		}
		full, err := workspace.ResolveWithOptions(workspaceRoot, filepath.FromSlash(candidate), workspace.Options{AllowMemoryRead: true})
		if err != nil {
			continue
		}
		info, err := os.Stat(full)
		if err != nil || info.IsDir() || info.Size() > 512*1024 {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		seen[candidate] = true
		snippet := strx.TruncateBytes(string(data), 16*1024, "\n…(arquivo truncado)")
		fmt.Fprintf(&b, "\n[Arquivo anexado @%s]\n```\n%s\n```\n", filepath.ToSlash(candidate), snippet)
		count++
		if count >= 4 {
			break
		}
	}
	return b.String()
}

// RunTurn executes one user objective on a specific lane (team) with stack context injected.
func (m *SessionManager) RunTurn(
	parentCtx context.Context,
	laneID, workspace, teamName, objective, jiraKey, sessionID string,
) (orchestrator.Result, error) {
	return m.RunTurnWithOptions(parentCtx, laneID, workspace, teamName, objective, jiraKey, sessionID, "auto", nil)
}

// RunTurnWithImages executes one user objective (and optional attached images) on a specific lane.
func (m *SessionManager) RunTurnWithImages(
	parentCtx context.Context,
	laneID, workspace, teamName, objective, jiraKey, sessionID string,
	images []ImageInput,
) (orchestrator.Result, error) {
	return m.RunTurnWithOptions(parentCtx, laneID, workspace, teamName, objective, jiraKey, sessionID, "auto", images)
}

// RunTurnWithOptions executes one user objective using an explicit RunMode ("auto", "direct", "plan", "squad")
// and automatically injects stack metadata + .hivemind/MEMORY.md shared team memory.
func (m *SessionManager) RunTurnWithOptions(
	parentCtx context.Context,
	laneID, workspace, teamName, objective, jiraKey, sessionID, runMode string,
	images []ImageInput,
) (orchestrator.Result, error) {
	cfg, err := team.Load(workspace)
	if err != nil {
		return orchestrator.Result{}, fmt.Errorf("carregue ou configure uma equipe primeiro (.hivemind/team.yaml): %w", err)
	}
	selected, err := cfg.ActiveTeam(teamName)
	if err != nil {
		return orchestrator.Result{}, err
	}

	attachments, err := DecodeImageInputs(images)
	if err != nil {
		return orchestrator.Result{}, err
	}

	userText := strings.TrimSpace(objective)
	if userText == "" && len(attachments) > 0 {
		userText = "Analise a(s) imagem(ns) anexada(s)."
	}
	stack := DetectStack(workspace)
	memBlock := loadWorkspaceMemorySnippet(workspace)
	atBlock := expandAtFileMentions(workspace, userText)
	fullObjective := stack.FormatForChief() + memBlock + atBlock + "\n[Objetivo do Usuário]\n" + userText

	ctx, cancel := context.WithCancel(parentCtx)
	m.mu.Lock()
	if prev, ok := m.cancels[laneID]; ok {
		prev()
	}
	m.cancels[laneID] = cancel
	m.mu.Unlock()

	defer func() {
		cancel()
		m.mu.Lock()
		delete(m.cancels, laneID)
		delete(m.approvals, laneID)
		m.mu.Unlock()
	}()

	store, err := sessionx.Open(workspace)
	if err != nil {
		return orchestrator.Result{}, err
	}

	var gh githubx.Client = githubx.Disabled{Reason: "sem remote origin"}
	g := gitx.Local{}
	if url, err := g.RemoteURL(ctx, workspace); err == nil {
		if owner, repo, ok := githubx.ParseRemote(url); ok {
			gh = githubx.Detect(owner, repo)
		}
	}

	var jira jirax.Client = jirax.Disabled{Reason: "Jira desabilitado"}
	if cfg.Settings.JiraEnabled {
		jira = jirax.FromEnv()
	}

	normMode := strings.ToLower(strings.TrimSpace(runMode))
	if normMode == "" {
		normMode = "auto"
	}

	askApproval := func(prompt string) bool {
		if cfg.Settings.IsFullAccess() {
			return true
		}
		if m.Emit == nil {
			return false
		}
		reply := make(chan bool, 1)
		m.mu.Lock()
		m.approvals[laneID] = reply
		m.mu.Unlock()

		m.Emit(LaneEvent{
			LaneID: laneID,
			Team:   selected.Name,
			Event: orchestrator.Event{
				Kind:   "approval_required",
				Stage:  "chief",
				Author: "chefe",
				Label:  "Permissão Necessária",
				Text:   prompt,
			},
		})
		select {
		case ok := <-reply:
			return ok
		case <-ctx.Done():
			return false
		}
	}

	nativeRoot := NativeSkillsDir()
	nativeSkills := skillx.LoadNative(nativeRoot)

	runner := &orchestrator.Runner{
		Workspace:          workspace,
		Project:            cfg,
		Team:               selected,
		Git:                g,
		GitHub:             gh,
		Jira:               jira,
		Sessions:           store,
		Confirm:            askApproval,
		Approve:            askApproval,
		Out:                io.Discard,
		Err:                io.Discard,
		EnableTriage:       normMode != "squad",
		EnableClosingChief: true,
		NativeSkills:       nativeSkills,
		NativeSkillsRoot:   nativeRoot,
		OnEvent: func(ev orchestrator.Event) {
			if m.Emit != nil {
				m.Emit(LaneEvent{LaneID: laneID, Team: selected.Name, Event: ev})
			}
		},
	}
	res, runErr := runner.Run(ctx, orchestrator.Request{
		Objective:   fullObjective,
		JiraKey:     jiraKey,
		SessionID:   sessionID,
		RunMode:     normMode,
		Attachments: attachments,
	})
	if runErr == nil && res.IntegrationBranch != "" {
		_, _ = SyncIntegrationToWorkspace(ctx, workspace)
	}
	return res, runErr
}

// Cancel stops the active run on laneID (cancelling only child processes started by that lane).
func (m *SessionManager) Cancel(laneID string) bool {
	m.ResolveApproval(laneID, false)
	m.mu.Lock()
	cancel, ok := m.cancels[laneID]
	m.mu.Unlock()
	if ok && cancel != nil {
		cancel()
		return true
	}
	return false
}

// CancelAll cancels all active lanes (used on IDE shutdown).
func (m *SessionManager) CancelAll() {
	m.mu.Lock()
	list := make([]context.CancelFunc, 0, len(m.cancels))
	for _, c := range m.cancels {
		list = append(list, c)
	}
	m.cancels = make(map[string]context.CancelFunc)
	m.mu.Unlock()
	for _, c := range list {
		c()
	}
}
