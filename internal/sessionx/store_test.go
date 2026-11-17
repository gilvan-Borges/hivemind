package sessionx

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
)

func TestPersistAndReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(context.Background(), &session.CreateRequest{
		AppName: AppName,
		UserID:  UserID,
		State:   map[string]any{"objective": "fazer healthcheck"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := SetObjective(created.Session, "fazer healthcheck", "PROJ-1"); err != nil {
		t.Fatal(err)
	}
	ev := &session.Event{
		Author:    "pm",
		Timestamp: time.Now().UTC(),
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{
				Role:  "model",
				Parts: []*genai.Part{{Text: "plano inicial"}},
			},
		},
		Actions: session.EventActions{StateDelta: map[string]any{
			"pm_plan":                   "plano inicial",
			session.KeyPrefixTemp + "x": "descartar",
		}},
	}
	if err := store.AppendEvent(context.Background(), created.Session, ev); err != nil {
		t.Fatal(err)
	}
	id := created.Session.ID()

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get(context.Background(), &session.GetRequest{AppName: AppName, UserID: UserID, SessionID: id})
	if err != nil {
		t.Fatal(err)
	}
	if got.Session.Events().Len() != 1 || got.Session.Events().At(0).Author != "pm" {
		t.Fatalf("events = %d", got.Session.Events().Len())
	}
	plan, err := got.Session.State().Get("pm_plan")
	if err != nil || plan != "plano inicial" {
		t.Fatalf("plan = %v %v", plan, err)
	}
	if _, err := got.Session.State().Get(session.KeyPrefixTemp + "x"); !errors.Is(err, session.ErrStateKeyNotExist) {
		t.Fatalf("temp key should not persist: %v", err)
	}
	text := got.Session.Events().At(0).Content.Parts[0].Text
	if text != "plano inicial" {
		t.Fatalf("text = %q", text)
	}
	metas, err := reopened.ListMeta()
	if err != nil || len(metas) != 1 || metas[0].JiraKey != "PROJ-1" {
		t.Fatalf("metas = %+v %v", metas, err)
	}
}
