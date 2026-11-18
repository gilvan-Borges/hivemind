// Package githubx talks to GitHub through the REST API or the gh CLI.
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
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// PullRequest is a pull request the CLI created or found.
type PullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title"`
}

// Comment is an issue or pull request comment.
type Comment struct {
	ID     int    `json:"id"`
	Author string `json:"author"`
	Body   string `json:"body"`
	Path   string `json:"path,omitempty"`
}

// PRReview represents a code review submitted on a pull request.
type PRReview struct {
	ID          int    `json:"id"`
	Author      string `json:"author"`
	State       string `json:"state"` // "APPROVED", "CHANGES_REQUESTED", "COMMENTED", "DISMISSED"
	Body        string `json:"body"`
	SubmittedAt string `json:"submittedAt,omitempty"`
}

// PullRequestDetail contains full authorship, branch, and review state of a PR.
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
	RequestedReviewers []string   `json:"requestedReviewers,omitempty"`
	Reviews            []PRReview `json:"reviews,omitempty"`
	ReviewComments     []Comment  `json:"reviewComments,omitempty"`
}

// CreatePR is the input for a new pull request.
type CreatePR struct {
	Title string
	Head  string
	Base  string
	Body  string
}

// Client is the GitHub surface used by tools and the orchestrator.
type Client interface {
	Enabled() bool
	CurrentUser(ctx context.Context) (string, error)
	CreatePullRequest(ctx context.Context, req CreatePR) (PullRequest, error)
	FindOpenPullRequestByHead(ctx context.Context, head string) (*PullRequest, error)
	GetPullRequest(ctx context.Context, number int) (PullRequestDetail, error)
	RequestReviewers(ctx context.Context, number int, reviewers []string) error
	SubmitReview(ctx context.Context, number int, event, body string) error
	MergePullRequest(ctx context.Context, number int, method, commitTitle string) error
	CommentOnIssue(ctx context.Context, number int, body string) error
	ListIssueComments(ctx context.Context, number int) ([]Comment, error)
	CIStatus(ctx context.Context, ref string) (string, error)
}

// Disabled explains why GitHub calls are turned off.
type Disabled struct{ Reason string }

func (d Disabled) Enabled() bool                                  { return false }
func (d Disabled) CurrentUser(context.Context) (string, error)    { return "", d.err() }
func (d Disabled) CreatePullRequest(context.Context, CreatePR) (PullRequest, error) {
	return PullRequest{}, d.err()
}
func (d Disabled) FindOpenPullRequestByHead(context.Context, string) (*PullRequest, error) {
	return nil, d.err()
}
func (d Disabled) GetPullRequest(context.Context, int) (PullRequestDetail, error) {
	return PullRequestDetail{}, d.err()
}
func (d Disabled) RequestReviewers(context.Context, int, []string) error   { return d.err() }
func (d Disabled) SubmitReview(context.Context, int, string, string) error { return d.err() }
func (d Disabled) MergePullRequest(context.Context, int, string, string) error {
	return d.err()
}
func (d Disabled) CommentOnIssue(context.Context, int, string) error { return d.err() }
func (d Disabled) ListIssueComments(context.Context, int) ([]Comment, error) {
	return nil, d.err()
}
func (d Disabled) CIStatus(context.Context, string) (string, error) { return "", d.err() }
func (d Disabled) err() error {
	if d.Reason != "" {
		return fmt.Errorf("%s", d.Reason)
	}
	return fmt.Errorf("GitHub não está configurado. Defina GITHUB_TOKEN ou autentique o gh")
}

// REST is the GitHub REST API.
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
	return &http.Client{Timeout: 30 * time.Second}
}

func (r *REST) base() string {
	if r.BaseURL != "" {
		return strings.TrimRight(r.BaseURL, "/")
	}
	return "https://api.github.com"
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
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := r.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
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

func (r *REST) GetPullRequest(ctx context.Context, number int) (PullRequestDetail, error) {
	if number <= 0 {
		return PullRequestDetail{}, fmt.Errorf("número de PR inválido: %d", number)
	}
	ownerEsc := url.PathEscape(r.Owner)
	repoEsc := url.PathEscape(r.Repo)
	var raw struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
		Merged  bool   `json:"merged"`
		User    struct {
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
	if err := r.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls/%d", ownerEsc, repoEsc, number), nil, &raw); err != nil {
		return PullRequestDetail{}, err
	}
	detail := PullRequestDetail{
		Number:  raw.Number,
		Title:   raw.Title,
		Body:    raw.Body,
		URL:     raw.HTMLURL,
		State:   strings.ToLower(raw.State),
		Merged:  raw.Merged,
		Author:  raw.User.Login,
		HeadRef: raw.Head.Ref,
		BaseRef: raw.Base.Ref,
	}
	for _, rr := range raw.RequestedReviewers {
		if strings.TrimSpace(rr.Login) != "" {
			detail.RequestedReviewers = append(detail.RequestedReviewers, rr.Login)
		}
	}

	var rawReviews []struct {
		ID          int    `json:"id"`
		State       string `json:"state"`
		Body        string `json:"body"`
		SubmittedAt string `json:"submitted_at"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := r.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", ownerEsc, repoEsc, number), nil, &rawReviews); err == nil {
		for _, rv := range rawReviews {
			detail.Reviews = append(detail.Reviews, PRReview{
				ID:          rv.ID,
				Author:      rv.User.Login,
				State:       strings.ToUpper(strings.TrimSpace(rv.State)),
				Body:        rv.Body,
				SubmittedAt: rv.SubmittedAt,
			})
		}
	}

	var rawComments []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
		Path string `json:"path"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := r.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/pulls/%d/comments", ownerEsc, repoEsc, number), nil, &rawComments); err == nil {
		for _, c := range rawComments {
			detail.ReviewComments = append(detail.ReviewComments, Comment{
				ID:     c.ID,
				Author: c.User.Login,
				Body:   c.Body,
				Path:   c.Path,
			})
		}
	}
	return detail, nil
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
	var raw []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := r.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", url.PathEscape(r.Owner), url.PathEscape(r.Repo), number), nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Comment, 0, len(raw))
	for _, c := range raw {
		out = append(out, Comment{ID: c.ID, Author: c.User.Login, Body: c.Body})
	}
	return out, nil
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

// CLI shells out to gh.
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
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	if err != nil {
		return out, fmt.Errorf("gh %s: %w (%s)", strings.Join(args, " "), err, out)
	}
	return out, nil
}

func (c *CLI) repo() string { return c.Owner + "/" + c.Repo }

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
	return PullRequest{URL: out, Title: req.Title}, nil
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
	out, err := c.run(ctx, "pr", "view", fmt.Sprint(number), "--repo", c.repo(), "--json", "number,title,body,url,state,mergedAt,author,headRefName,baseRefName,reviewRequests,reviews")
	if err != nil {
		return PullRequestDetail{}, err
	}
	var raw struct {
		Number      int     `json:"number"`
		Title       string  `json:"title"`
		Body        string  `json:"body"`
		URL         string  `json:"url"`
		State       string  `json:"state"`
		MergedAt    *string `json:"mergedAt"`
		HeadRefName string  `json:"headRefName"`
		BaseRefName string  `json:"baseRefName"`
		Author      struct {
			Login string `json:"login"`
		} `json:"author"`
		ReviewRequests []struct {
			Login string `json:"login"`
		} `json:"reviewRequests"`
		Reviews []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			State       string `json:"state"`
			Body        string `json:"body"`
			SubmittedAt string `json:"submittedAt"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return PullRequestDetail{}, fmt.Errorf("decodificar PR #%d: %w", number, err)
	}
	st := strings.ToLower(raw.State)
	if st == "merged" {
		st = "closed"
	}
	detail := PullRequestDetail{
		Number:  raw.Number,
		Title:   raw.Title,
		Body:    raw.Body,
		URL:     raw.URL,
		State:   st,
		Merged:  raw.MergedAt != nil || strings.EqualFold(raw.State, "MERGED"),
		Author:  raw.Author.Login,
		HeadRef: raw.HeadRefName,
		BaseRef: raw.BaseRefName,
	}
	for _, rr := range raw.ReviewRequests {
		if strings.TrimSpace(rr.Login) != "" {
			detail.RequestedReviewers = append(detail.RequestedReviewers, rr.Login)
		}
	}
	for _, rv := range raw.Reviews {
		detail.Reviews = append(detail.Reviews, PRReview{
			Author:      rv.Author.Login,
			State:       strings.ToUpper(strings.TrimSpace(rv.State)),
			Body:        rv.Body,
			SubmittedAt: rv.SubmittedAt,
		})
	}
	return detail, nil
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

func (c *CLI) CommentOnIssue(ctx context.Context, number int, body string) error {
	_, err := c.run(ctx, "issue", "comment", fmt.Sprint(number), "--repo", c.repo(), "--body", body)
	return err
}

func (c *CLI) ListIssueComments(ctx context.Context, number int) ([]Comment, error) {
	out, err := c.run(ctx, "issue", "view", fmt.Sprint(number), "--repo", c.repo(), "--comments", "--json", "comments")
	if err != nil {
		return nil, err
	}
	var raw struct {
		Comments []struct {
			ID     int    `json:"id"`
			Body   string `json:"body"`
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"comments"`
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("ler comentários do gh: %w", err)
	}
	outc := make([]Comment, 0, len(raw.Comments))
	for _, cmt := range raw.Comments {
		outc = append(outc, Comment{ID: cmt.ID, Author: cmt.Author.Login, Body: cmt.Body})
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

// Detect picks REST when a token is present and gh otherwise.
func Detect(owner, repo string) Client {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GH_TOKEN"))
	}
	if token != "" && owner != "" && repo != "" {
		return &REST{Token: token, Owner: owner, Repo: repo}
	}
	if path, err := exec.LookPath("gh"); err == nil && owner != "" && repo != "" {
		return &CLI{Bin: path, Owner: owner, Repo: repo}
	}
	return Disabled{Reason: "GitHub não está configurado. Defina GITHUB_TOKEN ou autentique o gh, e confira o remote origin"}
}

// ParseRemote extracts owner and repo from a GitHub remote URL.
func ParseRemote(raw string) (owner, repo string, ok bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSuffix(raw, ".git")
	if strings.HasPrefix(raw, "git@github.com:") {
		rest := strings.TrimPrefix(raw, "git@github.com:")
		parts := strings.Split(rest, "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], true
		}
	}
	idx := strings.Index(raw, "github.com")
	if idx < 0 {
		return "", "", false
	}
	rest := raw[idx+len("github.com"):]
	rest = strings.TrimLeft(rest, "/:")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

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
// is allowed ONLY when a review was requested, and never allows self-approving one's own PR.
func CheckPRReviewAllowed(pr PullRequestDetail, currentUser, event string, explicitReviewRequest bool) error {
	me := strings.TrimSpace(currentUser)
	ev := strings.ToUpper(strings.TrimSpace(event))
	if me != "" && strings.EqualFold(strings.TrimSpace(pr.Author), me) && (ev == "APPROVE" || ev == "REQUEST_CHANGES") {
		return fmt.Errorf("governança PR: não é permitido auto-aprovar ou pedir mudanças no próprio PR #%d; use github_comment_pr e github_request_review", pr.Number)
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
