package modelx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/testutil/fakeagy"
)

func TestStreamJSONDeltasStayOutOfArgv(t *testing.T) {
	dir := t.TempDir()
	bin := fakeagy.Write(t, dir)
	t.Setenv("AGY_FAKE_MODE", "stream")
	var got []StreamEvent
	err := (&Agy{Bin: bin, Dir: dir, Timeout: 5 * time.Second}).Stream(context.Background(), "responda apenas OK\n\"aspas\"", func(ev StreamEvent) {
		got = append(got, ev)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Text != "Olá" || got[1].ToolName != "write_file" || got[2].Type != "result" {
		t.Fatalf("events = %+v", got)
	}
	args := string(mustRead(t, filepath.Join(dir, "args")))
	stdin := string(mustRead(t, filepath.Join(dir, "stdin")))
	if strings.Contains(args, "responda apenas OK") || strings.Contains(args, "\n-p\n") || strings.HasPrefix(args, "-p\n") {
		t.Fatalf("prompt vazou para o argv:\n%s", args)
	}
	if !strings.Contains(stdin, "responda apenas OK") || !strings.Contains(stdin, "\"aspas\"") {
		t.Fatalf("stdin = %s", stdin)
	}
	body := string(mustRead(t, filepath.Join(dir, "schema-body")))
	if !strings.Contains(body, "tool_calls") {
		t.Fatalf("schema = %s", body)
	}
	for _, line := range strings.Split(args, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			t.Fatalf("json-schema inline: %s", line)
		}
	}
}

func TestStreamNilReceiverDoesNotPanic(t *testing.T) {
	t.Setenv("HIVEMIND_AGY_PATH", "")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	var nilAgy *Agy
	err := nilAgy.Stream(context.Background(), "oi", nil)
	if err == nil {
		t.Fatal("expected error when agy binary is absent")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
