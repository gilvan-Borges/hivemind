# Auditoria Arquitetural & Specs de Refatoração — HiveMind

> Revisão técnica completa do repositório `c:\hivemind` (Orquestrador, Jev/ModelX/Vault, IDE Go + Frontend TS, Git/GitHub/Jira, Relaywire, MCP, CLI/TUI, `HiveMind-App` e CI/Deploy), comparando a implementação atual com padrões de engenharia do **Google (SRE, ADK, IAM, Desktop/Mobile)** e da **Anthropic (Agent Harness, KV-Cache, Context Engineering, Sandboxing)**.
>
> **Documento vivo de execução (todas as pendências + o que já foi feito):** [**`PLANO_IMPLEMENTACAO.md`**](PLANO_IMPLEMENTACAO.md)

---

## Matriz de Achados e Especificações

| # | Spec | Escopo Principal | Severidade | Principais Achados |
|---|---|---|---|---|
| **01** | [01-orchestrator-modular-harness.md](01-orchestrator-modular-harness.md) | `internal/orchestrator/`, `internal/tools/`, `internal/resident/` | Alta | God-objects (`orchestrator.go` 3.114L, `tools.go` 1.695L, `broker.go` 1.028L), erros silenciosos em state/audit, side effect em leitura. |
| **02** | [02-prompt-cache-and-context-compaction.md](02-prompt-cache-and-context-compaction.md) | `orchestrator.go`, `pr_dossier.go`, `context_guard.go`, `sessionx/`, `mcp.go`, `handoff.go` | Crítica | Invalidação de KV-cache por mutação de `SystemInstruction`/`Tools` mid-turn, memória duplicada, compactação por contagem sem awareness de tokens, corte UTF-8 inseguro. |
| **03** | [03-concurrency-races-and-persistence.md](03-concurrency-races-and-persistence.md) | `sessionx/store.go`, `resident/routine.go`, `orchestrator.go`, `workspace/storage.go`, `reviewx/` | Crítica | Ponteiros compartilhados + mutação in-place em `session.Event`, I/O sob lock global, `routines.json` sem lock, TOCTOU em migração legada, escritas não-atômicas. |
| **04** | [04-tools-sandbox-and-pathsec.md](04-tools-sandbox-and-pathsec.md) | `internal/workspace/`, `internal/pathsec/`, `internal/tools/`, `internal/sandboxx/`, `shellx/` | Crítica | `write_file`/`edit_file`/`list_dir`/`find_files` sem `IsSensitivePath`, Windows AppContainer ignora `AllowNetwork` e deixa ACEs permanentes, Linux `bwrap` expõe `$HOME`, `run_shell` faz head-truncate. |
| **05** | [05-secrets-vault-and-multi-tenant-isolation.md](05-secrets-vault-and-multi-tenant-isolation.md) | `internal/modelx/`, `internal/secretx/` | Crítica | `modelx` salva chaves/OAuth em JSON plano; `WithAgyVaultLease` muta slot global `gemini:antigravity` sob lock local; failover do pool vaza entre organizações. |
| **06** | [06-jev-router-resilience-and-telemetry.md](06-jev-router-resilience-and-telemetry.md) | `internal/jev/`, `internal/modelx/`, `orchestrator/jev*.go` | Alta | Rede síncrona no hot-path; `HalfOpenInFlight` sem TTL vaza slot; split-brain de saúde sem file lock; erros por substring sem TypeSafe; `usage` ignora cache tokens/Cursor/CLI; bypass de `DailyUSD`. |
| **07** | [07-ide-architecture-services-and-reactive-ui.md](07-ide-architecture-services-and-reactive-ui.md) | `ide/app.go`, `ide/internal/idecore/`, `ide/frontend/src/` | Alta | God-structs (`app.go`, `diff.go` 2.668L); leaks ao fechar aba (`CloseProject` ausente); polling duplicado; frontend ignora `wailsjs`, `innerHTML` total destruindo foco/input e streaming `O(N²)`; XSS/a11y/CSS 7.128L. |
| **08** | [08-git-github-jira-and-multi-repo-delivery.md](08-git-github-jira-and-multi-repo-delivery.md) | `internal/gitx/`, `internal/githubx/`, `internal/jirax/`, `idecore/diff.go` | Média-Alta | `gitx.Local` sem env não-interativo completo; `ParseRemote` hardcoded `github.com` (sem GHES); zero paginação; `ListProjectIssues` muta `team.yaml`; evolução multi-repo no UI/PR linking. |
| **09** | [09-relay-e2ee-mcpx-and-runtime-unification.md](09-relay-e2ee-mcpx-and-runtime-unification.md) | `internal/relay/`, `relayclient/`, `remote/`, `internal/mcpx/`, `cmd/`, `cli/`, `tui/` | Crítica | Relay `TypeRPC` sem anti-replay; `relay.json` em texto plano; rate limiter apaga mapa (`>4096`); LAN bridge com `os.WriteFile` por `GET`; `mcpx` vaza `os.Environ()` sem trust gate; 4 wirings divergentes de `Runner`. |
| **10** | [10-mobile-companion-supply-chain-and-ci.md](10-mobile-companion-supply-chain-and-ci.md) | `HiveMind-App/`, `scripts/`, `.github/workflows/ci.yml`, `deploy/relay/` | Alta | `HiveMind-App` sem modos SDD/Resident nem cache offline (Room); `usesCleartextTraffic="true"` global; R8/signing debug; zero auto-update (drift IDE vs `AppData`); CI sem `./internal/remote`, `-race` ou Gradle. |
| **11** | [11-external-workers-worktrees-and-ux.md](11-external-workers-worktrees-and-ux.md) | `internal/orchestrator/`, `internal/workerx/`, `internal/gitx/`, `internal/resident/`, `ide/` | Média-Alta | Workers CLI externos (Cursor), `.worktreeinclude` & checkpoints, `SOUL.md`/webhooks/Slack, custo USD, PTY+Split Panes, Design Mode CDP, Hybrid Skill Stubs e Race Mode. |
| **12** | [12-skill-router-project-brain-and-kits.md](12-skill-router-project-brain-and-kits.md) | `internal/skillrouter/`, `internal/skillx/`, `internal/brain/`, `internal/resident/`, `internal/mcpx/`, `internal/modelx/`, `internal/jev/`, `internal/kits/`, `ide/` | Estratégica | Motor de Decisão (`Domínio × Intenção × Plataforma`), Slash Playbooks, Skill Security Gate (`NVIDIA/SkillSpector`), Mente Coletiva (`Graphify` + layer semântica local), `Obscura`/Agent Reach/`StrictNetworkGuard=false`, Jev Free-Tier e Kits Android/Kotlin & GameDev Studio. |

---

## Ordem Recomendada de Execução

1. **Onda 1 — Segurança, Dados & Confiabilidade (Risco Crítico):** Specs **04**, **05**, **09**, **03** *(Concluída)*
2. **Onda 2 — Economia de Tokens, Latência & Roteamento Jev:** Specs **02**, **06** *(Concluída)*
3. **Onda 3 — Modularização Go, Runtime Único & VCS Delivery:** Specs **01**, **08**, **09** *(Concluída)*
4. **Onda 4 — IDE Reativa, Lifecycle, Blame/Conflitos & WSB:** Spec **07** *(Concluída)*
5. **Onda 5 — Mobile Offline/Paridade, Auto-Update & CI/Deploy:** Spec **10** *(Concluída)*
6. **Onda 6 — Workers CLI Externos, Worktrees & UX Avançada:** Spec **11** *(Concluída)*
7. **Ondas 7–10 — Motor de Decisão de Skills, Mente Coletiva (`Graphify`), Agent Hive Conectado, Jev Free-Tier & Kits Android/GameDev:** Spec **12** *(Planejada — executar Onda 7 → 8 → 9 → 10)*
