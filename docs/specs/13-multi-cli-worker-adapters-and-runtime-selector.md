# Spec 13 — Adaptadores Multi-CLI de Worker Externo (`agy`, `claude`, `codex`, `cursor`, `opencode`) e Seletor de Modo de Execução

> **Status:** Especificada (Pronta para Implementação)
> **Dependências:** Spec 11 (`internal/workerx/`, `CursorWorkerAdapter`, `CallbackServer`, `PostHocGuard`), Spec 04 (`pathsec`), Spec 06 (`jev` ledger / `UsageReport`)
> **Alvos:** `internal/workerx/`, `internal/team/`, `internal/orchestrator/worker_dispatch.go`, `internal/modelx/`, `ide/app_team.go`, `ide/frontend/src/`, `HiveMind-App/`

---

## 1. Contexto e Objetivo

Hoje o HiveMind opera os agentes de desenvolvimento (`RoleDeveloper`) em dois modos distintos, mas sem uma escolha explícita e unificada para todos os CLIs de mercado:

1. **Motor Nativo HiveMind (`worker: ""` ou `"native"`):**
   - O loop de agente (ADK Go), o catálogo de ferramentas (`read_file`, `edit_file`, `list_tree`, `web_fetch`, `run_shell`, `ask_chief`, `spawn_subagent`), a compactação de contexto (`contextx`) e os guardas (`context_guard`, `dev_guard`) são executados **100% pelo HiveMind**.
   - O provedor (`agy`, `opencode`, `gemini`, `anthropic`, `openai`, `openrouter`, `jev`) atua exclusivamente como **motor de LLM (inferência)**. No caso do `agy`, o processo roda em diretório temporário vazio (`internal/modelx/agy.go`) apenas para responder JSON de decisão.

2. **Modo Worker CLI Externo — HiveMind como Capa Governada (`worker: "agy" | "claude" | "codex" | "cursor" | "opencode"`):**
   - O usuário quer aproveitar o **agente completo** de um CLI externo (com as próprias ferramentas, subagentes internos, indexação e loop nativo do **Antigravity CLI `agy`**, **Claude Code `claude`**, **OpenAI Codex `codex`**, **Cursor CLI `agent`** ou **OpenCode `opencode`**), usando o **HiveMind como camada de orquestração e governança**:
     - **Chefe (Triagem & Arquitetura):** analisa o pedido/PR/Jira, divide em fatias (`slices`) com `owned_paths` e prepara as worktrees Git isoladas.
     - **Despacho Governado (`workerx`):** o CLI escolhido roda **diretamente dentro da worktree isolada** da sua fatia, conectado ao `CallbackServer` local (`/heartbeat`, `/ask` bloqueante com aprovação humana na IDE/Mobile/Orb e `/done` idempotente com `UsageReport`).
     - **Auditoria Pós-Hoc (`PostHocGuard`):** ao encerrar o processo do CLI externo, o HiveMind audita `git status`/`git diff` na worktree, reverte automaticamente alterações fora de `owned_paths` ou em caminhos sensíveis (`pathsec.IsSensitivePath`) e bloqueia commits/pushes não autorizados.
     - **QA & Fecho:** o código aprovado pelo `PostHocGuard` segue normalmente para integração, revisão do **QA** e entrega/PR pelo **Chefe (Fecho)**.

---

## 2. Semântica de Configuração (`team.Agent.Worker`)

Em `.hivemind/team.yaml` (`internal/team/team.go`), o campo `worker` de `team.Agent` passa a ter semântica explícita e determinística:

| Valor de `agent.worker` | Modo de Execução | Comportamento |
| :--- | :--- | :--- |
| `""` ou `"native"` (padrão) | **Agente Nativo HiveMind** | Usa o loop ADK e as ferramentas nativas do HiveMind; `provider`/`model` (ou `jev`) fornecem apenas a inferência LLM. *(Compatibilidade retroativa: se `worker == ""` e `provider == "cursor"`, mantém fallback para `"cursor"` ou migra na normalização).* |
| `"agy"` | **Worker CLI — Antigravity (`agy`)** | Executa o binário `agy` diretamente no `WorktreeDir` como agente autônomo (com suas tools e subagentes nativos), sob o `Worker Contract`. |
| `"claude"` | **Worker CLI — Claude Code (`claude`)** | Executa `claude` (`--print` / `--output-format stream-json`) diretamente no `WorktreeDir` sob o `Worker Contract`. |
| `"codex"` | **Worker CLI — OpenAI Codex (`codex`)** | Executa `codex` (`exec` / modo não-interativo no `WorktreeDir`) sob o `Worker Contract`. |
| `"cursor"` | **Worker CLI — Cursor Agent (`agent`)** | Executa o `CursorWorkerAdapter` já existente no `WorktreeDir` sob o `Worker Contract`. |
| `"opencode"` | **Worker CLI — OpenCode (`opencode`)** | Executa `opencode run --format json` diretamente no `WorktreeDir` sob o `Worker Contract`. |

### 2.1 Normalização (`team.NormalizeWorker`)
Adicionar em `internal/team/team.go`:
```go
const (
	WorkerNative   = ""
	WorkerAgy      = "agy"
	WorkerClaude   = "claude"
	WorkerCodex    = "codex"
	WorkerCursor   = "cursor"
	WorkerOpenCode = "opencode"
)

func NormalizeWorker(worker, provider string) string
```
- Valores reconhecidos: `""` (ou `"native"`, `"hivemind"` -> `""`), `"agy"`, `"claude"`, `"codex"`, `"cursor"`, `"opencode"`.
- Retrocompatibilidade: quando `worker == ""` e `strings.EqualFold(provider, "cursor")`, retorna `"cursor"`. Para todos os demais provedores (`agy`, `opencode`, `jev`, etc.), `worker == ""` permanece **Agente Nativo HiveMind** até que o usuário selecione explicitamente um Worker CLI.

---

## 3. Arquitetura dos Adaptadores em `internal/workerx/`

Todos os adaptadores implementam `workerx.ExternalWorkerAdapter`:

```go
type ExternalWorkerAdapter interface {
	Provider() string
	Execute(ctx context.Context, req DispatchRequest, cb Callbacks) (DoneEvent, error)
}
```

### 3.1 Ciclo de Vida Comum (`baseCLIWorker`)
Para evitar duplicação entre `cursor`, `agy`, `claude`, `codex` e `opencode`, extrair o pipeline comum em `internal/workerx/cli_runner.go`:

1. **Validação de Worktree:** recusa execução se `req.WorktreeDir` estiver vazio.
2. **Servidor de Callback Loopback (`127.0.0.1:0`):**
   - Inicia `NewCallbackServer(reg, cb)` com token efêmero por `DispatchID`.
   - Expõe endpoints `/heartbeat`, `/ask` (HITL bloqueante) e `/done` (idempotente) + variáveis de ambiente `HIVEMIND_CALLBACK_URL`, `HIVEMIND_DISPATCH_ID`, `HIVEMIND_TASK_ID`.
3. **Materialização do Briefing (`.hivemind/worker-task.md`):**
   - Grava o preâmbulo do `Worker Contract` + `HandoffBrief` (`Objective`, `ContextBrief`, `Slice`, `OwnedPaths`, instruções de `curl` / marcadores inline `[HIVEMIND_HEARTBEAT]`, `[HIVEMIND_DONE]`).
   - Sempre remove `.hivemind/worker-task.md` (e `.hivemind/cursor-task.md`) no `defer` antes do `PostHocGuard` para nunca sujar a worktree.
4. **Higienização de Ambiente (`shellx.SanitizeEnv` + Cofre Isolado):**
   - Nunca vaza segredos do cofre do Residente; injeta apenas a credencial/perfil pertencente ao provedor do worker (ex.: perfil OAuth isolado do `agy` via `modelx.WithAgyProfileLock`, `ANTHROPIC_API_KEY` para `claude`, `OPENAI_API_KEY` para `codex`, `CURSOR_API_KEY` para `cursor`).
5. **Política de Permissão (`req.PermissionMode`):**
   - Flags de auto-aprovação irrestrita do CLI externo só são passadas quando `req.PermissionMode == "full_access"`; em `"governed"` ou `"auto_edit"`, operações destrutivas fora de edição de arquivos exigem `/ask` via `CallbackServer`.
6. **Stream de Eventos & Telemetria (`UsageReport`):**
   - Faz parse em tempo real do stream stdout (NDJSON / `stream-json` / texto) para emitir:
     - `cb.OnTool(toolName, toolPath)` -> aparece ao vivo na timeline da IDE e do `HiveMind-App`;
     - `cb.OnHeartbeat(...)` -> mantém o status vivo na UI;
     - `UsageReport` (`InputTokens`, `OutputTokens`, `CacheReadTokens`, `CostMicroUSD`, `Model`) -> emitido como evento `worker_usage` e somado ao ledger de custo da sessão (`Runner.recordWorkerUsage`).
7. **Auditoria Pós-Hoc (`RunPostHocGuard`):**
   -Executada em `internal/orchestrator/worker_dispatch.go` logo após `adapter.Execute`:
   - Reverte qualquer arquivo criado/modificado fora de `dev.OwnedPaths` ou bloqueado por `pathsec.IsSensitivePath`.

---

### 3.2 Especificação de Cada Adaptador CLI

| Adaptador | Binário (`LookPath`) | Invocação no `WorktreeDir` | Flags por `PermissionMode` | Parse de Stream & Usage |
| :--- | :--- | :--- | :--- | :--- |
| **`AgyWorkerAdapter`** (`agy_adapter.go`) | `modelx.LookAgy()` (`agy`) | `agy -p "Siga .hivemind/worker-task.md" --output-format stream-json` *(com `cmd.Dir = req.WorktreeDir` e suporte a `ProfileID` do cofre OAuth)* | `full_access`: `--trust` / auto-approve; `governed`: padrão com `/ask` | Reutiliza parser de stream de `modelx/agy.go` (`tool_call`, `result`, `usage`) |
| **`ClaudeWorkerAdapter`** (`claude_adapter.go`) | `LookClaude()` (`claude`) | `claude -p "Siga .hivemind/worker-task.md" --output-format stream-json --verbose` (`--model <req.Model>` opcional) | `full_access`: `--dangerously-skip-permissions`; `auto_edit`: `--permission-mode acceptEdits`; `governed`: `--permission-mode default` | Eventos NDJSON `assistant` (`tool_use`), `result` (`usage.input_tokens`, `usage.output_tokens`, `usage.cache_read_input_tokens`, `total_cost_usd`) |
| **`CodexWorkerAdapter`** (`codex_adapter.go`) | `LookCodex()` (`codex`) | `codex exec --json -C <req.WorktreeDir> "Siga .hivemind/worker-task.md"` (`-m <req.Model>` opcional) | `full_access`: `--dangerously-bypass-approvals-and-sandbox`; `auto_edit`: `--full-auto`; `governed`: `--ask-for-approval untrusted` | Eventos JSONL `item.completed` (`command_execution`, `file_change`), `turn.completed` (`usage.input_tokens`, `usage.cached_input_tokens`, `usage.output_tokens`) |
| **`CursorWorkerAdapter`** (`cursor_adapter.go`) | `modelx.LookCursor()` (`agent` / `cursor-agent`) | Já implementado (`--print --output-format stream-json`) | `full_access`: `--trust --force` | Já implementado (`modelx.ParseCursorStreamLine` + `UsageReport`) |
| **`OpenCodeWorkerAdapter`** (`opencode_adapter.go`) | `modelx.LookOpenCode()` (`opencode`) | `opencode run --format json -m <req.Model> "Siga .hivemind/worker-task.md"` (`cmd.Dir = req.WorktreeDir`) | Herda `PermissionMode` via env/config efêmera no `WorktreeDir` | Reutiliza parser JSON de `modelx/opencode.go` (`step_finish`, `tool`, `tokens`, `cost`) |

### 3.3 Registro Padrão (`workerx.NewDefaultRegistry`)
`workerx.NewDefaultRegistry()` passa a registrar os 5 adaptadores:
- `"agy"` -> `NewAgyWorkerAdapter()`
- `"claude"` -> `NewClaudeWorkerAdapter()`
- `"codex"` -> `NewCodexWorkerAdapter()`
- `"cursor"` -> `NewCursorWorkerAdapter()`
- `"opencode"` -> `NewOpenCodeWorkerAdapter()`

E em `internal/orchestrator/worker_dispatch.go`, `externalAdapterFor(a team.Agent)` consulta:
```go
workerName := team.NormalizeWorker(a.Worker, a.Provider)
if workerName == "" {
	return nil, false
}
reg := r.ExternalWorkers
if reg == nil {
	reg = workerx.NewDefaultRegistry()
}
return reg.Get(workerName)
```

---

## 4. Descoberta de CLIs Instalados (`WorkerStatus`)

Para que a IDE e o `hivemind doctor` mostrem quais Workers CLI estão instalados e prontos na máquina do usuário:

- Adicionar `workerx.DetectAvailableWorkers(ctx context.Context) []WorkerCLIStatus`:
  ```go
  type WorkerCLIStatus struct {
      ID       string `json:"id"`       // "native" | "agy" | "claude" | "codex" | "cursor" | "opencode"
      Label    string `json:"label"`    // Ex.: "Antigravity CLI (agy)"
      Binary   string `json:"binary"`   // Caminho resolvido ou nome do binário
      Installed bool  `json:"installed"`
      LoggedIn  bool  `json:"loggedIn"`
      Detail   string `json:"detail,omitempty"`
  }
  ```
- Expor na IDE via binding Wails `ListAvailableWorkers() []workerx.WorkerCLIStatus` em `ide/app_team.go`.

---

## 5. Experiência na UI (IDE & Mobile)

Seguir rigorosamente a Spec SDD [`.specify/specs/011-multi-cli-worker-mode/spec.md`](../../.specify/specs/011-multi-cli-worker-mode/spec.md) (Artigos `D1–D9` da Constituição):

1. **Quadro de Controle Multiagente (`ide/frontend/src/multiagent-board.ts`) e Modal Configurar Equipe (`ide/frontend/src/team.ts`):**
   - Para cada agente `Desenvolvedor` (`role == "developer"`), exibir o seletor **"Motor de Execução (Runtime / Worker)"**:
     - `HiveMind Nativo (Tools + Jev/LLM)` -> `worker: ""`
     - `Antigravity Agent CLI (agy)` -> `worker: "agy"`
     - `Claude Code CLI (claude)` -> `worker: "claude"`
     - `OpenAI Codex CLI (codex)` -> `worker: "codex"`
     - `Cursor Agent CLI (agent)` -> `worker: "cursor"`
     - `OpenCode CLI (opencode)` -> `worker: "opencode"`
   - Indicar com badge de status (`instalado` / `não encontrado no PATH`) a disponibilidade de cada CLI retornada por `ListAvailableWorkers()`.
   - Quando um `worker` externo está selecionado, o badge do card do Dev no Quadro Multiagente exibe `Worker CLI · <nome>` em `BrandGold` (`--accent`).

2. **Contabilização no Ledger de Custo (`Runner.recordWorkerUsage`):**
   - Ao receber `doneEv.Usage` em `RunExternalWorkerDev`, além de emitir o evento `worker_usage`, somar `InputTokens`, `OutputTokens`, `CacheReadTokens` e `CostMicroUSD` em `r.agentCosts[dev.ADK]`, `r.roleCosts["developer"]` e `r.turnCostMicroUSD`, para que o Quadro Multiagente e o `HiveMind-App` mostrem os tokens e custo reais do worker CLI!

---

## 6. Critérios de Aceite

- [ ] `team.NormalizeWorker` distingue claramente `worker: ""` (**Agente Nativo HiveMind**) de `worker: "agy" | "claude" | "codex" | "cursor" | "opencode"` (**Worker CLI Externo**), mantendo retrocompatibilidade para `provider: "cursor"`.
- [ ] `workerx.NewDefaultRegistry()` registra `agy`, `claude`, `codex`, `cursor` e `opencode`, todos compartilhando `CallbackServer` (`/heartbeat`, `/ask`, `/done`), limpeza garantida de `.hivemind/worker-task.md` e `PostHocGuard` (`owned_paths` + `pathsec`).
- [ ] `doneEv.Usage` soma tokens e `CostMicroUSD` no ledger de custo do `Runner` (`agentCosts` / `turnCostMicroUSD`).
- [ ] A IDE permite trocar o **Motor de Execução (Worker)** de cada Dev em 1 clique no **Quadro de Controle Multiagente** e no modal **Configurar Equipe**, persistindo em `.hivemind/team.yaml`.
- [ ] Testes unitários cobrindo todos os 5 adaptadores com binários/scripts fake (`internal/workerx/*_test.go`), `go vet ./...`, `go test ./...`, `check-design-tokens.mjs` e `npm test && npm run build` verdes.
