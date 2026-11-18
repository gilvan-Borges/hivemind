package idecore

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
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

// RunTurn executes one user objective on a specific lane (team) with stack context injected.
func (m *SessionManager) RunTurn(
	parentCtx context.Context,
	laneID, workspace, teamName, objective, jiraKey, sessionID string,
) (orchestrator.Result, error) {
	cfg, err := team.Load(workspace)
	if err != nil {
		return orchestrator.Result{}, fmt.Errorf("carregue ou configure uma equipe primeiro (.hivemind/team.yaml): %w", err)
	}
	selected, err := cfg.ActiveTeam(teamName)
	if err != nil {
		return orchestrator.Result{}, err
	}

	stack := DetectStack(workspace)
	fullObjective := stack.FormatForChief() + "\n[Objetivo do Usuário]\n" + strings.TrimSpace(objective)

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

	runner := &orchestrator.Runner{
		Workspace: workspace,
		Project:   cfg,
		Team:      selected,
		Git:       g,
		GitHub:    gh,
		Jira:      jira,
		Sessions:  store,
		Out:       io.Discard,
		Err:       io.Discard,
		OnEvent: func(ev orchestrator.Event) {
			if m.Emit != nil {
				m.Emit(LaneEvent{LaneID: laneID, Team: selected.Name, Event: ev})
			}
		},
	}
	return runner.Run(ctx, orchestrator.Request{
		Objective: fullObjective,
		JiraKey:   jiraKey,
		SessionID: sessionID,
	})
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
