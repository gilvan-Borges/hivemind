package sessionx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
)

func TestCompactOldToolOutputsAlsoTrimsWriteFileArguments(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(context.Background(), &session.CreateRequest{AppName: AppName, UserID: UserID})
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 5000)
	writeCall := &session.Event{
		Author:    "dev_1",
		Timestamp: time.Now().UTC(),
		LLMResponse: model.LLMResponse{Content: &genai.Content{Role: "model", Parts: []*genai.Part{{
			FunctionCall: &genai.FunctionCall{ID: "w1", Name: "write_file", Args: map[string]any{"path": "a.txt", "content": big}},
		}}}},
	}
	if err := store.AppendEvent(context.Background(), created.Session, writeCall); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		ev := &session.Event{
			Author:      "qa",
			Timestamp:   time.Now().UTC(),
			LLMResponse: model.LLMResponse{Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "ok"}}}},
		}
		if err := store.AppendEvent(context.Background(), created.Session, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := CompactOldToolOutputs(created.Session, 5); err != nil {
		t.Fatal(err)
	}
	ev := created.Session.Events().At(0)
	content, _ := ev.LLMResponse.Content.Parts[0].FunctionCall.Args["content"].(string)
	if len(content) >= len(big) || !strings.Contains(content, "compactado") {
		t.Fatalf("write_file content was not compacted: len=%d", len(content))
	}
	if orig, _ := writeCall.LLMResponse.Content.Parts[0].FunctionCall.Args["content"].(string); orig != big {
		t.Fatalf("caller event was mutated in place: len=%d", len(orig))
	}
	// Mutating the snapshot returned by Events() must not affect stored events.
	ev.LLMResponse.Content.Parts[0].FunctionCall.Args["content"] = "mutated"
	again, _ := created.Session.Events().At(0).LLMResponse.Content.Parts[0].FunctionCall.Args["content"].(string)
	if again != content {
		t.Fatalf("Events() leaked internal mutable map: got %q want %q", again, content)
	}
}

func TestCompactDoNotTouchRecentEvents(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(context.Background(), &session.CreateRequest{AppName: AppName, UserID: UserID})
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("y", 5000)
	recentCall := &session.Event{
		Author:    "dev_1",
		Timestamp: time.Now().UTC(),
		LLMResponse: model.LLMResponse{Content: &genai.Content{Role: "model", Parts: []*genai.Part{{
			FunctionCall: &genai.FunctionCall{ID: "w2", Name: "write_file", Args: map[string]any{"path": "b.txt", "content": big}},
		}}}},
	}
	if err := store.AppendEvent(context.Background(), created.Session, recentCall); err != nil {
		t.Fatal(err)
	}
	if err := CompactOldToolOutputs(created.Session, 5); err != nil {
		t.Fatal(err)
	}
	content, _ := recentCall.LLMResponse.Content.Parts[0].FunctionCall.Args["content"].(string)
	if content != big {
		t.Fatal("recent write_file call must stay intact")
	}
}

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
	if err := reopened.Delete(context.Background(), &session.DeleteRequest{AppName: AppName, UserID: UserID, SessionID: "../../outside"}); err == nil {
		t.Fatal("expected traversal session id to be rejected")
	}
	if err := reopened.Delete(context.Background(), &session.DeleteRequest{AppName: AppName, UserID: UserID, SessionID: id}); err != nil {
		t.Fatalf("delete session failed: %v", err)
	}
	metasAfter, err := reopened.ListMeta()
	if err != nil || len(metasAfter) != 0 {
		t.Fatalf("expected 0 sessions after delete, got %+v (%v)", metasAfter, err)
	}
}
