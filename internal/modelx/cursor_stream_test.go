package modelx

import (
	"os"
	"strings"
	"testing"
)

func TestCursorFixtureKeepsSpacesAndHidesInternalFields(t *testing.T) {
	raw, err := os.ReadFile("testdata/cursor_stream_glued.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	var tr cursorTranscript
	var result string
	var deltas []string
	for _, line := range strings.Split(string(raw), "\n") {
		n, ok := parseCursorStreamLine(line)
		if !ok {
			continue
		}
		blob := n.Text + n.ToolName + n.ToolPath
		for _, banned := range []string{"startedAtMs", "toolCallId", "hookAdditionalContexts", "segredo"} {
			if strings.Contains(blob, banned) {
				t.Fatalf("campo interno ou corpo vazou em %+v", n)
			}
		}
		if n.Final {
			result = n.Text
			continue
		}
		deltas = append(deltas, tr.absorb(n, `C:\hivemind`))
	}
	got := tr.finish(result)
	if strings.Contains(got, "Voulero") || !strings.Contains(got, "Vou ler o arquivo.") || !strings.Contains(got, "Agora busco.") {
		t.Fatalf("whitespace dos deltas perdido:\n%s", got)
	}
	for _, want := range []string{
		"[[hmtool:read:.hivemind/cursor-task.md]]",
		"[[hmtool:getMcpTools:]]",
		"[[hmtool:grep:ide/frontend]]",
		"Li o arquivo e busquei no frontend.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("falta %q em\n%s", want, got)
		}
	}
	joined := strings.Join(deltas, "")
	if strings.Contains(joined, "Cursor ·") || strings.Contains(joined, "startedAtMs") {
		t.Fatalf("delta ainda mistura aviso cru: %s", joined)
	}
}

func TestCursorDeltaKeepsSpaceOnlyToken(t *testing.T) {
	var tr cursorTranscript
	for _, line := range []string{
		`{"type":"assistant","timestamp_ms":1,"message":{"content":[{"type":"text","text":"A"}]}}`,
		`{"type":"assistant","timestamp_ms":2,"message":{"content":[{"type":"text","text":" "}]}}`,
		`{"type":"assistant","timestamp_ms":3,"message":{"content":[{"type":"text","text":"B"}]}}`,
	} {
		n, ok := parseCursorStreamLine(line)
		if !ok {
			t.Fatalf("delta recusado: %s", line)
		}
		tr.absorb(n, "")
	}
	if got := tr.finish(""); got != "A B" {
		t.Fatalf("espaço entre tokens sumiu: %q", got)
	}
}

func TestCursorToolMetaAloneIsDropped(t *testing.T) {
	line := `{"type":"tool_call","subtype":"started","tool_call":{"startedAtMs":1,"toolCallId":"x","hookAdditionalContexts":[]}}`
	if _, ok := parseCursorStreamLine(line); ok {
		t.Fatal("objeto só com campos internos não pode virar texto")
	}
}
