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
	"net/url"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/names"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// SubtaskItem is a concise summary of a Jira subtask or child issue.
type SubtaskItem struct {
	Key            string `json:"key"`
	Summary        string `json:"summary"`
	Status         string `json:"status"`
	StatusCategory string `json:"statusCategory,omitempty"`
	Done           bool   `json:"done,omitempty"`
}

// Issue is the part of a Jira issue the CLI and IDE use.
type Issue struct {
	Key           string        `json:"key"`
	Summary       string        `json:"summary"`
	Description   string        `json:"description"`
	Status        string        `json:"status"`
	IssueType     string        `json:"issueType,omitempty"`
	IsSubtask     bool          `json:"isSubtask,omitempty"`
	ParentKey     string        `json:"parentKey,omitempty"`
	ParentSummary string        `json:"parentSummary,omitempty"`
	Subtasks      []SubtaskItem `json:"subtasks,omitempty"`
}

// IsDoneStatus reports whether a Jira status name represents a completed issue.
func IsDoneStatus(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	switch s {
	case "done", "concluído", "concluido", "fechado", "closed", "resolved", "resolvido", "finalizado", "entregue":
		return true
	}
	return strings.Contains(s, "conclu") || strings.Contains(s, "done") || strings.Contains(s, "fechad")
}

// Client is the Jira surface used by tools and `hivemind run --jira`.
type Client interface {
	Enabled() bool
	GetIssue(ctx context.Context, key string) (Issue, error)
	Search(ctx context.Context, jql string, limit int) ([]Issue, error)
	CreateIssue(ctx context.Context, projectKey, issueType, summary, description, parentKey string) (Issue, error)
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
func (d Disabled) CreateIssue(context.Context, string, string, string, string, string) (Issue, error) {
	return Issue{}, d.err()
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

func cleanIssueKey(raw string) (string, error) {
	key := names.IssueKey(raw)
	if key == "" {
		return "", fmt.Errorf("chave Jira inválida: %q", raw)
	}
	return url.PathEscape(key), nil
}

func (h *HTTP) GetIssue(ctx context.Context, key string) (Issue, error) {
	safeKey, err := cleanIssueKey(key)
	if err != nil {
		return Issue{}, err
	}
	var raw issuePayload
	if err := h.do(ctx, http.MethodGet, "/rest/api/3/issue/"+safeKey+"?fields=summary,description,status,issuetype,parent,subtasks", nil, &raw); err != nil {
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
		"fields":     []string{"summary", "description", "status", "issuetype", "parent", "subtasks"},
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

func buildADFDoc(text string) map[string]any {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	blocks := make([]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		blocks = append(blocks, map[string]any{
			"type": "paragraph",
			"content": []any{
				map[string]any{"type": "text", "text": line},
			},
		})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{
			"type": "paragraph",
			"content": []any{
				map[string]any{"type": "text", "text": " "},
			},
		})
	}
	return map[string]any{
		"type":    "doc",
		"version": 1,
		"content": blocks,
	}
}

func (h *HTTP) CreateIssue(ctx context.Context, projectKey, issueType, summary, description, parentKey string) (Issue, error) {
	proj := strings.ToUpper(strings.TrimSpace(projectKey))
	if proj == "" {
		return Issue{}, fmt.Errorf("informe a chave do projeto Jira (ex: PROJ)")
	}
	for _, r := range proj {
		if !((r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
			return Issue{}, fmt.Errorf("chave de projeto Jira inválida: %q", projectKey)
		}
	}
	it := strings.TrimSpace(issueType)
	if it == "" {
		it = "Task"
	}
	sum := strings.TrimSpace(summary)
	if sum == "" {
		return Issue{}, fmt.Errorf("o título (summary) da issue Jira não pode ficar vazio")
	}
	fields := map[string]any{
		"project":     map[string]string{"key": proj},
		"summary":     sum,
		"issuetype":   map[string]string{"name": it},
		"description": buildADFDoc(description),
	}
	if strings.TrimSpace(parentKey) != "" {
		cleanParent := names.IssueKey(parentKey)
		if cleanParent == "" {
			return Issue{}, fmt.Errorf("chave Jira pai inválida: %q", parentKey)
		}
		fields["parent"] = map[string]string{"key": cleanParent}
	}
	var raw struct {
		Key string `json:"key"`
	}
	if err := h.do(ctx, http.MethodPost, "/rest/api/3/issue", map[string]any{"fields": fields}, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{Key: raw.Key, Summary: sum, Description: description}, nil
}

func (h *HTTP) CreateSubtask(ctx context.Context, parent, summary, description string) (Issue, error) {
	cleanParent := names.IssueKey(parent)
	if cleanParent == "" {
		return Issue{}, fmt.Errorf("chave Jira pai inválida: %q", parent)
	}
	project := cleanParent
	if i := strings.IndexByte(cleanParent, '-'); i > 0 {
		project = cleanParent[:i]
	}
	return h.CreateIssue(ctx, project, "Sub-task", summary, description, cleanParent)
}

func (h *HTTP) Comment(ctx context.Context, key, body string) error {
	safeKey, err := cleanIssueKey(key)
	if err != nil {
		return err
	}
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
	return h.do(ctx, http.MethodPost, "/rest/api/3/issue/"+safeKey+"/comment", payload, nil)
}

type jiraTransitionItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	To   struct {
		Name           string `json:"name"`
		StatusCategory struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"statusCategory"`
	} `json:"to"`
}

func normJiraTransitionToken(s string) string {
	replacer := strings.NewReplacer(
		"á", "a", "à", "a", "ã", "a", "â", "a",
		"é", "e", "ê", "e",
		"í", "i",
		"ó", "o", "ô", "o", "õ", "o",
		"ú", "u", "ü", "u",
		"ç", "c",
	)
	clean := replacer.Replace(strings.ToLower(strings.TrimSpace(s)))
	var b strings.Builder
	for _, r := range clean {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func classifyTransitionSemanticIntent(norm string) string {
	switch {
	case norm == "done" || strings.HasPrefix(norm, "conclu") || strings.HasPrefix(norm, "fechad") ||
		strings.HasPrefix(norm, "finaliz") || strings.HasPrefix(norm, "resolvid") || norm == "closed" || norm == "resolved":
		return "done"
	case strings.Contains(norm, "revis") || strings.Contains(norm, "review") || strings.Contains(norm, "homolog") || strings.Contains(norm, "validac"):
		return "review"
	case strings.Contains(norm, "andamento") || strings.Contains(norm, "inprogress") || strings.Contains(norm, "progress") ||
		norm == "fazendo" || norm == "doing" || strings.Contains(norm, "execuc") || strings.Contains(norm, "desenvolv"):
		return "in_progress"
	case norm == "todo" || norm == "afazer" || strings.Contains(norm, "pendente") || norm == "backlog" || norm == "open" || norm == "aberto" || strings.Contains(norm, "planejamento"):
		return "todo"
	default:
		return ""
	}
}

func matchJiraTransitionID(transitions []jiraTransitionItem, nameOrID string) string {
	target := strings.TrimSpace(nameOrID)
	if target == "" || len(transitions) == 0 {
		return ""
	}
	// 1. Exact ID, transition Name, or destination status Name.
	for _, tr := range transitions {
		if tr.ID == target || strings.EqualFold(strings.TrimSpace(tr.Name), target) || strings.EqualFold(strings.TrimSpace(tr.To.Name), target) {
			return tr.ID
		}
	}
	normTarget := normJiraTransitionToken(target)
	if normTarget == "" {
		return ""
	}
	// 2. Normalized accent/plural-insensitive equality or prefix match on tr.Name or tr.To.Name
	// (e.g. "Concluído" <-> "CONCLUÍDOS" / "Concluir").
	for _, tr := range transitions {
		nName := normJiraTransitionToken(tr.Name)
		nTo := normJiraTransitionToken(tr.To.Name)
		if (nName != "" && (nName == normTarget || strings.TrimSuffix(nName, "s") == strings.TrimSuffix(normTarget, "s"))) ||
			(nTo != "" && (nTo == normTarget || strings.TrimSuffix(nTo, "s") == strings.TrimSuffix(normTarget, "s"))) {
			return tr.ID
		}
	}
	for _, tr := range transitions {
		nName := normJiraTransitionToken(tr.Name)
		nTo := normJiraTransitionToken(tr.To.Name)
		if len(normTarget) >= 4 && ((nName != "" && (strings.Contains(nName, normTarget) || strings.Contains(normTarget, nName))) ||
			(nTo != "" && (strings.Contains(nTo, normTarget) || strings.Contains(normTarget, nTo)))) {
			return tr.ID
		}
	}
	// 3. Semantic intent match (PT-BR <-> EN synonyms: "Concluído" <-> "Done", "Em andamento" <-> "In Progress", "Revisão" <-> "In Review", "A fazer" <-> "To Do").
	intent := classifyTransitionSemanticIntent(normTarget)
	if intent != "" {
		for _, tr := range transitions {
			trIntent := classifyTransitionSemanticIntent(normJiraTransitionToken(tr.Name))
			toIntent := classifyTransitionSemanticIntent(normJiraTransitionToken(tr.To.Name))
			if trIntent == intent || toIntent == intent {
				return tr.ID
			}
		}
		// Fallback to Jira statusCategory.key ("done" / "new") when unambiguous.
		for _, tr := range transitions {
			catKey := strings.ToLower(strings.TrimSpace(tr.To.StatusCategory.Key))
			if intent == "done" && catKey == "done" {
				return tr.ID
			}
			if intent == "todo" && catKey == "new" {
				return tr.ID
			}
		}
	}
	return ""
}

func (h *HTTP) Transition(ctx context.Context, key, nameOrID string) error {
	safeKey, err := cleanIssueKey(key)
	if err != nil {
		return err
	}
	var raw struct {
		Transitions []jiraTransitionItem `json:"transitions"`
	}
	if err := h.do(ctx, http.MethodGet, "/rest/api/3/issue/"+safeKey+"/transitions", nil, &raw); err != nil {
		return err
	}
	id := matchJiraTransitionID(raw.Transitions, nameOrID)
	if id == "" {
		var avail []string
		for _, tr := range raw.Transitions {
			if strings.TrimSpace(tr.To.Name) != "" && !strings.EqualFold(tr.Name, tr.To.Name) {
				avail = append(avail, fmt.Sprintf("%q (id=%s -> %q)", tr.Name, tr.ID, tr.To.Name))
			} else {
				avail = append(avail, fmt.Sprintf("%q (id=%s)", tr.Name, tr.ID))
			}
		}
		return fmt.Errorf("transição %q não encontrada em %s (transições disponíveis: %s)", nameOrID, safeKey, strings.Join(avail, ", "))
	}
	return h.do(ctx, http.MethodPost, "/rest/api/3/issue/"+safeKey+"/transitions", map[string]any{
		"transition": map[string]string{"id": id},
	}, nil)
}

// FromEnv builds a client from environment variables or the native HiveMind Jira connector config.
func FromEnv() Client {
	base, email, token, _, _ := LoadCredentials()
	if base == "" || email == "" || token == "" {
		return Disabled{Reason: "configure o Conector Jira Nativo na IDE ou defina JIRA_BASE_URL, JIRA_EMAIL e JIRA_API_TOKEN"}
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
			Name           string `json:"name"`
			StatusCategory struct {
				Key string `json:"key"`
			} `json:"statusCategory"`
		} `json:"status"`
		IssueType struct {
			Name    string `json:"name"`
			Subtask bool   `json:"subtask"`
		} `json:"issuetype"`
		Parent struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
			} `json:"fields"`
		} `json:"parent"`
		Subtasks []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
				Status  struct {
					Name           string `json:"name"`
					StatusCategory struct {
						Key string `json:"key"`
					} `json:"statusCategory"`
				} `json:"status"`
			} `json:"fields"`
		} `json:"subtasks"`
	} `json:"fields"`
}

func (p issuePayload) issue() Issue {
	typeName := strings.TrimSpace(p.Fields.IssueType.Name)
	parentKey := strings.TrimSpace(p.Fields.Parent.Key)
	isSub := p.Fields.IssueType.Subtask ||
		strings.Contains(strings.ToLower(typeName), "sub") ||
		(parentKey != "" && len(p.Fields.Subtasks) == 0 && !strings.EqualFold(typeName, "Epic"))
	out := Issue{
		Key:           p.Key,
		Summary:       p.Fields.Summary,
		Description:   strings.TrimSpace(flatten(p.Fields.Description)),
		Status:        p.Fields.Status.Name,
		IssueType:     typeName,
		IsSubtask:     isSub,
		ParentKey:     parentKey,
		ParentSummary: strings.TrimSpace(p.Fields.Parent.Fields.Summary),
	}
	for _, st := range p.Fields.Subtasks {
		if strings.TrimSpace(st.Key) == "" {
			continue
		}
		catKey := strings.TrimSpace(st.Fields.Status.StatusCategory.Key)
		done := strings.EqualFold(catKey, "done") || IsDoneStatus(st.Fields.Status.Name)
		out.Subtasks = append(out.Subtasks, SubtaskItem{
			Key:            st.Key,
			Summary:        st.Fields.Summary,
			Status:         st.Fields.Status.Name,
			StatusCategory: catKey,
			Done:           done,
		})
	}
	return out
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
	return strx.TruncateBytes(s, n, "")
}
