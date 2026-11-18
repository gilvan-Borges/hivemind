// Package githubx talks to GitHub through the REST API or the gh CLI.
package githubx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
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
	CreatePullRequest(ctx context.Context, req CreatePR) (PullRequest, error)
	CommentOnIssue(ctx context.Context, number int, body string) error
	ListIssueComments(ctx context.Context, number int) ([]Comment, error)
	CIStatus(ctx context.Context, ref string) (string, error)
}

// Disabled explains why GitHub calls are turned off.
type Disabled struct{ Reason string }

func (d Disabled) Enabled() bool { return false }
func (d Disabled) CreatePullRequest(context.Context, CreatePR) (PullRequest, error) {
	return PullRequest{}, d.err()
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

func (r *REST) CreatePullRequest(ctx context.Context, req CreatePR) (PullRequest, error) {
	var raw struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
	}
	err := r.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/pulls", r.Owner, r.Repo), map[string]string{
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

func (r *REST) CommentOnIssue(ctx context.Context, number int, body string) error {
	return r.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", r.Owner, r.Repo, number), map[string]string{
		"body": body,
	}, nil)
}

func (r *REST) ListIssueComments(ctx context.Context, number int) ([]Comment, error) {
	var raw []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := r.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/issues/%d/comments", r.Owner, r.Repo, number), nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Comment, 0, len(raw))
	for _, c := range raw {
		out = append(out, Comment{ID: c.ID, Author: c.User.Login, Body: c.Body})
	}
	return out, nil
}

func (r *REST) CIStatus(ctx context.Context, ref string) (string, error) {
	var raw struct {
		State string `json:"state"`
	}
	if err := r.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/commits/%s/status", r.Owner, r.Repo, ref), nil, &raw); err != nil {
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
}

func (c *CLI) Enabled() bool { return c.Bin != "" && c.Owner != "" && c.Repo != "" }

func (c *CLI) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	hideWindow(cmd)
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

func (c *CLI) CreatePullRequest(ctx context.Context, req CreatePR) (PullRequest, error) {
	out, err := c.run(ctx, "pr", "create", "--repo", c.repo(), "--base", req.Base, "--head", req.Head, "--title", req.Title, "--body", req.Body)
	if err != nil {
		return PullRequest{}, err
	}
	return PullRequest{URL: out, Title: req.Title}, nil
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
	out, err := c.run(ctx, "api", fmt.Sprintf("repos/%s/commits/%s/status", c.repo(), ref), "--jq", ".state")
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
