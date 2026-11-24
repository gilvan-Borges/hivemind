package workerx

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
)

func TestHelperCursorWorkerProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CURSOR_WORKER_HELPER") != "1" {
		return
	}
	url := os.Getenv("HIVEMIND_WORKER_URL")
	tok := os.Getenv("HIVEMIND_WORKER_TOKEN")
	dispID := os.Getenv("HIVEMIND_WORKER_DISPATCH_ID")
	taskID := os.Getenv("HIVEMIND_WORKER_TASK_ID")
	cwd, _ := os.Getwd()

	matches, err := filepath.Glob(filepath.Join(cwd, ".hivemind", "cursor-task-*.md"))
	if err != nil || len(matches) != 1 {
		os.Exit(2)
	}
	taskBytes, err := os.ReadFile(matches[0])
	if err != nil || !strings.Contains(string(taskBytes), dispID) {
		os.Exit(2)
	}

	post := func(path string, v any) int {
		b, _ := json.Marshal(v)
		req, _ := http.NewRequest(http.MethodPost, url+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			os.Exit(3)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// 1. Heartbeat
	if code := post("/heartbeat", HeartbeatEvent{DispatchID: dispID, TaskID: taskID, Message: "implementando"}); code != http.StatusOK {
		os.Exit(4)
	}
	// 2. Blocking Ask
	askBody, _ := json.Marshal(AskRequest{DispatchID: dispID, Question: "Confirmar criação de endpoint?"})
	askReq, _ := http.NewRequest(http.MethodPost, url+"/ask", bytes.NewReader(askBody))
	askReq.Header.Set("Authorization", "Bearer "+tok)
	askRespRaw, err := http.DefaultClient.Do(askReq)
	if err != nil {
		os.Exit(5)
	}
	var askResp AskResponse
	_ = json.NewDecoder(askRespRaw.Body).Decode(&askResp)
	askRespRaw.Body.Close()
	if !askResp.Approved {
		os.Exit(6)
	}

	// 3. Edit allowed file inside worktree.
	_ = os.WriteFile(filepath.Join(cwd, "pkg", "api", "handler.go"), []byte("package api\nfunc Endpoint() string { return \"ok\" }\n"), 0o644)

	// 4. First /done accepted (200), second duplicate /done rejected (409).
	if code := post("/done", DoneEvent{DispatchID: dispID, TaskID: taskID, Outcome: "success", Summary: "feito", FilesModified: []string{"pkg/api/handler.go"}}); code != http.StatusOK {
		os.Exit(7)
	}
	if code := post("/done", DoneEvent{DispatchID: dispID, TaskID: taskID, Outcome: "failed", Summary: "duplicado"}); code != http.StatusConflict {
		os.Exit(8)
	}

	// Emit Cursor CLI NDJSON stream lines.
	ts := int64(1)
	b1, _ := json.Marshal(map[string]any{"type": "assistant", "timestamp_ms": ts, "message": map[string]any{"content": []map[string]string{{"type": "text", "text": "endpoint criado"}}}})
	b2, _ := json.Marshal(map[string]any{"type": "result", "is_error": false, "result": "endpoint concluído"})
	os.Stdout.Write(append(b1, '\n'))
	os.Stdout.Write(append(b2, '\n'))
	os.Exit(0)
}

func TestCursorWorkerAdapter_EndToEndContractAndPostHocGuard(t *testing.T) {
	dir, g := initWorkerTestRepo(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	adapter := &CursorWorkerAdapter{
		LookCursorFn: func() (string, error) { return exe, nil },
		APIKeyFn:     func() string { return "sk-test-fake-0000" },
		BaseArgs:     []string{"-test.run=^TestHelperCursorWorkerProcess$", "--"},
		Git:          g,
	}

	var hbCount, askCount int
	var outLines []string
	req := DispatchRequest{
		DispatchID:     "disp-e2e-1",
		TaskID:         "task-e2e-1",
		Provider:       "cursor",
		Model:          "auto",
		AgentName:      "dev_cursor",
		WorktreeDir:    dir,
		PermissionMode: "governed",
		Env:            map[string]string{"GO_WANT_CURSOR_WORKER_HELPER": "1"},
		Handoff: HandoffBrief{
			RunID:      "run-e2e-1",
			Objective:  "Adicionar func Endpoint em pkg/api/handler.go",
			OwnedPaths: []string{"pkg/api/**"},
		},
	}

	done, err := adapter.Execute(context.Background(), req, Callbacks{
		OnHeartbeat: func(HeartbeatEvent) { hbCount++ },
		OnAsk: func(_ context.Context, q AskRequest) (AskResponse, error) {
			askCount++
			return AskResponse{Approved: true, Answer: "sim"}, nil
		},
		OnOutput: func(line string) { outLines = append(outLines, line) },
	})
	if err != nil {
		t.Fatalf("CursorWorkerAdapter.Execute failed: %v", err)
	}
	if done.Outcome != "success" || hbCount != 1 || askCount != 1 {
		t.Fatalf("unexpected execution outcome=%+v hb=%d ask=%d", done, hbCount, askCount)
	}

	guardRes, err := ValidatePostHocDiff(context.Background(), gitx.Local{}, dir, req.Handoff.OwnedPaths, done.FilesModified...)
	if err != nil || !guardRes.Passed() {
		t.Fatalf("expected post-hoc guard to pass on allowed path, got %+v err=%v", guardRes, err)
	}
	if len(guardRes.Modified) != 1 || guardRes.Modified[0] != "pkg/api/handler.go" {
		t.Fatalf("unexpected modified list: %#v", guardRes.Modified)
	}
}
