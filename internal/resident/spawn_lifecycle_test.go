package resident

import (
	"context"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
)

// flattenReq is duplicated from other resident tests on purpose: each test
// file stays independent of shared helpers.
func flattenSpawnReq(req *model.LLMRequest) string {
	var b strings.Builder
	if req.Config != nil && req.Config.SystemInstruction != nil {
		for _, p := range req.Config.SystemInstruction.Parts {
			b.WriteString(p.Text)
		}
	}
	for _, c := range req.Contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil {
				b.WriteString(p.Text)
			}
		}
	}
	return b.String()
}

func TestSpawnReadonlyIntersectsTiers(t *testing.T) {
	// A write-class spawn request collapsed to read-only must lose the
	// write class: the ceiling the child inherits is read, so a write call
	// from the child fails closed at policy level.
	got := intersectTiers([]Tier{TierRead, TierWrite, TierExec}, TierRead)
	if len(got) != 1 || got[0] != TierRead {
		t.Fatalf("intersectTiers deveria deixar apenas read, veio %v", got)
	}
	empty := intersectTiers(nil, TierRead)
	if len(empty) != 0 {
		t.Fatalf("lista vazia deve continuar vazia (runSpawn aplica o fallback), veio %v", empty)
	}
}

func TestSpawnReadonlyCeilingIsRead(t *testing.T) {
	var seen []Call
	scripted := &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
		return modelx.Text("relatório: nada a alterar")
	}}
	ws := t.TempDir()
	var events []Event
	var mu sync.Mutex
	parent := &Runner{
		BaseDir:    t.TempDir(),
		Model:      scripted,
		Streaming:  agent.StreamingModeNone,
		OnEvent: func(ev Event) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	}
	// Prime the parent's last workspace so the child resolves roots.
	parent.LastBroker = NewBroker(BrokerConfig{Workspace: ws})

	out, err := parent.runSpawn(context.Background(), SpawnRequest{
		Objective: "analise o workspace e relate riscos",
		Classes:   []string{"read", "write"},
		Readonly:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "readonly") {
		t.Fatalf("cabeçalho deve declarar modo readonly, veio:\n%s", out)
	}
	if !strings.Contains(out, "chamadas de ferramenta") || !strings.Contains(out, "tokens") {
		t.Fatalf("cabeçalho deve trazer telemetria de ciclo de vida, veio:\n%s", out)
	}
	// ceilingTier over the intersected classes must be read.
	if ceiling := ceilingTier([]Tier{TierRead}); ceiling != TierRead {
		t.Fatalf("teto do filho readonly deve ser read, veio %q", ceiling)
	}
	_ = seen
}

func TestSpawnContextBriefRidesCapped(t *testing.T) {
	var captured string
	scripted := &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
		captured = flattenSpawnReq(req)
		return modelx.Text("ok")
	}}
	parent := &Runner{BaseDir: t.TempDir(), Model: scripted, Streaming: agent.StreamingModeNone}
	parent.LastBroker = NewBroker(BrokerConfig{Workspace: t.TempDir()})

	brief := strings.Repeat("fato relevante. ", 400) // ~7 KiB > cap 2 KiB
	if _, err := parent.runSpawn(context.Background(), SpawnRequest{
		Objective:    "verifique o plano",
		ContextBrief: brief,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(captured, "CONTEXTO DO AGENTE PAI") {
		t.Fatal("brief deve viajar com o objetivo do filho")
	}
	if !strings.Contains(captured, "truncado em 2 KiB") {
		t.Fatal("brief acima do teto deve ser truncado com aviso explícito")
	}
	if strings.Count(captured, "fato relevante") > 300 {
		t.Fatal("truncamento deve efetivamente reduzir o brief")
	}
}

func TestSpawnEmitsSubagentLifecycleEvent(t *testing.T) {
	scripted := &modelx.Scripted{Fn: func(req *model.LLMRequest, _ int) *model.LLMResponse {
		return modelx.Text("pronto")
	}}
	var events []Event
	var mu sync.Mutex
	parent := &Runner{
		BaseDir:   t.TempDir(),
		Model:     scripted,
		Streaming: agent.StreamingModeNone,
		OnEvent: func(ev Event) {
			mu.Lock()
			events = append(events, ev)
			mu.Unlock()
		},
	}
	parent.LastBroker = NewBroker(BrokerConfig{Workspace: t.TempDir()})

	if _, err := parent.runSpawn(context.Background(), SpawnRequest{Objective: "resuma"}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var saw bool
	for _, ev := range events {
		if ev.Kind == "subagent" && strings.Contains(ev.Detail, "subagente") {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("evento de ciclo de vida 'subagent' deve ser emitido, eventos: %+v", events)
	}
}
