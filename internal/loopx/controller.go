package loopx

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ExecRequest is one fresh iteration. The previous turn's transcript is not included.
type ExecRequest struct {
	LoopID       string
	Iteration    int
	Workspace    string
	Prompt       string
	Display      string
	Model        string
	ReadOnly     bool
	Full         bool
	Conversation string
	SessionID    string
}

// ExecResult is what the executor finished. NeedsHuman pauses the loop for a person.
type ExecResult struct {
	Summary      string
	Tokens       int64
	CostMicroUSD int64
	NeedsHuman   bool
}

// Executor runs one iteration. The host's default is the Agent Hive itself.
type Executor interface {
	Run(ctx context.Context, req ExecRequest) (ExecResult, error)
}

// Event is one loop_* fact for the IDE, the Orb and the app.
type Event struct {
	Kind           string
	LoopID         string
	Iteration      int
	Phase          string
	Text           string
	Detail         string
	Outcome        string
	ConversationID string
	SessionID      string
	Workspace      string
}

// RunOptions bounds one wake of the controller.
type RunOptions struct {
	MaxSteps       int
	Accompanied    bool
	ConversationID string
	SessionID      string
}

// Controller is the durable cycle Plan → Execute → Check → Decide.
type Controller struct {
	Executor Executor
	Commands CommandRunner
	Judge    Judge
	Tree     Tree
	Full     func(workspace string, accompanied bool, mode string) bool
	OnEvent  func(Event)
	Now      func() time.Time
}

// Start persists a new loop and returns its state. It does not run it.
func (c *Controller) Start(workspace string, spec Spec, conv, session string) (State, error) {
	if err := EnsureIgnored(workspace); err != nil {
		return State{}, err
	}
	spec, err := Normalize(spec)
	if err != nil {
		return State{}, err
	}
	if busy, err := otherRunning(workspace, spec.ID); err != nil {
		return State{}, err
	} else if busy {
		return State{}, fmt.Errorf("já existe um loop em execução neste projeto")
	}
	hash, err := SaveSpec(workspace, spec)
	if err != nil {
		return State{}, err
	}
	st, err := UpdateState(workspace, spec.ID, func(st *State) error {
		*st = State{
			Status:         StatusPaused,
			SpecHash:       hash,
			ConversationID: conv,
			SessionID:      session,
			Branch:         spec.Git.Branch,
		}
		return nil
	})
	if err != nil {
		return State{}, err
	}
	c.emit(Event{
		Kind: "loop_started", LoopID: spec.ID, Text: spec.Name,
		Detail:         fmt.Sprintf("máx %d iterações", spec.Limits.MaxIterations),
		ConversationID: conv, SessionID: session, Workspace: workspace,
	})
	return st, nil
}

// Run walks iterations until Decide stops, the step cap is hit, or ctx ends.
func (c *Controller) Run(ctx context.Context, workspace, id string, opt RunOptions) (State, error) {
	spec, hash, err := LoadSpec(workspace, id)
	if err != nil {
		return State{}, err
	}
	st, err := LoadState(workspace, id)
	if err != nil {
		return State{}, err
	}
	if st.Status == StatusFinished || st.Status == StatusStopped || st.StopRequested {
		return st, nil
	}
	if busy, err := otherRunning(workspace, id); err != nil {
		return State{}, err
	} else if busy {
		return st, fmt.Errorf("já existe um loop em execução neste projeto")
	}
	if st.SpecHash != "" && hash != st.SpecHash {
		return c.finish(workspace, spec, OutcomeBlocked, "a spec foi alterada durante o loop")
	}
	if _, err := UpdateState(workspace, id, func(st *State) error {
		if st.StopRequested || st.Status == StatusStopped || st.Status == StatusFinished {
			return nil
		}
		st.Status = StatusRunning
		if st.SpecHash == "" {
			st.SpecHash = hash
		}
		if opt.ConversationID != "" {
			st.ConversationID = opt.ConversationID
		}
		if opt.SessionID != "" {
			st.SessionID = opt.SessionID
		}
		if st.StartedAt.IsZero() {
			st.StartedAt = c.now()
		}
		return nil
	}); err != nil {
		return State{}, err
	}
	branchOK := c.prepareGit(ctx, workspace, spec)
	c.seedFingerprint(ctx, workspace, id)
	steps := 0
	for {
		if ctx.Err() != nil {
			return c.park(workspace, id)
		}
		st, terminal, err := c.iterate(ctx, workspace, spec, opt, branchOK)
		if err != nil {
			return st, err
		}
		if terminal || st.Status == StatusPaused || st.Status == StatusStopped || st.Status == StatusWaiting {
			return st, nil
		}
		steps++
		if opt.MaxSteps > 0 && steps >= opt.MaxSteps {
			return UpdateState(workspace, id, func(st *State) error {
				if st.Status == StatusRunning {
					st.Status = StatusWaiting
					st.Phase = ""
				}
				return nil
			})
		}
	}
}

// Stop ends the loop. The in-flight iteration keeps the stopped status.
func (c *Controller) Stop(workspace, id string) (State, error) {
	return UpdateState(workspace, id, func(st *State) error {
		st.StopRequested = true
		st.Status = StatusStopped
		st.Outcome = StatusStopped
		st.Phase = ""
		return nil
	})
}

// Pause persists paused. The caller cancels the run context.
func (c *Controller) Pause(workspace, id string) (State, error) {
	return UpdateState(workspace, id, func(st *State) error {
		if st.Status == StatusFinished || st.Status == StatusStopped {
			return nil
		}
		st.Status = StatusPaused
		st.Phase = ""
		return nil
	})
}

func (c *Controller) iterate(ctx context.Context, workspace string, spec Spec, opt RunOptions, branchOK bool) (State, bool, error) {
	lim, err := spec.ParsedLimits()
	if err != nil {
		return State{}, false, err
	}
	st, err := LoadState(workspace, spec.ID)
	if err != nil {
		return State{}, false, err
	}
	if st.StopRequested || st.Status == StatusStopped {
		return st, true, nil
	}
	if st.Status == StatusPaused {
		return st, true, nil
	}
	if st.SpecHash != "" {
		if _, hash, herr := LoadSpec(workspace, spec.ID); herr != nil || hash != st.SpecHash {
			st, err = c.finish(workspace, spec, OutcomeBlocked, "a spec foi alterada durante o loop")
			return st, true, err
		}
	}
	now := c.now()
	if st.StartedAt.IsZero() {
		st.StartedAt = now
	}
	elapsed := now.Sub(st.StartedAt)
	if preBudget(st, lim, elapsed) {
		st, err = c.finish(workspace, spec, OutcomeBudgetExhausted, "teto do loop atingido antes da próxima iteração")
		return st, true, err
	}
	if c.Executor == nil {
		return st, false, fmt.Errorf("executor do loop ausente")
	}

	next := st.Iteration + 1
	full := false
	if c.Full != nil {
		full = c.Full(workspace, opt.Accompanied, spec.Permissions.Access())
	}
	item, initializing := c.plan(workspace, spec, st)
	prompt := ExecPrompt(workspace, spec, st, next)
	if initializing {
		prompt += "\n\nEsta é a primeira iteração. Responda com um JSON array de features (id, title, priority, passes false, steps). Não grave esse arquivo você mesmo.\n"
	}
	if item.ID != "" {
		prompt += fmt.Sprintf("\n\nItem desta iteração (%s): %s\n", item.ID, item.Title)
		for _, step := range item.Steps {
			prompt += "- " + step + "\n"
		}
	}
	display := fmt.Sprintf("Loop %s · iteração %d", spec.ID, next)
	if item.ID != "" {
		display += " · " + item.ID
	}
	c.emit(Event{
		Kind: "loop_iteration", LoopID: spec.ID, Iteration: next, Phase: "execute",
		Text: display, ConversationID: st.ConversationID, SessionID: st.SessionID, Workspace: workspace,
	})

	execCtx := ctx
	cancel := func() {}
	if lim.MaxDuration > 0 {
		remain := lim.MaxDuration - elapsed
		if remain < time.Second {
			remain = time.Second
		}
		execCtx, cancel = context.WithTimeout(ctx, remain)
	}
	var beforeFiles map[string]string
	if c.Tree != nil {
		if _, files, snapErr := c.Tree.Snapshot(ctx, workspace); snapErr == nil {
			beforeFiles = files
		}
	}
	featBefore, _ := ReadFeatureBytes(workspace, spec.ID, spec.Features)
	res, runErr := c.Executor.Run(execCtx, ExecRequest{
		LoopID: spec.ID, Iteration: next, Workspace: workspace,
		Prompt: prompt, Display: display, Model: spec.Executor.Model,
		Full: full, Conversation: st.ConversationID, SessionID: st.SessionID,
	})
	cancel()
	if ctx.Err() != nil {
		parked, perr := c.park(workspace, spec.ID)
		return parked, true, perr
	}
	if runErr != nil && (res.NeedsHuman || humanErr(runErr)) {
		st, err = c.finish(workspace, spec, OutcomeBlocked, runErr.Error())
		return st, true, err
	}

	var changed []string
	var fp string
	haveFP := false
	if c.Tree != nil {
		if got, files, snapErr := c.Tree.Snapshot(ctx, workspace); snapErr == nil {
			fp = got
			haveFP = true
			changed = ChangedFiles(beforeFiles, files)
		}
	}
	featAfter, _ := ReadFeatureBytes(workspace, spec.ID, spec.Features)
	integrityOK, integrityReason := checkIntegrity(spec, changed, featBefore, featAfter)
	wroteFeatures := false
	if initializing && runErr == nil {
		if items, perr := ParseFeatureBlock(res.Summary); perr == nil {
			if serr := SaveFeatures(workspace, spec.ID, spec.Features, items); serr == nil {
				wroteFeatures = true
			}
		}
	}

	checkCtx := ctx
	if full {
		checkCtx = WithHostFull(ctx, true)
	}
	results := RunChecks(checkCtx, c.Commands, workspace, spec.Checks)
	for _, r := range results {
		c.emit(Event{
			Kind: "loop_check", LoopID: spec.ID, Iteration: next, Phase: "verify",
			Text: r.Name, Detail: fmt.Sprintf("ok=%t exit=%d", r.OK, r.Exit),
			ConversationID: st.ConversationID, SessionID: st.SessionID, Workspace: workspace,
		})
	}
	ok, passing := ChecksOK(results)
	summary := strings.TrimSpace(res.Summary)
	if runErr != nil && summary == "" {
		summary = runErr.Error()
	}
	reason := summary
	if !ok {
		reason = failReason(results, summary)
	}
	if !integrityOK {
		reason = strings.TrimSpace(integrityReason + " " + reason)
	}
	featuresRequired := strings.TrimSpace(spec.Features) != ""
	allPass := false
	if featuresRequired && !initializing {
		if items, exists, _ := LoadFeatures(workspace, spec.ID, spec.Features); exists {
			if integrityOK && ok && item.ID != "" {
				items = MarkPass(items, item.ID, true)
				_ = SaveFeatures(workspace, spec.ID, spec.Features, items)
			}
			allPass = AllPass(items)
		}
	}
	judgeRequired := spec.Judge.On()
	judgeVerdict := JudgeSkipped
	if judgeRequired && !initializing && ok && integrityOK {
		if c.Judge == nil {
			judgeVerdict = JudgeNotMet
			reason = strings.TrimSpace(reason + " juiz indisponível")
		} else {
			jr, jerr := c.Judge.Run(ctx, JudgeRequest{
				LoopID: spec.ID, Workspace: workspace,
				Model:        ResolveJudgeModel(spec.Judge.Model, spec.Executor.Model),
				Prompt:       JudgePrompt(spec, results, changed, summary),
				Conversation: st.ConversationID, SessionID: st.SessionID,
			})
			if jerr != nil {
				judgeVerdict = JudgeNotMet
				reason = strings.TrimSpace(reason + " " + jerr.Error())
			} else {
				judgeVerdict = jr.Verdict
				if jr.Reason != "" {
					reason = jr.Reason
				}
				if jr.NextHint != "" {
					reason = strings.TrimSpace(reason + " " + jr.NextHint)
				}
			}
		}
	}

	var terminal bool
	var outcome string
	st, err = UpdateState(workspace, spec.ID, func(st *State) error {
		if st.StopRequested || st.Status == StatusStopped || st.Status == StatusPaused || st.Status == StatusFinished {
			return nil
		}
		st.Iteration = next
		st.Phase = "decide"
		st.Status = StatusRunning
		st.Tokens += res.Tokens
		if res.CostMicroUSD > 0 {
			st.SpentMicroUSD += res.CostMicroUSD
		}
		st.CurrentItem = item.ID
		NoteProgress(st, fp, haveFP, passing)
		if wroteFeatures {
			st.ConsecutiveNoProgress = 0
		}
		switch judgeVerdict {
		case JudgeImpossible:
			st.ConsecutiveImpossible++
		case JudgeMet, JudgeNotMet:
			st.ConsecutiveImpossible = 0
		}
		outcome = Decide(Snapshot{
			ChecksOK:              ok,
			IntegrityOK:           integrityOK,
			FeaturesRequired:      featuresRequired,
			FeaturesAllPass:       allPass,
			Judge:                 judgeVerdict,
			JudgeRequired:         judgeRequired && !initializing,
			Iterations:            st.Iteration,
			Elapsed:               c.now().Sub(st.StartedAt),
			SpentMicroUSD:         st.SpentMicroUSD,
			SpentTokens:           st.Tokens,
			ConsecutiveNoProgress: st.ConsecutiveNoProgress,
			ConsecutiveImpossible: st.ConsecutiveImpossible,
			Limits:                lim,
		})
		st.LastVerdict = outcome
		st.LastReason = clip(reason, 500)
		if outcome == OutcomeContinue {
			st.Status = StatusRunning
			return nil
		}
		st.Status = StatusFinished
		st.Outcome = outcome
		st.Phase = ""
		terminal = true
		return nil
	})
	if err != nil {
		return st, false, err
	}
	if st.StopRequested || st.Status == StatusStopped || st.Status == StatusPaused {
		return st, true, nil
	}
	commit := ""
	if integrityOK && branchOK && c.Tree != nil && spec.Git.CommitEach() {
		sha, cerr := c.Tree.Commit(ctx, workspace, fmt.Sprintf("loop(%s): iteração %d\n\n%s", spec.ID, st.Iteration, clip(summary, 200)))
		if cerr != nil {
			reason = strings.TrimSpace(reason + " commit: " + cerr.Error())
		} else {
			commit = sha
		}
	}
	if terminal && outcome == OutcomeDone && branchOK && c.Tree != nil && spec.Git.OpenPR() {
		url, perr := c.Tree.OpenPR(ctx, workspace, spec.Name, spec.Objective, "main")
		st, _ = UpdateState(workspace, spec.ID, func(st *State) error {
			if perr != nil {
				st.LastReason = clip(strings.TrimSpace(st.LastReason+" PR: "+perr.Error()), 500)
				return nil
			}
			st.PRURL = url
			return nil
		})
	}
	_ = AppendProgress(workspace, spec.ID, st.Iteration, summary, outcome)
	_ = AppendIteration(workspace, spec.ID, IterationRecord{
		Iteration: st.Iteration, At: c.now(), Phase: "decide", Summary: clip(summary, 2000),
		ChecksOK: ok && integrityOK, Verdict: outcome, Reason: clip(reason, 500),
		SpentMicroUSD: st.SpentMicroUSD, Tokens: st.Tokens, Commit: commit,
	})
	c.emit(Event{
		Kind: "loop_verdict", LoopID: spec.ID, Iteration: st.Iteration, Phase: "decide",
		Text: outcome, Detail: st.LastReason, Outcome: outcome,
		ConversationID: st.ConversationID, SessionID: st.SessionID, Workspace: workspace,
	})
	if terminal {
		c.emit(Event{
			Kind: "loop_finished", LoopID: spec.ID, Iteration: st.Iteration,
			Text: outcome, Detail: st.LastReason, Outcome: outcome,
			ConversationID: st.ConversationID, SessionID: st.SessionID, Workspace: workspace,
		})
	}
	return st, terminal, nil
}

func (c *Controller) prepareGit(ctx context.Context, workspace string, spec Spec) bool {
	if c == nil || c.Tree == nil || strings.TrimSpace(spec.Git.Branch) == "" {
		return false
	}
	return c.Tree.EnsureBranch(ctx, workspace, spec.Git.Branch) == nil
}

func (c *Controller) seedFingerprint(ctx context.Context, workspace, id string) {
	if c == nil || c.Tree == nil {
		return
	}
	st, err := LoadState(workspace, id)
	if err != nil || st.HaveFingerprint {
		return
	}
	fp, _, err := c.Tree.Snapshot(ctx, workspace)
	if err != nil {
		return
	}
	_, _ = UpdateState(workspace, id, func(st *State) error {
		if st.HaveFingerprint {
			return nil
		}
		st.Fingerprint = fp
		st.HaveFingerprint = true
		return nil
	})
}

func (c *Controller) plan(workspace string, spec Spec, st State) (Feature, bool) {
	if strings.TrimSpace(spec.Features) == "" {
		return Feature{}, false
	}
	items, exists, err := LoadFeatures(workspace, spec.ID, spec.Features)
	if err != nil || !exists {
		return Feature{}, true
	}
	item, ok := NextFeature(items)
	if !ok {
		return Feature{}, false
	}
	_ = st
	return item, false
}

func checkIntegrity(spec Spec, changed []string, before, after []byte) (bool, string) {
	if FeaturesTampered(before, after) {
		return false, "features.json só pode mudar o campo passes"
	}
	rel := filepathSlash(strings.TrimSpace(spec.Features))
	files := make([]string, 0, len(changed))
	for _, f := range changed {
		if rel != "" && (f == rel || strings.HasSuffix(f, "/"+rel)) {
			continue
		}
		files = append(files, f)
	}
	bad := Violations(files, spec.ProtectedPaths)
	if len(bad) == 0 {
		return true, ""
	}
	return false, "diff tocou caminho protegido: " + strings.Join(bad, ", ")
}

func (c *Controller) finish(workspace string, spec Spec, outcome, reason string) (State, error) {
	st, err := UpdateState(workspace, spec.ID, func(st *State) error {
		if st.StopRequested || st.Status == StatusStopped {
			return nil
		}
		st.Status = StatusFinished
		st.Outcome = outcome
		st.LastVerdict = outcome
		st.LastReason = clip(reason, 500)
		st.Phase = ""
		return nil
	})
	if err != nil {
		return st, err
	}
	if st.Status == StatusStopped {
		return st, nil
	}
	c.emit(Event{
		Kind: "loop_finished", LoopID: spec.ID, Iteration: st.Iteration,
		Text: st.Outcome, Detail: st.LastReason, Outcome: st.Outcome,
		ConversationID: st.ConversationID, SessionID: st.SessionID, Workspace: workspace,
	})
	return st, nil
}

func (c *Controller) park(workspace, id string) (State, error) {
	st, err := UpdateState(workspace, id, func(st *State) error {
		if st.StopRequested || st.Status == StatusStopped {
			st.Status = StatusStopped
			st.Outcome = StatusStopped
			st.Phase = ""
			return nil
		}
		if st.Status == StatusFinished {
			return nil
		}
		st.Status = StatusPaused
		st.Phase = ""
		return nil
	})
	return st, err
}

func (c *Controller) emit(ev Event) {
	if c == nil || c.OnEvent == nil {
		return
	}
	c.OnEvent(ev)
}

func (c *Controller) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}

func preBudget(st State, lim Limits, elapsed time.Duration) bool {
	if lim.MaxIterations > 0 && st.Iteration >= lim.MaxIterations {
		return true
	}
	if lim.MaxDuration > 0 && !st.StartedAt.IsZero() && elapsed >= lim.MaxDuration {
		return true
	}
	if lim.MaxCostMicroUSD > 0 && st.SpentMicroUSD >= lim.MaxCostMicroUSD {
		return true
	}
	if lim.MaxTokens > 0 && st.Tokens >= lim.MaxTokens {
		return true
	}
	return false
}

func otherRunning(workspace, id string) (bool, error) {
	list, err := List(workspace)
	if err != nil {
		return false, err
	}
	for _, sum := range list {
		if sum.ID != id && sum.Status == StatusRunning {
			return true, nil
		}
	}
	return false, nil
}

func humanErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "aprovação") || strings.Contains(msg, "aprovacao")
}

func failReason(results []CheckResult, summary string) string {
	var b strings.Builder
	for _, r := range results {
		if r.OK {
			continue
		}
		fmt.Fprintf(&b, "%s exit=%d", r.Name, r.Exit)
		if r.Error != "" {
			fmt.Fprintf(&b, " (%s)", r.Error)
		}
		if r.Tail != "" {
			fmt.Fprintf(&b, ": %s", clip(r.Tail, 240))
		}
		b.WriteString("; ")
	}
	if summary != "" {
		b.WriteString(clip(summary, 240))
	}
	return strings.TrimSpace(b.String())
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ExecPrompt is the only context an iteration receives: the objective, the last verdict and the diary.
func ExecPrompt(workspace string, spec Spec, st State, iteration int) string {
	var b strings.Builder
	b.WriteString("Você executa uma iteração de um loop do Agent Hive. O contexto é novo.\n")
	b.WriteString("O host roda os checks e decide se o loop acaba. Não altere loop.yaml, features.json nem arquivos de teste protegidos.\n")
	b.WriteString("Faça só o que falta para o objetivo. Termine com um resumo curto do que mudou.\n\n")
	fmt.Fprintf(&b, "Loop: %s\nIteração: %d\n\nObjetivo:\n%s\n", spec.ID, iteration, spec.Objective)
	if len(spec.DoneWhen) > 0 {
		b.WriteString("\nCritério:\n")
		for _, line := range spec.DoneWhen {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	}
	if st.LastVerdict != "" {
		fmt.Fprintf(&b, "\nÚltimo veredito: %s\n%s\n", st.LastVerdict, st.LastReason)
	}
	if tail := ReadProgressTail(workspace, spec.ID, 6000); tail != "" {
		b.WriteString("\nDiário:\n")
		b.WriteString(tail)
		b.WriteByte('\n')
	}
	return b.String()
}
