// Package runtime provides the single unified bootstrap for CLI, TUI, headless serve,
// and IDE orchestration runners plus a persistent workspace-scoped MCP host pool (Spec 09 §3.3 / W3-04).
package runtime

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workerx"
)

// MCPPool maintains warm per-workspace MCP hosts across turns so the IDE and daemon
// avoid cold spawning servers on every run.
type MCPPool struct {
	mu      sync.Mutex
	hosts   map[string]*mcpx.Host
	startFn func(ctx context.Context, workspace string) *mcpx.Host
}

// NewMCPPool constructs a workspace-scoped MCP pool.
func NewMCPPool() *MCPPool {
	return &MCPPool{
		hosts:   make(map[string]*mcpx.Host),
		startFn: mcpx.Start,
	}
}

func poolKey(workspace string) string {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		ws = "."
	}
	if abs, err := filepath.Abs(ws); err == nil {
		ws = abs
	}
	return strings.ToLower(filepath.Clean(ws))
}

// Acquire returns the existing warm MCP host for workspace or starts one lazily.
func (p *MCPPool) Acquire(ctx context.Context, workspace string) *mcpx.Host {
	if p == nil {
		return mcpx.Start(ctx, workspace)
	}
	key := poolKey(workspace)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hosts == nil {
		p.hosts = make(map[string]*mcpx.Host)
	}
	if h, ok := p.hosts[key]; ok && h != nil {
		return h
	}
	start := p.startFn
	if start == nil {
		start = mcpx.Start
	}
	h := start(ctx, workspace)
	p.hosts[key] = h
	return h
}

// ReleaseWorkspace closes and removes the warm MCP host for workspace.
func (p *MCPPool) ReleaseWorkspace(workspace string) {
	if p == nil {
		return
	}
	key := poolKey(workspace)
	p.mu.Lock()
	h := p.hosts[key]
	delete(p.hosts, key)
	p.mu.Unlock()
	if h != nil {
		_ = h.Close()
	}
}

// CloseAll shuts down every pooled MCP host.
func (p *MCPPool) CloseAll() {
	if p == nil {
		return
	}
	p.mu.Lock()
	all := p.hosts
	p.hosts = make(map[string]*mcpx.Host)
	p.mu.Unlock()
	for _, h := range all {
		if h != nil {
			_ = h.Close()
		}
	}
}

// RunnerOptions configures unified construction of an orchestrator.Runner across CLI, TUI, serve, and IDE.
type RunnerOptions struct {
	Workspace          string
	LaneID             string
	ReviewRoot         string
	Project            team.File
	Team               team.Team
	Git                gitx.Git
	GitHub             githubx.Client
	Jira               jirax.Client
	Sessions           *sessionx.Store
	Confirm            shellx.Confirmer
	Approve            func(prompt string) bool
	ApproveGate        func(gate, prompt string) bool
	BudgetApprove      func(prompt string) bool
	MCP                *mcpx.Host
	MCPPool            *MCPPool
	NativeSkills       []skillx.Skill
	NativeSkillsRoot   string
	SkipSeedSkills     bool
	DisableMCP         bool
	Out                io.Writer
	Err                io.Writer
	Streaming          agent.StreamingMode
	EnableTriage       bool
	EnableClosingChief bool
	OnEvent            func(orchestrator.Event)
	ExternalWorkers    *workerx.AdapterRegistry
	Catalog            *jev.CatalogStore
}

// BuildRunner constructs a configured orchestrator.Runner together with a cleanup callback
// that releases transient resources (leaving pooled MCP hosts warm until workspace close).
func BuildRunner(ctx context.Context, opts RunnerOptions) (*orchestrator.Runner, func()) {
	ws := strings.TrimSpace(opts.Workspace)
	if ws == "" {
		ws = "."
	}
	g := opts.Git
	if g == nil {
		g = gitx.Local{}
	}
	gh := opts.GitHub
	if gh == nil && g.IsRepo(ctx, ws) {
		if u, err := g.RemoteURL(ctx, ws); err == nil {
			if owner, repo, ok := githubx.ParseRemoteForConfiguredHosts(u); ok {
				gh = githubx.Detect(owner, repo)
			}
		}
	}
	jira := opts.Jira
	if jira == nil && opts.Project.Settings.JiraEnabled {
		jira = jirax.FromWorkspace(ws)
	}

	nativeRoot := strings.TrimSpace(opts.NativeSkillsRoot)
	if nativeRoot == "" {
		nativeRoot = skillx.UserSkillsDir()
	}
	nativeSkills := opts.NativeSkills
	if nativeSkills == nil && !opts.SkipSeedSkills {
		_, _ = skillx.SeedProductSkills(nativeRoot)
		nativeSkills = skillx.LoadNative(nativeRoot)
	}

	var mcpHost *mcpx.Host
	var cleanup func()
	switch {
	case opts.MCP != nil:
		mcpHost = opts.MCP
	case opts.DisableMCP:
		mcpHost = nil
	case opts.MCPPool != nil:
		mcpHost = opts.MCPPool.Acquire(ctx, ws)
	default:
		mcpHost = mcpx.Start(ctx, ws)
		cleanup = func() { _ = mcpHost.Close() }
	}
	if cleanup == nil {
		cleanup = func() {}
	}

	budgetApprove := opts.BudgetApprove
	if budgetApprove == nil {
		budgetApprove = opts.Approve
	}

	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	errW := opts.Err
	if errW == nil {
		errW = io.Discard
	}

	r := &orchestrator.Runner{
		Workspace:          ws,
		LaneID:             strings.TrimSpace(opts.LaneID),
		ReviewRoot:         strings.TrimSpace(opts.ReviewRoot),
		Project:            opts.Project,
		Team:               opts.Team,
		Git:                g,
		GitHub:             gh,
		Jira:               jira,
		Sessions:           opts.Sessions,
		Confirm:            opts.Confirm,
		Approve:            opts.Approve,
		MCP:                mcpHost,
		NativeSkills:       nativeSkills,
		NativeSkillsRoot:   nativeRoot,
		Out:                out,
		Err:                errW,
		Streaming:          opts.Streaming,
		EnableTriage:       opts.EnableTriage,
		EnableClosingChief: opts.EnableClosingChief,
		OnEvent:            opts.OnEvent,
		ExternalWorkers:    opts.ExternalWorkers,
		Catalog:            opts.Catalog,
	}
	r.NewModel = orchestrator.NewJevModelFactory(orchestrator.JevFactoryOptions{
		Workspace:   ws,
		Git:         g,
		Catalog:     opts.Catalog,
		Approve:     budgetApprove,
		ApproveGate: opts.ApproveGate,
		OnEvent:     opts.OnEvent,
	})
	return r, cleanup
}
