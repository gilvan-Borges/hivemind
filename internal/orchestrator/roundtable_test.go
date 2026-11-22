package orchestrator

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestLiveRoundtableSupervisorAutoCoachesAndDeliversSteerDirectives(t *testing.T) {
	var (
		evMu   sync.Mutex
		events []Event
	)
	r := &Runner{
		Team: team.Team{
			Name: "principal",
			Agents: []team.Agent{
				{Name: "chefe", Role: team.RoleArchitect, Provider: "agy", Model: "agy"},
				{Name: "dev", Role: team.RoleDeveloper, Provider: "agy", Model: "agy"},
				{Name: "qa", Role: team.RoleQA, Provider: "agy", Model: "agy"},
			},
		},
		sharedBoard:       newTeamBoard(),
		chiefPlan:         "Implementar DecisaoBiometriaEvent em src/auth/event/ e publicar no outbox.",
		chiefContextBrief: "Classe Usuario.java L628 método registrarDecisaoAutomatica.",
		LiveRoundtableModel: &modelx.Scripted{Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
			return modelx.Text(fmt.Sprintf("Chefe Sênior (intervenção #%d): PARE de buscar e edite src/auth/Usuario.java na linha 628 criando DecisaoBiometriaEvent.java.", call))
		}},
		OnEvent: func(ev Event) {
			evMu.Lock()
			events = append(events, ev)
			evMu.Unlock()
		},
	}

	devAgent := r.Team.Agents[1]
	cb := r.newLiveRoundtableSupervisorCallback(devAgent, "dev", "", func() int { return 0 })
	readTool := stubNamedTool{name: "read_file"}
	shellTool := stubNamedTool{name: "run_shell"}
	writeTool := stubNamedTool{name: "write_file"}

	// 1. User/Chief live SteerRun injection is delivered on the very next tool completion.
	if !r.Steer("Arquiteto Sênior", "Mexa direto em src/auth/Usuario.java L628") {
		t.Fatal("expected Steer to succeed")
	}
	res1, err := cb(nil, readTool, map[string]any{"path": "f1.java"}, map[string]any{"text": "conteudo 1"}, nil)
	if err != nil {
		t.Fatalf("unexpected callback error: %v", err)
	}
	if res1 == nil || !strings.Contains(fmt.Sprint(res1["text"]), "Mexa direto em src/auth/Usuario.java L628") {
		t.Fatalf("expected live steer directive injected into tool output, got %v", res1)
	}

	// 2. When Dev explores 10 files in a row without editing, Chief automatically intervenes in the Roundtable.
	var TenthOut map[string]any
	for i := 2; i <= 10; i++ {
		out, _ := cb(nil, readTool, map[string]any{"path": fmt.Sprintf("f%d.java", i)}, map[string]any{"text": "ok"}, nil)
		if i == 10 {
			TenthOut = out
		}
	}
	if TenthOut == nil || !strings.Contains(fmt.Sprint(TenthOut["text"]), "INTERVENÇÃO AO VIVO DO CHEFE") {
		t.Fatalf("expected automatic Chief roundtable intervention at 10 exploratory reads, got %v", TenthOut)
	}
	if !strings.Contains(fmt.Sprint(TenthOut["text"]), "DecisaoBiometriaEvent.java") {
		t.Fatalf("expected Chief LLM guidance in intervention, got %v", TenthOut["text"])
	}

	// 3. Two consecutive tool/build failures trigger immediate Chief error-recovery coaching.
	_, _ = cb(nil, shellTool, map[string]any{"command": "mvn test"}, map[string]any{"exit_code": 1, "text": "COMPILATION ERROR in Usuario.java"}, nil)
	errOut, _ := cb(nil, shellTool, map[string]any{"command": "mvn test"}, map[string]any{"exit_code": 1, "text": "COMPILATION ERROR in Usuario.java"}, nil)
	if errOut == nil || !strings.Contains(fmt.Sprint(errOut["text"]), "CORREÇÃO AO VIVO DO CHEFE") {
		t.Fatalf("expected automatic Chief coaching on 2 consecutive build failures, got %v", errOut)
	}

	// 4. Writing a file resets exploration and error streaks.
	if out, _ := cb(nil, writeTool, map[string]any{"path": "src/auth/DecisaoBiometriaEvent.java"}, map[string]any{"text": "gravado"}, nil); out != nil {
		t.Fatalf("expected no intervention on successful write_file, got %v", out)
	}

	evMu.Lock()
	defer evMu.Unlock()
	if len(events) < 3 {
		t.Fatalf("expected live roundtable handoff events emitted to UI, got %d", len(events))
	}
}
