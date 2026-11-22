package modelx

import "testing"

func TestParseCursorStreamLineUsageOnResult(t *testing.T) {
	line := `{"type":"result","result":"concluído","usage":{"input_tokens":1200,"output_tokens":300,"cache_read_input_tokens":800,"total_cost_usd":0.01234}}`
	notice, ok := ParseCursorStreamLine(line)
	if !ok {
		t.Fatalf("result line not parsed")
	}
	if !notice.Final || notice.Failed {
		t.Fatalf("expected final non-error notice")
	}
	if notice.Usage == nil {
		t.Fatalf("usage missing from parsed result event")
	}
	if notice.Usage.InputTokens != 1200 || notice.Usage.OutputTokens != 300 || notice.Usage.CacheReadTokens != 800 {
		t.Fatalf("token fields wrong: %+v", notice.Usage)
	}
	if notice.Usage.CostUSD == nil || *notice.Usage.CostUSD < 0.01233 || *notice.Usage.CostUSD > 0.01235 {
		t.Fatalf("cost field wrong: %+v", notice.Usage.CostUSD)
	}
}

func TestParseCursorStreamLineWithoutUsage(t *testing.T) {
	line := `{"type":"result","result":"concluído"}`
	notice, ok := ParseCursorStreamLine(line)
	if !ok || notice.Usage != nil {
		t.Fatalf("result without usage must parse to nil usage: %+v", notice)
	}
}
