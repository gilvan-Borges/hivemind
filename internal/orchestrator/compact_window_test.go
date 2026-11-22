package orchestrator

import (
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/contextx"
)

func TestCompactionStatsCallbackWindowDetail(t *testing.T) {
	var got []Event
	r := &Runner{OnEvent: func(ev Event) { got = append(got, ev) }}
	cb := r.compactionStatsCallback("dev_1", "Dev 1")

	cb(contextx.Stats{Compacted: 4, TokensSaved: 8_000, ContextTokens: 134_000, ContextWindow: 200_000})
	cb(contextx.Stats{Compacted: 2, TokensSaved: 3_000, ContextTokens: 150_000, ContextWindow: 200_000})

	if len(got) != 2 {
		t.Fatalf("esperado 2 eventos compact, veio %d", len(got))
	}
	first := got[0]
	if !strings.Contains(first.Detail, "janela 67% ocupada no gatilho (134000/200000 tokens)") {
		t.Fatalf("primeiro evento deve trazer ocupacao de janela, veio %q", first.Detail)
	}
	if !strings.Contains(first.Detail, "1ª compactação deste agente no run") {
		t.Fatalf("primeiro evento deve marcar primeira compactacao, veio %q", first.Detail)
	}
	if first.Stage != "dev" {
		t.Fatalf("stage deve ser dev, veio %q", first.Stage)
	}
	second := got[1]
	if strings.Contains(second.Detail, "1ª compactação") {
		t.Fatalf("segundo evento nao deve repetir marcador de primeira compactacao, veio %q", second.Detail)
	}
	if !strings.Contains(second.Detail, "janela 75% ocupada no gatilho") {
		t.Fatalf("segundo evento deve trazer ocupacao atualizada, veio %q", second.Detail)
	}
}

func TestCompactionStatsCallbackNoWindowStillEmits(t *testing.T) {
	var got []Event
	r := &Runner{OnEvent: func(ev Event) { got = append(got, ev) }}
	cb := r.compactionStatsCallback("qa_1", "QA")

	cb(contextx.Stats{Compacted: 1, TokensSaved: 500})
	if len(got) != 1 {
		t.Fatalf("esperado 1 evento, veio %d", len(got))
	}
	if strings.Contains(got[0].Detail, "janela") {
		t.Fatalf("sem metricas de janela o detalhe nao deve menciona-la, veio %q", got[0].Detail)
	}
	if got[0].Tokens != 500 {
		t.Fatalf("tokens poupados devem seguir no evento, veio %d", got[0].Tokens)
	}
}
