package idecore

import (
	"context"
	"strconv"
	"strings"
	"time"

	"google.golang.org/adk/v2/session"

	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// TokenStat holds accumulated input, output, and total tokens plus LLM call count.
type TokenStat struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	TotalTokens  int64 `json:"totalTokens"`
	Calls        int   `json:"calls"`
}

// SessionSummary represents one persisted session with its accumulated token usage.
type SessionSummary struct {
	ID           string `json:"id"`
	UpdatedAt    string `json:"updatedAt"`
	Title        string `json:"title"`
	JiraKey      string `json:"jiraKey,omitempty"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	TotalTokens  int64  `json:"totalTokens"`
}

// WorkspaceSessionsSummary aggregates all saved sessions and total project token consumption.
type WorkspaceSessionsSummary struct {
	Sessions            []SessionSummary `json:"sessions"`
	ProjectInputTokens  int64            `json:"projectInputTokens"`
	ProjectOutputTokens int64            `json:"projectOutputTokens"`
	ProjectTotalTokens  int64            `json:"projectTotalTokens"`
}

// RestoredMessage is one chat message reconstructed from a persisted session.
type RestoredMessage struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // "user", "chief", "tool"
	Label  string `json:"label"`
	Text   string `json:"text"`
	Tokens int64  `json:"tokens,omitempty"`
}

// SessionDetail holds the full transcript and per-agent token breakdown of one session.
type SessionDetail struct {
	ID           string               `json:"id"`
	Title        string               `json:"title"`
	JiraKey      string               `json:"jiraKey,omitempty"`
	InputTokens  int64                `json:"inputTokens"`
	OutputTokens int64                `json:"outputTokens"`
	TotalTokens  int64                `json:"totalTokens"`
	AgentUsage   map[string]TokenStat `json:"agentUsage"`
	Messages     []RestoredMessage    `json:"messages"`
}

func cleanObjectiveTitle(raw string) string {
	s := strings.TrimSpace(raw)
	if idx := strings.Index(s, "[Objetivo do Usuário]"); idx >= 0 {
		s = strings.TrimSpace(s[idx+len("[Objetivo do Usuário]"):])
	}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return strx.TruncateRunes(line, 100, "…")
		}
	}
	if s == "" {
		return "(sem título)"
	}
	return s
}

// ListWorkspaceSessions reads all persisted sessions (newest first) and computes token totals.
func ListWorkspaceSessions(ctx context.Context, workspace string) (WorkspaceSessionsSummary, error) {
	store, err := sessionx.Open(workspace)
	if err != nil {
		return WorkspaceSessionsSummary{}, err
	}
	metas, err := store.ListMeta()
	if err != nil {
		return WorkspaceSessionsSummary{}, err
	}

	var out WorkspaceSessionsSummary
	out.Sessions = make([]SessionSummary, 0, len(metas))

	// Reverse order so newest session comes first.
	for i := len(metas) - 1; i >= 0; i-- {
		m := metas[i]
		inTok, outTok, totTok := m.InputTokens, m.OutputTokens, m.TotalTokens
		// Backfill older sessions whose index.json predates token fields.
		if totTok == 0 {
			if got, err := store.Get(ctx, &session.GetRequest{
				AppName:   sessionx.AppName,
				UserID:    sessionx.UserID,
				SessionID: m.ID,
			}); err == nil && got.Session != nil {
				evs := got.Session.Events()
				for k := 0; k < evs.Len(); k++ {
					ev := evs.At(k)
					if ev == nil || ev.Partial || ev.LLMResponse.UsageMetadata == nil {
						continue
					}
					u := ev.LLMResponse.UsageMetadata
					inTok += int64(u.PromptTokenCount)
					outTok += int64(u.CandidatesTokenCount)
					t := int64(u.TotalTokenCount)
					if t == 0 && (u.PromptTokenCount > 0 || u.CandidatesTokenCount > 0) {
						t = int64(u.PromptTokenCount) + int64(u.CandidatesTokenCount)
					}
					totTok += t
				}
			}
		}

		out.ProjectInputTokens += inTok
		out.ProjectOutputTokens += outTok
		out.ProjectTotalTokens += totTok

		ts := ""
		if !m.UpdatedAt.IsZero() {
			ts = m.UpdatedAt.Local().Format("02/01 15:04")
		} else {
			ts = time.Now().Format("02/01 15:04")
		}
		out.Sessions = append(out.Sessions, SessionSummary{
			ID:           m.ID,
			UpdatedAt:    ts,
			Title:        cleanObjectiveTitle(m.Objective),
			JiraKey:      m.JiraKey,
			InputTokens:  inTok,
			OutputTokens: outTok,
			TotalTokens:  totTok,
		})
	}
	return out, nil
}

// LoadSessionDetail reconstructs the chat transcript and per-agent token stats for sessionID.
func LoadSessionDetail(ctx context.Context, workspace, sessionID string) (SessionDetail, error) {
	store, err := sessionx.Open(workspace)
	if err != nil {
		return SessionDetail{}, err
	}
	got, err := store.Get(ctx, &session.GetRequest{
		AppName:   sessionx.AppName,
		UserID:    sessionx.UserID,
		SessionID: sessionID,
	})
	if err != nil {
		return SessionDetail{}, err
	}
	sess := got.Session
	objRaw, _ := sess.State().Get("objective")
	jiraRaw, _ := sess.State().Get("jira_key")

	detail := SessionDetail{
		ID:         sess.ID(),
		Title:      cleanObjectiveTitle(strings.TrimSpace(toString(objRaw))),
		JiraKey:    strings.TrimSpace(toString(jiraRaw)),
		AgentUsage: make(map[string]TokenStat),
	}

	evs := sess.Events()
	for i := 0; i < evs.Len(); i++ {
		ev := evs.At(i)
		if ev == nil || ev.Partial {
			continue
		}
		var evTot int64
		if ev.LLMResponse.UsageMetadata != nil {
			u := ev.LLMResponse.UsageMetadata
			inT := int64(u.PromptTokenCount)
			outT := int64(u.CandidatesTokenCount)
			totT := int64(u.TotalTokenCount)
			if totT == 0 && (inT > 0 || outT > 0) {
				totT = inT + outT
			}
			evTot = totT
			detail.InputTokens += inT
			detail.OutputTokens += outT
			detail.TotalTokens += totT

			author := ev.Author
			if author == "" {
				author = "model"
			}
			st := detail.AgentUsage[author]
			st.InputTokens += inT
			st.OutputTokens += outT
			st.TotalTokens += totT
			st.Calls++
			detail.AgentUsage[author] = st
		}

		if ev.LLMResponse.Content == nil {
			continue
		}
		role := strings.ToLower(ev.LLMResponse.Content.Role)
		for pIdx, part := range ev.LLMResponse.Content.Parts {
			if part == nil || part.Thought {
				continue
			}
			if part.FunctionCall != nil {
				detail.Messages = append(detail.Messages, RestoredMessage{
					ID:    ev.ID + "-fc-" + strconv.Itoa(pIdx),
					Kind:  "tool",
					Label: ev.Author,
					Text:  part.FunctionCall.Name,
				})
				continue
			}
			if part.Text == "" {
				continue
			}
			txt := strings.TrimSpace(part.Text)
			if txt == "" || strings.HasPrefix(txt, "Prossigam com a implementação e validação conforme o plano") {
				continue
			}
			if role == "user" || ev.Author == "user" {
				txt = cleanUserDisplay(txt)
				detail.Messages = append(detail.Messages, RestoredMessage{
					ID:    ev.ID + "-u",
					Kind:  "user",
					Label: "Usuário",
					Text:  txt,
				})
			} else {
				lbl := ev.Author
				if lbl == "" {
					lbl = "Chefe"
				}
				detail.Messages = append(detail.Messages, RestoredMessage{
					ID:     ev.ID + "-m",
					Kind:   "chief",
					Label:  lbl,
					Text:   txt,
					Tokens: evTot,
				})
			}
		}
	}
	return detail, nil
}

// DeleteWorkspaceSession removes a saved session from .hivemind/sessions.
func DeleteWorkspaceSession(ctx context.Context, workspace, sessionID string) error {
	store, err := sessionx.Open(workspace)
	if err != nil {
		return err
	}
	return store.Delete(ctx, &session.DeleteRequest{
		AppName:   sessionx.AppName,
		UserID:    sessionx.UserID,
		SessionID: sessionID,
	})
}

func cleanUserDisplay(raw string) string {
	if idx := strings.Index(raw, "[Objetivo do Usuário]"); idx >= 0 {
		return strings.TrimSpace(raw[idx+len("[Objetivo do Usuário]"):])
	}
	return raw
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
