# Plano Vivo de Implementação — HiveMind

> **REGRA DE MANUTENÇÃO CONTÍNUA:** Este documento é a fonte única da verdade sobre o que **já foi entregue** e o que **falta implementar** no HiveMind (`hivemind`) e no `HiveMind-App`.
> **REGRA DE REPOSITÓRIO PÚBLICO (`AGENTS.md` / D9):** Nunca registrar nomes de clientes/empresas privadas, domínios reais, e-mails pessoais reais, usernames de terceiros, segredos ou binários (`.exe`/`.apk`) em código, testes, specs ou commits — apenas os dados de autoria do mantenedor (`Gilvan Borges` / `gilvan-Borges`). Use sempre dados sintéticos (`org-alpha`, `acme`, `PROJ-123`, `app-mobile`, `@reviewer`, `example.com`).
> Sempre que qualquer tarefa abaixo for concluída:
> 1. Marque o checkbox como `[x]` com a data e o comando/teste de verificação.
> 2. Atualize a coluna **Status** na tabela de Inventário de Specs abaixo e no cabeçalho da spec correspondente.

Última revisão geral: **2026-11-21**

---

## 1. Inventário Geral de Todas as Specs e Entregas

### 1.1 Specs 100% Concluídas

| ID / Documento | Escopo | Status Atual | Evidência Principal no Código |
|---|---|---|---|
| [D-001 (.specify)](../../.specify/specs/001-havrus-design-system/spec.md) | Sistema Visual & Paleta Havrus Obsidian & Brand Gold | **Implementado** | `ide/frontend/src/styles.css` (`:root`), `check-design-tokens.mjs`, `docs/design/01-paleta-e-tokens-havrus.md` |
| [D-002 (.specify)](../../.specify/specs/002-icons-dedupe/spec.md) | Deduplicação de ícones Material Symbols | **Implementado** | `ide/frontend/src/icons.ts` |
| [D-002M (.specify)](../../.specify/specs/002-mobile-claude-code-ui/spec.md) | UI Chat-first estilo Claude Code no `HiveMind-App` | **Implementado** | `HiveMind-App` (`MainActivity.kt`, `ChatScreen.kt`) |
| [D-003 (.specify)](../../.specify/specs/003-ai-usage-page/spec.md) | Página central de Uso & Limites + cartões por conta `agy` | **Implementado** | `ide/frontend/src/modals.ts`, `internal/modelx/usage.go` |
| [D-004 (.specify)](../../.specify/specs/004-brand-gold-logo/spec.md) | Destaque Dourado Havrus & Logo/`.ico` HiveMind | **Implementado** | `assets/brand/hivemind-icon.png`, `ide/build/windows/icon.ico`, `styles.css` |
| [D-005A (.specify)](../../.specify/specs/005-companion-mascote/spec.md) | Mascote flutuante & toasts nativos Windows/OS | **Implementado** | `ide/frontend/src/companion/`, `ide/internal/notify/` |
| [D-005B (.specify)](../../.specify/specs/005-equipe-ia-stream/spec.md) | Chip do modelo, stream Cursor corrigido e chat expandido | **Implementado** | `ide/frontend/src/models.ts`, `ide/frontend/src/chat.ts`, `internal/modelx/cursor_stream.go` |
| [D-006 (.specify)](../../.specify/specs/006-scm-git-progress/spec.md) | Progresso Git ao vivo (`--progress`), timeout e cancelamento | **Implementado** | `ide/internal/idecore/gitaction.go`, `ide/frontend/src/git.ts` |
| [D-008 (.specify)](../../.specify/specs/008-equipe-sidebar-layout/spec.md) | Redesign Havrus Dark Islands da barra lateral **Equipe** | **Implementado** | `ide/frontend/src/explorer.ts`, `ide/frontend/src/styles.css` |
| [`SPEC_REVISAO_CONTEXTO.md`](../SPEC_REVISAO_CONTEXTO.md) | Ferramentas line-range e compactação in-turn v1 | **Implementado** | `internal/tools/tools.go`, `internal/orchestrator/context_guard.go` |
| [`SPEC_AUTO_REVIEW_FLUXO.md`](../SPEC_AUTO_REVIEW_FLUXO.md) | Revisão automática de PRs sem merge em worktree efêmera | **Implementado** | `ide/internal/idecore/autoreview.go`, `internal/orchestrator/orchestrator.go` |
| [`SPEC_RELAY.md`](../SPEC_RELAY.md) | Relay E2EE `hivemind-relay` + `relaywire` | **Base Implementada** | `cmd/hivemind-relay/`, `internal/relay/`, `relaywire/` *(hardening no Spec 09)* |
| *Entrega 2026-11-21* | Provedor Oficial **TypeSafe AI** para decisão Jev (`/v1/systemone`) | **Implementado** | `internal/modelx/typesafe.go`, `internal/jev/score.go`, `ide/frontend/src/modals.ts` |
| *Entrega 2026-11-21* | **Worktree Composta Multi-Repo** (`monorepo` + `app-mobile`, `hivemind` + `HiveMind-App`) | **Implementado** | `internal/gitx/nested_repos.go`, `internal/orchestrator/orchestrator.go`, `ide/internal/idecore/runner.go` |
| *Entrega 2026-11-21* | Descoberta Multi-Repo no Source Control & Vault Lease Isolado | **Implementado** | `ide/internal/idecore/git_repos.go`, `internal/modelx/agy_vault_windows.go` |
| *Onda 2 (2026-11-21)* | [Spec 02](specs/02-prompt-cache-and-context-compaction.md) — Prefixo KV-cache estável + compactação token-aware | **Implementado** | `internal/contextx/`, `orchestrator/orchestrator.go` (reminder na cauda), `handoff.go`, `fence.go` |
| *Onda 2 (2026-11-21)* | [Spec 06](specs/06-jev-router-resilience-and-telemetry.md) — Jev hot-path < 5ms, lease half-open, ProviderError, telemetria de custo | **Implementado** | `internal/jev/health.go`, `jev/score.go`, `modelx/httpcache.go`, `modelx/cli_exec.go`, `modelx/usage.go` (JSONL), `fslockx/` |
| [Spec 03](03-concurrency-races-and-persistence.md) | Concorrência, Eliminação de Data Races e Persistência Atômica | **Implementado (Onda 1)** | `internal/atomicx/`, `sessionx/store.go`, `resident/lock_windows.go`, `orchestrator/jev_wrapper.go` |
| [Spec 04](04-tools-sandbox-and-pathsec.md) | Blindagem de Tools, `pathsec` e Sandbox OS | **Implementado (Onda 1)** | `internal/pathsec/sensitive.go`, `internal/sandboxx/`, `internal/tools/` (`fileReadTracker`, `.gitignore`, Head+Tail log) |
| [Spec 05](05-secrets-vault-and-multi-tenant-isolation.md) | Cofre Unificado (`secretx`), Isolamento AGY e Multi-Tenant Pool | **Implementado (Onda 1)** | `internal/secretx/vault_windows.go`, `internal/modelx/vault_store.go`, `agy_vault_windows.go`, `account_pool.go`, `relayclient/config.go` |
| *Onda 4 (2026-11-21)* | [D-007 (.specify)](../../.specify/specs/007-hive-floating-resident/spec.md) — **Agent Hive Flutuante** (`W4-03`/`W4-04`) | **Implementado** | `ide/internal/orb/hub.go`, `ide/orb_host.go`, `ide/orb_window.go`, `ide/frontend/src/orb/view.ts`, `resident.ts` |
| *Onda 5 (2026-11-21)* | [Spec 10](10-mobile-companion-supply-chain-and-ci.md) — `HiveMind-App` offline Room, R8, versionamento `-ldflags`, auto-updater & CI | **Implementado** | `HiveMind-App/` (Room, `KeyTrust.kt`, `CleartextGuard.kt`, `Theme.kt`), `VERSION`, `internal/version/`, `internal/updater/`, `.github/workflows/ci.yml`, `deploy/relay/` |

### 1.2 Specs Parcialmente Concluídas

| Documento | O que já foi feito | O que continua pendente | Onda Alvo |
|---|---|---|---|
| [`SPEC_ATUALIZACAO.md`](../SPEC_ATUALIZACAO.md) | Fases **1 a 7** + mutex Kernel `lock_windows.go` (`W1-04`) + **Fase 9** (`VERSION`/`-ldflags`, `internal/updater`, `checksums.txt`, R8 no `HiveMind-App`, `W5-04`/`W5-05`) | **Fase 8** (dono único do núcleo IDE/serve/orb) + plugar chamada de `internal/updater` em `ide/app.go` | Onda 4 |
| [`SPEC_HIVE_AGENT.md`](../SPEC_HIVE_AGENT.md) | Fases **1, 2, 3 e 5** concluídas (Broker modularizado em `handlers_*.go`, sandbox `AppContainer`/`bwrap`, `secretx`, memória, `cron_add`, subagentes, hash de skill, workstream) | **Fase 4** (Browser e Desktop isolados em Windows Sandbox `.wsb` / container com gVisor, hoje desligados por padrão) | Onda 4 |
| [`SPEC_JEV_ORQUESTRADOR.md`](../SPEC_JEV_ORQUESTRADOR.md) | Fases **1 a 5** + TypeSafe AI concluídas; **Onda 2** entregou Vertex AI em `models.ts` e failover Jev opcional em `resident/config.go` (`Config.JevFailover`) | — (spec concluída pela Onda 2) | ~~Onda 2~~ ✅ |
| [`SPEC_APP_MOBILE_NATIVO.md`](../SPEC_APP_MOBILE_NATIVO.md) | **100% Concluída (Onda 5):** Room cache offline, `peer_presence`, modos `plan:sdd`/`plan:sdd-jira`/`resident`, `provider_health`/`relay_gap`/Jev, TOFU, `network_security_config.xml`, R8 e tokens `Theme.kt` (`SurfacePanel`, `Inter`/`JetBrains Mono`) | — | ~~Onda 5~~ ✅ |
| [`ROADMAP.md`](../ROADMAP.md) | Fases **0 a 4** + streaming real do `agy` + `qa_approve` com evidência (`W3-03`) + remoção de `ignore_io.go` (`W3-06`) + single-source allowlist (`W3-09`) + versão dinâmica (`W5-04`) | `git blame`/resolução de conflitos na IDE (`W4-08`) | Onda 4 |
| [Spec 01](01-orchestrator-modular-harness.md) | `tools` fatiado em `policy/fs/shell/git/github/jira/qa.go` + `resident/broker.go` fatiado em `handlers_*.go` (`W3-02`) | Quebrar `internal/orchestrator/orchestrator.go` (`W3-01`) | Onda 3 |
| [Spec 07](07-ide-architecture-services-and-reactive-ui.md) | Proteção de foco/input em `renderLeftSidebar`/`renderModalLayer` (`W4-06`), markdown incremental, `safeImageSrc`, CSP, ARIA focus-trap e CSS `@layer` (`W4-07`) | Quebrar `ide/app.go`/`diff.go` (`W4-01`), `CloseProject` (`W4-02`), bindings `wailsjs` (`W4-05`), `git blame` (`W4-08`) | Onda 4 |
| [Spec 08](08-git-github-jira-and-multi-repo-delivery.md) | `gitx.Local` non-interactive env + `EnsureWorktree` + `LiteralPathspec` (`W3-06`); `githubx` GHES + paginação `Link`/GraphQL (`W3-07`); heurísticas Jira genéricas + `ListProjectIssues` read-only + helper `githubx/crosslink.go` (`W3-08`) | Plugar `crosslink.go` no `orchestrator` pós-Onda 2 | Onda 3 |
| [Spec 09](09-relay-e2ee-mcpx-and-runtime-unification.md) | Onda 1 + **Onda 3 (`W3-05`)**: `remote`/`relayclient` movidos para `internal/` e `hivemind serve` headless em `internal/cli/servehost.go` | `internal/runtime/bootstrap.go` unificando construção de Runner/Jev/mcpx entre CLI/TUI/IDE (`W3-04`) | Onda 3 |

### 1.3 Specs 100% Pendentes (Backlog Consolidado)

- [Spec 11](11-external-workers-worktrees-and-ux.md) — Workers CLI externos (piloto Cursor CLI), ergonomia de worktrees, `SOUL.md`/webhooks/Slack, custo USD e UX avançada (**Onda 6**)

---

## 2. Plano de Execução por Onda (Checklists Acionáveis)

### Onda 1 — Segurança Crítica, Cofre `secretx`, Concorrência & Sandbox
*Objetivo: eliminar risco de exposição de segredos, data races, bypass de arquivos sensíveis e replay de RPCs.*

- [x] **W1-01** `[Spec 03]` Criado `internal/atomicx` (`WriteFile` via temp + `Sync` + `Rename` com retry Windows) e migradas escritas em `reviewx/review.go`, `gitx/integrate.go`, `modelx/openrouter_models.go`, `ide/internal/remote/config.go`, `resident/routine.go`, `workspace/storage.go` e `tools/tools.go`. *(2026-11-21 — `go test ./internal/atomicx/... ./internal/reviewx/... ./internal/gitx/... ./internal/modelx/... ./internal/resident/... ./internal/workspace/... ./internal/tools/...` + `ide/internal/remote/...`)*
- [x] **W1-02a** `[Spec 03]` `sessionx.cloneEvent`, `fileSession.Events()` retornando clones, `AppendEvent` clonando entrada e `CompactOldToolOutputs` sem mutação in-place (+ `store_test.go` validando imutabilidade). *(2026-11-21 — `go test -count=1 ./internal/sessionx/...`)*
- [x] **W1-02b** `[Spec 03]` I/O de disco de `Store.Create` movido para fora de `Store.mu` em `internal/sessionx/store.go` via `fs.persist`. *(2026-11-21 — `go test -count=1 ./internal/sessionx/...`)*
- [x] **W1-03a** `[Spec 03]` Adicionados `routinesMu` em `resident/routine.go` (`ClaimDue`, `SaveRoutine`, `DeleteRoutine`, `SetRoutineEnabled`), `notesMu` em `reviewx/review.go`, `migrateMu` em `workspace/storage.go` e `brokerMu` protegendo `LastBroker` em `resident/resident.go`. *(2026-11-21 — `go test ./internal/resident/... ./internal/reviewx/... ./internal/workspace/...`)*
- [x] **W1-03b** `[Spec 03]` Adicionado `delegateMu` serializando closure `delegate_to_squad` e leituras pós-turno em `orchestrator/orchestrator.go`, além de CAS geracional (`gen`) em `jevModelWrapper` (`orchestrator/jev_wrapper.go`). *(2026-11-21 — `go test -count=1 ./internal/orchestrator/...`)* *(Nota: `LastBroker` segue protegido por `brokerMu`; flock cross-process em `routine.go` se necessário pode entrar em F2B)*
- [x] **W1-04** `[Spec 03 / SPEC_ATUALIZACAO F8]` Substituído PID file em `internal/resident/lock_windows.go` por Named Kernel Mutex (`CreateMutexW` `Local\HiveMind.Residente.<hash>`) + `LockFileEx`. *(2026-11-21 — `go test ./internal/resident/...`)*
- [x] **W1-05a** `[Spec 04]` Expandido `pathsec.IsSensitivePath` (`.crt`, `.der`, `.tfvars`, `terraform.tfstate`, `.htpasswd`, `kubeconfig`, `.kube`, `.docker`, `*secret*`) e aplicado em `write_file`, `edit_file`, `list_dir` (`tools.go`) e `find_files` (`search.go`). *(2026-11-21 — `go test ./internal/pathsec/... ./internal/tools/...`)*
- [x] **W1-05b** `[Spec 04]` Validado que `workspace.Resolve` (`workspace/jail.go`) permanece bloqueando `.git` e `.hivemind`, enquanto todas as tools em `tools.go`/`search.go` aplicam `pathsec.IsSensitivePath` antes e após resolução (`canRead`, `canList`, `canWrite`, `editFileSurgical`, `runSearchText`, `runFindFiles`). *(2026-11-21 — `go test -count=1 ./internal/tools/...`)*
- [x] **W1-06** `[Spec 04]` Corrigido `sandbox_windows.go` (`internetClient` + `privateNetworkClientServer` apenas quando `AllowNetwork=true`; ACL do `exeDir` best-effort sem falhar em `Program Files`) e `sandbox_linux.go` (`--tmpfs` ocultando `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.kube`, `~/.docker`). *(2026-11-21 — `go test ./internal/sandboxx/...`)*
- [x] **W1-07a** `[Spec 04]` Criado `strx.TruncateHeadTailBytes` e aplicado truncamento **Head + Tail** (`4 KiB + 12 KiB`) em `runShellSandboxed` (`tools.go`) e `shellx.Local.Run` + cap de `25` matches por arquivo em `search.go`. *(2026-11-21 — `go test ./internal/strx/... ./internal/shellx/... ./internal/tools/...`)*
- [x] **W1-07b** `[Spec 04]` Gravado arquivo de log completo no truncamento >16 KiB de `runShellSandboxed`, criado `fileReadTracker` por `mtime` entre `read_file`/`write_file`/`edit_file` (`tools.go`), e adicionado `loadGitignoreMatcher` em `search.go` (`read_file` sem range preservado sem prefixo por compatibilidade com `TestReadFileLineRange`). *(2026-11-21 — `go test -count=1 ./internal/tools/...`)*
- [x] **W1-08** `[Spec 05]` Migrados segredos de `modelx` (`api-keys.json`, `provider-profiles.json`, `credentials.json`, `oauth-session.json` via `modelx/vault_store.go`) e `relayclient` (`relay.json`) para `internal/secretx` mantendo apenas referências `"vault:..."` em disco, e centralizado P/Invoke CredMan em `internal/secretx/vault_windows.go` (+ stubs `!windows` em `raw_cred_other.go`). *(2026-11-21 — `go test -count=1 ./internal/secretx/... ./internal/modelx/...` + `ide/internal/relayclient/...`)*
- [x] **W1-09** `[Spec 05]` Protegidos `AcquireAgyVaultGuard` e `WithAgyVaultLease` com Named Kernel Mutex `Local\HiveMind.AgyVaultSlot` + backup de recovery pós-crash (`modelx:agy:lease_backup` restaurado em `EnsureLiveAgyCredentialRestored`), e propagado `ctx` do workspace em `LoadOpenCodeCredentialsForContext`, `JevCredentialPresenceForContext` e `JevCredentialFingerprintsForContext`. *(2026-11-21 — `go test -count=1 ./internal/modelx/...`)* *(Nota: CLI `agy` usa slot `gemini:antigravity` sob lease cross-process quando não em modo `adc` isolado por `CLOUDSDK_CONFIG`)*
- [x] **W1-10** `[Spec 05]` Restringido `ListAccountPoolForProvider` em `internal/modelx/account_pool.go` para excluir contas de outras orgs, contas sem `OrgPatterns` e `__system__` quando o workspace casa com `orgx` (+ `TestAccountPoolNeverCrossesOrganizationBoundaries`). *(2026-11-21 — `go test -count=1 ./internal/modelx/...`)*
- [x] **W1-11** `[Spec 09]` Adicionados `Seq`, `IssuedAtUnix` e `Nonce` em `relaywire.RPCRequest` (`protocol.go`), janela `±90s` e deduplicação LRU anti-replay (`box.Nonce`, `req.Nonce`, `req.Seq`) em `relayclient.Client.handleRPC` + `TestClientRejectsReplayedAndExpiredRPC`. *(2026-11-21 — `go test -count=1 ./relaywire/...` + `ide/internal/relayclient/...`)*
- [x] **W1-12** `[Spec 09]` Substituído wipe global `len > 4096` por eviction parcial/expiração preservando lockouts ativos em `relay/limiter.go` e `remote/lan.go`, persistidos `qr_tickets` no SQLite com check `LoginBlocked` em `relay/store.go`/`server.go`, e emitido `TypePeerPresence` em `Hub.Attach`/`Detach` (`relay/hub.go`; mantido `LastSeq+1` na LAN para `TestSeqGapAsksResume`). *(2026-11-21 — `go test -count=1 ./internal/relay/...` + `ide/internal/remote/...`)*
- [x] **W1-13** `[Spec 09]` Implementado **Workspace Trust Gate** (`IsWorkspaceConfigTrusted`/`TrustWorkspaceConfig` com SHA-256 em `trusted-mcp.json`), escrita `0o600` via `atomicx.WriteFile` em `mcpx/config.go`, `filteredCommandEnv` sem vazar segredos do host, captura de `stderr` (`cappedStderr`), timeouts (`Tools: 15s`, `Call: 60s`) em `mcpx/sdk.go` e versões pinadas em `mcpx/defaults.go` (`cmd.exe /d /c` mantido no Windows para shims/TestLocalCommandHidesWindowOnWindows). *(2026-11-21 — `go test -count=1 ./internal/mcpx/...`)*
- [x] **W1-14** `[Verificação Onda 1]` Bateria completa da Onda 1 verde sem nenhum token em claro nos JSONs testados (`go test -p 1 -count=1 ./internal/{secretx,modelx,atomicx,strx,pathsec,workspace,sessionx,reviewx,resident,sandboxx,shellx,tools,mcpx,relay,orchestrator}/... ./relaywire/...` + `ide/internal/{remote,relayclient}/...` + `GOOS=linux go vet ./internal/secretx/...`). *(2026-11-21; nota: `go test -race` não executou neste host Windows pois `gcc` / CGO não está no `%PATH%` — validar `-race` na CI Linux na Onda 5)*

---

### Onda 2 — KV-Cache, Compactação Token-Aware e Roteador Jev
*Objetivo: maximizar hits de cache de prompt, reduzir custo/latência e impedir circuitos presos ou gastos silenciosos.*
*(Onda concluída em 2026-11-21 — Spec 02 + Spec 06 implementados na íntegra.)*

- [x] **W2-01** `[Spec 02]` `llmCore` com `InstructionProvider` 100% estático; deltas dinâmicos (state keys, `sharedBoard.SummaryFor`, gate de QA do closing) via `newDynamicContextReminderCallback` em `<system-reminder>` anexado à cauda. *(2026-11-21 — `TestSystemInstructionAndToolsStayInvariantAcrossTurns` com hash do prefixo idêntico em 10 turnos; `TestRoleTargetedSkillsInjection`/`TestResumedSessionPreservesPriorContext` atualizados ao contrato de cauda)*
- [x] **W2-02** `[Spec 02]` `newChiefToolStrippingModelCallback` removido (guardrails de orçamento ficam no `BeforeToolCallback`); `team_memory` em um único lugar estável (primeira mensagem user, vista pelo Chefe); `Unused` → `struct{}` em `tools.go`; wrappers MCP limitados a 6 com catálogo de schemas sob demanda via `mcp_list_servers`. *(2026-11-21 — `go test ./internal/tools/... ./internal/orchestrator/...`)*
- [x] **W2-03** `[Spec 02]` `internal/contextx` unificado (55% micro / 75% corte estrutural, não-destrutivo, janela do catálogo Jev) consumido por orchestrator e resident; `s[:N]` → `strx.TruncateBytes` em `handoff.go`/`fence.go` (+ atomic write); compactação destrutiva mid-run removida (`CompactOldToolOutputs` só entre turnos). *(2026-11-21 — `TestHandoffTruncationIsUTF8SafeAtBoundary`, `TestFenceTruncationIsUTF8SafeAtBoundary`, `go test ./internal/contextx/...`)*
- [x] **W2-04** `[Spec 06]` `sharedHTTPTransport()` único (HTTP/2 + keep-alive) sob `retryHTTPClient`/OpenCode/TypeSafe; `ocRouteCache` com `baseURL` na chave; `promptCacheTransport` no pipeline OpenCode; `retryHTTPClient` no Anthropic Vertex. *(2026-11-21 — `go test ./internal/modelx/...`)*
- [x] **W2-05** `[Spec 06]` `swrCache` (stale-while-revalidate + TTL negativo + `singleflight`) em `GetOpenRouterBalanceUSD` (hot-path nunca bloqueia), `openRouterCheckAuth`, `fetchOpenCodeCatalog`, `enrichOpenRouterDailyDiscounts` (backoff 1h) e `LookAgyCached`/`LookCursorCached` no hot-path do Jev. *(2026-11-21 — `go test ./internal/modelx/... ./internal/jev/...`)*
- [x] **W2-06** `[Spec 06]` `HalfOpenLeaseUntil` (lease TTL 2min) + `ReleaseHalfOpen` com `defer` nos paths de política/cancelamento em `jev.go`/`jev_wrapper.go`; `saveLocked` com `fslockx` + merge cross-process; `RecordSpend` com debounce 2s; `Summary` read-only. *(2026-11-21 — `TestHalfOpenLeaseReleasesOnPolicyErrorAndCancel`, `TestHalfOpenLeaseSelfHealsAfterTTL`, `TestSaveAdoptsUnknownDiskRecords`, `TestRecordSpendDebounces`)*
- [x] **W2-07** `[Spec 06]` `modelx.ProviderError` tipado (openai/anthropic/genai via `errors.As`) + redação unificada `ya29.`/`oc_`/`ts_`/`refresh_token`/`access_token` em `jev.RedactSecrets` aplicada nas bordas (`stream.go`, `typesafe.go`). *(2026-11-21 — `go test ./internal/jev/... ./internal/modelx/...`)*
- [x] **W2-08** `[Spec 06]` `selectBestInTierWithOpts` em pass único com slice reutilizável (`sync.Pool`); bypass `ignoreDailyBudget: true` removido (refusal `budget` → fluxo `approveFn`); `policy.json`/`.hivemind/jev.yaml` recarregados por `mtime`; `usage.go` em JSONL append-only (`ai-usage.jsonl` + rotação 20 MiB, migração do legado) com `CachedContentTokenCount`/`DiscountPct`; `RecordAIUsage` movido para o orchestrator (`recordUsage` central com rotas por agente, IDE sem duplicar); `OutcomeLog.NoteQA(repo, lane)`. *(2026-11-21 — `TestCurated21OpenRouterModelsAndPromotionDifficultyRouting` atualizado ao budget guard, `TestNoteQAKeyedByRepoAndLane`, `TestRecordAIUsageBuffersUntilFlush`/`TestRecordAIUsagePrunesAfter31Days`/`TestFlushMergesLedgerWrittenByAnotherProcess` no contrato JSONL)*
- [x] **W2-09** `[ROADMAP F5 / Spec 06]` `internal/modelx/cli_exec.go`: `cliCommand` (wrap `.cmd`/`.bat`) + `readCLIStream` (leitor NDJSON único com drain de linhas gigantes) unificando agy/stream/cursor; `Agy.GenerateContent(..., stream=true)` emite deltas `Partial` + resposta final via `--output-format stream-json` com lease de vault e env isolado. *(2026-11-21 — `TestAgyGenerateContentStreamsDeltas` com fake stream-json; `TestAgyCmdWrapper` guardado para Windows)*
- [x] **W2-10** `[SPEC_JEV_ORQUESTRADOR]` **Vertex AI** exposto em `ide/frontend/src/models.ts` (`getProviderList`, `getModelsForProvider` com `vertex:*`, `jev:vertex`); failover Jev opcional no `internal/resident/config.go` (`BuildLLM` re-score excluído o par que falhou, `Config.JevFailover`). *(2026-11-21 — `npm test` 79/79 + `npm run build` em `ide/frontend`; `go test ./internal/resident/...`)*
- [x] **W2-11** `[Verificação Onda 2]` `go test -p 1 -count=1 ./internal/jev/... ./internal/modelx/... ./internal/orchestrator/...` verde (p99 Score 1034us/Route 1033us < 5ms com 2000 modelos; invariância de prefixo KV-cache em 10 turnos) + `go test ./internal/idecore/` no módulo `ide/` verde. *(2026-11-21)*

---

### Onda 3 — Modularização Go, Runtime Único CLI/TUI/IDE e Endurecimento VCS
*Objetivo: fatiar monólitos Go, equalizar CLI/TUI com IDE e completar suporte enterprise em Git/GitHub/Jira.*

- [ ] **W3-01** `[Spec 01]` Quebrar `internal/orchestrator/orchestrator.go` (`3.114L`) em `runner.go`, `triage.go`, `pipeline.go` e `delivery.go`; parar criação implícita de `MEMORY.md` em `teamMemorySnippet()`, usar `ctx` do turno em `recordQAStall(ctx)` e checar erros de `State().Set` e `Audit.RecordOutcome`.
- [x] **W3-02** `[Spec 01]` Fatiar `internal/tools/tools.go` (`1.695L`) em `policy.go`, `fs.go`, `shell.go`, `git.go`, `github.go`, `jira.go`, `qa.go` e dividir `internal/resident/broker.go` (`1.028L`) em middleware + handlers por domínio.
- [x] **W3-03** `[ROADMAP F5]` Endurecer `qa_approve` em `internal/tools/qa.go` para exigir evidência estruturada de comando/teste executado (ou justificativa em tarefas pure-docs) quando aplicável *(mocks em `internal/orchestrator/*_test.go` atualizados para o gate)*.
- [ ] **W3-04** `[Spec 09]` Extrair `internal/runtime/bootstrap.go` unificando construção de `Runner`, `GitHub`, `Jira`, `Sessions`, `mcpx` (com pool persistente na IDE) e `NewJevModelFactory` entre CLI, TUI e IDE.
- [x] **W3-05** `[Spec 09]` Mover `ide/internal/remote` e `ide/internal/relayclient` para `internal/` permitindo `hivemind serve` headless sem depender de `hivemind-ide.exe`.
- [x] **W3-06** `[Spec 08 / ROADMAP]` Unificar env Git não-interativo (`GCM_INTERACTIVE=Never`, `GIT_ASKPASS=""`) em `gitx.Local.Run`, proteger `EnsureWorktree` contra remoção em falha transitória, usar `LiteralPathspec` em `DiscardFileChanges` e remover `internal/gitx/ignore_io.go`.
- [x] **W3-07** `[Spec 08]` Atualizar `githubx.ParseRemote` para suportar hosts **GitHub Enterprise Server (GHES)**, adicionar paginação (`Link` header + cursor GraphQL) e eliminar duplicação REST/GraphQL de `idecore/diff.go`.
- [ ] **W3-08** `[Spec 08]` Remover termos específicos de cliente de `matchJiraProjectFromNames`, impedir mutação de `.hivemind/team.yaml` durante `ListProjectIssues` e implementar cross-linking automático entre múltiplos PRs abertos em entregas multi-repo *(parcial: sanitização e helper `githubx/crosslink.go` prontos; falta plugar o cross-link no `orchestrator`)*.
- [x] **W3-09** `[ROADMAP]` Unificar fonte única para allowlist de shell (`internal/team` vs `examples/team.yaml`).
- [ ] **W3-10** `[Verificação Onda 3]` Validar `go test -race ./...` na raiz do módulo, nenhum arquivo Go > 600 linhas em `orchestrator`/`tools`/`resident` e testes de paginação + GHES verdes.

---

### Onda 4 — IDE Reativa, Lifecycle de Projetos & Agent Hive Flutuante
*Objetivo: zerar leaks na IDE, eliminar sobrescrita de foco/input e entregar o Agent Hive flutuante unificado.*

- [ ] **W4-01** `[Spec 07]` Quebrar `ide/app.go` (`1.834L`) em serviços coesos e fatiar `ide/internal/idecore/diff.go` (`2.668L`) em `git_diff.go`, `worktree_sync.go`, `git_ops.go`, `jira_discovery.go`, `github_prs.go`.
- [ ] **W4-02** `[Spec 07]` Implementar `CloseProject(workspace)` em `ide/app.go` chamado por `closeProjectTab()`, encerrando `FileWatcher`, `GitHubMonitor`, `AutoReviewManager` e `LSPManager`; unificar poller de PRs (60s/120s) e observar `.git/HEAD` + `.git/index` (incluindo sub-repos) no watcher.
- [x] **W4-03** `[Spec 007 — H001..H005]` Estender `ide/internal/orb/hub.go` e `ide/orb_host.go` / `orb_window.go` com `OrbResidentState`, streaming ao vivo do `ResidentManager`, ações `resident_*` e janela `396×540` com clamp no monitor.
- [x] **W4-04** `[Spec 007 — D001..D003, H006..H011]` Atualizar `ide/frontend/src/orb/view.ts`, `styles.css`, `boot.ts` e `resident.ts` com aba **Conversa** padrão, aprovação fixa acima do composer, preservação do rascunho no `<textarea>`, sincronia **Abrir IDE** e testes Vitest.
- [ ] **W4-05** `[Spec 07]` Adotar diretamente bindings tipados `wailsjs/go/main/App` em `ide/frontend/src/`, removendo duplicações manuais de `types.ts`.
- [x] **W4-06** `[Spec 07]` Bloquear re-renders totais de `renderLeftSidebar()` / `renderModalLayer()` sob eventos assíncronos enquanto houver input focado/sujo (preservando mensagens de commit e tokens sendo digitados).
- [x] **W4-07** `[Spec 07]` Implementar parser **incremental por blocos** em `markdown.ts` (fim do `O(N²)` no streaming do chat), aplicar `safeImageSrc` em `modals.ts:1162`, adicionar CSP em `index.html`, foco/teclado ARIA + focus trap nos modais e modularizar `styles.css` em `@layer`.
- [ ] **W4-08** `[ROADMAP F5]` Adicionar `git blame`, histórico por arquivo e resolução guiada de conflitos no painel Git da IDE.
- [ ] **W4-09** `[SPEC_HIVE_AGENT F4]` Implementar provisão isolada real em Windows Sandbox (`.wsb` com `ProtectedClient`, rede filtrada e workspace read-only) para `browser_open` / `desktop_*`.
- [ ] **W4-10** `[Verificação Onda 4]` Validar `check-design-tokens.mjs`, `npm test`, `npm run build`, `go test ./ide/...` e teste manual de abrir/fechar 10 abas + uso completo do Agent Hive flutuante com a IDE fechada na bandeja.

---

### Onda 5 — `HiveMind-App` Offline-First, Auto-Update Assinado & CI/CD
*Objetivo: fechar paridade do app Android, eliminar drift entre código e binário instalado e blindar pipeline de release.*

- [x] **W5-01** `[Spec 10 / SPEC_APP_MOBILE]` Adicionar `plan:sdd`, `plan:sdd-jira` e `resident` em `ChatScreen.kt`; desserializar `provider_health`, `relay_gap` e metadados Jev (`provider`, `model`, `tier`, `cost`, `gate`) em `Models.kt`/`HubState.kt`.
- [x] **W5-02** `[Spec 10]` Implementar cache offline **Room/SQLite** em `HiveMind-App` (projetos, lanes, mensagens e diffs) + presença instantânea via evento `peer_presence`.
- [x] **W5-03** `[Spec 10]` Substituir `usesCleartextTraffic="true"` por `network_security_config.xml` (restrito a LAN/Tailscale), exigir confirmação na rotação de chave TOFU, habilitar R8 (`isMinifyEnabled = true`) + signing externo de release e corrigir `Theme.kt` (`SurfacePanel #0C0C0C`, remover alias `SteelBlue`, fontes `Inter`/`JetBrains Mono`).
- [x] **W5-04** `[Spec 10 / SPEC_ATUALIZACAO F9]` Criar fonte única de versão (`VERSION` + SHA commit via `-ldflags` em `hivemind`, `hivemind-ide`, `hivemind-relay`) e remover cópia redundante de binário em `ide/build/`.
- [x] **W5-05** `[Spec 10 / SPEC_ATUALIZACAO F9]` Atualizar `scripts/build-installer.ps1` (remover fallback `npm install`, gerar `checksums.txt` SHA-256, suportar `signtool.exe`) e implementar verificador/auto-updater in-app via GitHub Releases (resolvendo drift vs `AppData\Local\Programs\HiveMind`).
- [x] **W5-06** `[Spec 10]` Endurecer `.github/workflows/ci.yml`: incluir `go test -race ./internal/remote/...`, job Android (`./gradlew testDebugUnitTest lintDebug`) e `govulncheck ./...`.
- [x] **W5-07** `[Spec 10]` Endurecer `deploy/relay/`: fixar digest `@sha256` em `Dockerfile` + `HEALTHCHECK`, alinhar usuário `relay` entre Dockerfile e `hivemind-relay.service`, limites cgroup/seccomp e HSTS/CSP no `Caddyfile`.
- [ ] **W5-08** `[Verificação Onda 5]` Validar CI verde ponta-a-ponta, `assembleRelease` do `HiveMind-App` com R8 ativo e upgrade simulado do instalador Windows com conferência SHA-256 *(parcial: `assembleRelease` R8 local validado e CI workflow corrigido; monitorando Actions pós-push; simulação do instalador Windows pendente)*.

---

### Onda 6 — Workers CLI Externos (Piloto Cursor), Ergonomia de Worktrees, Residente Event-Driven & UX Avançada
*Objetivo: abrir o squad para workers CLI externos sob contrato governado (começando pelo Cursor CLI), eliminar fricção diária em worktrees, adicionar custo USD por run e evoluir o residente e a UX da IDE.*

- [x] **W6-01** `[Spec 11 — P4]` Calcular e exibir **custo USD acumulado por run, agente e papel** (`Chefe`, `Dev`, `QA`) usando preços do catálogo Jev (`PromptMicroUSD`, `CompletionMicroUSD`, `CachedContentTokenCount`, `DiscountPct`) no resumo CLI/TUI, cabeçalho da lane na IDE e página Uso & Limites.
- [x] **W6-02** `[Spec 11 — P2]` Implementar `.worktreeinclude` (caminhos literais gitignored filtrados por `pathsec.IsSensitivePath`, bloqueando `.env`/chaves), `sharedDirectories` via symlink/junction para caches grandes (`node_modules`, `.gradle`, `.cache`), criação assíncrona com progresso/Retry e **worktree checkpoints** (`git stash create`/ref interna) para rollback de turno.
- [x] **W6-03** `[Spec 11 — P2]` Criar painel de administração de worktrees na IDE (listar worktrees ativas/órfãs, branch/tamanho, `archive` e `prune`).
- [x] **W6-04** `[Spec 11 — P1]` Definir o **Worker Contract** (`dispatch`, `heartbeat`, `ask` bloqueante via HITL e `worker_done` idempotente por `dispatchId`) + validação pós-hoc de `git diff` contra `owned_paths` e `pathsec`, com adaptador piloto para o **Cursor CLI (`agent`)** reusando `internal/modelx/cursor.go` (`WithAgentDir`, `cursor_stream.go`, política `--trust`/`--force`) e interface extensível para OpenCode/outros CLIs.
- [x] **W6-05** `[Spec 11 — P3]` Adicionar `SOUL.md` editável ao lado de `MEMORY.md` no residente, receptor de **Webhooks event-driven** (`standard-webhooks` + HMAC para eventos GitHub) e conector **Slack** opcional, todos encapsulados em `WrapUntrustedData` com aprovação HITL obrigatória antes de escrita/PR.
- [x] **W6-06** `[Spec 11 — P5/P6]` Implementar toggle **Chat ↔ Terminal (PTY)** na IDE compartilhando o mesmo `sessionID` e **Split Panes de Lanes** lado a lado para acompanhar múltiplos agentes simultaneamente.
- [x] **W6-07** `[Spec 11 — P7]` Implementar **Design Mode via CDP** reaproveitando o Chrome DevTools MCP (`mcpx/defaults.go`) conectado ao browser real do usuário (pick element → seletor + HTML + estilos computados direto no chat).
- [x] **W6-08** `[Spec 11 — Extras]` Adicionar **Hybrid Skill Stubs** (`hivemind skills get <topic>` servido pelo binário version-matched) e preset **Race Mode** sobre `parallelagent` para comparar diffs/testes de N agentes na mesma task.
- [x] **W6-09** `[Verificação Onda 6]` Validar execução ponta-a-ponta de um worker externo **Cursor CLI** respeitando `worker_done` + guard pós-hoc de `owned_paths`/`pathsec`, rollback via worktree checkpoint, exibição de custo USD e testes de webhook com `WrapUntrustedData`.
