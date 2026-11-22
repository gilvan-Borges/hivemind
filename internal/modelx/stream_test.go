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

func TestAgyDecisionDeltaFilter(t *testing.T) {
	t.Run("texto simples passa intacto com espacos iniciais", func(t *testing.T) {
		var f agyDecisionDeltaFilter
		got := f.Push("  ") + f.Push("Olá ") + f.Push("mundo!")
		if got != "  Olá mundo!" {
			t.Fatalf("got = %q", got)
		}
	})

	t.Run("envelope apenas com tool_calls nao vaza JSON bruto", func(t *testing.T) {
		raw := `{"text":"","tool_calls":[{"id":"tc_list_root","name":"list_dir","arguments":{"path":"."}}]}`
		for _, step := range []int{1, 3, 7, len(raw)} {
			var f agyDecisionDeltaFilter
			var out strings.Builder
			for i := 0; i < len(raw); i += step {
				end := i + step
				if end > len(raw) {
					end = len(raw)
				}
				out.WriteString(f.Push(raw[i:end]))
			}
			if out.String() != "" {
				t.Fatalf("step=%d vazou texto: %q", step, out.String())
			}
		}
	})

	t.Run("envelope com texto escapes unicode e pares substitutos em chunks", func(t *testing.T) {
		raw := "  {\"text\":\"Olá\\n\\\"mundo\\\" \\u00e9 \\uD83D\\uDE00\\\\fim\",\"tool_calls\":[]}"
		var f agyDecisionDeltaFilter
		var out strings.Builder
		for i := 0; i < len(raw); i += 2 {
			end := i + 2
			if end > len(raw) {
				end = len(raw)
			}
			out.WriteString(f.Push(raw[i:end]))
		}
		want := "Olá\n\"mundo\" é \U0001F600\\fim"
		if out.String() != want {
			t.Fatalf("got = %q, want = %q", out.String(), want)
		}
	})

	t.Run("tool_calls antes de text ainda extrai text", func(t *testing.T) {
		raw := `{"tool_calls":[{"id":"1","name":"read_file","arguments":{"path":"README.md","meta":{"nested":[1,2]}}}],"text":"Resposta final"}`
		var f agyDecisionDeltaFilter
		var out strings.Builder
		for i := 0; i < len(raw); i += 5 {
			end := i + 5
			if end > len(raw) {
				end = len(raw)
			}
			out.WriteString(f.Push(raw[i:end]))
		}
		if out.String() != "Resposta final" {
			t.Fatalf("got = %q", out.String())
		}
	})
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
