package idecore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/remote"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// AutoReviewLaneID is the dedicated IDE chat lane used by automatic Flow PR reviews
// so background reviews never collide with the user's manual lanes.
const AutoReviewLaneID = "lane-autoreview"

// AutoReviewEntry records one completed or failed automatic PR review in history.
type AutoReviewEntry = remote.AutoReviewEntry

// AutoReviewStatus is the live snapshot emitted to the IDE Flow tab and status bar.
type AutoReviewStatus = remote.AutoReviewStatus

type autoReviewRecord struct {
	Fingerprint string    `json:"fingerprint"`
	ReviewedAt  time.Time `json:"reviewedAt,omitempty"`
	FailCount   int       `json:"failCount,omitempty"`
	NextRetryAt time.Time `json:"nextRetryAt,omitempty"`
}

type autoReviewDiskState struct {
	Seen       map[string]autoReviewRecord `json:"seen"`
	History    []AutoReviewEntry           `json:"history"`
	RecentRuns []time.Time                 `json:"recentRuns,omitempty"`
}

// AutoReviewManager coordinates per-workspace background PR review loops.
type AutoReviewManager struct {
	mu       sync.Mutex
	sessions *SessionManager
	emit     func(AutoReviewStatus)
	workers  map[string]*autoReviewWorker

	// Injectable hooks for deterministic unit testing.
	ListPRsFn   func(ctx context.Context, workspace string) ([]ProjectPullRequest, error)
	RunReviewFn func(ctx context.Context, workspace, reviewRoot, teamName, objective string) (orchestrator.Result, error)
	PrepareWTFn func(ctx context.Context, workspace string, pr ProjectPullRequest) (string, func(), error)
	NowFn       func() time.Time
}

type autoReviewWorker struct {
	workspace   string
	config      team.AutoReviewSettings
	state       string
	activePR    int
	activeTitle string
	queueCount  int
	lastPollAt  time.Time
	nextPollAt  time.Time
	lastError   string
	disk        autoReviewDiskState

	loopCancel   context.CancelFunc
	activeCancel context.CancelFunc
	pollNowCh    chan struct{}
	doneCh       chan struct{}
}

// NewAutoReviewManager constructs an AutoReviewManager backed by sessions.
func NewAutoReviewManager(sessions *SessionManager, emit func(AutoReviewStatus)) *AutoReviewManager {
	m := &AutoReviewManager{
		sessions: sessions,
		emit:     emit,
		workers:  make(map[string]*autoReviewWorker),
	}
	m.ListPRsFn = ListProjectPullRequests
	m.PrepareWTFn = PrepareEphemeralPRWorktree
	m.NowFn = time.Now
	m.RunReviewFn = func(ctx context.Context, workspace, reviewRoot, teamName, objective string) (orchestrator.Result, error) {
		if m.sessions == nil {
			return orchestrator.Result{}, fmt.Errorf("session manager indisponível")
		}
		return m.sessions.RunAutoReviewTurn(ctx, AutoReviewLaneID, workspace, reviewRoot, teamName, objective)
	}
	return m
}

func autoReviewWorkspaceKey(workspace string) string {
	abs, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil {
		abs = strings.TrimSpace(workspace)
	}
	return strings.ToLower(filepath.Clean(abs))
}

func (m *AutoReviewManager) now() time.Time {
	if m.NowFn != nil {
		return m.NowFn()
	}
	return time.Now()
}

// PRAutoReviewFingerprint computes a stable key representing the PR's reviewable state.
// A PR is only reviewed once per fingerprint; new commits or new human replies change it.
func PRAutoReviewFingerprint(pr ProjectPullRequest) string {
	return fmt.Sprintf("#%d|%s|%s|%s|%s",
		pr.Number,
		strings.TrimSpace(pr.HeadRef),
		strings.TrimSpace(pr.UpdatedAt),
		strings.TrimSpace(pr.AttentionReason),
		strings.TrimSpace(pr.MyResponse),
	)
}

// SelectEligibleAutoReviewPRs filters PRs matching the Flow pending-reviews criteria,
// skipping own PRs, drafts (unless allowed), already-reviewed fingerprints, and PRs in backoff.
func SelectEligibleAutoReviewPRs(
	prs []ProjectPullRequest,
	cfg team.AutoReviewSettings,
	seen map[string]autoReviewRecord,
	now time.Time,
) []ProjectPullRequest {
	var out []ProjectPullRequest
	for _, pr := range prs {
		if pr.IsMine {
			continue
		}
		if pr.IsDraft && !cfg.IncludeDrafts {
			continue
		}
		isPending := pr.NeedsAttention || (pr.ReviewRequested && strings.TrimSpace(pr.MyResponse) == "")
		if !isPending {
			continue
		}
		fp := PRAutoReviewFingerprint(pr)
		key := strconv.Itoa(pr.Number)
		if rec, ok := seen[key]; ok && rec.Fingerprint == fp {
			if rec.FailCount == 0 {
				continue // Already reviewed at this exact state.
			}
			if rec.FailCount >= 3 || now.Before(rec.NextRetryAt) {
				continue // In cooldown or exhausted retries for this state.
			}
		}
		out = append(out, pr)
	}
	// FIFO by oldest updatedAt so no PR starves in the queue.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UpdatedAt == out[j].UpdatedAt {
			return out[i].Number < out[j].Number
		}
		return out[i].UpdatedAt < out[j].UpdatedAt
	})
	return out
}

// BuildAutoReviewObjective constructs the objective prompt for one queued PR.
func BuildAutoReviewObjective(pr ProjectPullRequest, runTests bool, commentOnly bool) string {
	var b strings.Builder
	branchInfo := ""
	if pr.HeadRef != "" || pr.BaseRef != "" {
		branchInfo = fmt.Sprintf(" (%s -> %s)", pr.HeadRef, pr.BaseRef)
	}
	if pr.NeedsAttention && strings.TrimSpace(pr.MyResponse) != "" {
		reason := strings.ToLower(strings.TrimSpace(pr.AttentionReason))
		if reason == "" {
			reason = "nova atualização"
		}
		fmt.Fprintf(&b, "Reavaliar automaticamente o PR #%d de @%s%s — %s. Houve %s após minha última revisão; leia os comentários mais recentes com github_get_pr antes de responder.",
			pr.Number, pr.Author, branchInfo, pr.Title, reason)
	} else {
		fmt.Fprintf(&b, "Revisar automaticamente o PR #%d de @%s%s — %s.",
			pr.Number, pr.Author, branchInfo, pr.Title)
	}
	if runTests {
		b.WriteString("\nVocê está operando em uma worktree isolada na branch deste PR: pode rodar os testes afetados via run_shell e incluir o resultado objetivo no parecer.")
	}
	if commentOnly {
		b.WriteString("\nModo somente-comentário ativo: envie o parecer com github_review_pr usando event=\"COMMENT\".")
	}
	b.WriteString("\nPROIBIDO MESCLAR: nunca tente fazer merge deste PR.")
	if body := strings.TrimSpace(pr.Body); body != "" {
		b.WriteString("\n\nDescrição do PR:\n")
		b.WriteString(strx.TruncateBytes(body, 2500, "\n…"))
	}
	return b.String()
}

// PrepareEphemeralPRWorktree checks out a PR's head into a disposable detached worktree
// under the external workspace worktrees dir so tests can run without touching the
// user's checkout or branch.
func PrepareEphemeralPRWorktree(ctx context.Context, workspaceDir string, pr ProjectPullRequest) (string, func(), error) {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) {
		return "", func() {}, fmt.Errorf("workspace não é um repositório git")
	}
	wtRoot := workspace.WorktreesDir(workspaceDir)
	if err := os.MkdirAll(wtRoot, 0o755); err != nil {
		return "", func() {}, err
	}
	wtPath := filepath.Join(wtRoot, fmt.Sprintf("_autoreview_pr_%d", pr.Number))

	cleanup := func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = runGit(cleanCtx, workspaceDir, "worktree", "remove", "--force", wtPath)
		_ = os.RemoveAll(wtPath)
		_, _ = runGit(cleanCtx, workspaceDir, "worktree", "prune")
	}

	// Clean any stale worktree left from a previous interrupted run.
	cleanup()

	ref := ""
	if pr.Number > 0 {
		if _, err := runGit(ctx, workspaceDir, "fetch", "--no-tags", "origin", fmt.Sprintf("pull/%d/head", pr.Number)); err == nil {
			ref = "FETCH_HEAD"
		}
	}
	if ref == "" && strings.TrimSpace(pr.HeadRef) != "" {
		head := strings.TrimSpace(pr.HeadRef)
		if _, err := runGit(ctx, workspaceDir, "fetch", "--no-tags", "origin", head); err == nil {
			ref = "FETCH_HEAD"
		} else if _, err := runGit(ctx, workspaceDir, "rev-parse", "--verify", "origin/"+head); err == nil {
			ref = "origin/" + head
		} else if _, err := runGit(ctx, workspaceDir, "rev-parse", "--verify", head); err == nil {
			ref = head
		}
	}
	if ref == "" {
		return "", func() {}, fmt.Errorf("não foi possível obter o commit do PR #%d (%s)", pr.Number, pr.HeadRef)
	}

	if _, err := runGit(ctx, workspaceDir, "worktree", "add", "--detach", wtPath, ref); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("falha ao criar worktree efêmera do PR #%d: %w", pr.Number, err)
	}
	return wtPath, cleanup, nil
}

// GetStatus returns the current AutoReviewStatus snapshot for workspace.
func (m *AutoReviewManager) GetStatus(workspace string) AutoReviewStatus {
	key := autoReviewWorkspaceKey(workspace)
	m.mu.Lock()
	w, ok := m.workers[key]
	if ok {
		st := m.snapshotLocked(w)
		m.mu.Unlock()
		return st
	}
	m.mu.Unlock()

	cfg := defaultAutoReviewConfig(workspace)
	disk := loadAutoReviewDiskState(workspace)
	return AutoReviewStatus{
		Workspace:        workspace,
		Enabled:          cfg.Enabled,
		State:            "idle",
		Config:           cfg,
		ReviewedLastHour: countRunsLastHour(disk.RecentRuns, m.now()),
		History:          append([]AutoReviewEntry(nil), disk.History...),
	}
}

// SyncWorkspace loads persisted auto_review settings when opening a project and
// starts the background loop automatically if enabled.
func (m *AutoReviewManager) SyncWorkspace(ctx context.Context, workspace string) AutoReviewStatus {
	cfg := defaultAutoReviewConfig(workspace)
	if !cfg.Enabled {
		return m.GetStatus(workspace)
	}
	st, err := m.startOrUpdateWorker(ctx, workspace, cfg)
	if err != nil {
		return m.GetStatus(workspace)
	}
	return st
}

// Configure persists cfg into .hivemind/team.yaml (when team.yaml exists) and
// starts, updates, or stops the background auto-review worker for workspace.
func (m *AutoReviewManager) Configure(ctx context.Context, workspace string, cfg team.AutoReviewSettings) (AutoReviewStatus, error) {
	norm := normalizeAutoReviewSettings(cfg)
	if file, err := team.Load(workspace); err == nil {
		file.Settings.AutoReview = norm
		if saveErr := team.Save(workspace, file); saveErr != nil {
			return AutoReviewStatus{}, saveErr
		}
	}
	return m.startOrUpdateWorker(ctx, workspace, norm)
}

func (m *AutoReviewManager) startOrUpdateWorker(parentCtx context.Context, workspace string, cfg team.AutoReviewSettings) (AutoReviewStatus, error) {
	key := autoReviewWorkspaceKey(workspace)
	m.mu.Lock()
	w, exists := m.workers[key]
	if !exists {
		w = &autoReviewWorker{
			workspace: workspace,
			config:    cfg,
			state:     "idle",
			disk:      loadAutoReviewDiskState(workspace),
			pollNowCh: make(chan struct{}, 1),
		}
		m.workers[key] = w
	} else {
		w.config = cfg
	}

	if !cfg.Enabled {
		cancelLoop := w.loopCancel
		cancelActive := w.activeCancel
		w.loopCancel = nil
		w.activeCancel = nil
		w.state = "idle"
		w.activePR = 0
		w.activeTitle = ""
		w.nextPollAt = time.Time{}
		st := m.snapshotLocked(w)
		m.mu.Unlock()
		if cancelActive != nil {
			cancelActive()
		}
		if cancelLoop != nil {
			cancelLoop()
		}
		m.emitStatus(st)
		return st, nil
	}

	// Start loop if not already running.
	if w.loopCancel == nil {
		if parentCtx == nil {
			parentCtx = context.Background()
		}
		loopCtx, cancel := context.WithCancel(parentCtx)
		w.loopCancel = cancel
		w.doneCh = make(chan struct{})
		w.lastError = ""
		w.state = "idle"
		go m.runLoop(loopCtx, w)
	} else {
		// Wake up loop immediately so updated settings take effect.
		select {
		case w.pollNowCh <- struct{}{}:
		default:
		}
	}
	st := m.snapshotLocked(w)
	m.mu.Unlock()
	m.emitStatus(st)
	return st, nil
}

// TriggerPoll triggers an immediate Flow check for workspace.
func (m *AutoReviewManager) TriggerPoll(workspace string) AutoReviewStatus {
	key := autoReviewWorkspaceKey(workspace)
	m.mu.Lock()
	w, ok := m.workers[key]
	if !ok {
		m.mu.Unlock()
		return m.GetStatus(workspace)
	}
	if w.loopCancel != nil {
		select {
		case w.pollNowCh <- struct{}{}:
		default:
		}
	}
	st := m.snapshotLocked(w)
	m.mu.Unlock()
	return st
}

// StopActivePR cancels only the currently running PR review in workspace.
func (m *AutoReviewManager) StopActivePR(workspace string) bool {
	key := autoReviewWorkspaceKey(workspace)
	m.mu.Lock()
	w, ok := m.workers[key]
	if !ok || w.activeCancel == nil {
		m.mu.Unlock()
		return false
	}
	cancel := w.activeCancel
	m.mu.Unlock()
	cancel()
	if m.sessions != nil {
		m.sessions.Cancel(AutoReviewLaneID)
	}
	return true
}

// StopWorkspace halts any active auto-review loop and PR review for workspace.
func (m *AutoReviewManager) StopWorkspace(workspace string) {
	if m == nil {
		return
	}
	key := autoReviewWorkspaceKey(workspace)
	m.mu.Lock()
	w, ok := m.workers[key]
	if !ok {
		m.mu.Unlock()
		return
	}
	delete(m.workers, key)
	cancelActive := w.activeCancel
	cancelLoop := w.loopCancel
	done := w.doneCh
	w.activeCancel = nil
	w.loopCancel = nil
	w.doneCh = nil
	m.mu.Unlock()

	if cancelActive != nil {
		cancelActive()
	}
	if cancelLoop != nil {
		cancelLoop()
	}
	if done != nil {
		<-done
	}
}

// StopAll halts all active auto-review workers (called on IDE shutdown).
func (m *AutoReviewManager) StopAll() {
	m.mu.Lock()
	var cancels []context.CancelFunc
	for _, w := range m.workers {
		if w.activeCancel != nil {
			cancels = append(cancels, w.activeCancel)
			w.activeCancel = nil
		}
		if w.loopCancel != nil {
			cancels = append(cancels, w.loopCancel)
			w.loopCancel = nil
		}
	}
	m.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

func (m *AutoReviewManager) runLoop(ctx context.Context, w *autoReviewWorker) {
	defer close(w.doneCh)

	// Immediate first check when turned on.
	m.pollAndProcessOne(ctx, w)

	for {
		m.mu.Lock()
		interval := time.Duration(w.config.EffectivePollIntervalSeconds()) * time.Second
		w.nextPollAt = m.now().Add(interval)
		st := m.snapshotLocked(w)
		m.mu.Unlock()
		m.emitStatus(st)

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-w.pollNowCh:
			timer.Stop()
		case <-timer.C:
		}

		if ctx.Err() != nil {
			return
		}
		m.pollAndProcessOne(ctx, w)
	}
}

// pollAndProcessOne performs one Flow poll and reviews at most one eligible PR.
func (m *AutoReviewManager) pollAndProcessOne(ctx context.Context, w *autoReviewWorker) {
	m.mu.Lock()
	if !w.config.Enabled {
		m.mu.Unlock()
		return
	}
	cfg := w.config
	w.state = "polling"
	w.lastError = ""
	st := m.snapshotLocked(w)
	m.mu.Unlock()
	m.emitStatus(st)

	pollCtx, pollCancel := context.WithTimeout(ctx, 25*time.Second)
	prs, err := m.ListPRsFn(pollCtx, w.workspace)
	pollCancel()

	now := m.now()
	if err != nil {
		m.mu.Lock()
		w.lastPollAt = now
		w.state = "error"
		w.lastError = err.Error()
		st = m.snapshotLocked(w)
		m.mu.Unlock()
		m.emitStatus(st)
		return
	}

	m.mu.Lock()
	w.lastPollAt = now
	w.disk.RecentRuns = pruneRunsOlderThanHour(w.disk.RecentRuns, now)
	eligible := SelectEligibleAutoReviewPRs(prs, cfg, w.disk.Seen, now)
	w.queueCount = len(eligible)

	if len(eligible) == 0 {
		w.state = "idle"
		w.activePR = 0
		w.activeTitle = ""
		st = m.snapshotLocked(w)
		m.mu.Unlock()
		m.emitStatus(st)
		return
	}

	if len(w.disk.RecentRuns) >= cfg.EffectiveMaxPRsPerHour() {
		w.state = "paused_limit"
		w.lastError = fmt.Sprintf("Limite de %d PRs/hora atingido; aguardando janela liberar.", cfg.EffectiveMaxPRsPerHour())
		st = m.snapshotLocked(w)
		m.mu.Unlock()
		m.emitStatus(st)
		return
	}

	target := eligible[0]
	prCtx, prCancel := context.WithCancel(ctx)
	w.activeCancel = prCancel
	w.state = "reviewing"
	w.activePR = target.Number
	w.activeTitle = target.Title
	st = m.snapshotLocked(w)
	m.mu.Unlock()
	m.emitStatus(st)

	started := m.now()
	reviewRoot := ""
	cleanup := func() {}
	ranInWorktree := false
	if cfg.EffectiveRunTests() && m.PrepareWTFn != nil {
		wtPath, fn, wtErr := m.PrepareWTFn(prCtx, w.workspace, target)
		if wtErr == nil && wtPath != "" {
			reviewRoot = wtPath
			cleanup = fn
			ranInWorktree = true
		}
	}
	defer cleanup()

	commentOnly := cfg.EffectiveReviewAction() == "comment_only"
	objective := BuildAutoReviewObjective(target, ranInWorktree, commentOnly)
	teamName := ""
	if tf, lErr := team.Load(w.workspace); lErr == nil {
		teamName = tf.Active
	}

	res, runErr := m.RunReviewFn(prCtx, w.workspace, reviewRoot, teamName, objective)
	wasCanceled := prCtx.Err() == context.Canceled
	prCancel()

	finished := m.now()
	durMs := finished.Sub(started).Milliseconds()
	fp := PRAutoReviewFingerprint(target)
	key := strconv.Itoa(target.Number)

	m.mu.Lock()
	w.activeCancel = nil
	w.activePR = 0
	w.activeTitle = ""
	if w.queueCount > 0 {
		w.queueCount--
	}

	entry := AutoReviewEntry{
		PRNumber:   target.Number,
		Title:      target.Title,
		Author:     target.Author,
		HeadRef:    target.HeadRef,
		BaseRef:    target.BaseRef,
		Tokens:     res.TotalTokens,
		DurationMs: durMs,
		SessionID:  res.SessionID,
		ReviewedAt: finished.Format(time.RFC3339),
	}

	if runErr != nil {
		if wasCanceled {
			entry.Status = "canceled"
			entry.Error = "Revisão interrompida"
			w.state = "idle"
		} else {
			entry.Status = "error"
			entry.Error = strx.TruncateBytes(runErr.Error(), 300, "…")
			prev := w.disk.Seen[key]
			fails := 1
			if prev.Fingerprint == fp {
				fails = prev.FailCount + 1
			}
			backoff := 5 * time.Minute
			if fails == 2 {
				backoff = 15 * time.Minute
			} else if fails >= 3 {
				backoff = 60 * time.Minute
			}
			w.disk.Seen[key] = autoReviewRecord{
				Fingerprint: fp,
				FailCount:   fails,
				NextRetryAt: finished.Add(backoff),
			}
			lowErr := strings.ToLower(runErr.Error())
			if strings.Contains(lowErr, "429") || strings.Contains(lowErr, "quota") || strings.Contains(lowErr, "unauthorized") || strings.Contains(lowErr, "401") {
				w.state = "paused_limit"
			} else {
				w.state = "error"
			}
			w.lastError = entry.Error
		}
	} else {
		entry.Status = "reviewed"
		entry.Summary = strx.TruncateBytes(strings.TrimSpace(res.Summary), 280, "…")
		w.disk.Seen[key] = autoReviewRecord{
			Fingerprint: fp,
			ReviewedAt:  finished,
			FailCount:   0,
		}
		w.disk.RecentRuns = append(pruneRunsOlderThanHour(w.disk.RecentRuns, finished), finished)
		w.state = "idle"
		w.lastError = ""
	}

	w.disk.History = append([]AutoReviewEntry{entry}, w.disk.History...)
	if len(w.disk.History) > 20 {
		w.disk.History = w.disk.History[:20]
	}
	saveAutoReviewDiskState(w.workspace, w.disk)
	st = m.snapshotLocked(w)
	m.mu.Unlock()
	m.emitStatus(st)

	// If there are still more eligible PRs queued and we succeeded, kick the next check promptly.
	if runErr == nil && st.QueueCount > 0 && ctx.Err() == nil {
		select {
		case w.pollNowCh <- struct{}{}:
		default:
		}
	}
}

func (m *AutoReviewManager) snapshotLocked(w *autoReviewWorker) AutoReviewStatus {
	now := m.now()
	st := AutoReviewStatus{
		Workspace:        w.workspace,
		Enabled:          w.config.Enabled,
		State:            w.state,
		Config:           normalizeAutoReviewSettings(w.config),
		ActivePR:         w.activePR,
		ActivePRTitle:    w.activeTitle,
		QueueCount:       w.queueCount,
		ReviewedLastHour: countRunsLastHour(w.disk.RecentRuns, now),
		LastError:        w.lastError,
		History:          append([]AutoReviewEntry(nil), w.disk.History...),
	}
	if !w.lastPollAt.IsZero() {
		st.LastPollAt = w.lastPollAt.Format(time.RFC3339)
	}
	if !w.nextPollAt.IsZero() && w.config.Enabled {
		st.NextPollAt = w.nextPollAt.Format(time.RFC3339)
	}
	return st
}

func (m *AutoReviewManager) emitStatus(st AutoReviewStatus) {
	if m.emit != nil {
		m.emit(st)
	}
}

func normalizeAutoReviewSettings(in team.AutoReviewSettings) team.AutoReviewSettings {
	runTests := in.EffectiveRunTests()
	return team.AutoReviewSettings{
		Enabled:             in.Enabled,
		PollIntervalSeconds: in.EffectivePollIntervalSeconds(),
		RunTests:            &runTests,
		ReviewAction:        in.EffectiveReviewAction(),
		IncludeDrafts:       in.IncludeDrafts,
		MaxPRsPerHour:       in.EffectiveMaxPRsPerHour(),
		MaxTokensPerPR:      in.EffectiveMaxTokensPerPR(0),
		Model:               strings.TrimSpace(in.Model),
	}
}

func defaultAutoReviewConfig(workspace string) team.AutoReviewSettings {
	if tf, err := team.Load(workspace); err == nil {
		return normalizeAutoReviewSettings(tf.Settings.AutoReview)
	}
	return normalizeAutoReviewSettings(team.AutoReviewSettings{})
}

func pruneRunsOlderThanHour(runs []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-1 * time.Hour)
	var out []time.Time
	for _, t := range runs {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}

func countRunsLastHour(runs []time.Time, now time.Time) int {
	return len(pruneRunsOlderThanHour(runs, now))
}

func autoReviewStatePath(workspaceDir string) string {
	return workspace.AutoReviewStatePath(workspaceDir)
}

func loadAutoReviewDiskState(workspace string) autoReviewDiskState {
	st := autoReviewDiskState{Seen: make(map[string]autoReviewRecord)}
	data, err := os.ReadFile(autoReviewStatePath(workspace))
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	if st.Seen == nil {
		st.Seen = make(map[string]autoReviewRecord)
	}
	return st
}

func saveAutoReviewDiskState(workspace string, st autoReviewDiskState) {
	p := autoReviewStatePath(workspace)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = atomicWriteFile(p, data, 0o600)
}
