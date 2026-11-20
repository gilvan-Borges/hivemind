package idecore

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
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
	TeamName         string                 `json:"teamName"`
	ChiefRole        string                 `json:"chiefRole"` // "architect" or "project_manager"
	DevCount         int                    `json:"devCount"`
	MaxDynamicDevs   int                    `json:"maxDynamicDevs,omitempty"`
	DefaultModel     string                 `json:"defaultModel"`
	AgentModels      map[string]string      `json:"agentModels,omitempty"`
	PermissionMode   string                 `json:"permissionMode,omitempty"` // "governed", "auto_edit", "full_access"
	ShellUnsandboxed bool                   `json:"shellUnsandboxed,omitempty"`
	Worktrees        bool                   `json:"worktrees"`
	AutoCommit       bool                   `json:"autoCommit"`
	AutoPR           bool                   `json:"autoPR"`
	JiraEnabled      bool                   `json:"jiraEnabled"`
	JiraProject      string                 `json:"jiraProject"`
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
	file.Settings.ShellUnsandboxed = in.ShellUnsandboxed
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

// EnableProjectJira marks settings.jira_enabled=true (and sets jira_project when empty and defaultProject is provided)
// if a team config already exists in workspace.
func EnableProjectJira(workspace, defaultProject string) {
	if strings.TrimSpace(workspace) == "" {
		return
	}
	cfg, err := team.Load(workspace)
	if err != nil {
		return
	}
	changed := false
	if !cfg.Settings.JiraEnabled {
		cfg.Settings.JiraEnabled = true
		changed = true
	}
	if strings.TrimSpace(cfg.Settings.JiraProject) == "" && strings.TrimSpace(defaultProject) != "" {
		cfg.Settings.JiraProject = strings.ToUpper(strings.TrimSpace(defaultProject))
		changed = true
	}
	if changed {
		_ = team.Save(workspace, cfg)
	}
}

// UpdateProjectJiraKey sets settings.jira_project in .hivemind/team.yaml (creating a starter team file if needed).
// Passing "NONE" disables Jira for this workspace; passing "*" queries all projects; passing "" resets to auto-match.
func UpdateProjectJiraKey(workspaceDir, jiraProjectKey string) (team.File, error) {
	cfg, err := team.Load(workspaceDir)
	if err != nil {
		return team.File{}, err
	}
	val := strings.ToUpper(strings.TrimSpace(jiraProjectKey))
	switch val {
	case "NONE", "OFF":
		cfg.Settings.JiraEnabled = false
		cfg.Settings.JiraProject = "NONE"
	default:
		cfg.Settings.JiraEnabled = true
		cfg.Settings.JiraProject = val
	}
	if err := team.Save(workspaceDir, cfg); err != nil {
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
	LaneID string             `json:"laneId"`
	Team   string             `json:"team"`
	Event  orchestrator.Event `json:"event"`
}

type pendingApproval struct {
	laneID string
	seq    uint64
	ch     chan bool
}

// SessionManager tracks concurrent team runs across parallel IDE lanes.
type SessionManager struct {
	mu          sync.Mutex
	cancels     map[string]context.CancelFunc
	approvals   map[string]pendingApproval
	approvalSeq uint64
	Emit        func(LaneEvent)
	// HostSignal wakes an opt-in listener. It must not run tools.
	HostSignal func(kind string)
}

func (m *SessionManager) noteHostSignal(kind string) {
	if m == nil || m.HostSignal == nil {
		return
	}
	m.HostSignal(kind)
}

// NewSessionManager creates a SessionManager.
func NewSessionManager(emit func(LaneEvent)) *SessionManager {
	return &SessionManager{
		cancels:   make(map[string]context.CancelFunc),
		approvals: make(map[string]pendingApproval),
		Emit:      emit,
	}
}

// ApprovalMetadata enriches an approval_required event for the UI and remote app.
type ApprovalMetadata struct {
	Label    string
	ToolName string
	Detail   string
	Gate     string
}

// RequestApproval emits an approval_required event with a unique approvalId and
// blocks until the user responds for that specific approvalId or ctx is canceled.
// Safe for concurrent tool calls on the same lane: each request gets its own slot.
func (m *SessionManager) RequestApproval(ctx context.Context, laneID, teamName, prompt string) bool {
	return m.RequestApprovalWithMeta(ctx, laneID, teamName, prompt, ApprovalMetadata{})
}

// RequestApprovalWithGate emits an approval_required event with an explicit gate
// identifier (e.g. "budget", "worktree") and waits for human resolution.
func (m *SessionManager) RequestApprovalWithGate(ctx context.Context, laneID, teamName, gate, prompt string) bool {
	return m.RequestApprovalWithMeta(ctx, laneID, teamName, prompt, ApprovalMetadata{Gate: gate})
}

// RequestApprovalWithMeta behaves like RequestApproval while attaching risk tier,
// tool name, and target detail so the IDE UI and app display what the agent wants to do.
func (m *SessionManager) RequestApprovalWithMeta(ctx context.Context, laneID, teamName, prompt string, meta ApprovalMetadata) bool {
	if m.Emit == nil {
		return false
	}
	reply := make(chan bool, 1)
	m.mu.Lock()
	m.approvalSeq++
	seq := m.approvalSeq
	approvalID := fmt.Sprintf("%s:appr:%d", laneID, seq)
	m.approvals[approvalID] = pendingApproval{
		laneID: laneID,
		seq:    seq,
		ch:     reply,
	}
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.approvals, approvalID)
		m.mu.Unlock()
	}()

	m.noteHostSignal("aprovacao")
	label := strings.TrimSpace(meta.Label)
	if label == "" {
		if meta.Gate == "budget" {
			label = "Aprovação de Orçamento (Jev)"
		} else if meta.Gate == "worktree" {
			label = "Aprovação de Worktree (Cursor)"
		} else {
			label = "Permissão Necessária"
		}
	}
	m.Emit(LaneEvent{
		LaneID: laneID,
		Team:   teamName,
		Event: orchestrator.Event{
			Kind:       "approval_required",
			Stage:      "chief",
			Author:     "chefe",
			Label:      label,
			ToolName:   meta.ToolName,
			Detail:     meta.Detail,
			Gate:       meta.Gate,
			Text:       prompt,
			ApprovalID: approvalID,
		},
	})
	select {
	case ok := <-reply:
		return ok
	case <-ctx.Done():
		return false
	}
}

// ResolveApproval delivers the user's yes/no decision to a pending approval gate.
// id may be the unique approvalId emitted in the approval_required event, or (for
// backwards compatibility) a laneID, in which case the oldest pending approval
// for that lane is resolved.
func (m *SessionManager) ResolveApproval(id string, approved bool) bool {
	m.mu.Lock()
	p, ok := m.approvals[id]
	targetKey := id
	if !ok {
		var oldestSeq uint64
		for k, cand := range m.approvals {
			if cand.laneID == id && (!ok || cand.seq < oldestSeq) {
				p = cand
				targetKey = k
				oldestSeq = cand.seq
				ok = true
			}
		}
	}
	if ok {
		delete(m.approvals, targetKey)
	}
	m.mu.Unlock()
	if ok && p.ch != nil {
		select {
		case p.ch <- approved:
		default:
		}
		return true
	}
	return false
}

func (m *SessionManager) denyLaneApprovalsLocked(laneID string) []chan bool {
	var channels []chan bool
	for k, p := range m.approvals {
		if laneID == "" || p.laneID == laneID {
			if p.ch != nil {
				channels = append(channels, p.ch)
			}
			delete(m.approvals, k)
		}
	}
	return channels
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

// EnsureWorkspaceMemory creates the external workspace MEMORY.md with starter guidance if missing
// (migrating any legacy <workspace>/.hivemind/MEMORY.md) and returns its virtual relative path.
func EnsureWorkspaceMemory(workspaceDir string) (string, error) {
	rel := ".hivemind/MEMORY.md"
	target := workspace.MemoryPath(workspaceDir)
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
	target := workspace.MemoryPath(workspaceDir)
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

// CleanupLegacyProjectState migrates any legacy .hivemind/MEMORY.md, .hivemind/sessions,
// and .hivemind/worktrees out of workspaceDir into the external user data dir, and prunes
// merged worktrees so the project tree only keeps .hivemind/team.yaml and .hivemind/skills.
func CleanupLegacyProjectState(ctx context.Context, workspaceDir string) {
	if strings.TrimSpace(workspaceDir) == "" {
		return
	}
	_ = workspace.MemoryPath(workspaceDir)
	_ = workspace.SessionsDir(workspaceDir)
	_ = workspace.AutoReviewStatePath(workspaceDir)
	_ = workspace.LogsDir(workspaceDir)
	g := gitx.Local{}
	gitx.CleanupLegacyInRepoWorktrees(ctx, g, workspaceDir)
	var baseBranch string
	if cfg, err := team.Load(workspaceDir); err == nil {
		baseBranch = cfg.Settings.BaseBranch
	}
	gitx.CleanupMergedWorktrees(ctx, g, workspaceDir, baseBranch)
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
		candidate := strings.TrimRight(string(rs[i+1:j]), ".,")
		startLine, endLine := 0, 0
		endPos := j
		// Optional line range suffix: @file.go:L10-L20, @file.go:10-20, @file.go:L42, @file.go:42
		if j < len(rs) && rs[j] == ':' {
			k := j + 1
			for k < len(rs) {
				c := rs[k]
				if (c >= '0' && c <= '9') || c == 'L' || c == 'l' || c == '-' {
					k++
				} else {
					break
				}
			}
			spec := string(rs[j+1 : k])
			if s, e, ok := parseAtLineRange(spec); ok {
				startLine, endLine = s, e
				endPos = k
			} else {
				// Non-line colon suffix (e.g. URL scheme or label): keep legacy skip.
				i = j - 1
				continue
			}
		}
		i = endPos - 1
		// Skip injection when Ctrl+L already pasted an inline fenced snippet immediately after @file:L10-L20.
		if startLine > 0 && hasFollowingCodeFence(rs, endPos) {
			continue
		}
		dedupKey := fmt.Sprintf("%s:%d-%d", candidate, startLine, endLine)
		if candidate == "" || seen[dedupKey] {
			continue
		}
		full, err := workspace.ResolveWithOptions(workspaceRoot, filepath.FromSlash(candidate), workspace.Options{AllowMemoryRead: true})
		if err != nil {
			continue
		}
		info, err := os.Stat(full)
		if err != nil || info.IsDir() || info.Size() > 1024*1024 {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		seen[dedupKey] = true
		relSlash := filepath.ToSlash(candidate)
		if startLine > 0 {
			snippet, actualStart, actualEnd, total := sliceMentionLines(string(data), startLine, endLine)
			if snippet == "" {
				continue
			}
			label := fmt.Sprintf("@%s:L%d-L%d (de %d linhas)", relSlash, actualStart, actualEnd, total)
			fmt.Fprintf(&b, "\n[Trecho anexado %s]\n```\n%s\n```\n", label, snippet)
		} else {
			if info.Size() > 512*1024 {
				continue
			}
			snippet := strx.TruncateBytes(string(data), 16*1024, "\n…(arquivo truncado)")
			fmt.Fprintf(&b, "\n[Arquivo anexado @%s]\n```\n%s\n```\n", relSlash, snippet)
		}
		count++
		if count >= 6 {
			break
		}
	}
	return b.String()
}

func parseAtLineRange(spec string) (start, end int, ok bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0, 0, false
	}
	parts := strings.Split(spec, "-")
	if len(parts) > 2 {
		return 0, 0, false
	}
	parseNum := func(s string) (int, bool) {
		s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "L"), "l")
		if s == "" {
			return 0, false
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return 0, false
		}
		return n, true
	}
	s, ok := parseNum(parts[0])
	if !ok {
		return 0, 0, false
	}
	if len(parts) == 1 {
		return s, 0, true
	}
	e, ok := parseNum(parts[1])
	if !ok || e < s {
		return 0, 0, false
	}
	return s, e, true
}

func hasFollowingCodeFence(rs []rune, pos int) bool {
	k := pos
	for k < len(rs) && (rs[k] == ' ' || rs[k] == '\t' || rs[k] == '\r' || rs[k] == '\n') {
		k++
	}
	return k+2 < len(rs) && rs[k] == '`' && rs[k+1] == '`' && rs[k+2] == '`'
}

func sliceMentionLines(content string, startLine, endLine int) (snippet string, actualStart, actualEnd, total int) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total = len(lines)
	if total == 0 {
		return "", 0, 0, 0
	}
	if endLine <= 0 {
		// Single line reference (@file:L42): include +/- 10 lines of surrounding context.
		actualStart = startLine - 10
		actualEnd = startLine + 10
	} else {
		actualStart = startLine
		actualEnd = endLine
	}
	if actualStart < 1 {
		actualStart = 1
	}
	if actualStart > total {
		actualStart = total
	}
	if actualEnd > total {
		actualEnd = total
	}
	if actualEnd < actualStart {
		actualEnd = actualStart
	}
	const maxLines = 400
	if actualEnd-actualStart+1 > maxLines {
		actualEnd = actualStart + maxLines - 1
	}
	var b strings.Builder
	for i := actualStart; i <= actualEnd; i++ {
		fmt.Fprintf(&b, "L%d: %s\n", i, lines[i-1])
	}
	snippet = strx.TruncateBytes(strings.TrimRight(b.String(), "\n"), 16*1024, "\n…(trecho truncado)")
	return snippet, actualStart, actualEnd, total
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
	laneID, workspaceDir, teamName, objective, jiraKey, sessionID, runMode string,
	images []ImageInput,
) (orchestrator.Result, error) {
	cfg, err := team.Load(workspaceDir)
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
	stack := DetectStack(workspaceDir)
	memBlock := loadWorkspaceMemorySnippet(workspaceDir)
	atBlock := expandAtFileMentions(workspaceDir, userText)
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
		pending := m.denyLaneApprovalsLocked(laneID)
		m.mu.Unlock()
		for _, ch := range pending {
			select {
			case ch <- false:
			default:
			}
		}
	}()

	store, err := sessionx.Open(workspaceDir)
	if err != nil {
		return orchestrator.Result{}, err
	}

	var gh githubx.Client = githubx.Disabled{Reason: "sem remote origin"}
	g := gitx.Local{}
	if url, err := g.RemoteURL(ctx, workspaceDir); err == nil {
		if owner, repo, ok := githubx.ParseRemote(url); ok {
			gh = githubx.Detect(owner, repo)
		}
	}

	var jira jirax.Client = jirax.Disabled{Reason: "Jira desabilitado"}
	if cfg.Settings.JiraEnabled {
		jira = jirax.FromWorkspace(workspaceDir)
	} else if res := jirax.ResolveForWorkspace(workspaceDir); res.MatchMode != "isolated_none" {
		if cand := jirax.FromWorkspace(workspaceDir); cand.Enabled() {
			jira = cand
		}
	}

	normMode := strings.ToLower(strings.TrimSpace(runMode))
	if normMode == "" {
		normMode = "auto"
	}

	askApproval := func(prompt string) bool {
		if cfg.Settings.IsFullAccess() {
			return true
		}
		return m.RequestApproval(ctx, laneID, selected.Name, prompt)
	}

	nativeRoot := NativeSkillsDir()
	_, _ = skillx.SeedProductSkills(nativeRoot)
	nativeSkills := skillx.LoadNative(nativeRoot)
	mcpHost := mcpx.Start(ctx, workspaceDir)
	defer mcpHost.Close()

	var routedMu sync.Mutex
	routedModels := map[string][2]string{}
	recordLaneEvent := func(ev orchestrator.Event) {
		if (ev.Kind == "model_route" || ev.Kind == "provider_failover") && ev.Author != "" && ev.Provider != "" {
			routedMu.Lock()
			routedModels[strings.ToLower(strings.TrimSpace(ev.Author))] = [2]string{ev.Provider, ev.Model}
			routedMu.Unlock()
		}
		if ev.Kind == "usage" && (ev.Tokens > 0 || ev.InputTokens > 0 || ev.OutputTokens > 0) {
			prov, mod := resolveAgentProviderModel(selected, ev.Author)
			routedMu.Lock()
			if rm, ok := routedModels[strings.ToLower(strings.TrimSpace(ev.Author))]; ok && rm[0] != "" {
				prov, mod = rm[0], rm[1]
			}
			routedMu.Unlock()
			modelx.RecordAIUsage(prov, mod, int64(ev.InputTokens), int64(ev.OutputTokens), int64(ev.Tokens))
		}
		if ev.Kind == "finish" {
			m.noteHostSignal("lane")
		}
		if m.Emit != nil {
			m.Emit(LaneEvent{LaneID: laneID, Team: selected.Name, Event: ev})
		}
	}
	budgetApprove := func(prompt string) bool {
		return m.RequestApprovalWithGate(ctx, laneID, selected.Name, "budget", prompt)
	}
	gateApprove := func(gate, prompt string) bool {
		return m.RequestApprovalWithGate(ctx, laneID, selected.Name, gate, prompt)
	}

	runner := &orchestrator.Runner{
		Workspace:          workspaceDir,
		Project:            cfg,
		Team:               selected,
		Git:                g,
		GitHub:             gh,
		Jira:               jira,
		Sessions:           store,
		Confirm:            askApproval,
		Approve:            askApproval,
		MCP:                mcpHost,
		Out:                io.Discard,
		Err:                io.Discard,
		EnableTriage:       true,
		EnableClosingChief: true,
		NativeSkills:       nativeSkills,
		NativeSkillsRoot:   nativeRoot,
		OnEvent:            recordLaneEvent,
	}
	runner.NewModel = orchestrator.NewJevModelFactory(orchestrator.JevFactoryOptions{
		Workspace:   workspaceDir,
		Git:         g,
		Approve:     budgetApprove,
		ApproveGate: gateApprove,
		OnEvent:     recordLaneEvent,
	})
	res, runErr := runner.Run(ctx, orchestrator.Request{
		Objective:   fullObjective,
		JiraKey:     jiraKey,
		SessionID:   sessionID,
		RunMode:     normMode,
		Attachments: attachments,
	})
	if runErr == nil && res.IntegrationBranch != "" && !res.PendingQA {
		// Never copy integration output over uncommitted user work. A dirty
		// workspace makes SyncIntegrationToWorkspace return ErrWorkspaceDirty
		// without touching a single file; the user is told what happened.
		n, syncErr := SyncIntegrationToWorkspace(ctx, workspaceDir)
		if syncErr == nil {
			// Once the delivery worktree is synced (or already pushed), clean up temporary worktrees.
			if len(res.PullRequests) > 0 {
				gitx.RemoveWorktree(ctx, g, workspaceDir, workspace.WorktreePath(workspaceDir, "_integracao"), "")
			}
			gitx.CleanupMergedWorktrees(ctx, g, workspaceDir, cfg.Settings.BaseBranch)
		}
		if m.Emit != nil {
			switch {
			case syncErr == nil && n > 0:
				m.Emit(LaneEvent{LaneID: laneID, Team: selected.Name, Event: orchestrator.Event{
					Kind:  "handoff",
					Stage: "chief",
					Label: "Integração sincronizada",
					Text:  fmt.Sprintf("%d arquivo(s) da integração foram trazidos para o workspace.", n),
				}})
			case errors.Is(syncErr, ErrWorkspaceDirty):
				m.Emit(LaneEvent{LaneID: laneID, Team: selected.Name, Event: orchestrator.Event{
					Kind:  "handoff",
					Stage: "chief",
					Label: "Integração não sincronizada",
					Text:  "O workspace tem alterações locais não commitadas; para não sobrescrever seu trabalho, a integração não foi trazida. Faça commit/stash e sincronize manualmente.",
				}})
			case syncErr != nil:
				m.Emit(LaneEvent{LaneID: laneID, Team: selected.Name, Event: orchestrator.Event{
					Kind:  "error",
					Stage: "chief",
					Label: "Falha ao sincronizar a integração",
					Text:  syncErr.Error(),
				}})
			}
		}
	}
	return res, runErr
}

func resolveAgentProviderModel(t team.Team, author string) (provider, modelSpec string) {
	low := strings.ToLower(strings.TrimSpace(author))
	for _, ag := range t.Agents {
		if strings.EqualFold(ag.Name, low) {
			return ag.Provider, ag.Model
		}
	}
	switch {
	case strings.Contains(low, "qa"):
		for _, ag := range t.Agents {
			if ag.Role == team.RoleQA {
				return ag.Provider, ag.Model
			}
		}
	case strings.Contains(low, "dev") || strings.Contains(low, "desenvolvedor"):
		for _, ag := range t.Agents {
			if ag.Role == team.RoleDeveloper {
				return ag.Provider, ag.Model
			}
		}
	case strings.Contains(low, "chief") || strings.Contains(low, "chefe") || strings.Contains(low, "arch") || strings.Contains(low, "arquiteto") || strings.Contains(low, "pm") || strings.Contains(low, "triage") || strings.Contains(low, "closing") || strings.Contains(low, "fecho"):
		for _, ag := range t.Agents {
			if ag.Role == team.RoleArchitect || ag.Role == team.RoleProjectManager || ag.Name == "chefe" {
				return ag.Provider, ag.Model
			}
		}
	}
	if len(t.Agents) > 0 {
		return t.Agents[0].Provider, t.Agents[0].Model
	}
	return "agy", "agy"
}

// RunAutoReviewTurn runs a single autonomous PR review in an isolated lane with
// hard merge/write blocks (DisableMerge=true, DisableGitWrite=true) and an optional
// ephemeral worktree (reviewRoot) for executing project tests without touching the
// user's active checkout.
func (m *SessionManager) RunAutoReviewTurn(
	parentCtx context.Context,
	laneID, workspaceDir, reviewRoot, teamName, objective string,
) (orchestrator.Result, error) {
	cfg, err := team.Load(workspaceDir)
	if err != nil {
		return orchestrator.Result{}, fmt.Errorf("carregue ou configure uma equipe primeiro (.hivemind/team.yaml): %w", err)
	}
	selected, err := cfg.ActiveTeam(teamName)
	if err != nil {
		return orchestrator.Result{}, err
	}

	stack := DetectStack(workspaceDir)
	memBlock := loadWorkspaceMemorySnippet(workspaceDir)
	fullObjective := stack.FormatForChief() + memBlock + "\n[Objetivo do Usuário]\n" + strings.TrimSpace(objective)

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
		pending := m.denyLaneApprovalsLocked(laneID)
		m.mu.Unlock()
		for _, ch := range pending {
			select {
			case ch <- false:
			default:
			}
		}
	}()

	store, err := sessionx.Open(workspaceDir)
	if err != nil {
		return orchestrator.Result{}, err
	}

	var gh githubx.Client = githubx.Disabled{Reason: "sem remote origin"}
	g := gitx.Local{}
	if url, err := g.RemoteURL(ctx, workspaceDir); err == nil {
		if owner, repo, ok := githubx.ParseRemote(url); ok {
			gh = githubx.Detect(owner, repo)
		}
	}

	var jira jirax.Client = jirax.Disabled{Reason: "Jira desabilitado"}
	if cfg.Settings.JiraEnabled {
		jira = jirax.FromWorkspace(workspaceDir)
	}

	// Never block waiting for human clicks during unattended auto-review: dangerous
	// shell commands are rejected automatically, while safe allowlisted test commands run.
	denyDangerous := func(string) bool { return false }

	nativeRoot := NativeSkillsDir()
	_, _ = skillx.SeedProductSkills(nativeRoot)
	nativeSkills := skillx.LoadNative(nativeRoot)
	mcpHost := mcpx.Start(ctx, workspaceDir)
	defer mcpHost.Close()

	var autoRoutedMu sync.Mutex
	autoRoutedModels := map[string][2]string{}
	recordAutoReviewEvent := func(ev orchestrator.Event) {
		if (ev.Kind == "model_route" || ev.Kind == "provider_failover") && ev.Author != "" && ev.Provider != "" {
			autoRoutedMu.Lock()
			autoRoutedModels[strings.ToLower(strings.TrimSpace(ev.Author))] = [2]string{ev.Provider, ev.Model}
			autoRoutedMu.Unlock()
		}
		if ev.Kind == "usage" && (ev.Tokens > 0 || ev.InputTokens > 0 || ev.OutputTokens > 0) {
			prov, mod := resolveAgentProviderModel(selected, ev.Author)
			autoRoutedMu.Lock()
			if rm, ok := autoRoutedModels[strings.ToLower(strings.TrimSpace(ev.Author))]; ok && rm[0] != "" {
				prov, mod = rm[0], rm[1]
			}
			autoRoutedMu.Unlock()
			modelx.RecordAIUsage(prov, mod, int64(ev.InputTokens), int64(ev.OutputTokens), int64(ev.Tokens))
		}
		if ev.Kind == "finish" {
			m.noteHostSignal("lane")
		}
		if m.Emit != nil {
			m.Emit(LaneEvent{LaneID: laneID, Team: selected.Name, Event: ev})
		}
	}

	runner := &orchestrator.Runner{
		Workspace:          workspaceDir,
		ReviewRoot:         reviewRoot,
		Project:            cfg,
		Team:               selected,
		Git:                g,
		GitHub:             gh,
		Jira:               jira,
		Sessions:           store,
		Confirm:            denyDangerous,
		Approve:            denyDangerous,
		MCP:                mcpHost,
		Out:                io.Discard,
		Err:                io.Discard,
		EnableTriage:       true,
		EnableClosingChief: false,
		NativeSkills:       nativeSkills,
		NativeSkillsRoot:   nativeRoot,
		OnEvent:            recordAutoReviewEvent,
	}
	runner.NewModel = orchestrator.NewJevModelFactory(orchestrator.JevFactoryOptions{
		Workspace: workspaceDir,
		Approve:   denyDangerous,
		OnEvent:   recordAutoReviewEvent,
	})
	return runner.Run(ctx, orchestrator.Request{
		Objective: fullObjective,
		RunMode:   "auto-review",
	})
}

// Cancel stops the active run on laneID (cancelling only child processes started by that lane).
func (m *SessionManager) Cancel(laneID string) bool {
	m.mu.Lock()
	pending := m.denyLaneApprovalsLocked(laneID)
	cancel, ok := m.cancels[laneID]
	m.mu.Unlock()
	for _, ch := range pending {
		select {
		case ch <- false:
		default:
		}
	}
	if ok && cancel != nil {
		cancel()
		return true
	}
	return len(pending) > 0
}

// CancelAll cancels all active lanes (used on IDE shutdown).
func (m *SessionManager) CancelAll() {
	m.mu.Lock()
	pending := m.denyLaneApprovalsLocked("")
	list := make([]context.CancelFunc, 0, len(m.cancels))
	for _, c := range m.cancels {
		list = append(list, c)
	}
	m.cancels = make(map[string]context.CancelFunc)
	m.mu.Unlock()
	for _, ch := range pending {
		select {
		case ch <- false:
		default:
		}
	}
	for _, c := range list {
		c()
	}
}
