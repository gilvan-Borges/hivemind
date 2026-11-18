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
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// WizardInput holds the interactive team builder choices from the IDE.
type WizardInput struct {
	TeamName       string            `json:"teamName"`
	ChiefRole      string            `json:"chiefRole"` // "architect" or "project_manager"
	DevCount       int               `json:"devCount"`
	DefaultModel   string            `json:"defaultModel"`
	AgentModels    map[string]string `json:"agentModels,omitempty"`
	Worktrees      bool              `json:"worktrees"`
	AutoCommit     bool              `json:"autoCommit"`
	AutoPR         bool              `json:"autoPR"`
	JiraEnabled    bool              `json:"jiraEnabled"`
	JiraProject    string            `json:"jiraProject"`
	BaseBranch     string            `json:"baseBranch"`
	MaxTokens      int64             `json:"maxTokens"`
}

// BuildTeamFromWizard constructs a team.Team and updates/creates .hivemind/team.yaml
// without modifying the project's root .gitignore.
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
		{
			Name:     "tech_lead",
			Role:     team.RoleTechLead,
			Provider: team.NormalizeProvider("", pickModel("tech_lead")),
			Model:    pickModel("tech_lead"),
		},
	}
	if chiefRole != team.RoleArchitect {
		agents = append(agents, team.Agent{
			Name:     "arquiteto",
			Role:     team.RoleArchitect,
			Provider: team.NormalizeProvider("", pickModel("arquiteto")),
			Model:    pickModel("arquiteto"),
		})
	} else {
		agents = append(agents, team.Agent{
			Name:     "pm",
			Role:     team.RoleProjectManager,
			Provider: team.NormalizeProvider("", pickModel("pm")),
			Model:    pickModel("pm"),
		})
	}
	for i := 1; i <= in.DevCount; i++ {
		devName := fmt.Sprintf("dev_%d", i)
		if in.DevCount == 1 {
			devName = "dev"
		}
		m := pickModel(devName)
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
	}
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
	team.UpsertTeam(&file, newTeam)
	if err := team.Save(workspace, file); err != nil {
		return team.File{}, err
	}
	return file, nil
}

// LaneEvent wraps an orchestrator.Event with the IDE lane ID and team name.
type LaneEvent struct {
	LaneID string `json:"laneId"`
	Team   string `json:"team"`
	Event  orchestrator.Event `json:"event"`
}

// SessionManager tracks concurrent team runs across parallel IDE lanes.
type SessionManager struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	Emit    func(LaneEvent)
}

// NewSessionManager creates a SessionManager.
func NewSessionManager(emit func(LaneEvent)) *SessionManager {
	return &SessionManager{
		cancels: make(map[string]context.CancelFunc),
		Emit:    emit,
	}
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

	runner := &orchestrator.Runner{
		Workspace:          workspace,
		Project:            cfg,
		Team:               selected,
		Git:                g,
		GitHub:             gh,
		Jira:               jira,
		Sessions:           store,
		Out:                io.Discard,
		Err:                io.Discard,
		EnableTriage:       normMode != "squad",
		EnableClosingChief: true,
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
