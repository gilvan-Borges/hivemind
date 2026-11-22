package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestScoutParentFromAndStage(t *testing.T) {
	parent, ok := scoutParentFrom("scout_12_from_dev_1")
	if !ok || parent != "dev_1" {
		t.Fatalf("esperava dev_1, veio %q (ok=%v)", parent, ok)
	}
	if _, ok := scoutParentFrom("dev_1"); ok {
		t.Fatal("autor comum não deveria ser tratado como scout")
	}
	if got := inferStage("scout_3_from_dev_1", "Subagente (contexto isolado)"); got != "scout" {
		t.Fatalf("scout mapeou para stage %q; guard de dev seria contaminado", got)
	}
	if got := inferStage("scout_4_from_qa_1", "Subagente"); got != "scout" {
		t.Fatalf("scout de QA mapeou para stage %q; reset de budgets seria disparado", got)
	}
}

func TestCanonicalCostRoleInheritsScoutParent(t *testing.T) {
	cases := map[string]string{
		"scout_1_from_dev_1": "Dev",
		"scout_2_from_qa_1":  "QA",
		"scout_5_from_chefe": "Chefe",
		"scout_9_sem_pai":    "Outro",
	}
	for author, want := range cases {
		if got := CanonicalCostRole(author, "Subagente (contexto isolado)"); got != want {
			t.Fatalf("CanonicalCostRole(%q) = %q, esperava %q", author, got, want)
		}
	}
}

func TestScoutInheritsParentDiscount(t *testing.T) {
	r := &Runner{
		routedDiscount: map[string]int{"dev_1": 35},
	}
	if got := r.resolveAuthorDiscount("scout_7_from_dev_1"); got != 35 {
		t.Fatalf("scout deveria herdar desconto 35 do pai dev_1, veio %d", got)
	}
	if got := r.resolveAuthorDiscount("dev_1"); got != 35 {
		t.Fatalf("desconto direto de dev_1 deveria ser 35, veio %d", got)
	}
}

func TestReserveScoutSpawnCap(t *testing.T) {
	r := &Runner{Project: team.File{Settings: team.Settings{MaxSubagentCalls: 2}}}
	for i := 0; i < 2; i++ {
		if err := r.reserveScoutSpawn("dev_1"); err != nil {
			t.Fatalf("spawn %d inesperadamente recusado: %v", i+1, err)
		}
	}
	if err := r.reserveScoutSpawn("dev_1"); err == nil {
		t.Fatal("esperava recusa no 3º spawn do mesmo agente")
	}
	if err := r.reserveScoutSpawn("qa_1"); err != nil {
		t.Fatalf("cota deve ser por agente; QA recusado: %v", err)
	}
}

func TestRunScoutExecutesMissionWithCleanSession(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("resposta = 42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sawMission := false
	r := &Runner{
		Workspace: root,
		Project:   team.File{Settings: team.DefaultSettings()},
		NewModel: func(context.Context, team.Agent) (model.LLM, error) {
			return &modelx.Scripted{Fn: func(req *model.LLMRequest, call int) *model.LLMResponse {
				var all strings.Builder
				for _, c := range req.Contents {
					if c == nil {
						continue
					}
					for _, p := range c.Parts {
						if p != nil {
							all.WriteString(p.Text)
						}
					}
				}
				if strings.Contains(all.String(), "Missão do subagente") {
					sawMission = true
				}
				if call == 1 {
					return modelx.FunctionCall("read_file", "r1", map[string]any{"path": "notes.md"})
				}
				return modelx.Text("Encontrei resposta = 42 em notes.md:1.")
			}}, nil
		},
	}
	report, calls, _, err := r.runScout(
		context.Background(),
		team.Agent{Name: "dev_1", Role: team.RoleDeveloper, Provider: "acme", Model: "modelo-x"},
		"dev_1", root, "qual é a resposta gravada em notes.md?", "ler notes",
	)
	if err != nil {
		t.Fatalf("runScout falhou: %v", err)
	}
	if !sawMission {
		t.Fatal("o scout deveria ter recebido a mensagem de missão")
	}
	if calls != 2 {
		t.Fatalf("esperava 2 chamadas de LLM (read + relatório), veio %d", calls)
	}
	if !strings.Contains(report, "42") {
		t.Fatalf("relatório do scout sem a resposta: %q", report)
	}
}

func TestFilterReminderBlocksSkipsUnchangedAndPresent(t *testing.T) {
	req := &model.LLMRequest{Contents: []*genai.Content{{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{Text: "turno atual"}},
	}}}
	last := map[string]string{}
	blocks := []reminderBlock{
		{marker: "[chief_plan]", text: "[chief_plan]\nPLANO-A\n\n"},
		{marker: "[qa_report]", text: "[qa_report]\nNOVO\n\n"},
	}
	// Primeira passada: nada memorizado, ambos entram.
	if out := filterReminderBlocks(req, blocks, last); len(out) != 2 {
		t.Fatalf("primeira passada deveria injetar ambos, veio %d", len(out))
	}
	// O transcript ganhou os dois blocos (via appendSystemReminder).
	req.Contents[0].Parts[0].Text += "\n<system-reminder>\n[chief_plan]\nPLANO-A\n\n[qa_report]\nNOVO\n</system-reminder>"
	// Segunda passada: inalterados e presentes → nada a re-enviar.
	if out := filterReminderBlocks(req, blocks, last); len(out) != 0 {
		t.Fatalf("blocos inalterados e presentes deveriam ser omitidos, veio %d", len(out))
	}
	// O plano mudou: só ele volta.
	blocks[0].text = "[chief_plan]\nPLANO-B\n\n"
	if out := filterReminderBlocks(req, blocks, last); len(out) != 1 || out[0].marker != "[chief_plan]" {
		t.Fatalf("só o plano alterado deveria voltar, veio %d", len(out))
	}
}

func TestFilterReminderBlocksReinjectsAfterCompactionOrStaleSurvivor(t *testing.T) {
	// Primeira injeção memoriza o hash do plano.
	last := map[string]string{}
	full := filterReminderBlocks(&model.LLMRequest{Contents: []*genai.Content{{Parts: []*genai.Part{{Text: "início"}}}}},
		[]reminderBlock{{marker: "[chief_plan]", text: "[chief_plan]\nPLANO-A\n\n"}}, last)
	if len(full) != 1 {
		t.Fatal("primeira injeção deveria passar")
	}
	// Compactação removeu o bloco do transcript: mesmo hash, precisa reinjetar.
	compacted := &model.LLMRequest{Contents: []*genai.Content{{Parts: []*genai.Part{{Text: "histórico compactado"}}}}}
	if out := filterReminderBlocks(compacted, []reminderBlock{{marker: "[chief_plan]", text: "[chief_plan]\nPLANO-A\n\n"}}, last); len(out) != 1 {
		t.Fatal("bloco removido pela compactação deveria ser reinjetado")
	}
	// Caso P2: plano mudou para PLANO-B (hash memorizado = PLANO-B), mas uma compactação
	// posterior derrubou PLANO-B e deixou um turno antigo com "[chief_plan]\nPLANO-A".
	// Como o conteúdo atual (PLANO-B) não está no transcript, deve reinjetar PLANO-B.
	_ = filterReminderBlocks(compacted, []reminderBlock{{marker: "[chief_plan]", text: "[chief_plan]\nPLANO-B\n\n"}}, last)
	staleSurvivor := &model.LLMRequest{Contents: []*genai.Content{{Parts: []*genai.Part{{Text: "[chief_plan]\nPLANO-A"}}}}}
	if out := filterReminderBlocks(staleSurvivor, []reminderBlock{{marker: "[chief_plan]", text: "[chief_plan]\nPLANO-B\n\n"}}, last); len(out) != 1 {
		t.Fatal("quando apenas versão antiga do bloco sobreviveu no transcript, a versão atual deve ser reinjetada")
	}
}

func TestReminderBlockPresent(t *testing.T) {
	req := &model.LLMRequest{Contents: []*genai.Content{
		{Parts: []*genai.Part{{Text: "sem marcador"}}},
		{Parts: []*genai.Part{{Text: "contexto\n[chef_plan-qualquer]"}}},
	}}
	if reminderBlockPresent(req, "[chef_plan]") {
		t.Fatal("marcador parcial não deveria casar")
	}
	if !reminderBlockPresent(req, "[chef_plan-qualquer]") {
		t.Fatal("marcador exato deveria casar")
	}
	if reminderBlockPresent(nil, "[x]") || reminderBlockPresent(req, "") {
		t.Fatal("req nil ou marcador vazio deveriam devolver false")
	}
}
