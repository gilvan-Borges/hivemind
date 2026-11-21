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
| *Onda 6 (2026-11-21)* | [Spec 11](11-external-workers-worktrees-and-ux.md) — Workers CLI (`dispatch`/`heartbeat`/`ask`/`worker_done`, Cursor), `.worktreeinclude`/checkpoints, `SOUL.md`/webhooks/Slack, custo USD, PTY+Split Panes, Design Mode CDP, Hybrid Skill Stubs & Race Mode | **Implementado** | `internal/orchestrator/worker_dispatch.go`, `race.go`, `cost.go`, `internal/gitx/worktree_admin.go`, `internal/resident/webhook.go`, `slack.go`, `ide/internal/idecore/pty_manager.go`, `design_mode.go`, `ide/frontend/src/` |
| *Fechamento Onda 3–5* | [Spec 01](01-orchestrator-modular-harness.md) — Modularização do Orquestrador (`runner`/`pipeline`/`delivery`/`triage` <= 600L), Tools & Broker + `qa_approve` estruturado | **Implementado (Onda 3)** | `internal/orchestrator/{runner,pipeline,delivery,triage}.go`, `internal/tools/`, `internal/resident/` |
| *Fechamento Onda 3–5* | [Spec 07](07-ide-architecture-services-and-reactive-ui.md) — Split `app*.go`/`idecore`, `CloseProject` ref-counted, poller GitHub unificado, watcher `.git`, `wails-bridge.ts`, Blame/Histórico/Conflitos & UX reativa | **Implementado (Onda 4)** | `ide/app*.go`, `ide/internal/idecore/{git_diff,worktree_sync,git_ops,jira_discovery,github_prs,git_history,watcher}.go`, `ide/frontend/src/{wails-bridge,git-history}.ts`, `.specify/specs/010-git-blame-conflicts/` |
| *Fechamento Onda 3–5* | [Spec 08](08-git-github-jira-and-multi-repo-delivery.md) — Hardening `gitx`/`githubx` (GHES, paginação, cross-linking multi-repo no `orchestrator`) & Jira genérico | **Implementado (Onda 3)** | `internal/gitx/`, `internal/githubx/crosslink.go`, `internal/orchestrator/delivery.go`, `ide/internal/idecore/jira_discovery.go` |
| *Fechamento Onda 3–5* | [Spec 09](09-relay-e2ee-mcpx-and-runtime-unification.md) — `remote`/`relayclient` no módulo raiz, `hivemind serve` e `internal/runtime/bootstrap.go` com `MCPPool` | **Implementado (Onda 3)** | `internal/remote/`, `internal/relayclient/`, `internal/runtime/bootstrap.go`, `internal/cli/{runcmd,servehost}.go`, `internal/tui/model.go` |
| *Fechamento Onda 3–5* | [`SPEC_HIVE_AGENT.md`](../SPEC_HIVE_AGENT.md) — Broker Phase 1–5 + provisão isolada em Windows Sandbox (`.wsb`, `WSBSurface`) | **Implementado (Onda 4)** | `internal/resident/{broker,sandbox_wsb,handlers_surface}.go`, `ide/internal/idecore/resident.go` |
| *Fechamento Onda 3–5* | [`SPEC_JEV_ORQUESTRADOR.md`](../SPEC_JEV_ORQUESTRADOR.md) & [`SPEC_APP_MOBILE_NATIVO.md`](../SPEC_APP_MOBILE_NATIVO.md) & [`ROADMAP.md`](../ROADMAP.md) | **Implementado** | `internal/jev/`, `HiveMind-App/`, `ide/internal/idecore/git_history.go` |

### 1.2 Specs Parcialmente Concluídas

| Documento | O que já foi feito | O que continua pendente | Onda Alvo |
|---|---|---|---|
| [`SPEC_ATUALIZACAO.md`](../SPEC_ATUALIZACAO.md) | Fases **1 a 7** + **Fase 9** (`VERSION`/`-ldflags`, `internal/updater`, `checksums.txt`, R8, UI **Verificar atualizações…** em `ide/app.go` e `modals.ts` com SHA-256) | **Fase 8** (casca / dono único de instância fora do escopo das Ondas 1–6) | Pós-Onda 6 |

### 1.3 Specs 100% Pendentes (Backlog Consolidado)

- [Spec 12](12-skill-router-project-brain-and-kits.md) — **Motor de Decisão de Skills (`Domínio × Intenção × Plataforma`), Slash Playbooks (`/layout`, `/redesign`, `/sec`, `/mobile`, `/game`, `/audio`), Skill Security Gate (`Go` + `NVIDIA/SkillSpector`), Mente Coletiva por Projeto (`Graphify` oficial + layer semântica local), Agent Hive Conectado (`Obscura`, canais públicos + `StrictNetworkGuard=false`), Jev Free-Tier e Kits Android/Kotlin & GameDev Studio (`Godot`, `Unreal`, Web, `ACE-Step-1.5`)** (**Ondas 7 a 10**)

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

- [x] **W3-01** `[Spec 01]` Quebrar `internal/orchestrator/orchestrator.go` (`3.114L`) em `runner.go`, `agent_builder.go`, `session_open.go`, `triage.go`, `triage_helpers.go`, `triage_guards.go`, `pipeline.go`, `events.go` e `delivery.go`; parar criação implícita de `MEMORY.md` em `teamMemorySnippet()`, usar `ctx` do turno em `recordQAStall(ctx)` e checar erros de `State().Set`, `sessionx.UpdateState`, `Audit.RecordOutcome` e `mem.AppendSessionTurn`.
- [x] **W3-02** `[Spec 01]` Fatiar `internal/tools/tools.go` (`1.695L`) em `policy.go`, `fs.go`, `shell.go`, `git.go`, `github.go`, `jira.go`, `qa.go` e dividir `internal/resident/broker.go` (`1.028L`) em middleware + handlers por domínio.
- [x] **W3-03** `[ROADMAP F5]` Endurecer `qa_approve` em `internal/tools/qa.go` para exigir evidência estruturada de comando/teste executado (ou justificativa em tarefas pure-docs) quando aplicável *(mocks em `internal/orchestrator/*_test.go` atualizados para o gate)*.
- [x] **W3-04** `[Spec 09]` Extrair `internal/runtime/bootstrap.go` unificando construção de `Runner`, `GitHub`, `Jira`, `Sessions`, `mcpx` (com pool persistente `MCPPool` por workspace na IDE) e `NewJevModelFactory` entre CLI, TUI e IDE.
- [x] **W3-05** `[Spec 09]` Mover `ide/internal/remote` e `ide/internal/relayclient` para `internal/` permitindo `hivemind serve` headless sem depender de `hivemind-ide.exe`.
- [x] **W3-06** `[Spec 08 / ROADMAP]` Unificar env Git não-interativo (`GCM_INTERACTIVE=Never`, `GIT_ASKPASS=""`) em `gitx.Local.Run`, proteger `EnsureWorktree` contra remoção em falha transitória, normalizar paths 8.3 no Windows em `worktree_admin.go`, usar `LiteralPathspec` em `DiscardFileChanges` e remover `internal/gitx/ignore_io.go`.
- [x] **W3-07** `[Spec 08]` Atualizar `githubx.ParseRemote` para suportar hosts **GitHub Enterprise Server (GHES)**, adicionar paginação (`Link` header + cursor GraphQL) e eliminar duplicação REST/GraphQL de `idecore/diff.go`.
- [x] **W3-08** `[Spec 08]` Remover termos específicos de cliente de `matchJiraProjectFromNames`, impedir mutação de `.hivemind/team.yaml` durante `ListProjectIssues` e plugar cross-linking automático (`githubx/crosslink.go` + `UpdatePullRequestBody` / `CommentOnIssue`) entre múltiplos PRs abertos em entregas multi-repo em `internal/orchestrator/delivery.go`.
- [x] **W3-09** `[ROADMAP]` Unificar fonte única para allowlist de shell (`internal/team` vs `examples/team.yaml`).
- [x] **W3-10** `[Verificação Onda 3]` Validar nenhum arquivo `.go` não-teste > 600 linhas em `orchestrator`/`tools`/`resident`, testes de paginação + GHES + crosslink e suíte completa Go verde (`-race` executado no job Linux da CI).

---

### Onda 4 — IDE Reativa, Lifecycle de Projetos & Agent Hive Flutuante
*Objetivo: zerar leaks na IDE, eliminar sobrescrita de foco/input e entregar o Agent Hive flutuante unificado.*

- [x] **W4-01** `[Spec 07]` Quebrar `ide/app.go` em `app.go`, `app_project.go`, `app_git.go`, `app_run.go`, `app_settings.go`, `app_resident.go` e fatiar `ide/internal/idecore/diff.go` em `git_diff.go`, `worktree_sync.go`, `git_ops.go`, `jira_discovery.go`, `github_prs.go` e `github_prs_graphql.go`.
- [x] **W4-02** `[Spec 07]` Implementar `CloseProject(workspace)` ref-counted em `ide/app_project.go` chamado por `closeProjectTab()`, encerrando `FileWatcher`, `GitHubMonitor`, `AutoReviewManager`, `LSPManager`, `PTYManager`, `DesignModeService` e `MCPPool`; unificar poller de PRs via `GitHubMonitor.CachedPRs` e observar `.git/{HEAD,index,MERGE_HEAD,REBASE_HEAD}` (incluindo sub-repos) emitindo `hivemind:git-changed`.
- [x] **W4-03** `[Spec 007 — H001..H005]` Estender `ide/internal/orb/hub.go` e `ide/orb_host.go` / `orb_window.go` com `OrbResidentState`, streaming ao vivo do `ResidentManager`, ações `resident_*` e janela `396×540` com clamp no monitor.
- [x] **W4-04** `[Spec 007 — D001..D003, H006..H011]` Atualizar `ide/frontend/src/orb/view.ts`, `styles.css`, `boot.ts` e `resident.ts` com aba **Conversa** padrão, aprovação fixa acima do composer, preservação do rascunho no `<textarea>`, sincronia **Abrir IDE** e testes Vitest.
- [x] **W4-05** `[Spec 07]` Regenerar bindings `wailsjs/go/main/App` (`wails generate module`) e validar contrato tipado compile-time em `ide/frontend/src/wails-bridge.ts`.
- [x] **W4-06** `[Spec 07]` Bloquear re-renders totais de `renderLeftSidebar()` / `renderModalLayer()` sob eventos assíncronos enquanto houver input focado/sujo (preservando mensagens de commit e tokens sendo digitados).
- [x] **W4-07** `[Spec 07]` Implementar parser **incremental por blocos** em `markdown.ts` (fim do `O(N²)` no streaming do chat), aplicar `safeImageSrc` em `modals.ts`, adicionar CSP em `index.html`, foco/teclado ARIA + focus trap nos modais e modularizar `styles.css` em `@layer`.
- [x] **W4-08** `[ROADMAP F5 / Spec 010]` Adicionar `git blame`, histórico por arquivo (`--follow`) e resolução guiada de conflitos no painel Git e editor da IDE (`git_history.go`, `git-history.ts`).
- [x] **W4-09** `[SPEC_HIVE_AGENT F4]` Implementar provisão isolada em Windows Sandbox (`.wsb`, `BuildWSBXML`, `WSBSurface`, proteção contra pastas do cofre/segredos) em `internal/resident/sandbox_wsb.go` e `ResidentSetIsolatedSandbox`.
- [x] **W4-10** `[Verificação Onda 4]` Validar `check-design-tokens.mjs`, `tsc --noEmit`, `npm test`, `npm run build`, `go test ./ide/...` e teste automatizado `TestOpenCloseProjectLifecycleTenRepos` (abrir/fechar 10 projetos sem leak de watchers/refcounts).

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
- [x] **W5-08** `[Verificação Onda 5]` Validar `assembleRelease` R8 do `HiveMind-App`, plugar `internal/updater` em `ide/app.go` (`AppVersion`, `CheckForUpdates`, `DownloadUpdate`) + modal em `modals.ts` e simular download/verificação SHA-256 em `ide/updater_test.go`.

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

---

### Onda 7 — Motor de Decisão de Skills, Slash Playbooks, Skills Nativas & Skill Security Gate
*Objetivo: rotear automaticamente Domínio × Intenção × Plataforma com mínimo gasto de tokens, entregar `/layout`..`/scan-skill` e blindar skills externas antes da ativação.*

- [ ] **W7-01** `[Spec 12]` Criar detector e loader `.hivemind/project-profile.yaml` em `internal/skillrouter/profile.go` (`saas_product`, `finance_erp`, `services`, `sales_ecommerce`, `marketing_landing`, `mobile_android`, `game_*`, `backend_edge`).
- [ ] **W7-02** `[Spec 12]` Implementar `SkillRouter` (`internal/skillrouter/router.go`) com heurística < 1 ms + fallback Jev opcional, aplicando direto em `full_access` (chip editável) e sugerindo confirmação rápida em `governed`/`auto_edit`.
- [ ] **W7-03** `[Spec 12]` Criar Playbooks declarativos (`/layout`, `/redesign`, `/arch`, `/review`, `/sec`, `/mobile`, `/game`, `/audio`, `/brain`, `/scan-skill`) injetando até 2 *Hybrid Skill Stubs* sem invalidar KV-cache.
- [ ] **W7-04** `[Spec 12]` Autorar skills nativas em `internal/skillx/seeds/` (`ui-craft-product`, `ui-art-direction`, `ui-redesign-audit`, `security-cloudflare`, `arch-clean`, `code-review-rigor`, `android-kotlin-compose`, `gamedev-core`) + [`NOTICE.md`](../../internal/skillx/seeds/NOTICE.md).
- [ ] **W7-05** `[Spec 12]` Implementar **Skill Security Gate** nativo em `internal/skillx/scanner.go` (score `0–100`, prompt injection, exfiltração de cofre/env, `curl | sh`) integrado ao importador/editor de skills da IDE e CLI.
- [ ] **W7-06** `[Spec 12]` Adicionar ponte opcional `NVIDIA/SkillSpector` (CLI/MCP + SARIF) e auditoria de skills no `.github/workflows/ci.yml`.
- [ ] **W7-07** `[Spec 12]` Adicionar autocomplete `/playbooks` + chip da decisão no composer da IDE e relatório visual do scanner em Skills (Constitution `D1–D9`).
- [ ] **W7-08** `[Verificação Onda 7]` Testes do roteador por domínio/intenção/permissão, testes do scanner bloqueando skills maliciosas, `check-design-tokens`, `tsc`, `npm test` e `go test`.

---

### Onda 8 — Mente Coletiva do Projeto (`ProjectBrain` + Graphify Oficial)
*Objetivo: substituir a memória isolada por sessão por um grafo persistente do projeto compartilhado entre todas as equipes/lanes, testando Graphify oficial com fallback nativo Go/SQLite se ficar pesado.*

- [ ] **W8-01** `[Spec 12]` Criar contrato `ProjectBrain` em `internal/brain/` (`EnsureIndexed`, `QueryContext`, `RecordLearnings`, `InvalidateStale`, `Stats`) com storage local fora do repo em `%LOCALAPPDATA%\HiveMind\brains\<project-hash>\`.
- [ ] **W8-02** `[Spec 12]` Registrar `graphify` (`graphifyy[mcp]`) em `internal/mcpx/defaults.go` e implementar `GraphifyBrain` com telemetria de latência/RAM + invalidação incremental via `FileWatcher`.
- [ ] **W8-03** `[Spec 12]` Implementar camada semântica local (`collective.db` SQLite: `Decision`, `DomainRule`, `Gotcha`, `DesignRule`, `Feature`) com proveniência (`commit`, `sessionID`, `fileHashes`) e invalidação automática por drift.
- [ ] **W8-04** `[Spec 12]` Integrar `QueryContext` (com `TokenBudget` rígido) + extração pós-QA no `internal/orchestrator/` e adicionar UI **Mente do Projeto** + `/brain` na IDE *(sync via Git `.hivemind/brain/` reservado como atualização futura pós-Onda 10)*.
- [ ] **W8-05** `[Spec 12]` Medir benchmark do Graphify em repo grande (> 15s index / > 500ms query p95 / > 400MB RAM) e acionar implementação nativa Go + SQLite FTS5 atrás de `ProjectBrain` caso exceda os limites.
- [ ] **W8-06** `[Verificação Onda 8]` Validar compartilhamento de decisões/gotchas entre duas equipes/lanes independentes no mesmo projeto e invalidação após modificação de arquivo.

---

### Onda 9 — Agent Hive Conectado (`Obscura`, Canais Públicos & `StrictNetworkGuard`) + Jev Free-Tier
*Objetivo: dar visão web leve e leitura pública ao Residente com bloqueio estrito de hosts desligado por padrão (mas mantendo SSRF/vault/robots/`WrapUntrustedData`), e ampliar provedores gratuitos/OpenAI-compatible no Jev.*

- [ ] **W9-01** `[Spec 12]` Adicionar `StrictNetworkGuard bool` (`false` por padrão) em `internal/resident/` (`types.go`, `broker.go`, `netguard.go`) e `ide/` com toggle na UI, mantendo sempre anti-SSRF, zero acesso ao `secretx`, `--obey-robots` e `WrapUntrustedData`.
- [ ] **W9-02** `[Spec 12]` Adicionar `obscura` (`h4ckf0r0day/obscura`) em `internal/mcpx/defaults.go` + abstração `HeadlessBrowserProvider` (exclusão mútua vs `playwright`/`chrome-devtools` + screenshots leves).
- [ ] **W9-03** `[Spec 12]` Implementar canais públicos read-only estilo `Agent Reach` (`web_markdown`, `youtube_transcript`, `rss_feed`) + `ChannelDoctor()` na UI do Residente/Orb (vetando explicitamente extração de cookies do navegador pessoal).
- [ ] **W9-04** `[Spec 12]` Adicionar provedores **Groq**, **Cerebras**, **NVIDIA NIM** e **Mistral** em `internal/modelx/` + preset `free_economy` com failover em 429 no `internal/jev/`.
- [ ] **W9-05** `[Spec 12]` Adicionar provedor **Custom OpenAI-Compatible** na IDE (Ollama, LM Studio, LiteLLM, `freellmapi`) integrado ao cofre `secretx` e telemetria de tokens.
- [ ] **W9-06** `[Verificação Onda 9]` Validar `StrictNetworkGuard=false` vs `true`, bloqueio SSRF/vault, `Obscura` mockado, canais públicos encapsulados em `WrapUntrustedData` e failover Jev `free_economy`.

---

### Onda 10 — Kits Especializados (Android/Kotlin Compose & GameDev Studio + Áudio Local)
*Objetivo: equipar o orquestrador com ferramentas reais sob demanda para apps Android Kotlin/Compose e desenvolvimento gratuito/open-source de jogos (Godot, Unreal, Web e trilha local ACE-Step 1.5).*

- [ ] **W10-01** `[Spec 12]` Implementar Kit **Android/Kotlin** em `internal/kits/android/` (`adb devices`, `logcat` bounded, `screencap`, `uiautomator dump` e runner `./gradlew testDebugUnitTest lintDebug` com parser estruturado).
- [ ] **W10-02** `[Spec 12]` Integrar `/mobile` com inspeção visual do emulador Android Compose (screenshot + árvore de UI anexados antes da aprovação QA).
- [ ] **W10-03** `[Spec 12]` Implementar `internal/kits/gamedev/` + preset de time `game-studio` no Wizard (inspirado em `Donchitos/Claude-Code-Game-Studios`).
- [ ] **W10-04** `[Spec 12]` Registrar `godot-mcp` (`Coding-Solo/godot-mcp`) e `unreal-claude` (`Natfii/UnrealClaude`) em `internal/mcpx/defaults.go` (desligados por padrão) + runner headless para Godot e Web Games (**Phaser/Three.js/PixiJS/Bevy-WASM**).
- [ ] **W10-05** `[Spec 12]` Implementar conector local opcional **`ace-step/ACE-Step-1.5`** (`/audio` parametrizado por BPM/tom/duração/tags, exportação segura em `assets/audio/`, licença MIT).
- [ ] **W10-06** `[Spec 12]` Expor badges e configuração de Kits na IDE conforme `.specify/memory/constitution.md` (`D1–D9`).
- [ ] **W10-07** `[Verificação Onda 10]` Suíte completa dos Kits Android (mocks ADB/Gradle) e GameDev (Godot/Web/`game-studio`/`ACE-Step-1.5`), `go vet`/`go test` raiz e IDE, `check-design-tokens`, `tsc`, `npm test` e `npm run build`.
