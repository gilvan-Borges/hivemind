package idecore

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
)

// DefaultGitHubPollInterval controls how often background GitHub PR sync runs per workspace.
const DefaultGitHubPollInterval = 60 * time.Second

// GitHubNotificationEvent describes one newly detected GitHub activity relevant to the current user.
type GitHubNotificationEvent struct {
	Kind     string `json:"kind"` // "mention" | "my_pr_comment" | "my_pr_changes" | "my_pr_approved" | "review_requested" | "pr_reply" | "pr_commit"
	PRNumber int    `json:"prNumber"`
	Title    string `json:"title"`
	Author   string `json:"author,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Snippet  string `json:"snippet,omitempty"`
	URL      string `json:"url,omitempty"`
	IsMine   bool   `json:"isMine,omitempty"`
}

// GitHubSyncEvent is emitted to the IDE frontend after each background GitHub poll.
type GitHubSyncEvent struct {
	Workspace string                    `json:"workspace"`
	PRs       []ProjectPullRequest      `json:"prs"`
	Events    []GitHubNotificationEvent `json:"events,omitempty"`
	SyncedAt  string                    `json:"syncedAt"`
}

// DiffGitHubPullRequests compares previous and current PR snapshots and returns only
// newly surfaced events that require the user's attention (mentions, comments on own PR,
// review requests, approvals/changes, or new replies/commits in active review flow).
func DiffGitHubPullRequests(prev, curr []ProjectPullRequest) []GitHubNotificationEvent {
	prKey := func(p ProjectPullRequest) string {
		if u := strings.TrimSpace(p.URL); u != "" {
			return u
		}
		return fmt.Sprintf("%s#%d", strings.ToLower(strings.TrimSpace(p.Repo)), p.Number)
	}
	prevByKey := make(map[string]ProjectPullRequest, len(prev))
	for _, p := range prev {
		prevByKey[prKey(p)] = p
	}

	var events []GitHubNotificationEvent
	for _, cur := range curr {
		old, existed := prevByKey[prKey(cur)]
		actAdvanced := cur.LastActivityAt != "" && cur.LastActivityAt != old.LastActivityAt
		reasonChanged := cur.AttentionReason != "" && cur.AttentionReason != old.AttentionReason
		becameAttention := cur.NeedsAttention && (!existed || !old.NeedsAttention)

		// 1. Direct @mention in someone else's PR (or own PR) — highest priority.
		if cur.MentionedMe && (!existed || !old.MentionedMe || actAdvanced) {
			events = append(events, GitHubNotificationEvent{
				Kind:     "mention",
				PRNumber: cur.Number,
				Title:    cur.Title,
				Author:   firstNonEmpty(cur.LastActivityAuthor, cur.Author),
				Reason:   "Você foi marcado",
				Snippet:  cur.LastActivitySnippet,
				URL:      cur.URL,
				IsMine:   cur.IsMine,
			})
			continue
		}

		// 2. Own PR activity (ready to merge, changes requested, or new comment/reply).
		if cur.IsMine {
			if cur.AttentionReason == "Pronto p/ merge" && (!existed || old.AttentionReason != "Pronto p/ merge") {
				events = append(events, GitHubNotificationEvent{
					Kind:     "my_pr_approved",
					PRNumber: cur.Number,
					Title:    cur.Title,
					Author:   cur.LastActivityAuthor,
					Reason:   "Pronto p/ merge",
					Snippet:  cur.LastActivitySnippet,
					URL:      cur.URL,
					IsMine:   true,
				})
				continue
			}
			if cur.NeedsAttention && (becameAttention || actAdvanced || reasonChanged) {
				kind := "my_pr_comment"
				if cur.AttentionReason == "Ajustes" || cur.LastActivityKind == "review_changes" {
					kind = "my_pr_changes"
				}
				events = append(events, GitHubNotificationEvent{
					Kind:     kind,
					PRNumber: cur.Number,
					Title:    cur.Title,
					Author:   cur.LastActivityAuthor,
					Reason:   cur.AttentionReason,
					Snippet:  cur.LastActivitySnippet,
					URL:      cur.URL,
					IsMine:   true,
				})
			}
			continue
		}

		// 3. Third-party PRs entering or updating in the user's review Flow.
		if !cur.NeedsAttention && !(cur.ReviewRequested && cur.MyResponse == "") {
			continue
		}

		if (cur.AttentionReason == "Sua revisão" || cur.AttentionReason == "Re-revisão" || (cur.ReviewRequested && cur.MyResponse == "")) &&
			(!existed || (!old.ReviewRequested && !old.NeedsAttention) || (reasonChanged && (cur.AttentionReason == "Re-revisão" || cur.AttentionReason == "Sua revisão"))) {
			events = append(events, GitHubNotificationEvent{
				Kind:     "review_requested",
				PRNumber: cur.Number,
				Title:    cur.Title,
				Author:   cur.Author,
				Reason:   firstNonEmpty(cur.AttentionReason, "Sua revisão"),
				URL:      cur.URL,
				IsMine:   false,
			})
			continue
		}

		if cur.AttentionReason == "Novo commit" && (becameAttention || reasonChanged || (cur.UpdatedAt != "" && cur.UpdatedAt != old.UpdatedAt)) {
			events = append(events, GitHubNotificationEvent{
				Kind:     "pr_commit",
				PRNumber: cur.Number,
				Title:    cur.Title,
				Author:   cur.Author,
				Reason:   "Novo commit",
				URL:      cur.URL,
				IsMine:   false,
			})
			continue
		}

		if cur.NeedsAttention && (becameAttention || actAdvanced) {
			events = append(events, GitHubNotificationEvent{
				Kind:     "pr_reply",
				PRNumber: cur.Number,
				Title:    cur.Title,
				Author:   firstNonEmpty(cur.LastActivityAuthor, cur.Author),
				Reason:   firstNonEmpty(cur.AttentionReason, "Nova resposta"),
				Snippet:  cur.LastActivitySnippet,
				URL:      cur.URL,
				IsMine:   false,
			})
		}
	}
	return events
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// GitHubMonitor runs lightweight periodic PR sync loops per open workspace
// and emits GitHubSyncEvent whenever PRs refresh or new user-relevant activity arrives.
type GitHubMonitor struct {
	mu       sync.Mutex
	emit     func(GitHubSyncEvent)
	workers  map[string]*githubWatchWorker
	interval time.Duration

	// Injectable hooks for deterministic unit testing.
	ListPRsFn    func(ctx context.Context, workspace string) ([]ProjectPullRequest, error)
	IsGitHubFn   func(ctx context.Context, workspace string) bool
	NowFn        func() time.Time
	InitialDelay time.Duration
}

type githubWatchWorker struct {
	workspace  string
	hasBase    bool
	lastPRs    []ProjectPullRequest
	lastPollAt time.Time
	failStreak int
	cancel     context.CancelFunc
	triggerCh  chan struct{}
	doneCh     chan struct{}
}

// NewGitHubMonitor constructs a GitHubMonitor with the default polling interval.
func NewGitHubMonitor(emit func(GitHubSyncEvent)) *GitHubMonitor {
	return &GitHubMonitor{
		emit:         emit,
		workers:      make(map[string]*githubWatchWorker),
		interval:     DefaultGitHubPollInterval,
		ListPRsFn:    ListProjectPullRequests,
		IsGitHubFn:   isGitHubWorkspace,
		NowFn:        time.Now,
		InitialDelay: 2 * time.Second,
	}
}

func isGitHubWorkspace(ctx context.Context, workspace string) bool {
	g := gitx.Local{}
	repos, err := discoverGitRepoPaths(ctx, workspace)
	if err != nil {
		return false
	}
	for _, repo := range repos {
		remoteURL, err := g.RemoteURL(ctx, repo.root)
		if err != nil {
			continue
		}
		if _, _, ok := githubx.ParseRemoteForConfiguredHosts(remoteURL); ok {
			return true
		}
	}
	return false
}

func githubWorkspaceKey(workspace string) string {
	abs, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil {
		abs = strings.TrimSpace(workspace)
	}
	return strings.ToLower(filepath.Clean(abs))
}

func (m *GitHubMonitor) now() time.Time {
	if m.NowFn != nil {
		return m.NowFn()
	}
	return time.Now()
}

func (m *GitHubMonitor) pollInterval() time.Duration {
	if m.interval > 0 {
		return m.interval
	}
	return DefaultGitHubPollInterval
}

// EnsureWatching starts a background sync loop for workspace if not already running.
func (m *GitHubMonitor) EnsureWatching(parentCtx context.Context, workspace string) {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return
	}
	key := githubWorkspaceKey(ws)
	m.mu.Lock()
	if _, exists := m.workers[key]; exists {
		m.mu.Unlock()
		return
	}
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	loopCtx, cancel := context.WithCancel(parentCtx)
	w := &githubWatchWorker{
		workspace: ws,
		cancel:    cancel,
		triggerCh: make(chan struct{}, 1),
		doneCh:    make(chan struct{}),
	}
	m.workers[key] = w
	m.mu.Unlock()

	go m.runLoop(loopCtx, key, w)
}

// TriggerNow requests an immediate poll for workspace (no-op if not watched).
func (m *GitHubMonitor) TriggerNow(workspace string) {
	key := githubWorkspaceKey(workspace)
	m.mu.Lock()
	w := m.workers[key]
	m.mu.Unlock()
	if w == nil {
		return
	}
	select {
	case w.triggerCh <- struct{}{}:
	default:
	}
}

// RecordSnapshot updates the worker's cached PR snapshot after a manual refresh
// so the next background poll does not duplicate notifications for already-seen state.
func (m *GitHubMonitor) RecordSnapshot(workspace string, prs []ProjectPullRequest) {
	key := githubWorkspaceKey(workspace)
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.workers[key]
	if w == nil {
		return
	}
	cloned := append([]ProjectPullRequest(nil), prs...)
	w.lastPRs = cloned
	w.hasBase = true
	w.lastPollAt = m.now()
	w.failStreak = 0
}

// CachedPRs returns the last polled PR snapshot for workspace when it is fresher
// than maxAge (or any cached snapshot when maxAge <= 0). Used so AutoReviewManager
// reuses GitHubMonitor results instead of polling GitHub twice.
func (m *GitHubMonitor) CachedPRs(workspace string, maxAge time.Duration) ([]ProjectPullRequest, bool) {
	if m == nil {
		return nil, false
	}
	key := githubWorkspaceKey(workspace)
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.workers[key]
	if w == nil || !w.hasBase {
		return nil, false
	}
	if maxAge > 0 && (w.lastPollAt.IsZero() || m.now().Sub(w.lastPollAt) > maxAge) {
		return nil, false
	}
	cloned := append([]ProjectPullRequest(nil), w.lastPRs...)
	return cloned, true
}

// StopWorkspace stops background polling for a single workspace.
func (m *GitHubMonitor) StopWorkspace(workspace string) {
	key := githubWorkspaceKey(workspace)
	m.mu.Lock()
	w := m.workers[key]
	delete(m.workers, key)
	m.mu.Unlock()
	if w != nil {
		w.cancel()
		<-w.doneCh
	}
}

// StopAll stops all workspace GitHub watchers cleanly.
func (m *GitHubMonitor) StopAll() {
	m.mu.Lock()
	list := make([]*githubWatchWorker, 0, len(m.workers))
	for k, w := range m.workers {
		list = append(list, w)
		delete(m.workers, k)
	}
	m.mu.Unlock()
	for _, w := range list {
		w.cancel()
		<-w.doneCh
	}
}

func (m *GitHubMonitor) runLoop(ctx context.Context, key string, w *githubWatchWorker) {
	defer close(w.doneCh)

	if m.InitialDelay > 0 {
		timer := time.NewTimer(m.InitialDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-w.triggerCh:
			timer.Stop()
		case <-timer.C:
		}
	}

	for {
		m.pollOnce(ctx, key, w)

		wait := m.pollInterval()
		m.mu.Lock()
		streak := w.failStreak
		m.mu.Unlock()
		if streak > 1 {
			mult := streak
			if mult > 5 {
				mult = 5
			}
			wait = time.Duration(mult) * wait
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-w.triggerCh:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (m *GitHubMonitor) pollOnce(ctx context.Context, _ string, w *githubWatchWorker) {
	if ctx.Err() != nil {
		return
	}
	checkCtx, cancelCheck := context.WithTimeout(ctx, 5*time.Second)
	isGH := true
	if m.IsGitHubFn != nil {
		isGH = m.IsGitHubFn(checkCtx, w.workspace)
	}
	cancelCheck()
	if !isGH {
		m.mu.Lock()
		w.failStreak = 3 // back off gently on non-GitHub folders
		m.mu.Unlock()
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	listFn := m.ListPRsFn
	if listFn == nil {
		listFn = ListProjectPullRequests
	}
	prs, err := listFn(reqCtx, w.workspace)
	if err != nil {
		m.mu.Lock()
		w.failStreak++
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	var events []GitHubNotificationEvent
	if w.hasBase {
		events = DiffGitHubPullRequests(w.lastPRs, prs)
	}
	w.lastPRs = append([]ProjectPullRequest(nil), prs...)
	w.hasBase = true
	w.lastPollAt = m.now()
	w.failStreak = 0
	m.mu.Unlock()

	if m.emit != nil {
		m.emit(GitHubSyncEvent{
			Workspace: w.workspace,
			PRs:       prs,
			Events:    events,
			SyncedAt:  m.now().UTC().Format(time.RFC3339),
		})
	}
}

// FormatGitHubNotificationSummary returns a concise human-readable title and body for one event.
func FormatGitHubNotificationSummary(ev GitHubNotificationEvent) (title, body string) {
	who := ""
	if a := strings.TrimSpace(ev.Author); a != "" {
		who = "@" + a
	}
	switch ev.Kind {
	case "mention":
		if who != "" {
			title = fmt.Sprintf("%s marcou você no PR #%d", who, ev.PRNumber)
		} else {
			title = fmt.Sprintf("Você foi marcado no PR #%d", ev.PRNumber)
		}
	case "my_pr_approved":
		title = fmt.Sprintf("PR #%d pronto para merge", ev.PRNumber)
	case "my_pr_changes":
		title = fmt.Sprintf("Ajustes pedidos no seu PR #%d", ev.PRNumber)
	case "my_pr_comment":
		if who != "" {
			title = fmt.Sprintf("%s comentou no seu PR #%d", who, ev.PRNumber)
		} else {
			title = fmt.Sprintf("Novo comentário no seu PR #%d", ev.PRNumber)
		}
	case "review_requested":
		if ev.Reason == "Re-revisão" {
			title = fmt.Sprintf("Re-revisão pedida no PR #%d", ev.PRNumber)
		} else {
			title = fmt.Sprintf("Revisão solicitada no PR #%d", ev.PRNumber)
		}
	case "pr_commit":
		title = fmt.Sprintf("Novo commit no PR #%d", ev.PRNumber)
	default:
		if who != "" {
			title = fmt.Sprintf("Nova resposta de %s no PR #%d", who, ev.PRNumber)
		} else {
			title = fmt.Sprintf("Nova atividade no PR #%d", ev.PRNumber)
		}
	}

	if sn := strings.TrimSpace(ev.Snippet); sn != "" {
		if strings.TrimSpace(ev.Title) != "" {
			body = fmt.Sprintf("%s — “%s”", ev.Title, sn)
		} else {
			body = sn
		}
	} else {
		body = strings.TrimSpace(ev.Title)
	}
	return title, body
}
