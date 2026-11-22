# Plano Técnico de Design — 011-multi-cli-worker-mode

> Referência: `spec.md` · Constituição: `.specify/memory/constitution.md` · Spec Técnica: `docs/specs/13-multi-cli-worker-adapters-and-runtime-selector.md`

## 1. Arquivos Impactados
- **Backend / Core (`HiveMind`):**
  - `internal/team/team.go` (`WorkerNative`, `WorkerAgy`, `WorkerClaude`, `WorkerCodex`, `WorkerCursor`, `WorkerOpenCode`, `NormalizeWorker`)
  - `internal/workerx/cli_runner.go` (novo helper compartilhado de CallbackServer + briefing + stream + timeout)
  - `internal/workerx/agy_adapter.go`, `claude_adapter.go`, `codex_adapter.go`, `opencode_adapter.go`, `detect.go`
  - `internal/workerx/contract.go` (`NewDefaultRegistry` com os 5 adaptadores)
  - `internal/orchestrator/worker_dispatch.go` (`externalAdapterFor` usando `team.NormalizeWorker` + soma de `doneEv.Usage` no ledger do `Runner`)
  - `ide/app_team.go` (novo método Wails `ListAvailableWorkers()`)
- **Frontend (`HiveMind IDE`):**
  - `ide/frontend/src/types.ts` (tipo `WorkerCLIStatus` e binding `ListAvailableWorkers`)
  - `ide/frontend/src/multiagent-board.ts` (seletor de `Modo / Worker` no card de cada `Desenvolvedor` com troca imediata)
  - `ide/frontend/src/team.ts` (coluna/campo `Worker CLI` na edição de agentes da equipe)
  - `ide/frontend/src/styles.css` (reuso dos tokens `--surface-input`, `--accent`, `--status-ok`, `--status-warn`)

## 2. Mapeamento de Tokens e Compatibilidade
- Nenhum token `#hex` novo é necessário: o seletor reutiliza `.ma-select` (`--surface-input`, `--border-subtle`, `:focus-visible` com `--border-focus`) e `.ma-badge` (`--accent-subtle`, `--accent`) já validados no **Quadro de Controle Multiagente**.
- Quando um `Desenvolvedor` estiver com `worker != ""` (ex.: `worker: "claude"` ou `worker: "agy"`), o seletor de provedor/modelo continua visível (permitindo escolher o modelo passado via `--model` ao CLI externo ou deixar `auto` para o padrão do CLI).

## 3. Estratégia de Verificação
1. `go vet ./...` e `go test -count=1 ./internal/workerx/... ./internal/team/... ./internal/orchestrator/...`
2. `node ide/frontend/scripts/check-design-tokens.mjs`
3. `cd ide/frontend && npm test -- --run && npm run build`
4. `hivemind-ide.exe --smoke-check`
