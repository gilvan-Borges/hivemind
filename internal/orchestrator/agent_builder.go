package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"

	"github.com/gilvan-Borges/HiveMind/internal/contextx"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/tools"
)

func (r *Runner) llm(ctx context.Context, a team.Agent, adkName, outputKey, root, extra, jiraKey string, stateKeys []string, qa bool, disableGitWrite bool) (agent.Agent, error) {
	return r.llmCore(ctx, a, adkName, outputKey, root, extra, jiraKey, stateKeys, qa, disableGitWrite, nil)
}

func (r *Runner) wrapRouteEvent(next func(Event)) func(Event) {
	return func(ev Event) {
		if (ev.Kind == "model_route" || ev.Kind == "provider_failover") && strings.TrimSpace(ev.Provider) != "" && strings.TrimSpace(ev.Model) != "" {
			r.resolvedChoiceMu.Lock()
			r.lastWorkingRoute = team.Agent{Provider: ev.Provider, Model: ev.Model}
			if strings.TrimSpace(ev.Author) != "" {
				if r.routedModels == nil {
					r.routedModels = map[string][2]string{}
				}
				r.routedModels[strings.ToLower(strings.TrimSpace(ev.Author))] = [2]string{ev.Provider, ev.Model}
				if ev.DiscountPct > 0 {
					if r.routedDiscount == nil {
						r.routedDiscount = map[string]int{}
					}
					r.routedDiscount[strings.ToLower(strings.TrimSpace(ev.Author))] = ev.DiscountPct
				}
			}
			r.resolvedChoiceMu.Unlock()
		}
		if next != nil {
			next(ev)
		}
	}
}

// resolveAuthorRoute returns the (provider, model) pair last routed for the
// agent, falling back to the team config for pinned (non-Jev) agents.
func (r *Runner) resolveAuthorRoute(author string) (string, string) {
	r.resolvedChoiceMu.Lock()
	rm := r.routedModels[strings.ToLower(strings.TrimSpace(author))]
	r.resolvedChoiceMu.Unlock()
	if rm[0] != "" {
		return rm[0], rm[1]
	}
	for _, a := range r.Team.Agents {
		if strings.EqualFold(a.Name, author) {
			return team.NormalizeProvider(a.Provider, a.Model), a.Model
		}
	}
	return "", ""
}

func (r *Runner) llmCore(ctx context.Context, a team.Agent, adkName, outputKey, root, extra, jiraKey string, stateKeys []string, qa bool, disableGitWrite bool, ownedPaths []string) (agent.Agent, error) {
	ctx = modelx.WithAgentDir(ctx, root)
	label := roleLabel(a.Role) + " · " + a.Name
	inTok := jev.EstimateFirstStepInputTokens(len(r.objectiveText)+len(extra)+len(r.chiefPlan)+len(r.chiefContextBrief), len(r.teamMemorySnippet()), true)
	runMode := ""
	if r.autoReviewMode {
		runMode = "auto-review"
	}
	ctx = withJevRouteHint(ctx, jevRouteHint{
		RunMode:      runMode,
		Step:         outputKey,
		ObjectiveLen: len(r.objectiveText),
		Objective:    r.objectiveText,
		InputTokens:  inTok,
		HasImages:    r.hasImages,
		NeedsTools:   true,
		QAStall:      r.qaStallCount,
		Workspace:    r.Workspace,
		WorkDir:      root,
		RepoKey:      r.jevRepo,
		Author:       adkName,
		Label:        label,
		Stage:        inferStage(adkName, label),
		Git:          r.Git,
		Approve:      r.Approve,
		OnEvent:      r.wrapRouteEvent(r.OnEvent),
	})
	llm, err := r.modelFor(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("modelo de %s (provedor %s): %w", a.Name, a.Provider, err)
	}
	roleTools, err := tools.ForRole(a.Role, tools.Deps{
		Root:                 root,
		Settings:             r.Project.Settings,
		DisableGitWrite:      disableGitWrite,
		ReviewRequestedInObj: r.reviewRequested,
		Allowlist:            r.Project.Settings.ShellAllowlist,
		Confirm:              r.Confirm,
		Approve:              r.Approve,
		Git:                  r.Git,
		GitHub:               r.GitHub,
		Jira:                 r.Jira,
		JiraKey:              jiraKey,
		BaseBranch:           r.Project.Settings.BaseBranch,
		MCP:                  r.MCP,
	})
	if err != nil {
		return nil, err
	}
	instruction := a.Instructions
	if extra != "" {
		instruction += "\n\n" + extra
	}
	keys := append([]string(nil), stateKeys...)
	beforeModelCBs := []llmagent.BeforeModelCallback{
		contextx.NewCompactionCallback(contextx.Budget{
			ContextWindow:   agentContextWindow(a),
			ContextWindowFn: r.dynamicAgentContextWindow(adkName, a),
			KeepRecent:      r.Project.Settings.EffectiveInTurnKeepToolResults(),
		}),
		r.newDynamicContextReminderCallback(adkName, a, qa, outputKey, keys),
	}
	if a.Role == team.RoleDeveloper || a.Role == team.RoleQA {
		beforeModelCBs = append([]llmagent.BeforeModelCallback{r.handoffModelGuard(a.Role)}, beforeModelCBs...)
	}
	beforeToolCBs := []llmagent.BeforeToolCallback{
		newRepeatReadGuardCallback(),
	}
	var afterToolCBs []llmagent.AfterToolCallback
	if a.Role == team.RoleDeveloper {
		devExpBefore, devExpAfter := newDevExploreBudgetCallbacks(
			r.Project.Settings.EffectiveMaxDevExploreCalls(),
			func() int {
				r.handoffMu.Lock()
				defer r.handoffMu.Unlock()
				return r.qaLoopIter
			},
			r.Project.Settings.EffectiveMaxDevCalls(),
		)
		beforeToolCBs = append(beforeToolCBs, devExpBefore)
		afterToolCBs = append(afterToolCBs, devExpAfter)
		if guard := newOwnedPathsGuardCallback(root, adkName, ownedPaths, r.sharedBoard); guard != nil {
			beforeToolCBs = append(beforeToolCBs, guard)
		}
		if r.sharedBoard != nil {
			if noteTool, nErr := newShareTeamNoteTool(adkName, r.sharedBoard); nErr == nil {
				roleTools = append(roleTools, noteTool)
			}
			if touchCB := newTeamBoardTouchCallback(root, adkName, r.sharedBoard); touchCB != nil {
				afterToolCBs = append(afterToolCBs, touchCB)
			}
		}
	}
	if outputKey == "pm_plan" || outputKey == "tech_lead_plan" || outputKey == "architect_plan" {
		triBefore, triAfter := newChiefTriageBudgetCallbacks(jiraKey, defaultMaxTriageExploreCalls)
		beforeToolCBs = append([]llmagent.BeforeToolCallback{triBefore}, beforeToolCBs...)
		afterToolCBs = append(afterToolCBs, triAfter)
	}
	cfg := llmagent.Config{
		Name:                     adkName,
		Description:              roleLabel(a.Role) + " do time HiveMind.",
		Model:                    llm,
		Mode:                     llmagent.ModeChat,
		IncludeContents:          llmagent.IncludeContentsNone,
		DisallowTransferToParent: true,
		DisallowTransferToPeers:  true,
		Tools:                    roleTools,
		OutputKey:                outputKey,
		BeforeModelCallbacks:     beforeModelCBs,
		BeforeToolCallbacks:      beforeToolCBs,
		AfterToolCallbacks:       afterToolCBs,
		InstructionProvider: func(agent.ReadonlyContext) (string, error) {
			return instruction, nil
		},
	}
	if outputKey == "closing_summary" {
		cfg.BeforeToolCallbacks = append(cfg.BeforeToolCallbacks, func(ctx agent.Context, tl tool.Tool, _ map[string]any) (map[string]any, error) {
			if tl == nil || !isClosingPublishTool(tl.Name()) {
				return nil, nil
			}
			if qaApprovedReadonly(ctx.ReadonlyState()) {
				return nil, nil
			}
			return map[string]any{
				"blocked": true,
				"error":   "entrega NÃO aprovada pelo QA (qa_approve não foi chamado): operações de commit, push ou PR estão bloqueadas",
			}, nil
		})
	}
	if qa {
		cfg.AfterToolCallbacks = append(cfg.AfterToolCallbacks,
			func(tctx agent.Context, tl tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
				if err != nil || tl == nil || tctx.Actions() == nil {
					return nil, nil
				}
				switch tl.Name() {
				case "qa_approve":
					tctx.Actions().Escalate = true
				case "qa_request_changes":
					if msg := r.recordQAStall(tctx); msg != "" {
						tctx.Actions().Escalate = true
						if tctx.Actions().StateDelta == nil {
							tctx.Actions().StateDelta = map[string]any{}
						}
						tctx.Actions().StateDelta["qa_feedback"] = msg
						if r.Out != nil && r.Err != nil {
							fmt.Fprintf(r.Err, "aviso: %s\n", msg)
						}
					}
				}
				return nil, nil
			},
		)
	}
	return llmagent.New(cfg)
}

func (r *Runner) modelFor(ctx context.Context, a team.Agent) (model.LLM, error) {
	if r.Models != nil {
		if m, ok := r.Models[a.Name]; ok && m != nil {
			return m, nil
		}
		if a.Role == team.RoleDeveloper {
			if m, ok := r.Models["dev"]; ok && m != nil {
				return m, nil
			}
			if m, ok := r.Models["dev_1"]; ok && m != nil {
				return m, nil
			}
		}
	}
	buildOne := func(ag team.Agent) (model.LLM, error) {
		if r.NewModel != nil {
			return r.NewModel(ctx, ag)
		}
		if strings.EqualFold(strings.TrimSpace(ag.Provider), "jev") {
			return NewJevModelFactory(JevFactoryOptions{
				Workspace: r.Workspace,
				Git:       r.Git,
				Approve:   r.Approve,
				OnEvent:   r.OnEvent,
			})(ctx, ag)
		}
		return modelx.New(ctx, ag.Provider, ag.Model)
	}
	m, err := buildOne(a)
	if err != nil && (a.Role == team.RoleQA || a.Role == team.RoleDeveloper || a.Role == team.RoleTechLead) {
		errMsg := strings.ToLower(err.Error())
		isJevRefusal := strings.Contains(errMsg, "roteamento jev recusou") || strings.Contains(errMsg, "provedores estão esgotados") || strings.Contains(errMsg, "bloqueado pelo teto de orçamento")
		if jev.ClassifyError(err) == jev.ErrClassAuth || isJevRefusal {
			candidates := []team.Agent{}
			r.resolvedChoiceMu.Lock()
			working := r.lastWorkingRoute
			r.resolvedChoiceMu.Unlock()
			if working.Provider != "" && working.Model != "" {
				candidates = append(candidates, working)
			}
			chief := r.primaryChief()
			if !strings.EqualFold(chief.Provider, a.Provider) || !strings.EqualFold(chief.Model, a.Model) {
				candidates = append(candidates, chief)
			}
			for _, cand := range candidates {
				fallback := a
				fallback.Provider = cand.Provider
				fallback.Model = cand.Model
				if fm, fErr := buildOne(fallback); fErr == nil {
					return fm, nil
				}
			}
		}
	}
	return m, err
}
