package sessionx

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// EnsureChatSession opens the IDE session the Agent Hive shares with the
// timeline. An empty sessionID creates one. kind "hive" is stored so the
// session list can tell a Hive thread from a team run.
func EnsureChatSession(workspace, sessionID, title, kind string) (string, error) {
	store, err := Open(workspace)
	if err != nil {
		return "", err
	}
	ctx := context.Background()
	sessionID = strings.TrimSpace(sessionID)
	title = strings.TrimSpace(title)
	if sessionID != "" {
		got, err := store.Get(ctx, &session.GetRequest{AppName: AppName, UserID: UserID, SessionID: sessionID})
		if err == nil && got.Session != nil {
			if kind != "" {
				_ = UpdateState(got.Session, map[string]any{"conversationKind": kind})
			}
			if title != "" {
				obj, _ := got.Session.State().Get("objective")
				if strings.TrimSpace(stateString(obj)) == "" {
					_ = SetObjective(got.Session, title, "")
				}
			}
			return got.Session.ID(), nil
		}
		if err != nil && !errors.Is(err, session.ErrNotFound) && !strings.Contains(err.Error(), "not found") {
			return "", err
		}
	}
	state := map[string]any{}
	if title != "" {
		state["objective"] = title
	}
	if kind != "" {
		state["conversationKind"] = kind
	}
	created, err := store.Create(ctx, &session.CreateRequest{
		AppName:   AppName,
		UserID:    UserID,
		SessionID: sessionID,
		State:     state,
	})
	if err != nil {
		return "", err
	}
	return created.Session.ID(), nil
}

// AppendChatLine writes one user or model line into the IDE session.
// The same event ID is a no-op, so a retried sync does not duplicate the line.
func AppendChatLine(workspace, sessionID, eventID, role, author, text string) error {
	text = strings.TrimSpace(text)
	eventID = strings.TrimSpace(eventID)
	sessionID = strings.TrimSpace(sessionID)
	if text == "" || eventID == "" || sessionID == "" {
		return nil
	}
	store, err := Open(workspace)
	if err != nil {
		return err
	}
	ctx := context.Background()
	got, err := store.Get(ctx, &session.GetRequest{AppName: AppName, UserID: UserID, SessionID: sessionID})
	if err != nil {
		return err
	}
	evs := got.Session.Events()
	for i := 0; i < evs.Len(); i++ {
		ev := evs.At(i)
		if ev != nil && ev.ID == eventID {
			return nil
		}
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role != "user" {
		role = "model"
	}
	if strings.TrimSpace(author) == "" {
		if role == "user" {
			author = "user"
		} else {
			author = "Agent Hive"
		}
	}
	if role == "user" {
		author = "user"
	}
	return store.AppendEvent(ctx, got.Session, &session.Event{
		ID:        eventID,
		Author:    author,
		Timestamp: time.Now().UTC(),
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Role:  role,
				Parts: []*genai.Part{{Text: text}},
			},
		},
	})
}

func stateString(v any) string {
	s, _ := v.(string)
	return s
}
