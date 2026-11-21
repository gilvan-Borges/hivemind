# Spec 01 — Modularização do Orquestrador, Ferramentas e Broker do Resident

> **Status:** Em Progresso (Onda 3 parcial: `W3-02` e `W3-03` implementados — split de `internal/tools/`, `internal/resident/` e gate estruturado de `qa_approve`; `W3-01` split de `orchestrator.go` pendente)
> **Alvos:** `internal/orchestrator/`, `internal/tools/`, `internal/resident/`

---

## 1. Diagnóstico do Estado Atual

### O que foi observado no código
- **`internal/orchestrator/orchestrator.go` (3.114 linhas):**
  - `Runner` (`orchestrator.go:85-150`) acumula 35+ campos misturando dependências estáticas (`Git`, `GitHub`, `Jira`, `Sessions`, `MCP`), estado mutável por turno (`deliveryRoot`, `devSlices`, `qaLastStatus`, `qaStallCount`, `turnInputTokens`, `devBudgets`, `handoffTo`, `handoffEdges`, `lastWorkingRoute`, `nestedWorktrees`) e callbacks de interface (`OnEvent`).
  - `Runner.runTriageChief` (`orchestrator.go:2416-3096`, 681 linhas) acopla hidratação de PR/Jira, 3 chamadas distintas a `tools.ForRole`, closures inline `sdd_docs_ready` e `delegate_to_squad`, ~140 linhas de prompts em português hardcoded, catálogo de skills, callbacks de guarda e seu próprio loop `triageRunner.Run`.
  - `Runner.Run` (`285-612`, 328 linhas) e `Runner.finish` (`1565-1767`) misturam fetching do Jira, despacho de modos, lifecycle de worktree, execução ADK, guards de orçamento, criação de PR e comentários Jira.
- **`internal/tools/tools.go` (1.695 linhas):** concentra no mesmo arquivo a política de papéis (`ForRole`, `66-271`), operações de filesystem, shell sandboxed/unsandboxed, Git, 11 tools de GitHub, 6 de Jira e tools de QA.
- **`internal/resident/broker.go` (1.028 linhas):** um único `Broker` mistura middleware transversal (autorização por tier, aprovação humana, auditoria `fsync`, redação, fencing) com os 19 handlers concretos (`handleReadFile`..`handleHandoff`, `488-1013`).
- **Efeitos colaterais e descarte de erros:**
  - `Runner.teamMemorySnippet()` (`orchestrator.go:1908-1933`) cria `.hivemind/MEMORY.md` no workspace durante montagem read-only de prompt.
  - Múltiplas chamadas a `sessionx.UpdateState`, `State().Set` e `CompactOldToolOutputs` (`orchestrator.go:441, 467, 508, 510, 595, 1530-1531, 2717-2726, 3081, 3084`) descartam erros (`_ =`).
  - `Runner.recordQAStall()` (`orchestrator.go:1270`) usa `context.Background()` com timeout de 5s ignorando o cancelamento do `ctx` ativo.
  - `broker.go:258, 320, 386` ignoram erros de `Audit.RecordOutcome` e `resident.go:369` ignora erro de `mem.AppendSessionTurn`.

---

## 2. Comparação: HiveMind Atual vs Engenharia Google / Anthropic

| Dimensão | HiveMind Atual | Padrão Google (ADK) / Anthropic |
|---|---|---|
| **Estado do Runner** | Struct única `Runner` mutável compartilhada entre triagem, pipeline e fecho | Separação estrita entre `RunnerConfig` (dependências imutáveis) e `TurnExecutionState` (escopo por execução/turno) |
| **Pipeline ADK** | Montagem inline com closures e prompts literais gigantes em `orchestrator.go` | Fábrica declarativa de grafos (`triage.go`, `pipeline.go`, `delivery.go`) e catálogo de prompts desacoplado/testável |
| **Registro de Tools** | Switch gigante em `tools.go` e `broker.go` | Registry por domínio (`fs`, `shell`, `git`, `github`, `jira`, `qa`) + cadeia explícita de middlewares (`Auth -> Audit -> Sandbox -> Execute`) |
| **Persistência & Side-effects** | Leituras criam arquivos em disco e erros de estado são engolidos | Leitura 100% pura; falhas de persistência críticas retornam erro ou emitem evento estruturado de degradação |

---

## 3. Arquitetura Alvo

### 3.1 Decomposição de `internal/orchestrator`
1. `runner.go`: apenas interface pública `Runner`, `Request`, `Result`, e criação de `turnContext` isolado por chamada (`Run(ctx, req)` deixa de mutar dezenas de campos globais em `r`).
2. `triage.go`: orquestração dos 7 modos (`auto`, `direct`, `plan`, `plan:sdd`, `plan:sdd-jira`, `squad`, `auto-review`), builders puros de prompt (`BuildTriagePrompt(input)`) testáveis sem instanciar LLM, e definição isolada das tools de delegação (`delegate_to_squad`, `sdd_docs_ready`).
3. `pipeline.go`: construção da topologia ADK (`sequentialagent`, `parallelagent`, `loopagent`) e guards de orçamento/stall (`checkDevGuard`, `recordQAStall(ctx)`).
4. `delivery.go`: `prepareDevs`, consolidação, `finish` (commit/push/PR raiz + sub-repositórios + cleanup + comentário Jira).

### 3.2 Particionamento de `internal/tools` e `internal/resident`
1. Dividir `internal/tools/tools.go` por responsabilidade preservando a API pública `ForRole`: `policy.go` (`ForRole`), `fs.go` (`read_file`, `write_file`, `edit_file`, `list_dir`), `shell.go`, `git.go`, `github.go`, `jira.go`, `qa.go`.
2. Extrair os 19 handlers de `internal/resident/broker.go` para um sub-registro modular (`handlers_fs.go`, `handlers_net.go`, `handlers_cron.go`, `handlers_subagent.go`), mantendo `Broker` focado exclusivamente em política, aprovação humana, auditoria pré-efeito e redação/fencing.

### 3.3 Higiene de Erros e Pureza de Leitura
1. Remover criação implícita de `.hivemind/MEMORY.md` de `teamMemorySnippet()` — leitura ausente retorna `""` sem tocar no disco; criação só ocorre via comando explícito da IDE/CLI ou tool de escrita de memória.
2. Centralizar helpers `setStateChecked(ctx/sess, k, v)` e propagar erro/evento quando `Audit.RecordOutcome` ou persistência de sessão falhar.
3. Passar o `ctx` do turno para `recordQAStall(ctx)` com `context.WithTimeout(ctx, 5*time.Second)`.

---

## 4. Plano de Migração Incremental
1. **Fase 1:** Isolar estado mutável por turno em `turnSessionState` dentro de `orchestrator` e corrigir `recordQAStall(ctx)` + `teamMemorySnippet()` + verificação de erros de estado/auditoria.
2. **Fase 2:** Extrair `triage.go`, `pipeline.go` e `delivery.go` mantendo 100% dos testes de `internal/orchestrator` passando sem alteração de contrato.
3. **Fase 3:** Quebrar `internal/tools/tools.go` e `internal/resident/broker.go` em módulos por domínio com testes unitários isolados por handler/builder.

---

## 5. Critérios de Aceite
- Nenhum arquivo em `internal/orchestrator/`, `internal/tools/` ou `internal/resident/` acima de 600 linhas.
- `Runner.Run` suporta execução limpa sem vazar estado residual entre turnos sequenciais e sem criar arquivos durante leitura de memória.
- `go test -race -count=1 ./internal/orchestrator/... ./internal/tools/... ./internal/resident/...` passando 100%.
