package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// newTestModel builds a model over a temp workspace. When withTeam is true a
// full-squad team.yaml is written first, mirroring a configured project.
func newTestModel(t *testing.T, withTeam bool) model {
	t.Helper()
	dir := t.TempDir()
	if withTeam {
		f := team.File{
			Version:  1,
			Active:   "padrao",
			Settings: team.DefaultSettings(),
		}
		f.Teams = append(f.Teams, team.FullSquadTeam("padrao", "time de teste", "gemini-flash-latest", "gemini", 1))
		if err := team.Save(dir, f); err != nil {
			t.Fatal(err)
		}
	}
	return newModel(context.Background(), Options{Workspace: dir})
}

func TestNewModelWithoutTeamOpensWizard(t *testing.T) {
	m := newTestModel(t, false)
	if m.modal != modalWizard {
		t.Fatalf("modal = %v, queria modalWizard no primeiro uso", m.modal)
	}
	if m.hasTeam {
		t.Fatal("hasTeam deveria ser falso sem team.yaml")
	}
}

func TestNewModelWithTeamBuildsAgents(t *testing.T) {
	m := newTestModel(t, true)
	if !m.hasTeam {
		t.Fatal("hasTeam deveria ser verdadeiro com team.yaml presente")
	}
	if m.modal != modalNone {
		t.Fatalf("modal = %v, queria modalNone", m.modal)
	}
	if len(m.agents) != 5 {
		t.Fatalf("agentes = %d, queria 5 (pm, líder, arquiteto, dev, qa)", len(m.agents))
	}
	for _, a := range m.agents {
		if a.Status != "aguardando" {
			t.Fatalf("agente %s começou com status %q, queria aguardando", a.Name, a.Status)
		}
	}
}

func TestSlashPopupMatchesAndCompletes(t *testing.T) {
	m := newTestModel(t, true)
	m.ta.SetValue("/mo")
	if !m.slashOpen() {
		t.Fatal("slashOpen() deveria ser true para '/mo'")
	}
	matches := m.slashMatches()
	if len(matches) != 1 || matches[0].Name != "/modelo" {
		t.Fatalf("matches para '/mo' = %+v, queria apenas /modelo", matches)
	}
	m.completeSlash()
	if m.ta.Value() != "/modelo" {
		t.Fatalf("completeSlash deixou %q, queria '/modelo'", m.ta.Value())
	}

	m.ta.SetValue("sem barra")
	if m.slashOpen() {
		t.Fatal("slashOpen() não deveria abrir sem prefixo '/'")
	}
	m.ta.SetValue("/com espaço")
	if m.slashOpen() {
		t.Fatal("slashOpen() não deveria abrir com espaço no comando")
	}
}

func TestRunSlashCommands(t *testing.T) {
	m := newTestModel(t, true)

	next, _ := m.runSlash("/limpar")
	m = next.(model)
	if len(m.blocks) != 1 || !strings.Contains(m.blocks[0].Body, "Conversa limpa") {
		t.Fatalf("/limpar deveria zerar os blocos; blocos = %+v", m.blocks)
	}

	before := m.themeIdx
	next, _ = m.runSlash("/tema")
	m = next.(model)
	if m.themeIdx == before {
		t.Fatal("/tema deveria alternar o tema")
	}

	next, _ = m.runSlash("/ajuda")
	m = next.(model)
	last := m.blocks[len(m.blocks)-1]
	if last.Author != "HiveMind" || !strings.Contains(last.Body, "/sair") {
		t.Fatalf("/ajuda deveria listar os comandos; último bloco = %+v", last)
	}

	next, _ = m.runSlash("/comando-inexistente")
	m = next.(model)
	if !strings.Contains(m.blocks[len(m.blocks)-1].Body, "Comando desconhecido") {
		t.Fatal("comando desconhecido deveria avisar o usuário")
	}
}

func TestSubmitWithoutTeamIsRejected(t *testing.T) {
	m := newTestModel(t, false)
	m.ta.SetValue("adicionar um endpoint de healthcheck")
	next, _ := m.submit()
	m = next.(model)
	if m.running {
		t.Fatal("não deveria iniciar execução sem time")
	}
	if !strings.Contains(m.blocks[len(m.blocks)-1].Body, "Crie um time antes de enviar") {
		t.Fatalf("deveria pedir criação do time; blocos = %+v", m.blocks)
	}
}

func TestSubmitSlashAddsHistory(t *testing.T) {
	m := newTestModel(t, true)
	m.ta.SetValue("/tema")
	next, _ := m.submit()
	m = next.(model)
	if len(m.history) != 1 || m.history[0] != "/tema" {
		t.Fatalf("histórico = %v, queria ['/tema']", m.history)
	}
}

func TestOnStreamLifecycle(t *testing.T) {
	m := newTestModel(t, true)
	m.running = true
	m.events = make(chan streamed, 8)

	next, _ := m.onStream(streamed{Event: orchestrator.Event{Kind: "stage", Stage: "planejando"}})
	m = next.(model)
	if m.status != "planejando" {
		t.Fatalf("status = %q, queria 'planejando'", m.status)
	}

	next, _ = m.onStream(streamed{Event: orchestrator.Event{Kind: "usage", Author: "dev_1", Tokens: 120}})
	m = next.(model)
	if m.tokens != 120 {
		t.Fatalf("tokens = %d, queria 120", m.tokens)
	}

	next, _ = m.onStream(streamed{Event: orchestrator.Event{
		Kind:     "tool_call",
		Author:   "dev_1",
		ToolName: "read_file",
	}})
	m = next.(model)
	dev := m.findAgent(m.agentKeyFor("dev_1", ""))
	if dev == nil {
		t.Fatal("dev não encontrado nos agentes")
	}
	if !strings.HasPrefix(dev.Status, "usando") {
		t.Fatalf("status do dev = %q, queria 'usando read_file'", dev.Status)
	}

	next, _ = m.onStream(streamed{Event: orchestrator.Event{
		Kind:    "text",
		Author:  "dev_1",
		Text:    "primeira parte ",
		Partial: true,
	}})
	m = next.(model)
	blk := m.blocks[len(m.blocks)-1]
	if blk.Kind != "agent" || !strings.Contains(blk.Body, "primeira parte") {
		t.Fatalf("bloco parcial = %+v", blk)
	}

	next, _ = m.onStream(streamed{Event: orchestrator.Event{
		Kind:   "text",
		Author: "dev_1",
		Text:   "primeira parte completa",
	}})
	m = next.(model)
	blk = m.blocks[len(m.blocks)-1]
	if !strings.Contains(blk.Body, "primeira parte completa") {
		t.Fatalf("texto final deveria substituir o parcial; bloco = %+v", blk)
	}

	next, _ = m.onStream(streamed{Done: true, SessionID: "abc123"})
	m = next.(model)
	if m.running {
		t.Fatal("execução deveria ter terminado")
	}
	if m.sessionID != "abc123" || !m.persistedSession {
		t.Fatalf("sessão = %q persisted=%v, queria 'abc123' true", m.sessionID, m.persistedSession)
	}
	if m.status != "pronto" {
		t.Fatalf("status final = %q, queria 'pronto'", m.status)
	}
}

func TestOnStreamApprovalGate(t *testing.T) {
	m := newTestModel(t, true)
	reply := make(chan bool, 1)
	next, _ := m.onStream(streamed{ApprovalPrompt: "mesclar em main?", ApprovalReply: reply})
	m = next.(model)
	if m.modal != modalApprove {
		t.Fatalf("modal = %v, queria modalApprove", m.modal)
	}
	if m.pending != "mesclar em main?" {
		t.Fatalf("pending = %q", m.pending)
	}

	// aprovar via tecla 's' devolve true ao orquestrador
	next, _ = m.onModalKey("s")
	m = next.(model)
	if m.modal != modalNone {
		t.Fatalf("modal = %v, queria modalNone após autorizar", m.modal)
	}
	select {
	case ok := <-reply:
		if !ok {
			t.Fatal("autorização deveria enviar true")
		}
	default:
		t.Fatal("autorização deveria responder ao orquestrador")
	}
}

func TestOnStreamDenialGate(t *testing.T) {
	m := newTestModel(t, true)
	reply := make(chan bool, 1)
	next, _ := m.onStream(streamed{ApprovalPrompt: "mesclar em main?", ApprovalReply: reply})
	m = next.(model)
	next, _ = m.onModalKey("n")
	m = next.(model)
	select {
	case ok := <-reply:
		if ok {
			t.Fatal("negação deveria enviar false")
		}
	default:
		t.Fatal("negação deveria responder ao orquestrador")
	}
}

func TestOnStreamErrorMarksAgents(t *testing.T) {
	m := newTestModel(t, true)
	m.running = true
	m.agents[0].Status = "pensando"
	next, _ := m.onStream(streamed{Done: true, Err: errors.New("gemini 503")})
	m = next.(model)
	if m.status != "erro" {
		t.Fatalf("status = %q, queria 'erro'", m.status)
	}
	if m.running {
		t.Fatal("execução deveria parar no erro")
	}
	if !strings.Contains(m.blocks[len(m.blocks)-1].Body, "Erro na execução") {
		t.Fatalf("deveria registrar o erro; blocos = %+v", m.blocks)
	}
	if m.agents[0].Status != "erro" {
		t.Fatalf("agente em execução deveria marcar erro; status = %q", m.agents[0].Status)
	}
}

func TestOnStreamCancelStopsQuietly(t *testing.T) {
	m := newTestModel(t, true)
	m.running = true
	next, _ := m.onStream(streamed{Done: true, Err: contextCanceled()})
	m = next.(model)
	if m.status != "cancelado" {
		t.Fatalf("status = %q, queria 'cancelado'", m.status)
	}
	if m.running {
		t.Fatal("execução cancelada deveria parar")
	}
}

func TestOnStreamCompletionMarksAgentsDone(t *testing.T) {
	m := newTestModel(t, true)
	m.running = true
	m.agents[0].Status = "usando run_shell"
	next, _ := m.onStream(streamed{Done: true})
	m = next.(model)
	if m.agents[0].Status != "concluído" {
		t.Fatalf("status = %q, queria 'concluído'", m.agents[0].Status)
	}
}

func TestOnKeyEscCancelsRunningExecution(t *testing.T) {
	m := newTestModel(t, true)
	m.running = true
	ctx, cancel := context.WithCancel(context.Background())
	m.runCtx = ctx
	m.cancel = cancel
	next, _ := m.onKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)
	if m.running {
		t.Fatal("esc deveria cancelar a execução")
	}
	if m.status != "cancelado" {
		t.Fatalf("status = %q, queria 'cancelado'", m.status)
	}
	select {
	case <-m.runCtx.Done():
	default:
		t.Fatal("contexto da execução deveria estar cancelado")
	}
}

func TestAgentKeyFor(t *testing.T) {
	m := newTestModel(t, true)
	if got := m.agentKeyFor("dev_1", ""); got != "dev_1" {
		t.Fatalf("agentKeyFor('dev_1') = %q, queria 'dev_1'", got)
	}
	if got := m.agentKeyFor("dev_1_2", ""); got != "dev_1" {
		t.Fatalf("agentKeyFor('dev_1_2') = %q, queria 'dev_1' por prefixo", got)
	}
	if got := m.agentKeyFor("qa", ""); got != "qa" {
		t.Fatalf("agentKeyFor('qa') = %q, queria 'qa'", got)
	}
}

func TestRoleTitles(t *testing.T) {
	cases := []struct {
		role string
		name string
		want string
	}{
		{team.RoleProjectManager, "pm", "Gerente"},
		{team.RoleTechLead, "tech_lead", "Líder"},
		{team.RoleArchitect, "architect", "Arquiteto"},
		{team.RoleDeveloper, "dev_1", "Dev 1"},
		{team.RoleQA, "qa", "QA"},
	}
	for _, tc := range cases {
		if got := roleTitle(tc.role, tc.name); got != tc.want {
			t.Fatalf("roleTitle(%q, %q) = %q, queria %q", tc.role, tc.name, got, tc.want)
		}
	}
}

func TestShortModel(t *testing.T) {
	cases := map[string]string{
		"agy:gemini-3.8-flash-medium:high": "gemini-3.8-flash-medium",
		"agy:claude-sonnet-4-5":             "claude-sonnet-4-5",
		"agy":                               "agy",
		"":                                  "agy",
	}
	for spec, want := range cases {
		if got := shortModel(spec); got != want {
			t.Fatalf("shortModel(%q) = %q, queria %q", spec, got, want)
		}
	}
}

func TestIsCancel(t *testing.T) {
	if !isCancel(contextCanceled()) {
		t.Fatal("context.Canceled deveria ser reconhecido como cancelamento")
	}
	if isCancel(errors.New("conexão recusada")) {
		t.Fatal("erro comum não deveria ser tratado como cancelamento")
	}
}

func TestTrimWidthAndFit(t *testing.T) {
	if got := trimWidth("hivemind", 4); got != "hive" {
		t.Fatalf("trimWidth = %q, queria 'hive'", got)
	}
	if got := trimWidth("abc", 10); got != "abc" {
		t.Fatalf("trimWidth curto = %q, queria 'abc'", got)
	}
	lines := strings.Split(fit("a\nb\nc", 2, 2), "\n")
	if len(lines) != 2 {
		t.Fatalf("fit deveria cortar em 2 linhas; linhas = %d", len(lines))
	}
}

func TestAppendDeltaByTitle(t *testing.T) {
	m := newTestModel(t, true)
	m.appendDeltaByTitle("Chefe", "olá ")
	m.appendDeltaByTitle("Chefe", "mundo")
	blk := m.blocks[len(m.blocks)-1]
	if blk.Author != "Chefe" || blk.Body != "olá mundo" {
		t.Fatalf("deltas concatenados errado: %+v", blk)
	}
	m.appendDeltaByTitle("QA", "ok")
	if m.blocks[len(m.blocks)-1].Author != "QA" {
		t.Fatal("novo autor deveria abrir bloco novo")
	}
}

func contextCanceled() error {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx.Err()
}
