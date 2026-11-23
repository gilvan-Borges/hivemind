package loopx

import "testing"

func TestParseJudgeAndStrongerModel(t *testing.T) {
	got, err := ParseJudge("ok\n{\"verdict\":\"met\",\"reason\":\"testes verdes\",\"next_hint\":\"\"}")
	if err != nil || got.Verdict != JudgeMet || got.Reason != "testes verdes" {
		t.Fatalf("parse: %+v %v", got, err)
	}
	bad, err := ParseJudge(`{"verdict":"talvez"}`)
	if err != nil || bad.Verdict != JudgeNotMet {
		t.Fatalf("veredito desconhecido: %+v %v", bad, err)
	}
	if StrongerModel("claude-sonnet-4-5") != "claude-opus-4-5" {
		t.Fatal("sonnet deveria subir para opus")
	}
	if StrongerModel("gpt-4o-mini") != "gpt-4o" {
		t.Fatal("mini deveria subir")
	}
	if got := ResolveJudgeModel("review_model", "claude-haiku-3"); got != "claude-opus-4-5" {
		t.Fatalf("review_model: %s", got)
	}
	if got := ResolveJudgeModel("claude-opus-4-5", "claude-sonnet-4-5"); got != "claude-opus-4-5" {
		t.Fatalf("modelo explícito: %s", got)
	}
}
