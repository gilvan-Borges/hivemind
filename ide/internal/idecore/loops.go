package idecore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/loopx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
)

func loopKey(workspace, id string) string {
	return strings.TrimSpace(workspace) + "\n" + strings.TrimSpace(id)
}

func (m *ResidentManager) loopController() *loopx.Controller {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loopCtl == nil {
		m.loopCtl = &loopx.Controller{
			Executor: hiveLoopExec{m},
			Commands: loopCommands{m},
			Judge:    hiveJudge{m},
			Tree: loopx.GitWorktree{OpenPull: func(ctx context.Context, workspace, title, body, base string) (string, error) {
				pr, err := OpenPullRequest(ctx, workspace, title, body, base)
				if err != nil {
					return "", err
				}
				return pr.URL, nil
			}},
			Full:    m.loopFull,
			OnEvent: m.onLoopEvent,
		}
	}
	return m.loopCtl
}

func (m *ResidentManager) loopFull(workspace string, accompanied bool, mode string) bool {
	cfg, err := resident.LoadConfig(m.baseDir)
	project := err == nil && cfg.IsFullAccess()
	if workspacePermissionFull(workspace) {
		project = true
	}
	allow := err == nil && cfg.AllowFullUnattended
	return loopx.ApplyPermission(mode, project, accompanied, allow)
}

func (m *ResidentManager) interceptLoop(ctx context.Context, workspace, workstream, message, sessionID string, origin resident.Origin) (bool, resident.TurnResult, error) {
	cmd, ok := loopx.Parse(message)
	if !ok {
		return false, resident.TurnResult{}, nil
	}
	workspace = strings.TrimSpace(workspace)
	if cmd.Action != "list" && workspace == "" {
		return true, resident.TurnResult{}, fmt.Errorf("abra um projeto para usar o loop")
	}
	shared, err := m.prepareSharedTurn(workspace, workstream, message, sessionID, string(origin))
	if err != nil {
		return true, resident.TurnResult{}, err
	}
	text, err := m.dispatchLoop(ctx, workspace, cmd, origin, shared.Conv.ID, shared.Conv.SessionID)
	if err != nil {
		m.finishSharedTurn(shared, err.Error())
		return true, resident.TurnResult{}, err
	}
	m.finishSharedTurn(shared, text)
	return true, resident.TurnResult{
		FinalText:       text,
		ConversationID:  shared.Conv.ID,
		SharedSessionID: shared.Conv.SessionID,
		UserMessageID:   shared.UserID,
		ReplyMessageID:  shared.ReplyID,
	}, nil
}

func (m *ResidentManager) dispatchLoop(ctx context.Context, workspace string, cmd loopx.Command, origin resident.Origin, conv, session string) (string, error) {
	switch cmd.Action {
	case "list":
		return m.formatLoopList(workspace)
	case "stop":
		if err := m.StopLoop(workspace, cmd.ID); err != nil {
			return "", err
		}
		return "Loop " + cmd.ID + " parado.", nil
	case "pause":
		if err := m.PauseLoop(workspace, cmd.ID); err != nil {
			return "", err
		}
		return "Loop " + cmd.ID + " pausado.", nil
	case "resume":
		if err := m.ResumeLoop(ctx, workspace, cmd.ID); err != nil {
			return "", err
		}
		return "Loop " + cmd.ID + " retomado.", nil
	case "start":
		if cmd.Kind == loopx.KindTime || cmd.Kind == loopx.KindProactive {
			return m.startScheduledLoop(workspace, cmd, origin, conv, session)
		}
		return m.startGoalLoop(workspace, cmd, origin, conv, session)
	default:
		return "", fmt.Errorf("comando de loop desconhecido")
	}
}

func (m *ResidentManager) startGoalLoop(workspace string, cmd loopx.Command, origin resident.Origin, conv, session string) (string, error) {
	id, err := nextLoopID(workspace, loopx.Slug(cmd.Text))
	if err != nil {
		return "", err
	}
	spec := loopx.Spec{
		ID:        id,
		Name:      clipLoop(cmd.Text, 80),
		Kind:      loopx.KindGoal,
		Objective: cmd.Text,
		DoneWhen:  []string{cmd.Text},
		Trigger:   loopx.TriggerSpec{Manual: true},
	}
	if cmd.JudgeOff {
		off := false
		spec.Judge.Enabled = &off
	}
	if cmd.Max > 0 {
		spec.Limits.MaxIterations = cmd.Max
	}
	if len(cmd.Checks) == 0 {
		return "", fmt.Errorf("informe um check do host, por exemplo --check \"go test ./...\"")
	}
	for i, run := range cmd.Checks {
		spec.Checks = append(spec.Checks, loopx.Check{Name: fmt.Sprintf("check-%d", i+1), Run: run})
	}
	ctl := m.loopController()
	if _, err := ctl.Start(workspace, spec, conv, session); err != nil {
		return "", err
	}
	accompanied := origin == resident.OriginLocal || origin == resident.OriginRemote
	m.launchLoop(workspace, id, loopx.RunOptions{
		Accompanied:    accompanied,
		ConversationID: conv,
		SessionID:      session,
	})
	return "Loop " + id + " iniciado. O host confere e decide quando parar.", nil
}

func (m *ResidentManager) startScheduledLoop(workspace string, cmd loopx.Command, origin resident.Origin, conv, session string) (string, error) {
	if len(cmd.Checks) == 0 {
		return "", fmt.Errorf("informe um check do host, por exemplo --check \"go test ./...\"")
	}
	id, err := nextLoopID(workspace, loopx.Slug(cmd.Text))
	if err != nil {
		return "", err
	}
	every := ""
	if cmd.Every > 0 {
		every = cmd.Every.String()
	}
	spec := loopx.Spec{
		ID: id, Name: clipLoop(cmd.Text, 80), Kind: cmd.Kind, Objective: cmd.Text,
		DoneWhen: []string{cmd.Text},
		Trigger:  loopx.TriggerSpec{Cron: cmd.Cron, Every: every, Events: append([]string(nil), cmd.Events...)},
	}
	if cmd.Max > 0 {
		spec.Limits.MaxIterations = cmd.Max
	}
	if cmd.JudgeOff {
		off := false
		spec.Judge.Enabled = &off
	}
	for i, run := range cmd.Checks {
		spec.Checks = append(spec.Checks, loopx.Check{Name: fmt.Sprintf("check-%d", i+1), Run: run})
	}
	if _, err := m.loopController().Start(workspace, spec, conv, session); err != nil {
		return "", err
	}
	if strings.TrimSpace(cmd.Cron) != "" {
		if _, err := resident.SaveRoutine(m.baseDir, resident.Routine{
			Expr: cmd.Cron, Text: cmd.Text, Enabled: true, LoopID: id, Workspace: workspace,
		}); err != nil {
			return "", err
		}
		if m.sched != nil {
			m.sched.Arm()
		}
	}
	accompanied := origin == resident.OriginLocal || origin == resident.OriginRemote
	m.launchLoop(workspace, id, loopx.RunOptions{
		MaxSteps: 3, Accompanied: accompanied, ConversationID: conv, SessionID: session,
	})
	when := cmd.Cron
	if when == "" {
		when = "evento " + strings.Join(cmd.Events, ",")
	}
	return "Loop " + id + " armado (" + when + "). Cada disparo faz no máximo 3 iterações.", nil
}

func (m *ResidentManager) wakeLoops(kind string) {
	if m == nil {
		return
	}
	seen := map[string]bool{}
	var dirs []string
	if m.projectsFn != nil {
		for _, p := range m.projectsFn() {
			root := strings.TrimSpace(p.Root)
			if root == "" || seen[root] {
				continue
			}
			seen[root] = true
			dirs = append(dirs, root)
		}
	}
	m.mu.Lock()
	bound := strings.TrimSpace(m.boundWorkspace)
	m.mu.Unlock()
	if bound != "" && !seen[bound] {
		dirs = append(dirs, bound)
	}
	for _, ws := range dirs {
		list, err := loopx.List(ws)
		if err != nil {
			continue
		}
		for _, sum := range list {
			if sum.Status != loopx.StatusWaiting {
				continue
			}
			spec, _, serr := loopx.LoadSpec(ws, sum.ID)
			if serr != nil || !containsEvent(spec.Trigger.Events, kind) {
				continue
			}
			_ = m.WakeLoop(ws, sum.ID)
		}
	}
}

func containsEvent(events []string, kind string) bool {
	for _, ev := range events {
		if ev == kind {
			return true
		}
	}
	return false
}

// WakeLoop starts one waiting loop from cron or from an event. It is unattended.
func (m *ResidentManager) WakeLoop(workspace, id string) error {
	if !loopx.ValidID(id) || strings.TrimSpace(workspace) == "" {
		return fmt.Errorf("loop inválido")
	}
	st, err := loopx.LoadState(workspace, id)
	if err != nil {
		return err
	}
	if st.Status != loopx.StatusWaiting {
		return nil
	}
	m.launchLoop(workspace, id, loopx.RunOptions{
		MaxSteps: 3, Accompanied: false, ConversationID: st.ConversationID, SessionID: st.SessionID,
	})
	return nil
}

func (m *ResidentManager) launchLoop(workspace, id string, opt loopx.RunOptions) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	if m.loopStop == nil {
		m.loopStop = map[string]context.CancelFunc{}
	}
	if prev := m.loopStop[loopKey(workspace, id)]; prev != nil {
		prev()
	}
	m.loopStop[loopKey(workspace, id)] = cancel
	m.loopWG.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.loopWG.Done()
		defer m.clearLoop(workspace, id)
		_, _ = m.loopController().Run(ctx, workspace, id, opt)
	}()
}

func (m *ResidentManager) clearLoop(workspace, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.loopStop, loopKey(workspace, id))
}

func (m *ResidentManager) cancelLoop(workspace, id string) {
	m.mu.Lock()
	cancel := m.loopStop[loopKey(workspace, id)]
	delete(m.loopStop, loopKey(workspace, id))
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *ResidentManager) pauseLoops() bool {
	m.mu.Lock()
	stops := m.loopStop
	m.loopStop = nil
	ctl := m.loopCtl
	m.mu.Unlock()
	if len(stops) == 0 {
		return false
	}
	for key, cancel := range stops {
		ws, id, _ := strings.Cut(key, "\n")
		if ctl != nil && id != "" {
			_, _ = ctl.Pause(ws, id)
		}
		if cancel != nil {
			cancel()
		}
	}
	return true
}

// GetLoop returns one loop. A missing spec is not found.
func (m *ResidentManager) GetLoop(workspace, id string) (loopx.Summary, error) {
	if _, _, err := loopx.LoadSpec(workspace, id); err != nil {
		return loopx.Summary{}, fmt.Errorf("loop não encontrado")
	}
	return loopx.Summarize(workspace, id)
}

// ListLoops returns the loops stored in the workspace.
func (m *ResidentManager) ListLoops(workspace string) ([]loopx.Summary, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("workspace vazio")
	}
	return loopx.List(workspace)
}

// StopLoop ends a loop. It does not merge anything.
func (m *ResidentManager) StopLoop(workspace, id string) error {
	if _, err := m.loopController().Stop(workspace, id); err != nil {
		return err
	}
	m.cancelLoop(workspace, id)
	return nil
}

// PauseLoop freezes a loop until someone resumes it.
func (m *ResidentManager) PauseLoop(workspace, id string) error {
	if _, err := m.loopController().Pause(workspace, id); err != nil {
		return err
	}
	m.cancelLoop(workspace, id)
	return nil
}

// ResumeLoop continues a paused loop. The person who asked is accompanied.
func (m *ResidentManager) ResumeLoop(ctx context.Context, workspace, id string) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	st, err := loopx.LoadState(workspace, id)
	if err != nil {
		return err
	}
	if st.Status == loopx.StatusFinished || st.Status == loopx.StatusStopped {
		return fmt.Errorf("loop %s já encerrou (%s)", id, st.Status)
	}
	if st.Status == loopx.StatusRunning {
		return fmt.Errorf("loop %s já está em execução", id)
	}
	m.launchLoop(workspace, id, loopx.RunOptions{
		Accompanied:    true,
		ConversationID: st.ConversationID,
		SessionID:      st.SessionID,
	})
	return nil
}

func (m *ResidentManager) formatLoopList(workspace string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "Nenhum projeto aberto.", nil
	}
	list, err := loopx.List(workspace)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "Nenhum loop neste projeto.", nil
	}
	var b strings.Builder
	for _, sum := range list {
		fmt.Fprintf(&b, "%s · %s", sum.ID, sum.Status)
		if sum.Outcome != "" {
			fmt.Fprintf(&b, " (%s)", sum.Outcome)
		}
		fmt.Fprintf(&b, " · iteração %d", sum.Iteration)
		if sum.LastVerdict != "" {
			fmt.Fprintf(&b, " · %s", sum.LastVerdict)
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String()), nil
}

func (m *ResidentManager) onLoopEvent(ev loopx.Event) {
	if m == nil {
		return
	}
	if m.sessions != nil && m.sessions.Emit != nil {
		m.sessions.Emit(LaneEvent{
			LaneID: resident.LaneID,
			Team:   "hive-resident",
			Event: orchestrator.Event{
				Kind:           ev.Kind,
				Stage:          ev.Phase,
				Author:         "hive",
				Label:          "Loop",
				Text:           ev.Text,
				Detail:         ev.Detail,
				Origin:         string(resident.OriginLoop),
				ConversationID: ev.ConversationID,
				SessionID:      ev.SessionID,
				LoopID:         ev.LoopID,
				Iteration:      ev.Iteration,
			},
		})
	}
	if ev.Kind == "loop_started" || ev.Kind == "loop_finished" {
		m.noteLoopAudit(ev)
		m.appendLoopLine(ev)
	}
}

func (m *ResidentManager) noteLoopAudit(ev loopx.Event) {
	if m.audit == nil {
		return
	}
	tool := "loop_started"
	if ev.Kind == "loop_finished" {
		tool = "loop_finished"
	}
	_ = m.audit.RecordDecision(resident.AuditEntry{
		Origin:    resident.OriginLoop,
		Tool:      tool,
		Tier:      resident.TierRead,
		Decision:  "allow",
		LoopID:    ev.LoopID,
		Iteration: ev.Iteration,
		Reason:    clipLoop(ev.Text+" "+ev.Detail, 240),
	})
}

func (m *ResidentManager) appendLoopLine(ev loopx.Event) {
	if ev.ConversationID == "" {
		return
	}
	text := strings.TrimSpace(ev.Text)
	if ev.Detail != "" {
		text = strings.TrimSpace(text + " — " + ev.Detail)
	}
	if text == "" {
		return
	}
	id := fmt.Sprintf("loop-%s-%s-%d", ev.LoopID, ev.Kind, ev.Iteration)
	_, _ = m.conversations().AppendMessage(ev.ConversationID, resident.ConvMessage{
		ID: id, Role: "system", Author: "Loop", Text: text, Kind: "loop",
	})
}

func nextLoopID(workspace, base string) (string, error) {
	if !loopx.ValidID(base) {
		base = "loop"
	}
	id := base
	for n := 2; n < 50; n++ {
		if _, _, err := loopx.LoadSpec(workspace, id); err != nil {
			return id, nil
		}
		id = fmt.Sprintf("%s-%d", base, n)
		if len(id) > 48 {
			id = strings.Trim(id[:48], "-")
		}
	}
	return "", fmt.Errorf("não coube outro id para %s", base)
}

func clipLoop(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

type hiveJudge struct{ m *ResidentManager }

func (h hiveJudge) Run(ctx context.Context, req loopx.JudgeRequest) (loopx.JudgeResult, error) {
	if h.m != nil && h.m.testLoopJudge != nil {
		return h.m.testLoopJudge.Run(ctx, req)
	}
	full := false
	model := req.Model
	if model == "" || model == "review_model" {
		cfg, _ := resident.LoadConfig(h.m.baseDir)
		_, executor := resident.ResolveModelRoute(cfg)
		model = loopx.ResolveJudgeModel(req.Model, executor)
	}
	res, err := h.m.runTurn(ctx, req.Workspace, "", "Juiz do loop "+req.LoopID, req.SessionID, resident.OriginLoop, nil, turnOpts{
		loopID:       req.LoopID,
		readOnly:     true,
		forceFull:    &full,
		fresh:        true,
		model:        model,
		display:      "Juiz do loop " + req.LoopID,
		modelMessage: req.Prompt,
		skipCommand:  true,
	})
	if err != nil {
		return loopx.JudgeResult{}, err
	}
	return loopx.ParseJudge(res.FinalText)
}

type hiveLoopExec struct{ m *ResidentManager }

func (h hiveLoopExec) Run(ctx context.Context, req loopx.ExecRequest) (loopx.ExecResult, error) {
	if h.m != nil && h.m.testLoopExec != nil {
		return h.m.testLoopExec.Run(ctx, req)
	}
	full := req.Full
	res, err := h.m.runTurn(ctx, req.Workspace, "", req.Display, req.SessionID, resident.OriginLoop, nil, turnOpts{
		loopID:       req.LoopID,
		iteration:    req.Iteration,
		model:        req.Model,
		readOnly:     req.ReadOnly,
		forceFull:    &full,
		fresh:        true,
		display:      req.Display,
		modelMessage: req.Prompt,
		skipCommand:  true,
	})
	out := loopx.ExecResult{
		Summary:      res.FinalText,
		Tokens:       res.TotalTokens,
		CostMicroUSD: priceTurn(res.Provider, res.Model, res.InputTokens, res.OutputTokens),
	}
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "aprovação") || strings.Contains(msg, "aprovacao") {
			out.NeedsHuman = true
		}
	}
	return out, err
}

type loopCommands struct{ m *ResidentManager }

func (c loopCommands) Run(ctx context.Context, dir, command string, timeout time.Duration) (int, string, error) {
	if c.m != nil && c.m.testLoopCmd != nil {
		return c.m.testLoopCmd.Run(ctx, dir, command, timeout)
	}
	return (loopx.HostRunner{Sandbox: c.m.sandbox}).Run(ctx, dir, command, timeout)
}

func priceTurn(provider, model string, in, out int64) int64 {
	if in == 0 && out == 0 {
		return 0
	}
	path, err := jev.CatalogPath()
	if err != nil {
		return 0
	}
	cat, err := jev.LoadCatalogFrom(path)
	if err != nil {
		return 0
	}
	entry, ok := cat.Lookup(provider, model)
	if !ok {
		return 0
	}
	return jev.ComputeCostMicroUSD(entry, in, out, 0, 0)
}
