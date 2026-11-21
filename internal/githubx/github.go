// Package githubx provides a native GitHub REST API v2022-11-28 connector
// with an automatic `gh api` CLI fallback.
package githubx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// PullRequest is a pull request the connector created or found.
type PullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
}

// Comment is a general PR/issue comment or an inline code review comment.
type Comment struct {
	ID          int64  `json:"id"`
	Author      string `json:"author"`
	Body        string `json:"body"`
	Path        string `json:"path,omitempty"`
	Line        int    `json:"line,omitempty"`
	DiffHunk    string `json:"diffHunk,omitempty"`
	InReplyToID int64  `json:"inReplyToId,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

// PRReview represents a code review submitted on a pull request.
type PRReview struct {
	ID          int64  `json:"id"`
	Author      string `json:"author"`
	State       string `json:"state"` // "APPROVED", "CHANGES_REQUESTED", "COMMENTED", "DISMISSED"
	Body        string `json:"body"`
	SubmittedAt string `json:"submittedAt,omitempty"`
}

// PRFile represents a changed file and diff patch in a pull request.
type PRFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch,omitempty"`
}

// PullRequestSummary is a concise pull request entry returned by ListPullRequests.
type PullRequestSummary struct {
	Number             int      `json:"number"`
	Title              string   `json:"title"`
	URL                string   `json:"url"`
	State              string   `json:"state"`
	Draft              bool     `json:"draft"`
	Author             string   `json:"author"`
	HeadRef            string   `json:"headRef"`
	BaseRef            string   `json:"baseRef"`
	RequestedReviewers []string `json:"requestedReviewers,omitempty"`
	UpdatedAt          string   `json:"updatedAt,omitempty"`
}

// IssueDetail represents a GitHub issue with labels and comments.
type IssueDetail struct {
	Number   int       `json:"number"`
	Title    string    `json:"title"`
	Body     string    `json:"body"`
	URL      string    `json:"url"`
	State    string    `json:"state"`
	Author   string    `json:"author"`
	Labels   []string  `json:"labels,omitempty"`
	Comments []Comment `json:"comments,omitempty"`
}

// PullRequestDetail contains full authorship, branch, review, inline comment, and conversation state of a PR.
type PullRequestDetail struct {
	Number             int        `json:"number"`
	Title              string     `json:"title"`
	Body               string     `json:"body"`
	URL                string     `json:"url"`
	State              string     `json:"state"` // "open", "closed"
	Merged             bool       `json:"merged"`
	Author             string     `json:"author"`
	HeadRef            string     `json:"headRef"`
	BaseRef            string     `json:"baseRef"`
	ChangedFiles       int        `json:"changedFiles,omitempty"`
	Additions          int        `json:"additions,omitempty"`
	Deletions          int        `json:"deletions,omitempty"`
	RequestedReviewers []string   `json:"requestedReviewers,omitempty"`
	Reviews            []PRReview `json:"reviews,omitempty"`
	ReviewComments     []Comment  `json:"reviewComments,omitempty"`
	IssueComments      []Comment  `json:"issueComments,omitempty"`
	CommentFetchErrors []string   `json:"commentFetchErrors,omitempty"`
}

// CreatePR is the input for a new pull request.
type CreatePR struct {
	Title string
	Head  string
	Base  string
	Body  string
}

// Client is the native GitHub surface used by tools, the IDE, and the orchestrator.
type Client interface {
	Enabled() bool
	CurrentUser(ctx context.Context) (string, error)
	CreatePullRequest(ctx context.Context, req CreatePR) (PullRequest, error)
	FindOpenPullRequestByHead(ctx context.Context, head string) (*PullRequest, error)
	GetPullRequest(ctx context.Context, number int) (PullRequestDetail, error)
	ListPullRequests(ctx context.Context, state string, limit int) ([]PullRequestSummary, error)
	ListPullRequestFiles(ctx context.Context, number int) ([]PRFile, error)
	ListPullRequestComments(ctx context.Context, number int) ([]Comment, error)
	ReplyToReviewComment(ctx context.Context, number int, commentID int64, body string) error
	RequestReviewers(ctx context.Context, number int, reviewers []string) error
	SubmitReview(ctx context.Context, number int, event, body string) error
	MergePullRequest(ctx context.Context, number int, method, commitTitle string) error
	ClosePullRequest(ctx context.Context, number int) error
	GetIssue(ctx context.Context, number int) (IssueDetail, error)
	CloseIssue(ctx context.Context, number int) error
	CommentOnIssue(ctx context.Context, number int, body string) error
	ListIssueComments(ctx context.Context, number int) ([]Comment, error)
	CIStatus(ctx context.Context, ref string) (string, error)
}

// PullRequestBodyUpdater is optionally implemented by GitHub clients that support
// editing the body of an existing pull request (used for multi-repo PR cross-linking).
type PullRequestBodyUpdater interface {
	UpdatePullRequestBody(ctx context.Context, number int, body string) error
}

// Disabled explains why GitHub calls are turned off.
type Disabled struct{ Reason string }

func (d Disabled) Enabled() bool                               { return false }
func (d Disabled) CurrentUser(context.Context) (string, error) { return "", d.err() }
func (d Disabled) CreatePullRequest(context.Context, CreatePR) (PullRequest, error) {
	return PullRequest{}, d.err()
}
func (d Disabled) FindOpenPullRequestByHead(context.Context, string) (*PullRequest, error) {
	return nil, d.err()
}
func (d Disabled) GetPullRequest(context.Context, int) (PullRequestDetail, error) {
	return PullRequestDetail{}, d.err()
}
func (d Disabled) ListPullRequests(context.Context, string, int) ([]PullRequestSummary, error) {
	return nil, d.err()
}
func (d Disabled) ListPullRequestFiles(context.Context, int) ([]PRFile, error) {
	return nil, d.err()
}
func (d Disabled) ListPullRequestComments(context.Context, int) ([]Comment, error) {
	return nil, d.err()
}
func (d Disabled) ReplyToReviewComment(context.Context, int, int64, string) error { return d.err() }
func (d Disabled) RequestReviewers(context.Context, int, []string) error          { return d.err() }
func (d Disabled) SubmitReview(context.Context, int, string, string) error        { return d.err() }
func (d Disabled) MergePullRequest(context.Context, int, string, string) error    { return d.err() }
func (d Disabled) ClosePullRequest(context.Context, int) error                    { return d.err() }
func (d Disabled) GetIssue(context.Context, int) (IssueDetail, error) {
	return IssueDetail{}, d.err()
}
func (d Disabled) CloseIssue(context.Context, int) error             { return d.err() }
func (d Disabled) CommentOnIssue(context.Context, int, string) error { return d.err() }
func (d Disabled) ListIssueComments(context.Context, int) ([]Comment, error) {
	return nil, d.err()
}
func (d Disabled) CIStatus(context.Context, string) (string, error) { return "", d.err() }
func (d Disabled) err() error {
	if d.Reason != "" {
		return fmt.Errorf("%s", d.Reason)
	}
	return fmt.Errorf("GitHub não está configurado. Configure o Conector GitHub na IDE, defina GITHUB_TOKEN ou autentique o gh")
}

// REST is the native GitHub REST API v2022-11-28 connector.
type REST struct {
	BaseURL string
	Token   string
	Owner   string
	Repo    string
	HTTP    *http.Client

	mu         sync.Mutex
	cachedUser string
}

func (r *REST) Enabled() bool { return r.Token != "" && r.Owner != "" && r.Repo != "" }

func (r *REST) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return SharedHTTPClient()
}

func (r *REST) base() string {
	if r.BaseURL != "" {
		return strings.TrimRight(r.BaseURL, "/")
	}
	return DefaultAPIBaseURL
}

func (r *REST) do(ctx context.Context, method, path string, body any, dest any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.base()+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "HiveMind-IDE")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := r.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("GitHub %s %s: %s: %s", method, path, res.Status, truncate(string(payload), 500))
	}
	if dest == nil || len(payload) == 0 {
		return nil
	}
	return json.Unmarshal(payload, dest)
}

func (r *REST) CurrentUser(ctx context.Context) (string, error) {
	if envUser := strings.TrimSpace(os.Getenv("GITHUB_USER")); envUser != "" {
		return envUser, nil
	}
	r.mu.Lock()
	if r.cachedUser != "" {
		u := r.cachedUser
		r.mu.Unlock()
		return u, nil
	}
	r.mu.Unlock()

	var raw struct {
		Login string `json:"login"`
	}
	if err := r.do(ctx, http.MethodGet, "/user", nil, &raw); err != nil {
		return "", err
	}
	login := strings.TrimSpace(raw.Login)
	if login != "" {
		r.mu.Lock()
		r.cachedUser = login
		r.mu.Unlock()
	}
	return login, nil
}

func (r *REST) CreatePullRequest(ctx context.Context, req CreatePR) (PullRequest, error) {
	var raw struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
	}
	err := r.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/pulls", url.PathEscape(r.Owner), url.PathEscape(r.Repo)), map[string]string{
		"title": req.Title,
		"head":  req.Head,
		"base":  req.Base,
		"body":  req.Body,
	}, &raw)
	if err != nil {
		return PullRequest{}, err
	}
	return PullRequest{Number: raw.Number, URL: raw.HTMLURL, Title: raw.Title}, nil
}

func (r *REST) FindOpenPullRequestByHead(ctx context.Context, head string) (*PullRequest, error) {
	cleanHead := strings.TrimSpace(head)
	if cleanHead == "" {
		return nil, nil
	}
	var list []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
	}
	q := url.Values{}
	q.Set("state", "open")
	q.Set("head", r.Owner+":"+cleanHead)
	path := fmt.Sprintf("/repos/%s/%s/pulls?%s", url.PathEscape(r.Owner), url.PathEscape(r.Repo), q.Encode())
	if err := r.do(ctx, http.MethodGet, path, nil, &list); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &PullRequest{
		Number: list[0].Number,
		URL:    list[0].HTMLURL,
		Title:  list[0].Title,
	}, nil
}

// rawREST* structs are shared between REST and `gh api` CLI decoding so both
// transports expose the exact same schema and never diverge.
type rawRESTPull struct {
	Number       int    `json:"number"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	HTMLURL      string `json:"html_url"`
	State        string `json:"state"`
	Draft        bool   `json:"draft"`
	Merged       bool   `json:"merged"`
	UpdatedAt    string `json:"updated_at"`
	ChangedFiles int    `json:"changed_files"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	User         struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	RequestedReviewers []struct {
		Login string `json:"login"`
	} `json:"requested_reviewers"`
}

type rawRESTReview struct {
	ID          int64  `json:"id"`
	State       string `json:"state"`
	Body        string `json:"body"`
	SubmittedAt string `json:"submitted_at"`
	User        struct {
		Login string `json:"login"`
	} `json:"user"`
}

type rawRESTReviewComment struct {
	ID           int64  `json:"id"`
	Body         string `json:"body"`
	Path         string `json:"path"`
	Line         int    `json:"line"`
	OriginalLine int    `json:"original_line"`
	DiffHunk     string `json:"diff_hunk"`
	InReplyToID  int64  `json:"in_reply_to_id"`
	CreatedAt    string `json:"created_at"`
	User         struct {
		Login string `json:"login"`
	} `json:"user"`
}

type rawRESTIssueComment struct {
	ID        int64  `json:"id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
}

type rawRESTPRFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Patch     string `json:"patch"`
}

type rawRESTIssue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func mapRawPull(raw rawRESTPull) PullRequestDetail {
	detail := PullRequestDetail{
		Number:       raw.Number,
		Title:        raw.Title,
		Body:         raw.Body,
		URL:          raw.HTMLURL,
		State:        strings.ToLower(raw.State),
		Merged:       raw.Merged,
		Author:       raw.User.Login,
		HeadRef:      raw.Head.Ref,
		BaseRef:      raw.Base.Ref,
		ChangedFiles: raw.ChangedFiles,
		Additions:    raw.Additions,
		Deletions:    raw.Deletions,
	}
	for _, rr := range raw.RequestedReviewers {
		if strings.TrimSpace(rr.Login) != "" {
			detail.RequestedReviewers = append(detail.RequestedReviewers, rr.Login)
		}
	}
	return detail
}

func mapRawReviews(rawReviews []rawRESTReview) []PRReview {
	out := make([]PRReview, 0, len(rawReviews))
	for _, rv := range rawReviews {
		out = append(out, PRReview{
			ID:          rv.ID,
			Author:      rv.User.Login,
			State:       strings.ToUpper(strings.TrimSpace(rv.State)),
			Body:        rv.Body,
			SubmittedAt: rv.SubmittedAt,
		})
	}
	return out
}

func mapRawReviewComments(rawComments []rawRESTReviewComment) []Comment {
	out := make([]Comment, 0, len(rawComments))
	for _, c := range rawComments {
		line := c.Line
		if line == 0 {
			line = c.OriginalLine
		}
		out = append(out, Comment{
			ID:          c.ID,
			Author:      c.User.Login,
			Body:        c.Body,
			Path:        c.Path,
			Line:        line,
			DiffHunk:    c.DiffHunk,
			InReplyToID: c.InReplyToID,
			CreatedAt:   c.CreatedAt,
		})
	}
	return out
}

func mapRawIssueComments(rawComments []rawRESTIssueComment) []Comment {
	out := make([]Comment, 0, len(rawComments))
	for _, c := range rawComments {
		out = append(out, Comment{
			ID:        c.ID,
			Author:    c.User.Login,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
		})
	}
	return out
}

// fetchPage performs one authenticated GET and returns the raw payload plus
// the next page path (empty when there is no Link rel="next").
func (r *REST) fetchPage(ctx context.Context, path string) (payload []byte, nextPath string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.base()+path, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "HiveMind-IDE")
	res, err := r.client().Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, "", err
	}
	if res.StatusCode >= 300 {
		return nil, "", fmt.Errorf("GitHub GET %s: %s: %s", path, res.Status, truncate(string(body), 500))
	}
	if next, ok := ParseLinkNext(res.Header.Get("Link")); ok {
		nextPath, _ = nextPagePath(next, r.base())
	}
	return body, nextPath, nil
}

func (r *REST) GetPullRequest(ctx context.Context, number int) (PullRequestDetail, error) {
	if number <= 0 {
		return PullRequestDetail{}, fmt.Errorf("número de PR inválido: %d", number)
	}
	ownerEsc := url.PathEscape(r.Owner)
	repoEsc := url.PathEscape(r.Repo)
	var raw rawRESTPull
	if err := r.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls/%d", ownerEsc, repoEsc, number), nil, &raw); err != nil {
		return PullRequestDetail{}, err
	}
	detail := mapRawPull(raw)

	var rawReviews []rawRESTReview
	reviewsPath := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews?per_page=%d", ownerEsc, repoEsc, number, defaultPageSize)
	for page := 0; page < maxPages; page++ {
		payload, next, rvErr := r.fetchPage(ctx, reviewsPath)
		if rvErr != nil {
			detail.CommentFetchErrors = append(detail.CommentFetchErrors, fmt.Sprintf("revisões: %v", rvErr))
			break
		}
		var chunk []rawRESTReview
		if jsonErr := json.Unmarshal(payload, &chunk); jsonErr != nil {
			detail.CommentFetchErrors = append(detail.CommentFetchErrors, fmt.Sprintf("revisões: %v", jsonErr))
			break
		}
		rawReviews = append(rawReviews, chunk...)
		if next == "" || len(chunk) == 0 {
			break
		}
		reviewsPath = next
	}
	detail.Reviews = mapRawReviews(rawReviews)

	if revComments, err := r.ListPullRequestComments(ctx, number); err == nil {
		detail.ReviewComments = revComments
	} else {
		detail.CommentFetchErrors = append(detail.CommentFetchErrors, fmt.Sprintf("comentários inline: %v", err))
	}

	if issueComments, err := r.ListIssueComments(ctx, number); err == nil {
		detail.IssueComments = issueComments
	} else {
		detail.CommentFetchErrors = append(detail.CommentFetchErrors, fmt.Sprintf("comentários gerais: %v", err))
	}

	return detail, nil
}

func (r *REST) ListPullRequests(ctx context.Context, state string, limit int) ([]PullRequestSummary, error) {
	st := strings.ToLower(strings.TrimSpace(state))
	if st == "" {
		st = "open"
	}
	switch st {
	case "open", "closed", "all":
	default:
		return nil, fmt.Errorf("estado de PR inválido %q (use open, closed ou all)", state)
	}
	if limit <= 0 {
		limit = 30
	}
	// Paginated by the Link header so a request above 100 items (or a repo
	// with more open PRs than one page) is no longer silently truncated.
	var out []PullRequestSummary
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=%s&per_page=%d", url.PathEscape(r.Owner), url.PathEscape(r.Repo), st, defaultPageSize)
	for page := 0; page < maxPages && len(out) < limit; page++ {
		payload, next, err := r.fetchPage(ctx, path)
		if err != nil {
			return nil, err
		}
		var raw []rawRESTPull
		if err := json.Unmarshal(payload, &raw); err != nil {
			return nil, fmt.Errorf("decodificar lista de PRs: %w", err)
		}
		for _, item := range raw {
			if len(out) >= limit {
				break
			}
			var reqs []string
			for _, rr := range item.RequestedReviewers {
				if strings.TrimSpace(rr.Login) != "" {
					reqs = append(reqs, rr.Login)
				}
			}
			out = append(out, PullRequestSummary{
				Number:             item.Number,
				Title:              item.Title,
				URL:                item.HTMLURL,
				State:              strings.ToLower(item.State),
				Draft:              item.Draft,
				Author:             item.User.Login,
				HeadRef:            item.Head.Ref,
				BaseRef:            item.Base.Ref,
				RequestedReviewers: reqs,
				UpdatedAt:          item.UpdatedAt,
			})
		}
		if next == "" || len(raw) == 0 {
			break
		}
		path = next
	}
	return out, nil
}

func (r *REST) ListPullRequestFiles(ctx context.Context, number int) ([]PRFile, error) {
	if number <= 0 {
		return nil, fmt.Errorf("número de PR inválido: %d", number)
	}
	// Large PRs exceed one page: follow Link rel="next" (capped at maxPages).
	var out []PRFile
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/files?per_page=%d", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number, defaultPageSize)
	for page := 0; page < maxPages; page++ {
		payload, next, err := r.fetchPage(ctx, path)
		if err != nil {
			return nil, err
		}
		var raw []rawRESTPRFile
		if err := json.Unmarshal(payload, &raw); err != nil {
			return nil, err
		}
		for _, f := range raw {
			out = append(out, PRFile{
				Filename:  f.Filename,
				Status:    f.Status,
				Additions: f.Additions,
				Deletions: f.Deletions,
				Patch:     f.Patch,
			})
		}
		if next == "" || len(raw) == 0 {
			break
		}
		path = next
	}
	return out, nil
}

func (r *REST) ListPullRequestComments(ctx context.Context, number int) ([]Comment, error) {
	if number <= 0 {
		return nil, fmt.Errorf("número de PR inválido: %d", number)
	}
	var all []rawRESTReviewComment
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/comments?per_page=%d", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number, defaultPageSize)
	for page := 0; page < maxPages; page++ {
		payload, next, err := r.fetchPage(ctx, path)
		if err != nil {
			return nil, err
		}
		var raw []rawRESTReviewComment
		if err := json.Unmarshal(payload, &raw); err != nil {
			return nil, err
		}
		all = append(all, raw...)
		if next == "" || len(raw) == 0 {
			break
		}
		path = next
	}
	return mapRawReviewComments(all), nil
}

func (r *REST) ReplyToReviewComment(ctx context.Context, number int, commentID int64, body string) error {
	if number <= 0 {
		return fmt.Errorf("número de PR inválido: %d", number)
	}
	if commentID <= 0 {
		return fmt.Errorf("comment_id inválido: %d", commentID)
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/comments/%d/replies", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number, commentID)
	return r.do(ctx, http.MethodPost, path, map[string]string{"body": body}, nil)
}

func (r *REST) UpdatePullRequestBody(ctx context.Context, number int, body string) error {
	if number <= 0 {
		return fmt.Errorf("número de PR inválido: %d", number)
	}
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number)
	return r.do(ctx, http.MethodPatch, path, map[string]string{"body": body}, nil)
}

func (r *REST) RequestReviewers(ctx context.Context, number int, reviewers []string) error {
	if number <= 0 {
		return fmt.Errorf("número de PR inválido: %d", number)
	}
	clean := cleanLogins(reviewers)
	if len(clean) == 0 {
		return fmt.Errorf("informe ao menos um revisor para solicitar revisão")
	}
	return r.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/pulls/%d/requested_reviewers", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number), map[string]any{
		"reviewers": clean,
	}, nil)
}

func (r *REST) SubmitReview(ctx context.Context, number int, event, body string) error {
	if number <= 0 {
		return fmt.Errorf("número de PR inválido: %d", number)
	}
	ev := strings.ToUpper(strings.TrimSpace(event))
	switch ev {
	case "APPROVE", "REQUEST_CHANGES", "COMMENT":
	default:
		return fmt.Errorf("evento de revisão inválido %q (use APPROVE, REQUEST_CHANGES ou COMMENT)", event)
	}
	return r.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number), map[string]string{
		"event": ev,
		"body":  body,
	}, nil)
}

func (r *REST) MergePullRequest(ctx context.Context, number int, method, commitTitle string) error {
	if number <= 0 {
		return fmt.Errorf("número de PR inválido: %d", number)
	}
	m := strings.ToLower(strings.TrimSpace(method))
	if m == "" {
		m = "merge"
	}
	switch m {
	case "merge", "squash", "rebase":
	default:
		return fmt.Errorf("método de merge inválido %q (use merge, squash ou rebase)", method)
	}
	payload := map[string]string{"merge_method": m}
	if strings.TrimSpace(commitTitle) != "" {
		payload["commit_title"] = strings.TrimSpace(commitTitle)
	}
	return r.do(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number), payload, nil)
}

func (r *REST) ClosePullRequest(ctx context.Context, number int) error {
	if number <= 0 {
		return fmt.Errorf("número de PR inválido: %d", number)
	}
	return r.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number), map[string]string{
		"state": "closed",
	}, nil)
}

func (r *REST) GetIssue(ctx context.Context, number int) (IssueDetail, error) {
	if number <= 0 {
		return IssueDetail{}, fmt.Errorf("número de issue inválido: %d", number)
	}
	var raw rawRESTIssue
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number)
	if err := r.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return IssueDetail{}, err
	}
	detail := IssueDetail{
		Number: raw.Number,
		Title:  raw.Title,
		Body:   raw.Body,
		URL:    raw.HTMLURL,
		State:  strings.ToLower(raw.State),
		Author: raw.User.Login,
	}
	for _, lb := range raw.Labels {
		if strings.TrimSpace(lb.Name) != "" {
			detail.Labels = append(detail.Labels, lb.Name)
		}
	}
	if comments, err := r.ListIssueComments(ctx, number); err == nil {
		detail.Comments = comments
	}
	return detail, nil
}

func (r *REST) CloseIssue(ctx context.Context, number int) error {
	if number <= 0 {
		return fmt.Errorf("número de issue inválido: %d", number)
	}
	return r.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/%s/issues/%d", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number), map[string]string{
		"state": "closed",
	}, nil)
}

func (r *REST) CommentOnIssue(ctx context.Context, number int, body string) error {
	if number <= 0 {
		return fmt.Errorf("número de issue inválido: %d", number)
	}
	return r.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number), map[string]string{
		"body": body,
	}, nil)
}

func (r *REST) ListIssueComments(ctx context.Context, number int) ([]Comment, error) {
	if number <= 0 {
		return nil, fmt.Errorf("número de issue inválido: %d", number)
	}
	var all []rawRESTIssueComment
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=%d", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number, defaultPageSize)
	for page := 0; page < maxPages; page++ {
		payload, next, err := r.fetchPage(ctx, path)
		if err != nil {
			return nil, err
		}
		var raw []rawRESTIssueComment
		if err := json.Unmarshal(payload, &raw); err != nil {
			return nil, err
		}
		all = append(all, raw...)
		if next == "" || len(raw) == 0 {
			break
		}
		path = next
	}
	return mapRawIssueComments(all), nil
}

func sanitizeGitRef(ref string) (string, error) {
	clean := strings.TrimSpace(ref)
	if clean == "" || strings.Contains(clean, "..") || strings.ContainsAny(clean, "?#\\ \t\r\n") {
		return "", fmt.Errorf("referência Git inválida: %q", ref)
	}
	return url.PathEscape(clean), nil
}

func (r *REST) CIStatus(ctx context.Context, ref string) (string, error) {
	escapedRef, err := sanitizeGitRef(ref)
	if err != nil {
		return "", err
	}
	var raw struct {
		State string `json:"state"`
	}
	if err := r.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/commits/%s/status", url.PathEscape(r.Owner), url.PathEscape(r.Repo), escapedRef), nil, &raw); err != nil {
		return "", err
	}
	if raw.State == "" {
		return "pending", nil
	}
	return raw.State, nil
}

// CLI shells out to `gh api` (REST JSON directly — never fragile `--comments --json` combinations).
type CLI struct {
	Bin   string
	Owner string
	Repo  string

	mu         sync.Mutex
	cachedUser string
}

func (c *CLI) Enabled() bool { return c.Bin != "" && c.Owner != "" && c.Repo != "" }

func (c *CLI) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	procx.ConfigureContext(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = out
		}
		return out, fmt.Errorf("gh %s: %w (%s)", strings.Join(args, " "), err, detail)
	}
	return out, nil
}

func (c *CLI) repo() string { return c.Owner + "/" + c.Repo }

func (c *CLI) repoAPIPath(suffix string) string {
	return fmt.Sprintf("repos/%s/%s%s", url.PathEscape(c.Owner), url.PathEscape(c.Repo), suffix)
}

func (c *CLI) CurrentUser(ctx context.Context) (string, error) {
	if envUser := strings.TrimSpace(os.Getenv("GITHUB_USER")); envUser != "" {
		return envUser, nil
	}
	c.mu.Lock()
	if c.cachedUser != "" {
		u := c.cachedUser
		c.mu.Unlock()
		return u, nil
	}
	c.mu.Unlock()
	out, err := c.run(ctx, "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	login := strings.Trim(strings.TrimSpace(out), `"`)
	if login != "" {
		c.mu.Lock()
		c.cachedUser = login
		c.mu.Unlock()
	}
	return login, nil
}

func (c *CLI) CreatePullRequest(ctx context.Context, req CreatePR) (PullRequest, error) {
	out, err := c.run(ctx, "pr", "create", "--repo", c.repo(), "--base", req.Base, "--head", req.Head, "--title", req.Title, "--body", req.Body)
	if err != nil {
		return PullRequest{}, err
	}
	num := extractTrailingPRNumber(out)
	return PullRequest{Number: num, URL: out, Title: req.Title}, nil
}

func extractTrailingPRNumber(rawURL string) int {
	trimmed := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 || idx+1 >= len(trimmed) {
		return 0
	}
	n, _ := strconv.Atoi(trimmed[idx+1:])
	return n
}

func (c *CLI) FindOpenPullRequestByHead(ctx context.Context, head string) (*PullRequest, error) {
	cleanHead := strings.TrimSpace(head)
	if cleanHead == "" {
		return nil, nil
	}
	out, err := c.run(ctx, "pr", "list", "--repo", c.repo(), "--head", cleanHead, "--state", "open", "--limit", "1", "--json", "number,title,url")
	if err != nil {
		return nil, err
	}
	var list []PullRequest
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

func (c *CLI) GetPullRequest(ctx context.Context, number int) (PullRequestDetail, error) {
	if number <= 0 {
		return PullRequestDetail{}, fmt.Errorf("número de PR inválido: %d", number)
	}
	out, err := c.run(ctx, "api", c.repoAPIPath(fmt.Sprintf("/pulls/%d", number)))
	if err != nil {
		return PullRequestDetail{}, err
	}
	var raw rawRESTPull
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return PullRequestDetail{}, fmt.Errorf("decodificar PR #%d: %w", number, err)
	}
	detail := mapRawPull(raw)

	if revOut, err := c.run(ctx, "api", c.repoAPIPath(fmt.Sprintf("/pulls/%d/reviews?per_page=100", number))); err == nil {
		var rawReviews []rawRESTReview
		if jsonErr := json.Unmarshal([]byte(revOut), &rawReviews); jsonErr == nil {
			detail.Reviews = mapRawReviews(rawReviews)
		} else {
			detail.CommentFetchErrors = append(detail.CommentFetchErrors, fmt.Sprintf("revisões: %v", jsonErr))
		}
	} else {
		detail.CommentFetchErrors = append(detail.CommentFetchErrors, fmt.Sprintf("revisões: %v", err))
	}

	if revComments, err := c.ListPullRequestComments(ctx, number); err == nil {
		detail.ReviewComments = revComments
	} else {
		detail.CommentFetchErrors = append(detail.CommentFetchErrors, fmt.Sprintf("comentários inline: %v", err))
	}

	if issueComments, err := c.ListIssueComments(ctx, number); err == nil {
		detail.IssueComments = issueComments
	} else {
		detail.CommentFetchErrors = append(detail.CommentFetchErrors, fmt.Sprintf("comentários gerais: %v", err))
	}

	return detail, nil
}

func (c *CLI) ListPullRequests(ctx context.Context, state string, limit int) ([]PullRequestSummary, error) {
	st := strings.ToLower(strings.TrimSpace(state))
	if st == "" {
		st = "open"
	}
	switch st {
	case "open", "closed", "all":
	default:
		return nil, fmt.Errorf("estado de PR inválido %q (use open, closed ou all)", state)
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	out, err := c.run(ctx, "api", c.repoAPIPath(fmt.Sprintf("/pulls?state=%s&per_page=%d", st, limit)))
	if err != nil {
		return nil, err
	}
	var raw []rawRESTPull
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("decodificar lista de PRs: %w", err)
	}
	res := make([]PullRequestSummary, 0, len(raw))
	for _, item := range raw {
		var reqs []string
		for _, rr := range item.RequestedReviewers {
			if strings.TrimSpace(rr.Login) != "" {
				reqs = append(reqs, rr.Login)
			}
		}
		res = append(res, PullRequestSummary{
			Number:             item.Number,
			Title:              item.Title,
			URL:                item.HTMLURL,
			State:              strings.ToLower(item.State),
			Draft:              item.Draft,
			Author:             item.User.Login,
			HeadRef:            item.Head.Ref,
			BaseRef:            item.Base.Ref,
			RequestedReviewers: reqs,
			UpdatedAt:          item.UpdatedAt,
		})
	}
	return res, nil
}

func (c *CLI) ListPullRequestFiles(ctx context.Context, number int) ([]PRFile, error) {
	if number <= 0 {
		return nil, fmt.Errorf("número de PR inválido: %d", number)
	}
	out, err := c.run(ctx, "api", c.repoAPIPath(fmt.Sprintf("/pulls/%d/files?per_page=100", number)))
	if err != nil {
		return nil, err
	}
	var raw []rawRESTPRFile
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("decodificar arquivos do PR #%d: %w", number, err)
	}
	res := make([]PRFile, 0, len(raw))
	for _, f := range raw {
		res = append(res, PRFile{
			Filename:  f.Filename,
			Status:    f.Status,
			Additions: f.Additions,
			Deletions: f.Deletions,
			Patch:     f.Patch,
		})
	}
	return res, nil
}

func (c *CLI) ListPullRequestComments(ctx context.Context, number int) ([]Comment, error) {
	if number <= 0 {
		return nil, fmt.Errorf("número de PR inválido: %d", number)
	}
	out, err := c.run(ctx, "api", c.repoAPIPath(fmt.Sprintf("/pulls/%d/comments?per_page=100", number)))
	if err != nil {
		return nil, err
	}
	var raw []rawRESTReviewComment
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("decodificar comentários inline do PR #%d: %w", number, err)
	}
	return mapRawReviewComments(raw), nil
}

func (c *CLI) ReplyToReviewComment(ctx context.Context, number int, commentID int64, body string) error {
	if number <= 0 {
		return fmt.Errorf("número de PR inválido: %d", number)
	}
	if commentID <= 0 {
		return fmt.Errorf("comment_id inválido: %d", commentID)
	}
	_, err := c.run(ctx, "api", "-X", "POST", c.repoAPIPath(fmt.Sprintf("/pulls/%d/comments/%d/replies", number, commentID)), "-f", "body="+body)
	return err
}

func (c *CLI) RequestReviewers(ctx context.Context, number int, reviewers []string) error {
	if number <= 0 {
		return fmt.Errorf("número de PR inválido: %d", number)
	}
	clean := cleanLogins(reviewers)
	if len(clean) == 0 {
		return fmt.Errorf("informe ao menos um revisor")
	}
	_, err := c.run(ctx, "pr", "edit", fmt.Sprint(number), "--repo", c.repo(), "--add-reviewer", strings.Join(clean, ","))
	return err
}

func (c *CLI) SubmitReview(ctx context.Context, number int, event, body string) error {
	if number <= 0 {
		return fmt.Errorf("número de PR inválido: %d", number)
	}
	flag := "--comment"
	switch strings.ToUpper(strings.TrimSpace(event)) {
	case "APPROVE":
		flag = "--approve"
	case "REQUEST_CHANGES":
		flag = "--request-changes"
	case "COMMENT":
		flag = "--comment"
	default:
		return fmt.Errorf("evento de revisão inválido: %s", event)
	}
	_, err := c.run(ctx, "pr", "review", fmt.Sprint(number), "--repo", c.repo(), flag, "--body", body)
	return err
}

func (c *CLI) MergePullRequest(ctx context.Context, number int, method, commitTitle string) error {
	if number <= 0 {
		return fmt.Errorf("número de PR inválido: %d", number)
	}
	flag := "--merge"
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "", "merge":
		flag = "--merge"
	case "squash":
		flag = "--squash"
	case "rebase":
		flag = "--rebase"
	default:
		return fmt.Errorf("método de merge inválido: %s", method)
	}
	args := []string{"pr", "merge", fmt.Sprint(number), "--repo", c.repo(), flag}
	if strings.TrimSpace(commitTitle) != "" {
		args = append(args, "--subject", strings.TrimSpace(commitTitle))
	}
	_, err := c.run(ctx, args...)
	return err
}

func (c *CLI) ClosePullRequest(ctx context.Context, number int) error {
	if number <= 0 {
		return fmt.Errorf("número de PR inválido: %d", number)
	}
	_, err := c.run(ctx, "pr", "close", fmt.Sprint(number), "--repo", c.repo())
	return err
}

func (c *CLI) GetIssue(ctx context.Context, number int) (IssueDetail, error) {
	if number <= 0 {
		return IssueDetail{}, fmt.Errorf("número de issue inválido: %d", number)
	}
	out, err := c.run(ctx, "api", c.repoAPIPath(fmt.Sprintf("/issues/%d", number)))
	if err != nil {
		return IssueDetail{}, err
	}
	var raw rawRESTIssue
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return IssueDetail{}, fmt.Errorf("decodificar issue #%d: %w", number, err)
	}
	detail := IssueDetail{
		Number: raw.Number,
		Title:  raw.Title,
		Body:   raw.Body,
		URL:    raw.HTMLURL,
		State:  strings.ToLower(raw.State),
		Author: raw.User.Login,
	}
	for _, lb := range raw.Labels {
		if strings.TrimSpace(lb.Name) != "" {
			detail.Labels = append(detail.Labels, lb.Name)
		}
	}
	if comments, err := c.ListIssueComments(ctx, number); err == nil {
		detail.Comments = comments
	}
	return detail, nil
}

func (c *CLI) CloseIssue(ctx context.Context, number int) error {
	if number <= 0 {
		return fmt.Errorf("número de issue inválido: %d", number)
	}
	_, err := c.run(ctx, "issue", "close", fmt.Sprint(number), "--repo", c.repo())
	return err
}

func (c *CLI) CommentOnIssue(ctx context.Context, number int, body string) error {
	if number <= 0 {
		return fmt.Errorf("número de issue inválido: %d", number)
	}
	_, err := c.run(ctx, "issue", "comment", fmt.Sprint(number), "--repo", c.repo(), "--body", body)
	return err
}

// ListIssueComments uses `gh api` directly against the REST comments endpoint,
// avoiding the invalid `--comments --json` flag combination and GraphQL string IDs.
func (c *CLI) ListIssueComments(ctx context.Context, number int) ([]Comment, error) {
	if number <= 0 {
		return nil, fmt.Errorf("número de issue inválido: %d", number)
	}
	out, err := c.run(ctx, "api", c.repoAPIPath(fmt.Sprintf("/issues/%d/comments?per_page=100", number)))
	if err != nil {
		return nil, err
	}
	return decodeIssueCommentsPayload([]byte(out))
}

// decodeIssueCommentsPayload parses both standard REST JSON arrays (`[{"id":1,"body":"...","user":{"login":"..."}}]`)
// and legacy `gh --json comments` wrapper objects (`{"comments":[{"id":...,"body":"...","author":{"login":"..."}}]}`).
func decodeIssueCommentsPayload(data []byte) ([]Comment, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var raw []rawRESTIssueComment
		if err := json.Unmarshal(trimmed, &raw); err != nil {
			return nil, fmt.Errorf("ler comentários do GitHub: %w", err)
		}
		return mapRawIssueComments(raw), nil
	}
	var wrapped struct {
		Comments []struct {
			ID        json.RawMessage `json:"id"`
			Body      string          `json:"body"`
			CreatedAt string          `json:"createdAt"`
			Author    struct {
				Login string `json:"login"`
			} `json:"author"`
			User struct {
				Login string `json:"login"`
			} `json:"user"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(trimmed, &wrapped); err != nil {
		return nil, fmt.Errorf("ler comentários do gh: %w", err)
	}
	outc := make([]Comment, 0, len(wrapped.Comments))
	for _, cmt := range wrapped.Comments {
		var numID int64
		_ = json.Unmarshal(cmt.ID, &numID)
		author := strings.TrimSpace(cmt.Author.Login)
		if author == "" {
			author = strings.TrimSpace(cmt.User.Login)
		}
		outc = append(outc, Comment{
			ID:        numID,
			Author:    author,
			Body:      cmt.Body,
			CreatedAt: cmt.CreatedAt,
		})
	}
	return outc, nil
}

func (c *CLI) CIStatus(ctx context.Context, ref string) (string, error) {
	escapedRef, err := sanitizeGitRef(ref)
	if err != nil {
		return "", err
	}
	out, err := c.run(ctx, "api", fmt.Sprintf("repos/%s/%s/commits/%s/status", url.PathEscape(c.Owner), url.PathEscape(c.Repo), escapedRef), "--jq", ".state")
	if err != nil {
		return "", err
	}
	state := strings.Trim(strings.TrimSpace(out), `"`)
	if state == "" {
		return "pending", nil
	}
	return state, nil
}

// Detect returns the native GitHub REST API connector whenever a token is resolved
// (env var, saved IDE connector token, or `gh auth token`), falling back to `gh api` CLI.
func Detect(owner, repo string) Client {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(repo) == "" {
		return Disabled{Reason: "GitHub não está configurado. Confira o remote origin do repositório"}
	}
	token, baseURL, _ := LoadCredentials()
	if token != "" {
		return &REST{BaseURL: baseURL, Token: token, Owner: owner, Repo: repo}
	}
	if path, err := exec.LookPath("gh"); err == nil {
		return &CLI{Bin: path, Owner: owner, Repo: repo}
	}
	return Disabled{Reason: "GitHub não está configurado. Configure o Conector GitHub na IDE, defina GITHUB_TOKEN ou autentique o gh"}
}

// ParseRemote extracts owner and repo from a GitHub remote URL. The GHES-aware
// variant lives in remote.go as ParseRemoteForHost / ParseRemoteForConfiguredHosts.

// IsExplicitReviewObjective reports whether the user's prompt explicitly asks to review a PR.
func IsExplicitReviewObjective(objective string) bool {
	low := strings.ToLower(objective)
	for _, kw := range []string{
		"revisar", "revise", "revisão", "revisao",
		"code review", "fazer review", "faça review", "faca review",
		"avalie o pr", "analise o pr", "review pr", "review pull request",
	} {
		if strings.Contains(low, kw) {
			return true
		}
	}
	return false
}

// CheckPRCommentAllowed enforces the rule: commenting on a PR is allowed ONLY when
// replying on a PR authored by currentUser OR when there is a review request for it.
func CheckPRCommentAllowed(pr PullRequestDetail, currentUser string, explicitReviewRequest bool) error {
	me := strings.TrimSpace(currentUser)
	if me != "" && strings.EqualFold(strings.TrimSpace(pr.Author), me) {
		return nil
	}
	if explicitReviewRequest {
		return nil
	}
	if me != "" && isUserRequestedReviewer(pr, me) {
		return nil
	}
	return fmt.Errorf("governança PR: só é permitido comentar em PR de própria autoria (%s) ou quando há pedido de revisão (PR #%d é de %q)", me, pr.Number, pr.Author)
}

// CheckPRReviewAllowed enforces that submitting a PR review (APPROVE / REQUEST_CHANGES / COMMENT)
// is allowed ONLY on third-party PRs when a review was requested, and never on one's own PR.
func CheckPRReviewAllowed(pr PullRequestDetail, currentUser, event string, explicitReviewRequest bool) error {
	me := strings.TrimSpace(currentUser)
	if me != "" && strings.EqualFold(strings.TrimSpace(pr.Author), me) {
		return fmt.Errorf("governança PR: o PR #%d é de sua própria autoria (%s) — não emita revisão como revisor no próprio PR; use github_comment_pr ou github_reply_review_comment para responder ao revisor e github_request_review para pedir nova revisão", pr.Number, me)
	}
	if explicitReviewRequest || (me != "" && isUserRequestedReviewer(pr, me)) {
		return nil
	}
	return fmt.Errorf("governança PR: só é permitido aprovar/revisar o PR #%d quando houver pedido de revisão", pr.Number)
}

// HasReviewerApproval reports whether at least one external reviewer has an active APPROVED
// decision on pr, with zero unresolved CHANGES_REQUESTED and zero pending re-review requests.
func HasReviewerApproval(pr PullRequestDetail) (bool, string) {
	if pr.Merged {
		return false, fmt.Sprintf("o PR #%d já foi mesclado", pr.Number)
	}
	if pr.State != "" && !strings.EqualFold(pr.State, "open") {
		return false, fmt.Sprintf("o PR #%d não está aberto (estado=%s)", pr.Number, pr.State)
	}
	if len(pr.RequestedReviewers) > 0 {
		return false, fmt.Sprintf("aguardando nova revisão de: %s", strings.Join(pr.RequestedReviewers, ", "))
	}
	latestByReviewer := map[string]string{}
	var order []string
	for _, rv := range pr.Reviews {
		author := strings.ToLower(strings.TrimSpace(rv.Author))
		if author == "" || strings.EqualFold(author, strings.TrimSpace(pr.Author)) {
			continue
		}
		st := strings.ToUpper(strings.TrimSpace(rv.State))
		switch st {
		case "APPROVED", "CHANGES_REQUESTED":
			if _, seen := latestByReviewer[author]; !seen {
				order = append(order, author)
			}
			latestByReviewer[author] = st
		case "DISMISSED":
			delete(latestByReviewer, author)
		}
	}
	approvedCount := 0
	var changesBy []string
	for _, rev := range order {
		switch latestByReviewer[rev] {
		case "CHANGES_REQUESTED":
			changesBy = append(changesBy, rev)
		case "APPROVED":
			approvedCount++
		}
	}
	if len(changesBy) > 0 {
		return false, fmt.Sprintf("há alterações pendentes solicitadas por: %s", strings.Join(changesBy, ", "))
	}
	if approvedCount == 0 {
		return false, "nenhum revisor aprovou o PR ainda"
	}
	return true, ""
}

// CheckPRMergeAllowed verifies that pr is open and approved by a reviewer before merging.
func CheckPRMergeAllowed(pr PullRequestDetail) error {
	ok, why := HasReviewerApproval(pr)
	if !ok {
		return fmt.Errorf("governança PR: merge do PR #%d bloqueado (%s)", pr.Number, why)
	}
	return nil
}

func isUserRequestedReviewer(pr PullRequestDetail, login string) bool {
	for _, rr := range pr.RequestedReviewers {
		if strings.EqualFold(strings.TrimSpace(rr), login) {
			return true
		}
	}
	for _, rv := range pr.Reviews {
		if strings.EqualFold(strings.TrimSpace(rv.Author), login) {
			return true
		}
	}
	return false
}

func cleanLogins(raw []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range raw {
		u := strings.TrimPrefix(strings.TrimSpace(item), "@")
		if u == "" || seen[strings.ToLower(u)] {
			continue
		}
		seen[strings.ToLower(u)] = true
		out = append(out, u)
	}
	return out
}

func truncate(s string, n int) string {
	return strx.TruncateBytes(s, n, "")
}
