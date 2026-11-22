# Tasks de Implementação & Design — 011-multi-cli-worker-mode

> Referência: `spec.md` + `plan.md` + `docs/specs/13-multi-cli-worker-adapters-and-runtime-selector.md`

## Lista de Tarefas

- [ ] **W001 [Core/Team]** Adicionar constantes `Worker*` e `NormalizeWorker(worker, provider string)` em `internal/team/team.go` com testes unitários.
- [ ] **W002 [Core/WorkerX]** Implementar `cli_runner.go` e os adaptadores `AgyWorkerAdapter` (`agy`), `ClaudeWorkerAdapter` (`claude`), `CodexWorkerAdapter` (`codex`) e `OpenCodeWorkerAdapter` (`opencode`), registrando os 5 em `workerx.NewDefaultRegistry()`.
- [ ] **W003 [Core/Detect]** Implementar `workerx.DetectAvailableWorkers` e expor `ListAvailableWorkers()` no Wails (`ide/app_team.go`).
- [ ] **W004 [Core/Orchestrator]** Atualizar `externalAdapterFor` e contabilizar `doneEv.Usage` no ledger do `Runner` (`agentCosts`, `roleCosts`, `turnCostMicroUSD`) em `internal/orchestrator/worker_dispatch.go`.
- [ ] **D001 [SDD D1–D6 / IDE]** Adicionar seletor **"Modo / Worker de Execução"** (`HiveMind Nativo` vs `agy` / `claude` / `codex` / `cursor` / `opencode`) nos cards de Dev em `ide/frontend/src/multiagent-board.ts` e no modal `ide/frontend/src/team.ts`.
- [ ] **D002 [Verificação]** Validar `go test ./...`, `node ide/frontend/scripts/check-design-tokens.mjs` e `npm test && npm run build`.
