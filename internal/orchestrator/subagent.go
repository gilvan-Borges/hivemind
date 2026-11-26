package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/tools"
)

// Subagentes efêmeros (scouts): qualquer agente do squad pode disparar, no meio
// do próprio turno, um subagente de contexto LIMPO e ferramentas SOMENTE-LEITURA
// para investigar/mapear código e devolver um relatório denso. O pai não
// compartilha transcript com o filho, o filho não edita nada e não spawna
// outros scouts (limite de profundidade garantido por construção: o toolset
// read-only não expõe spawn_subagent).

const (
	// scoutMaxConcurrent limita quantos scouts do mesmo Runner rodam ao mesmo tempo.
	scoutMaxConcurrent = 2
	// scoutTimeout limita a missão inteira de um scout.
	scoutTimeout = 5 * time.Minute
	// scoutReportBytes limita o relatório devolvido ao agente chamador.
	scoutReportBytes = 12 * 1024
)

var scoutSeq atomic.Int64

func (r *Runner) scoutSlots() chan struct{} {
	r.scoutMu.Lock()
	defer r.scoutMu.Unlock()
	if r.scoutSlotsCh == nil {
		r.scoutSlotsCh = make(chan struct{}, scoutMaxConcurrent)
	}
	return r.scoutSlotsCh
}

// reserveScoutSpawn conta um spawn do agente chamador e recusa quando o teto
// por execução (EffectiveMaxSubagentCalls) foi atingido.
func (r *Runner) reserveScoutSpawn(callerADKName string, unlimitedOpt ...bool) error {
	if len(unlimitedOpt) > 0 && unlimitedOpt[0] {
		return nil
	}
	maxSpawns := r.Project.Settings.EffectiveMaxSubagentCalls()
	r.scoutMu.Lock()
	defer r.scoutMu.Unlock()
	if r.scoutSpawns == nil {
		r.scoutSpawns = map[string]int{}
	}
	used := r.scoutSpawns[callerADKName]
	if used >= maxSpawns {
		return fmt.Errorf("limite de %d subagentes por agente por execução atingido (%s); faça a leitura/pesquisa diretamente com read_file/search_text", maxSpawns, callerADKName)
	}
	r.scoutSpawns[callerADKName] = used + 1
	return nil
}

// newSpawnSubagentTool dá a um agente do squad a capacidade de disparar um
// subagente efêmero de contexto limpo (scout) para leitura/pesquisa sem queimar
// as próprias chamadas de orçamento.
func (r *Runner) newSpawnSubagentTool(caller team.Agent, callerADKName, root string, unlimitedOpt ...bool) (tool.Tool, error) {
	unlimited := len(unlimitedOpt) > 0 && unlimitedOpt[0]
	type spawnArgs struct {
		Objective   string `json:"objective" jsonschema:"Objetivo pontual e autossuficiente da missão: o que investigar/ler/mapear e qual pergunta exata deve ser respondida. O subagente não vê esta conversa."`
		Description string `json:"description,omitempty" jsonschema:"Rótulo curto (3-5 palavras) da missão, ex.: mapear endpoints do gateway."`
	}
	type spawnRes struct {
		Report   string `json:"report"`
		LLMCalls int    `json:"llm_calls"`
		Tokens   int64  `json:"tokens"`
	}
	return functiontool.New(functiontool.Config{
		Name:        "spawn_subagent",
		Description: "Spawna um subagente efêmero com contexto LIMPO e ferramentas SOMENTE-LEITURA (read_file, search_text, find_files, list_dir, git_status, git_diff) que investiga o código e devolve um relatório denso com caminhos e linhas exatos. Use para pesquisa paralela sem gastar suas próprias chamadas. O subagente NÃO edita arquivos, não commita e não spawna outros.",
	}, func(tctx agent.Context, args spawnArgs) (spawnRes, error) {
		objective := strings.TrimSpace(args.Objective)
		if objective == "" {
			return spawnRes{}, fmt.Errorf("informe objective com a missão autossuficiente do subagente")
		}
		if err := r.reserveScoutSpawn(callerADKName, unlimited); err != nil {
			return spawnRes{}, err
		}
		slots := r.scoutSlots()
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-tctx.Done():
			return spawnRes{}, fmt.Errorf("execução cancelada antes do subagente iniciar")
		}
		if r.OnEvent != nil {
			label := strx.TruncateRunes(strings.ReplaceAll(objective, "\n", " "), 160, "…")
			r.OnEvent(Event{
				Kind:   "subagent",
				Stage:  "scout",
				Author: callerADKName,
				Label:  "Subagente efêmero · " + callerADKName,
				Text:   label,
			})
		}
		report, calls, tokens, err := r.runScout(tctx, caller, callerADKName, root, objective, args.Description)
		if err != nil {
			return spawnRes{}, err
		}
		return spawnRes{Report: report, LLMCalls: calls, Tokens: tokens}, nil
	})
}

// runScout cria o agente filho com sessão in-memory própria, executa a missão
// e devolve o texto final do scout com a contagem de chamadas/tokens gastos.
// O nome do filho carrega o chamador (scout_<n>_from_<pai>) para o custo cair
// na rota/papel corretos sem que o guard de dev seja afetado.
func (r *Runner) runScout(ctx context.Context, caller team.Agent, callerADKName, root, objective, label string) (string, int, int64, error) {
	maxLLMCalls := r.Project.Settings.EffectiveMaxSubagentLLMCalls()
	ctx, cancel := context.WithTimeout(ctx, scoutTimeout)
	defer cancel()
	ctx = modelx.WithAgentDir(ctx, root)

	llm, err := r.modelFor(ctx, caller)
	if err != nil {
		return "", 0, 0, fmt.Errorf("modelo do subagente: %w", err)
	}
	scoutTools, err := tools.ForReadOnly(root, r.Project.Settings)
	if err != nil {
		return "", 0, 0, err
	}
	childName := fmt.Sprintf("scout_%d_from_%s", scoutSeq.Add(1), strings.ToLower(strings.TrimSpace(callerADKName)))
	child, err := llmagent.New(llmagent.Config{
		Name:                     childName,
		Description:              "Subagente efêmero somente-leitura do HiveMind.",
		Model:                    llm,
		Mode:                     llmagent.ModeChat,
		IncludeContents:          llmagent.IncludeContentsNone,
		DisallowTransferToParent: true,
		DisallowTransferToPeers:  true,
		Tools:                    scoutTools,
		Instruction:              scoutInstruction(maxLLMCalls),
	})
	if err != nil {
		return "", 0, 0, err
	}
	svc := session.InMemoryService()
	created, err := svc.Create(ctx, &session.CreateRequest{AppName: sessionx.AppName, UserID: sessionx.UserID, SessionID: childName})
	if err != nil {
		return "", 0, 0, err
	}
	adkRunner, err := runner.New(runner.Config{AppName: sessionx.AppName, Agent: child, SessionService: svc})
	if err != nil {
		return "", 0, 0, err
	}
	labels := map[string]string{childName: "Subagente (contexto isolado)"}
	var (
		report string
		calls  int
		inTok  int64
	)
	for ev, err := range adkRunner.Run(ctx, sessionx.UserID, created.Session.ID(),
		genai.NewContentFromText(scoutUserMessage(objective, label), genai.RoleUser),
		agent.RunConfig{MaxLLMCalls: maxLLMCalls}) {
		if err != nil {
			return "", calls, inTok, fmt.Errorf("subagente falhou: %w", err)
		}
		if ev == nil {
			continue
		}
		// O uso do filho entra no orçamento do run (recordUsage alimenta os
		// totais, o ledger e o guard MaxTokens) sem contaminar o guard do dev.
		r.recordUsage(ev, labels)
		if ev.Partial || ev.LLMResponse.Content == nil {
			continue
		}
		// Conta apenas respostas do modelo (chamada de tool ou texto), não
		// eventos de tool result, que também chegam com Content preenchido.
		hasModelPart := false
		for _, p := range ev.LLMResponse.Content.Parts {
			if p == nil {
				continue
			}
			if p.FunctionCall != nil || (p.Text != "" && !p.Thought) {
				hasModelPart = true
			}
		}
		if !hasModelPart {
			continue
		}
		calls++
		if u := ev.LLMResponse.UsageMetadata; u != nil {
			inTok += int64(u.PromptTokenCount)
		}
		for _, p := range ev.LLMResponse.Content.Parts {
			if p != nil && p.Text != "" && !p.Thought {
				report = strings.TrimSpace(p.Text)
			}
		}
	}
	if strings.TrimSpace(report) == "" {
		report = "O subagente concluiu sem produzir texto final."
	}
	return strx.TruncateBytes(report, scoutReportBytes, "\n…(relatório do subagente truncado)"), calls, inTok, nil
}

func scoutInstruction(maxLLMCalls int) string {
	return fmt.Sprintf(`Você é um subagente efêmero do HiveMind, criado por um agente do time para uma missão pontual de leitura e análise.

REGRAS DA MISSÃO:
- Contexto LIMPO: você não tem o histórico da conversa que te criou; tudo o que precisa está no objetivo da missão.
- Você é SOMENTE LEITURA: nunca edite ou grave arquivos, nunca execute comandos. Use read_file, search_text, find_files, list_dir, git_status e git_diff.
- Orçamento rígido: no máximo %d chamadas de LLM. Localize o que precisa com 1-2 buscas, leia trechos (start_line/end_line) em vez de arquivos inteiros e conclua.
- NÃO spawne outros subagentes e não peça aprovações: ninguém vai responder.

RELATÓRIO FINAL (sua última mensagem é o que o chamador recebe):
1. Resposta direta e conclusiva ao objetivo.
2. Evidências: caminhos de arquivo e símbolos exatos, com arquivo:linha quando possível.
3. Riscos ou pendências que o chamador precisa conhecer (se houver).
Sem preâmbulo, sem repetir o objetivo, máximo de ~40 linhas.`, maxLLMCalls)
}

func scoutUserMessage(objective, label string) string {
	var b strings.Builder
	b.WriteString("Missão do subagente")
	if l := strings.TrimSpace(label); l != "" {
		fmt.Fprintf(&b, " (%s)", l)
	}
	fmt.Fprintf(&b, ":\n%s", strings.TrimSpace(objective))
	return b.String()
}

// scoutParentFrom devolve o ADK name do agente chamador a partir do nome do
// scout ("scout_12_from_dev_1" -> "dev_1"); false quando não é um scout.
func scoutParentFrom(author string) (string, bool) {
	a := strings.ToLower(strings.TrimSpace(author))
	if !strings.HasPrefix(a, "scout_") {
		return "", false
	}
	_, parent, ok := strings.Cut(a, "_from_")
	return parent, ok
}
