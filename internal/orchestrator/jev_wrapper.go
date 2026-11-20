package orchestrator

import (
	"context"
	"fmt"
	"iter"
	"math"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
)

// jevModelWrapper wraps a routed model.LLM to track health/spend, classify errors
// past modelx retries, and perform at most one between-call failover per
// GenerateContent invocation without duplicating tool calls.
type jevModelWrapper struct {
	mu           sync.Mutex
	current      model.LLM
	choice       jev.Choice
	taskBase     jev.Task
	policy       jev.Policy
	repoPol      jev.RepoPolicy
	catalog      *jev.CatalogStore
	health       *jev.HealthStore
	outcomes     *jev.OutcomeLog
	credsFn      func() map[string]bool
	buildModel   func(ctx context.Context, provider, modelName string) (model.LLM, error)
	approve      func(gate, prompt string) bool
	onEvent      func(Event)
	git          gitx.Git
	workDir      string
	author       string
	label        string
	stage        string
	extraSecrets []string
}

func (w *jevModelWrapper) Name() string {
	w.mu.Lock()
	ch := w.choice
	cur := w.current
	w.mu.Unlock()
	if ch.Provider != "" && ch.Model != "" {
		return ch.Provider + ":" + ch.Model
	}
	if cur != nil {
		return cur.Name()
	}
	return "jev"
}

func (w *jevModelWrapper) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		w.mu.Lock()
		curLLM := w.current
		curChoice := w.choice
		w.mu.Unlock()

		exclude := map[string]bool{}
		if hasPriorToolActivity(req) {
			// Mid-step ADK calls with tool history stay off WorkspaceAgent.
		}

		var headBefore, statusBefore string
		var snapChecked bool
		if curChoice.Provider == "cursor" && w.git != nil && w.workDir != "" {
			headBefore, statusBefore = snapshotWorktree(ctx, w.git, w.workDir)
			snapChecked = true
		}

		started := time.Now()
		respErr, deliveredToolCall, lastResp := w.invokeOnce(ctx, curLLM, req, stream, yield)
		if respErr == nil {
			w.recordSuccessAndSpend(curChoice, lastResp)
			w.recordCall(curChoice, true, "", time.Since(started), lastResp, false)
			w.noteQA(lastResp)
			return
		}

		errClass := jev.ClassifyError(respErr)
		w.recordCall(curChoice, false, errClass, time.Since(started), nil, false)
		if errClass == jev.ErrClassPolicy || ctx.Err() != nil {
			yield(nil, fmt.Errorf("%s", w.explain(curChoice, respErr)))
			return
		}

		if w.health != nil {
			w.health.RecordFailure(curChoice.Provider, curChoice.Model, errClass, time.Time{})
		}

		// Do not repeat a GenerateContent call that already handed a tool call to ADK.
		if deliveredToolCall {
			yield(nil, fmt.Errorf("%s", w.explain(curChoice, respErr)))
			return
		}

		// Cursor worktree guard (§9 & §15 Fase 2): never hand a dirty worktree to
		// another writer after Cursor CLI fails without explicit approval.
		if curChoice.Provider == "cursor" && snapChecked && w.git != nil && w.workDir != "" {
			headAfter, statusAfter := snapshotWorktree(ctx, w.git, w.workDir)
			if headAfter != headBefore || statusAfter != statusBefore {
				prompt := fmt.Sprintf("Jev: o Cursor CLI falhou após modificar a worktree (%s). Permitir que outro modelo continue sobre as alterações atuais?", w.workDir)
				if w.approve == nil || !w.approve("worktree", prompt) {
					w.emitExhausted(fmt.Sprintf("Cursor falhou após editar a worktree e a continuação não foi aprovada; alterações mantidas em %s.", w.workDir))
					yield(nil, fmt.Errorf("worktree alterada pelo Cursor após falha sem aprovação para continuar: %s", jev.RedactSecrets(respErr.Error(), w.extraSecrets...)))
					return
				}
			}
		}

		exclude[pairKey(curChoice.Provider, curChoice.Model)] = true
		nextChoice, nextLLM, failErr := w.selectAndBuildFailover(ctx, req, curChoice, errClass, exclude)
		if failErr != nil {
			yield(nil, failErr)
			return
		}

		w.mu.Lock()
		w.current = nextLLM
		w.choice = nextChoice
		w.mu.Unlock()

		// Retry the same LLMRequest ONCE on the failover model (Spec §9).
		retryStart := time.Now()
		retryErr, _, retryResp := w.invokeOnce(ctx, nextLLM, req, stream, yield)
		if retryErr == nil {
			w.recordSuccessAndSpend(nextChoice, retryResp)
			w.recordCall(nextChoice, true, "", time.Since(retryStart), retryResp, true)
			w.noteQA(retryResp)
			return
		}
		retryClass := jev.ClassifyError(retryErr)
		w.recordCall(nextChoice, false, retryClass, time.Since(retryStart), nil, true)
		if retryClass != jev.ErrClassPolicy && ctx.Err() == nil && w.health != nil {
			w.health.RecordFailure(nextChoice.Provider, nextChoice.Model, retryClass, time.Time{})
		}
		yield(nil, fmt.Errorf("%s", w.explain(nextChoice, retryErr)))
	}
}

func (w *jevModelWrapper) explain(ch jev.Choice, err error) string {
	return modelx.FormatUpstreamError(ch.Provider, ch.Model, err, w.extraSecrets...)
}

func (w *jevModelWrapper) invokeOnce(
	ctx context.Context,
	llm model.LLM,
	req *model.LLMRequest,
	stream bool,
	yield func(*model.LLMResponse, error) bool,
) (errOut error, deliveredToolCall bool, lastResp *model.LLMResponse) {
	if llm == nil {
		return fmt.Errorf("modelo nulo no wrapper Jev"), false, nil
	}
	for resp, err := range llm.GenerateContent(ctx, req, stream) {
		if err != nil {
			return err, deliveredToolCall, lastResp
		}
		if resp == nil {
			continue
		}
		lastResp = resp
		if responseHasToolCall(resp) {
			deliveredToolCall = true
		}
		if !yield(resp, nil) {
			return nil, deliveredToolCall, lastResp
		}
	}
	return nil, deliveredToolCall, lastResp
}

func (w *jevModelWrapper) selectAndBuildFailover(
	ctx context.Context,
	req *model.LLMRequest,
	from jev.Choice,
	errClass jev.ErrorClass,
	exclude map[string]bool,
) (jev.Choice, model.LLM, error) {
	task := w.taskBase
	task.OnlyProvider = "" // Failover is never locked to a single failed/exhausted provider.
	task.InStep = true     // Cursor stays out of mid-step failover (Spec §9 / Fase 2).
	task.HasPendingToolCall = hasPriorToolActivity(req)
	if est := estimateRequestTokens(req); est > task.InputTokens {
		task.InputTokens = est
	}
	if w.outcomes != nil {
		task.Learn = w.outcomes.Snapshot(task.RepoKey)
	}

	creds := map[string]bool(nil)
	if w.credsFn != nil {
		creds = w.credsFn()
	}
	for attempt := 0; attempt < 3; attempt++ {
		if w.catalog != nil {
			w.catalog.RefreshIfStale(ctx)
		}
		cat := jev.DefaultStaticCatalog()
		if w.catalog != nil {
			cat = w.catalog.Snapshot()
		}
		var snap jev.HealthSnapshot
		if w.health != nil {
			snap = w.health.Snapshot(creds, exclude)
		} else {
			snap = jev.HealthSnapshot{Credentials: creds, ExcludePairs: exclude, AgyRemainingPct: -1}
		}
		if creds["openrouter"] {
			snap.OpenRouterBalanceUSD, snap.OpenRouterBalanceKnown = modelx.GetOpenRouterBalanceUSD(ctx)
		}

		// Failover bypasses the failed team.yaml pin so another healthy provider can take over.
		choice, ref := jev.Score(task, cat, w.policy, snap, jev.AgentPin{}, w.repoPol)
		if ref.Code == "budget" && ref.Need != nil {
			prompt := fmt.Sprintf(
				"Jev: failover de %s/%s precisa de %s/%s (~US$ %.4f por chamada), acima do teto US$ %.2f. Autorizar?",
				from.Provider, from.Model, ref.Need.Provider, ref.Need.Model,
				float64(ref.Need.EstimatedMicroUSD)/1_000_000.0, w.policy.AutoEscalateUSD,
			)
			if w.approve == nil || !w.approve("budget", prompt) {
				msg := "Todos os provedores dentro do orçamento estão indisponíveis e a escalada acima do teto não foi aprovada."
				w.emitExhausted(msg)
				return jev.Choice{}, nil, fmt.Errorf("%s", msg)
			}
			choice = *ref.Need
			choice.Reasons = []string{"budget_approved", "circuit_open"}
		} else if ref.Code != "" {
			msg := fmt.Sprintf("Todos os provedores estão esgotados ou indisponíveis (%s): %s", ref.Code, jev.RedactSecrets(ref.Reason, w.extraSecrets...))
			w.emitExhausted(msg)
			return jev.Choice{}, nil, fmt.Errorf("%s", msg)
		}

		if w.health != nil && !w.health.TryAcquireHalfOpen(choice.Provider, choice.Model) {
			exclude[pairKey(choice.Provider, choice.Model)] = true
			continue
		}

		llm, err := w.buildModel(ctx, choice.Provider, choice.Model)
		if err != nil {
			buildClass := jev.ClassifyError(err)
			if buildClass != jev.ErrClassPolicy && w.health != nil {
				w.health.RecordFailure(choice.Provider, choice.Model, buildClass, time.Time{})
			}
			exclude[pairKey(choice.Provider, choice.Model)] = true
			continue
		}

		w.emitFailover(from, choice, errClass)
		return choice, llm, nil
	}

	msg := "Todos os provedores elegíveis falharam durante o failover."
	w.emitExhausted(msg)
	return jev.Choice{}, nil, fmt.Errorf("%s", msg)
}

func (w *jevModelWrapper) recordCall(ch jev.Choice, ok bool, fail jev.ErrorClass, latency time.Duration, resp *model.LLMResponse, failover bool) {
	if w.outcomes == nil || ch.Provider == "" || ch.Model == "" {
		return
	}
	tokens := 0
	if resp != nil && resp.UsageMetadata != nil {
		tokens = int(resp.UsageMetadata.PromptTokenCount + resp.UsageMetadata.CandidatesTokenCount)
		if tokens < 0 {
			tokens = 0
		}
	}
	failCode := ""
	if !ok {
		failCode = string(fail)
		if failCode == "" {
			failCode = "error"
		}
	}
	_ = w.outcomes.Append(jev.Outcome{
		RepoKey:           w.taskBase.RepoKey,
		Role:              w.taskBase.Role,
		Class:             w.taskBase.Class,
		Complexity:        w.taskBase.Complexity,
		Provider:          ch.Provider,
		Model:             ch.Model,
		Tier:              ch.Tier,
		OK:                ok,
		FailCode:          failCode,
		Reason:            failCode,
		Tokens:            tokens,
		LatencyMs:         latency.Milliseconds(),
		EstimatedMicroUSD: ch.EstimatedMicroUSD,
		Failover:          failover,
	})
}

func (w *jevModelWrapper) noteQA(resp *model.LLMResponse) {
	if w.outcomes == nil || w.taskBase.Class != jev.ClassQA || resp == nil || resp.Content == nil {
		return
	}
	approved, saw := false, false
	for _, p := range resp.Content.Parts {
		if p == nil || p.FunctionCall == nil {
			continue
		}
		switch p.FunctionCall.Name {
		case "qa_request_changes":
			approved, saw = false, true
		case "qa_approve":
			if !saw {
				approved, saw = true, true
			}
		}
	}
	if !saw {
		return
	}
	_ = w.outcomes.NoteQA(w.taskBase.RepoKey, approved)
}

func (w *jevModelWrapper) recordSuccessAndSpend(ch jev.Choice, resp *model.LLMResponse) {
	if w.health == nil {
		return
	}
	w.health.RecordSuccess(ch.Provider, ch.Model)
	if resp == nil || resp.UsageMetadata == nil {
		return
	}
	inTok := int64(resp.UsageMetadata.PromptTokenCount)
	outTok := int64(resp.UsageMetadata.CandidatesTokenCount)
	if inTok <= 0 && outTok <= 0 {
		return
	}
	if w.catalog == nil {
		return
	}
	if entry, ok := w.catalog.Snapshot().Lookup(ch.Provider, ch.Model); ok {
		cost := float64(inTok)*entry.PromptMicroUSD + float64(outTok)*entry.CompletionMicroUSD
		if cost > 0 {
			w.health.RecordSpend(ch.Provider, int64(math.Round(cost)))
		}
	}
}

func (w *jevModelWrapper) emitFailover(from, to jev.Choice, errClass jev.ErrorClass) {
	if w.onEvent == nil {
		return
	}
	text := fmt.Sprintf("%s respondeu %s em %s. Seguindo em %s/%s.", from.Provider, errClass, from.Model, to.Provider, to.Model)
	var sum []jev.ProviderHealthView
	if w.health != nil {
		var creds map[string]bool
		if w.credsFn != nil {
			creds = w.credsFn()
		}
		sum = w.health.Summary(w.policy, creds)
	}
	reasons := append([]string{"circuit_open"}, to.Reasons...)
	w.onEvent(Event{
		Kind:              "provider_failover",
		Stage:             w.stage,
		Author:            jev.RedactSecrets(w.author, w.extraSecrets...),
		Label:             jev.RedactSecrets(w.label, w.extraSecrets...),
		Text:              jev.RedactSecrets(text, w.extraSecrets...),
		Provider:          to.Provider,
		Model:             to.Model,
		FromProvider:      from.Provider,
		FromModel:         from.Model,
		Tier:              string(to.Tier),
		Reasons:           reasons,
		EstimatedMicroUSD: to.EstimatedMicroUSD,
		DecisionMicros:    to.DecisionMicros,
		Health:            sum,
	})
}

func (w *jevModelWrapper) emitExhausted(text string) {
	if w.onEvent == nil {
		return
	}
	var sum []jev.ProviderHealthView
	if w.health != nil {
		var creds map[string]bool
		if w.credsFn != nil {
			creds = w.credsFn()
		}
		sum = w.health.Summary(w.policy, creds)
	}
	w.onEvent(Event{
		Kind:   "provider_exhausted",
		Stage:  w.stage,
		Author: jev.RedactSecrets(w.author, w.extraSecrets...),
		Label:  jev.RedactSecrets(w.label, w.extraSecrets...),
		Text:   jev.RedactSecrets(text, w.extraSecrets...),
		Health: sum,
	})
}

func pairKey(provider, model string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "\x00" + strings.TrimSpace(model)
}

func responseHasToolCall(resp *model.LLMResponse) bool {
	if resp == nil || resp.Content == nil {
		return false
	}
	for _, p := range resp.Content.Parts {
		if p != nil && p.FunctionCall != nil {
			return true
		}
	}
	return false
}

func hasPriorToolActivity(req *model.LLMRequest) bool {
	if req == nil {
		return false
	}
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil && (p.FunctionCall != nil || p.FunctionResponse != nil) {
				return true
			}
		}
	}
	return false
}

func estimateRequestTokens(req *model.LLMRequest) int {
	if req == nil {
		return 0
	}
	totalBytes := 0
	if req.Config != nil && req.Config.SystemInstruction != nil {
		for _, p := range req.Config.SystemInstruction.Parts {
			if p != nil {
				totalBytes += len(p.Text)
			}
		}
	}
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil {
				totalBytes += len(p.Text)
			}
		}
	}
	if totalBytes == 0 {
		return 0
	}
	return (totalBytes + 2) / 3
}

func snapshotWorktree(ctx context.Context, g gitx.Git, dir string) (head, status string) {
	snapCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	head, _ = g.Run(snapCtx, dir, "rev-parse", "HEAD")
	status, _ = g.Status(snapCtx, dir)
	return strings.TrimSpace(head), strings.TrimSpace(status)
}
