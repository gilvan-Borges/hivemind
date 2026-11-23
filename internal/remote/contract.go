package remote

import (
	"context"

	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// This file holds the mobile-bridge CONTRACT: the DTO types serialized to the
// HiveMind-App and the Deps interface the HTTP/SSE server drives. The types
// live in the ROOT module so `hivemind serve` (CLI) can run the bridge
// headless without the IDE module (Spec 09 §3.3). The IDE's idecore package
// re-exports them as type aliases, so existing IDE code and the JSON wire
// format stay untouched.

// ImageInput carries a base64 or data-URL encoded image from the chat UI.
type ImageInput struct {
	Name       string `json:"name"`
	MIMEType   string `json:"mimeType"`
	DataBase64 string `json:"dataBase64"`
}

// LaneEvent wraps an orchestrator.Event with the lane ID and team name.
type LaneEvent struct {
	LaneID string             `json:"laneId"`
	Team   string             `json:"team"`
	Event  orchestrator.Event `json:"event"`
}

// AutoReviewEntry is one finished automatic review in the workspace history.
type AutoReviewEntry struct {
	PRNumber   int    `json:"prNumber"`
	Title      string `json:"title"`
	Author     string `json:"author,omitempty"`
	HeadRef    string `json:"headRef,omitempty"`
	BaseRef    string `json:"baseRef,omitempty"`
	Status     string `json:"status"` // "reviewed" | "error" | "canceled"
	Summary    string `json:"summary,omitempty"`
	Tokens     int64  `json:"tokens,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	ReviewedAt string `json:"reviewedAt"`
	Error      string `json:"error,omitempty"`
}

// AutoReviewStatus is the live snapshot emitted to the IDE Flow tab and status bar.
type AutoReviewStatus struct {
	Workspace        string                  `json:"workspace"`
	Enabled          bool                    `json:"enabled"`
	State            string                  `json:"state"` // "idle" | "polling" | "reviewing" | "paused_limit" | "error"
	Config           team.AutoReviewSettings `json:"config"`
	ActivePR         int                     `json:"activePr,omitempty"`
	ActivePRTitle    string                  `json:"activePrTitle,omitempty"`
	QueueCount       int                     `json:"queueCount"`
	ReviewedLastHour int                     `json:"reviewedLastHour"`
	LastPollAt       string                  `json:"lastPollAt,omitempty"`
	NextPollAt       string                  `json:"nextPollAt,omitempty"`
	LastError        string                  `json:"lastError,omitempty"`
	History          []AutoReviewEntry       `json:"history,omitempty"`
}

// DiffLineKind classifies a line inside a unified diff.
type DiffLineKind string

const (
	DiffHeader  DiffLineKind = "header"
	DiffHunk    DiffLineKind = "hunk"
	DiffAdded   DiffLineKind = "added"
	DiffRemoved DiffLineKind = "removed"
	DiffContext DiffLineKind = "context"
)

// DiffLine is one colored line in the diff viewer.
type DiffLine struct {
	Kind DiffLineKind `json:"kind"`
	Text string       `json:"text"`
}

// FileDiff holds the parsed patch and line stats for one modified file.
type FileDiff struct {
	Path    string     `json:"path"`
	Status  string     `json:"status,omitempty"` // "M", "U", "A", "D", "R"
	Staged  bool       `json:"staged,omitempty"`
	Added   int        `json:"added"`
	Removed int        `json:"removed"`
	Lines   []DiffLine `json:"lines"`
}

// ProjectSubtask represents a Jira subtask or child issue summarized inside a parent issue card.
type ProjectSubtask struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Status  string `json:"status,omitempty"`
	Done    bool   `json:"done"`
	URL     string `json:"url,omitempty"`
}

// ProjectIssue represents a Jira or GitHub issue listed in the sidebar.
type ProjectIssue struct {
	Source         string           `json:"source"` // "jira" or "github"
	Key            string           `json:"key"`    // e.g. "PROJ-12" or "#42"
	Title          string           `json:"title"`
	Body           string           `json:"body,omitempty"`
	Status         string           `json:"status,omitempty"`
	IssueType      string           `json:"issueType,omitempty"`
	IsSubtask      bool             `json:"isSubtask,omitempty"`
	ParentKey      string           `json:"parentKey,omitempty"`
	ParentTitle    string           `json:"parentTitle,omitempty"`
	ParentURL      string           `json:"parentUrl,omitempty"`
	SubtaskTotal   int              `json:"subtaskTotal,omitempty"`
	SubtaskDone    int              `json:"subtaskDone,omitempty"`
	SubtaskPending int              `json:"subtaskPending,omitempty"`
	Subtasks       []ProjectSubtask `json:"subtasks,omitempty"`
	Author         string           `json:"author,omitempty"`
	URL            string           `json:"url,omitempty"`
	IsMine         bool             `json:"isMine,omitempty"`
	Repo           string           `json:"repo,omitempty"`
	RepoRoot       string           `json:"repoRoot,omitempty"`
}

// ProjectPullRequest represents an open GitHub pull request listed in the sidebar.
type ProjectPullRequest struct {
	Number              int      `json:"number"`
	Title               string   `json:"title"`
	Body                string   `json:"body,omitempty"`
	URL                 string   `json:"url"`
	State               string   `json:"state"`
	IsDraft             bool     `json:"isDraft"`
	Author              string   `json:"author,omitempty"`
	Repo                string   `json:"repo,omitempty"`
	RepoRoot            string   `json:"repoRoot,omitempty"`
	HeadRef             string   `json:"headRef,omitempty"`
	BaseRef             string   `json:"baseRef,omitempty"`
	UpdatedAt           string   `json:"updatedAt,omitempty"`
	ReviewDecision      string   `json:"reviewDecision,omitempty"`
	RequestedReviewers  []string `json:"requestedReviewers,omitempty"`
	IsMine              bool     `json:"isMine,omitempty"`
	ReviewRequested     bool     `json:"reviewRequested,omitempty"`
	MyResponse          string   `json:"myResponse,omitempty"`
	NeedsAttention      bool     `json:"needsAttention,omitempty"`
	AttentionReason     string   `json:"attentionReason,omitempty"`
	MentionedMe         bool     `json:"mentionedMe,omitempty"`
	LastActivityAt      string   `json:"lastActivityAt,omitempty"`
	LastActivityAuthor  string   `json:"lastActivityAuthor,omitempty"`
	LastActivityKind    string   `json:"lastActivityKind,omitempty"`
	LastActivitySnippet string   `json:"lastActivitySnippet,omitempty"`
}

// Deps abstracts the host services the HTTP API drives. It is implemented by
// the IDE (idecore.Adapter over SessionManager/ResidentManager/AutoReviewManager)
// and by the headless CLI serve host. Session listings read directly from the
// sessionx store so both hosts share one implementation.
type Deps interface {
	RunStart(ctx context.Context, laneID, workspace, teamName, objective, jiraKey, sessionID, runMode string, images []ImageInput) error
	RunCancel(laneID string) bool
	// RunSteer injects a live directive into a running lane without cancelling it.
	// Returns false when no run is active.
	RunSteer(laneID, message string) bool
	ApprovalResolve(id string, approved bool) bool
	ListSessions(ctx context.Context, workspace string) (sessionx.WorkspaceSessionsSummary, error)
	LoadSession(ctx context.Context, workspace, id string) (sessionx.SessionDetail, error)
	Diff(ctx context.Context, workspace string) ([]FileDiff, error)
	ListPRs(ctx context.Context, workspace string) ([]ProjectPullRequest, error)
	ListIssues(ctx context.Context, workspace string) ([]ProjectIssue, error)
	AutoReviewStatus(workspace string) AutoReviewStatus
	AutoReviewPoll(workspace string) AutoReviewStatus
}
