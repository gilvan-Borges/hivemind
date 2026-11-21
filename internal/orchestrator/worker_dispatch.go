package orchestrator

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync/atomic"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workerx"
)

var dispatchSeq uint64

func (r *Runner) externalAdapterFor(a team.Agent) (workerx.ExternalWorkerAdapter, bool) {
	if _, mockInjected := r.Models[a.Name]; mockInjected {
		return nil, false
	}
	prov := strings.ToLower(strings.TrimSpace(a.Worker))
	if prov == "" {
		prov = team.NormalizeProvider(a.Provider, a.Model)
	}
	if r.ExternalWorkers != nil {
		return r.ExternalWorkers.Get(prov)
	}
	if prov == "cursor" {
		return workerx.NewDefaultRegistry().Get("cursor")
	}
	return nil, false
}

func (r *Runner) hasExternalWorkers(agents []team.Agent) bool {
	for _, a := range agents {
		if _, ok := r.externalAdapterFor(a); ok {
			return true
		}
	}
	return false
}

// RunExternalWorkerDev executes a single developer agent through an
// ExternalWorkerAdapter inside its isolated worktree, enforces blocking ask via
// HITL, validates the post-hoc diff against owned_paths + pathsec, and reverts
// any disallowed modifications before integration into _integracao.
func (r *Runner) RunExternalWorkerDev(ctx context.Context, dev devEnv, adapter workerx.ExternalWorkerAdapter, slice string) (workerx.DoneEvent, workerx.GuardResult, error) {
	if adapter == nil {
		return workerx.DoneEvent{}, workerx.GuardResult{}, fmt.Errorf("adaptador externo nulo para %s", dev.Agent.Name)
	}
	seq := atomic.AddUint64(&dispatchSeq, 1)
	dispID := fmt.Sprintf("disp-%s-%d", dev.ADK, seq)
	taskID := fmt.Sprintf("task-%s-%d", dev.ADK, seq)
	label := roleLabel(dev.Agent.Role) + " · " + dev.Agent.Name

	if r.OnEvent != nil {
		r.OnEvent(Event{
			Kind:   "stage",
			Stage:  "dev",
			Author: dev.ADK,
			Label:  label,
			Detail: "worker externo " + adapter.Provider(),
		})
	}

	req := workerx.DispatchRequest{
		DispatchID:     dispID,
		TaskID:         taskID,
		Provider:       adapter.Provider(),
		Model:          dev.Agent.Model,
		AgentName:      dev.Agent.Name,
		WorktreeDir:    dev.Root,
		Branch:         dev.Branch,
		PermissionMode: r.Project.Settings.EffectivePermissionMode(),
		Handoff: workerx.HandoffBrief{
			RunID:        r.deliveryBranch,
			Objective:    r.objectiveText,
			ContextBrief: strings.TrimSpace(r.chiefContextBrief),
			Slice:        strings.TrimSpace(slice),
			OwnedPaths:   append([]string(nil), dev.OwnedPaths...),
		},
	}

	doneEv, err := adapter.Execute(ctx, req, workerx.Callbacks{
		OnHeartbeat: func(hb workerx.HeartbeatEvent) {
			if r.OnEvent != nil {
				r.OnEvent(Event{
					Kind:   "worker_heartbeat",
					Stage:  "dev",
					Author: dev.ADK,
					Label:  label,
					Text:   hb.Message,
				})
			}
		},
		OnAsk: func(_ context.Context, ask workerx.AskRequest) (workerx.AskResponse, error) {
			prompt := fmt.Sprintf("[Worker %s] %s", dev.Agent.Name, ask.Question)
			approved := false
			if r.Approve != nil {
				approved = r.Approve(prompt)
			}
			ans := "negado pelo usuário"
			if approved {
				ans = "aprovado pelo usuário"
			}
			return workerx.AskResponse{Approved: approved, Answer: ans}, nil
		},
		OnTool: func(name, path string) {
			if r.OnEvent != nil {
				r.OnEvent(Event{
					Kind:     "tool_call",
					Stage:    "dev",
					Author:   dev.ADK,
					Label:    label,
					ToolName: name,
					Detail:   path,
				})
			}
		},
	})

	g := r.Git
	if g == nil {
		g = gitx.Local{}
	}
	guardRes, guardErr := workerx.ValidatePostHocDiff(ctx, g, dev.Root, dev.OwnedPaths, doneEv.FilesModified...)
	if guardErr != nil {
		return doneEv, guardRes, guardErr
	}
	if !guardRes.Passed() {
		_ = workerx.RevertViolations(ctx, g, dev.Root, guardRes.Violations)
		var msgs []string
		for _, v := range guardRes.Violations {
			msgs = append(msgs, fmt.Sprintf("%s (%s)", v.Path, v.Reason))
		}
		violErr := fmt.Errorf("worker externo %s violou governança pós-hoc e teve arquivos revertidos: %s", dev.Agent.Name, strings.Join(msgs, ", "))
		if r.OnEvent != nil {
			r.OnEvent(Event{Kind: "error", Stage: "dev", Author: dev.ADK, Label: label, Text: violErr.Error()})
		}
		return doneEv, guardRes, violErr
	}
	if err != nil {
		return doneEv, guardRes, err
	}
	if strings.EqualFold(doneEv.Outcome, "failed") || strings.EqualFold(doneEv.Outcome, "aborted") {
		return doneEv, guardRes, fmt.Errorf("worker externo %s encerrou com status %s: %s", dev.Agent.Name, doneEv.Outcome, doneEv.Summary)
	}
	return doneEv, guardRes, nil
}

func (r *Runner) externalWorkerAgent(dev devEnv, adkName, reportKey, slice string, adapter workerx.ExternalWorkerAdapter) (agent.Agent, error) {
	return agent.New(agent.Config{
		Name:        adkName,
		Description: "Executa desenvolvedor externo governado pelo Worker Contract em worktree isolada.",
		Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				doneEv, guardRes, err := r.RunExternalWorkerDev(ctx, dev, adapter, slice)
				if err != nil {
					yield(nil, err)
					return
				}
				summary := strings.TrimSpace(doneEv.Summary)
				if summary == "" {
					summary = fmt.Sprintf("Worker externo (%s) concluído. Arquivos modificados: %s", adapter.Provider(), strings.Join(guardRes.Modified, ", "))
				}
				ev := session.NewEvent(ctx, ctx.InvocationID())
				ev.Author = adkName
				ev.LLMResponse = model.LLMResponse{
					Content: genai.NewContentFromText(summary, genai.RoleModel),
				}
				ev.Actions.StateDelta = map[string]any{
					reportKey: summary,
				}
				yield(ev, nil)
			}
		},
	})
}
