// Package jirax is a small Jira Cloud REST client.
// It uses basic auth with JIRA_EMAIL and JIRA_API_TOKEN.
package jirax

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Issue is the part of a Jira issue the CLI uses.
type Issue struct {
	Key         string `json:"key"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

// Client is the Jira surface used by tools and `hivemind run --jira`.
type Client interface {
	Enabled() bool
	GetIssue(ctx context.Context, key string) (Issue, error)
	Search(ctx context.Context, jql string, limit int) ([]Issue, error)
	CreateSubtask(ctx context.Context, parent, summary, description string) (Issue, error)
	Comment(ctx context.Context, key, body string) error
	Transition(ctx context.Context, key, nameOrID string) error
}

// Disabled refuses every call with a Portuguese reason.
type Disabled struct{ Reason string }

func (d Disabled) Enabled() bool { return false }
func (d Disabled) GetIssue(context.Context, string) (Issue, error) {
	return Issue{}, d.err()
}
func (d Disabled) Search(context.Context, string, int) ([]Issue, error) {
	return nil, d.err()
}
func (d Disabled) CreateSubtask(context.Context, string, string, string) (Issue, error) {
	return Issue{}, d.err()
}
func (d Disabled) Comment(context.Context, string, string) error    { return d.err() }
func (d Disabled) Transition(context.Context, string, string) error { return d.err() }
func (d Disabled) err() error {
	if d.Reason != "" {
		return fmt.Errorf("%s", d.Reason)
	}
	return fmt.Errorf("Jira está desativado")
}

// HTTP talks to Jira Cloud REST API v3.
type HTTP struct {
	BaseURL string
	Email   string
	Token   string
	HTTP    *http.Client
}

func (h *HTTP) Enabled() bool {
	return h.BaseURL != "" && h.Email != "" && h.Token != ""
}

func (h *HTTP) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (h *HTTP) base() string { return strings.TrimRight(h.BaseURL, "/") }

func (h *HTTP) do(ctx context.Context, method, path string, body any, dest any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.base()+path, reader)
	if err != nil {
		return err
	}
	req.SetBasicAuth(h.Email, h.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := h.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("Jira %s %s: %s: %s", method, path, res.Status, truncate(string(payload), 500))
	}
	if dest == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	return json.Unmarshal(payload, dest)
}

func (h *HTTP) GetIssue(ctx context.Context, key string) (Issue, error) {
	var raw issuePayload
	if err := h.do(ctx, http.MethodGet, "/rest/api/3/issue/"+key+"?fields=summary,description,status", nil, &raw); err != nil {
		return Issue{}, err
	}
	return raw.issue(), nil
}

func (h *HTTP) Search(ctx context.Context, jql string, limit int) ([]Issue, error) {
	if limit <= 0 {
		limit = 20
	}
	body := map[string]any{
		"jql":        jql,
		"maxResults": limit,
		"fields":     []string{"summary", "description", "status"},
	}
	var raw searchPayload
	err := h.do(ctx, http.MethodPost, "/rest/api/3/search/jql", body, &raw)
	if err != nil && strings.Contains(err.Error(), "404") {
		err = h.do(ctx, http.MethodPost, "/rest/api/3/search", body, &raw)
	}
	if err != nil {
		return nil, err
	}
	out := make([]Issue, 0, len(raw.Issues))
	for _, item := range raw.Issues {
		out = append(out, item.issue())
	}
	return out, nil
}

func (h *HTTP) CreateSubtask(ctx context.Context, parent, summary, description string) (Issue, error) {
	project := parent
	if i := strings.IndexByte(parent, '-'); i > 0 {
		project = parent[:i]
	}
	body := map[string]any{
		"fields": map[string]any{
			"project":   map[string]string{"key": project},
			"parent":    map[string]string{"key": parent},
			"summary":   summary,
			"issuetype": map[string]string{"name": "Sub-task"},
			"description": map[string]any{
				"type":    "doc",
				"version": 1,
				"content": []any{
					map[string]any{
						"type": "paragraph",
						"content": []any{
							map[string]any{"type": "text", "text": description},
						},
					},
				},
			},
		},
	}
	var raw struct {
		Key string `json:"key"`
	}
	if err := h.do(ctx, http.MethodPost, "/rest/api/3/issue", body, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{Key: raw.Key, Summary: summary, Description: description}, nil
}

func (h *HTTP) Comment(ctx context.Context, key, body string) error {
	payload := map[string]any{
		"body": map[string]any{
			"type":    "doc",
			"version": 1,
			"content": []any{
				map[string]any{
					"type": "paragraph",
					"content": []any{
						map[string]any{"type": "text", "text": body},
					},
				},
			},
		},
	}
	return h.do(ctx, http.MethodPost, "/rest/api/3/issue/"+key+"/comment", payload, nil)
}

func (h *HTTP) Transition(ctx context.Context, key, nameOrID string) error {
	var raw struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"transitions"`
	}
	if err := h.do(ctx, http.MethodGet, "/rest/api/3/issue/"+key+"/transitions", nil, &raw); err != nil {
		return err
	}
	id := ""
	for _, tr := range raw.Transitions {
		if tr.ID == nameOrID || strings.EqualFold(tr.Name, nameOrID) {
			id = tr.ID
			break
		}
	}
	if id == "" {
		return fmt.Errorf("transição %q não encontrada em %s", nameOrID, key)
	}
	return h.do(ctx, http.MethodPost, "/rest/api/3/issue/"+key+"/transitions", map[string]any{
		"transition": map[string]string{"id": id},
	}, nil)
}

// FromEnv builds a client from JIRA_BASE_URL, JIRA_EMAIL and JIRA_API_TOKEN.
func FromEnv() Client {
	base := strings.TrimSpace(os.Getenv("JIRA_BASE_URL"))
	email := strings.TrimSpace(os.Getenv("JIRA_EMAIL"))
	token := strings.TrimSpace(os.Getenv("JIRA_API_TOKEN"))
	if base == "" || email == "" || token == "" {
		return Disabled{Reason: "defina JIRA_BASE_URL, JIRA_EMAIL e JIRA_API_TOKEN"}
	}
	return &HTTP{BaseURL: base, Email: email, Token: token}
}

type searchPayload struct {
	Issues []issuePayload `json:"issues"`
}

type issuePayload struct {
	Key    string `json:"key"`
	Fields struct {
		Summary     string `json:"summary"`
		Description any    `json:"description"`
		Status      struct {
			Name string `json:"name"`
		} `json:"status"`
	} `json:"fields"`
}

func (p issuePayload) issue() Issue {
	return Issue{
		Key:         p.Key,
		Summary:     p.Fields.Summary,
		Description: strings.TrimSpace(flatten(p.Fields.Description)),
		Status:      p.Fields.Status.Name,
	}
}

func flatten(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case map[string]any:
		var b strings.Builder
		if text, ok := t["text"].(string); ok {
			b.WriteString(text)
		}
		if content, ok := t["content"].([]any); ok {
			for _, c := range content {
				part := flatten(c)
				if part == "" {
					continue
				}
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString(part)
			}
		}
		return b.String()
	case []any:
		var b strings.Builder
		for _, c := range t {
			part := flatten(c)
			if part == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(part)
		}
		return b.String()
	default:
		return ""
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
