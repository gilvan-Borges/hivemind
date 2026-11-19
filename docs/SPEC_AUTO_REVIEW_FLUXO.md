# Spec — Revisão Automática do Fluxo de PRs (Auto-Review sem Merge)

**Status:** Implementada  
**Data:** 2026-11-19  
**Escopo:** `internal/team`, `internal/tools`, `internal/orchestrator`, `ide/internal/idecore`, `ide/app.go`, `ide/frontend`

---

## 1. Visão Geral & Objetivo

Permitir que o usuário ligue um interruptor **"Revisão Automática"** na aba **Git → Fluxo** da HiveMind IDE. Enquanto ligado:

1. A IDE monitora periodicamente os Pull Requests pendentes na aba **Fluxo** (reaproveitando `idecore.ListProjectPullRequests` + `computePRFlowState`).
2. Assim que detectar um PR de terceiro aguardando atenção (`Sua revisão`, `Re-revisão`, `Nova resposta` ou `Novo commit`), a equipe inicia a revisão **automaticamente** em uma lane dedicada, **sem atrapalhar o trabalho manual do usuário**.
3. O agente pode **investigar o projeto inteiro**, **executar testes reais da branch do PR** em uma worktree isolada e efêmera, e **publicar a revisão/comentários no GitHub**.
4. **Regra absoluta (Hard-Gate em código):** no modo automático, **é tecnicamente impossível mesclar qualquer PR**, commitar, dar push ou alterar a branch local do usuário.

---

## 2. Garantias de Segurança (Hard-Gates — Nunca Apenas Prompt)

Para garantir que nenhum erro de modelo ou prompt consiga fazer merge no modo automático, o bloqueio é aplicado em **4 camadas independentes**:

| Camada | Garantia | Onde |
| --- | --- | --- |
| **1. Remoção física da ferramenta** | `tools.Deps.DisableMerge = true` omite completamente `github_merge_pr` do catálogo de ferramentas do agente. O modelo literalmente não enxerga a tool. | `internal/tools/tools.go` (`addGitHubGovernance`) |
| **2. Bloqueio de escrita Git** | `tools.Deps.DisableGitWrite = true` omite `git_commit`, `git_push`, `git_pull` e `github_create_pr`. | `internal/tools/tools.go` |
| **3. Shell isolado para testes** | Se testes estiverem ativos (`run_tests: true`), `run_shell` é liberado **somente em modo somente-leitura/teste**, mantendo `shellx.IsCommitOrPRCommand` bloqueando `git merge`, `git commit`, `git push`, `gh pr merge`, etc., e sem exigir pop-up humano infinito (timeout curto por comando + allowlist). | `internal/tools/tools.go`, `internal/shellx/policy.go` |
| **4. Worktree efêmera descartável** | Quando `run_tests: true`, o PR é baixado em `.hivemind/worktrees/_autoreview_pr_<N>` via `git fetch origin pull/<N>/head` em *detached HEAD* e destruído ao final (`git worktree remove --force`). O checkout e as edições locais do usuário **nunca são tocados**. | `ide/internal/idecore/autoreview.go` |

---

## 3. Elegibilidade & Prevenção de Loops (Quando um PR entra na fila)

Um PR da lista `ListProjectPullRequests(ctx, workspace)` entra na fila automática **somente se atender a TODOS os critérios**:

1. `!pr.IsMine` — nunca revisa PR de própria autoria (governança HiveMind).
2. `!pr.IsDraft` — ignora PRs em rascunho (`DRAFT`), a menos que o usuário marque explicitamente `include_drafts: true`.
3. `pr.NeedsAttention || (pr.ReviewRequested && pr.MyResponse == "")` — exatamente os itens da seção **Revisões Pendentes** da aba **Fluxo** (`Sua revisão`, `Re-revisão`, `Nova resposta`, `Novo commit`).
4. **Deduplicação por Fingerprint (`stateKey`):**
   - Cada PR processado grava em `.hivemind/auto-review-state.json` seu último fingerprint revisado:
     `prNumber + ":" + headSHA + ":" + lastActivityTimestamp` (ou `updatedAt` + `attentionReason`).
   - Se o fingerprint não mudou desde a última revisão automática, o PR é **pulado** (zero re-revisões duplicadas, mesmo após reiniciar a IDE).
   - Se o autor empurrar um **novo commit** ou responder um comentário, o fingerprint muda e o PR volta à fila automaticamente para **Reavaliação**.
5. **Backoff em caso de falha:** se a revisão de um PR falhar (erro de rede/modelo/quota), ele entra em cooldown exponencial (`5 min → 15 min → 60 min`, máx. 3 tentativas por fingerprint) para não queimar tokens em loop.

---

## 4. Arquitetura Técnica

### 4.1 Configuração Persistida (`internal/team/team.go`)

Novo bloco opcional `auto_review` dentro de `settings` no `.hivemind/team.yaml`:

```yaml
settings:
  auto_review:
    enabled: false                 # toggle principal (também controlável na UI)
    poll_interval_seconds: 120     # padrão: 120s (mínimo 60s)
    run_tests: true                # clona PR em worktree efêmera e permite rodar testes
    review_action: "full_review"   # "full_review" (APPROVE/REQUEST_CHANGES/COMMENT) | "comment_only" (sempre COMMENT)
    include_drafts: false          # padrão: ignora DRAFTs
    max_prs_per_hour: 6            # circuit breaker de volume
    max_tokens_per_pr: 600000      # teto de segurança por PR (reaproveita compactação in-turn)
    model: ""                      # opcional; vazio usa settings.review_model ou modelo do Chefe
```

### 4.2 Hard-Gates nas Ferramentas & Orquestrador (`internal/tools`, `internal/orchestrator`)

1. **`internal/tools/tools.go`**:
   - Adicionar campos em `tools.Deps`:
     - `DisableMerge bool` — quando `true`, `addGitHubGovernance` **não registra `github_merge_pr`**.
     - `ReviewCommentOnly bool` — quando `true`, `github_review_pr` força/valida `event == "COMMENT"` (recusando `APPROVE`/`REQUEST_CHANGES` se o usuário optou por `comment_only`).
     - `AllowReadOnlyTestShell bool` — quando `true` no papel `RoleProjectManager`, adiciona `run_shell` sujeito à trava `IsCommitOrPRCommand` (mesmo bloqueio dos desenvolvedores: nunca faz commit/push/merge/gh-pr-mutate) e sem `write_file`.
2. **`internal/orchestrator/orchestrator.go`**:
   - Novo `RunMode`: `"auto-review"`.
   - Quando `runMode == "auto-review"`:
     - Força `r.reviewRequested = true`, `DisableGitWrite = true`, `DisableMerge = true`, `AllowReadOnlyTestShell = cfg.Settings.AutoReview.RunTests`, `ReviewCommentOnly = (cfg.Settings.AutoReview.ReviewAction == "comment_only")`.
     - **Não** expõe `delegate_to_squad` (é revisão pura em modo chefe + testes na worktree do PR, nunca altera código para commitar).
     - Injeta no prompt o protocolo de revisão automática:
       - Ler `github_get_pr`, `github_pr_files`, `github_pr_diff`, `github_ci_status`.
       - Investigar o restante do repositório livremente com `search_text`, `find_files`, `read_file(start_line, end_line)`.
       - Quando `run_tests == true`, rodar os comandos de teste relevantes da stack (ex.: testes afetados pelo PR) na worktree efêmera e citar os resultados objetivos no parecer.
       - Publicar a revisão no GitHub via `github_review_pr` (e comentários inline/gerais quando útil).
       - **Proibição explícita:** nunca tentar mesclar; se o PR estiver aprovado e pronto, apenas registrar o parecer e deixar o merge para decisão humana.
     - Reaproveita automaticamente o `BeforeModelCallback` de compactação *in-turn* e `settings.review_model` / `auto_review.model`.

### 4.3 Daemon de Auto-Review na IDE (`ide/internal/idecore/autoreview.go`)

Novo componente `AutoReviewManager` acoplado ao `App` (um loop leve por workspace com auto-review ligado):

- **Estado exposto (`AutoReviewStatus`):**
  ```go
  type AutoReviewStatus struct {
      Workspace        string            `json:"workspace"`
      Enabled          bool              `json:"enabled"`
      State            string            `json:"state"` // "idle" | "polling" | "reviewing" | "paused_limit" | "error"
      ActivePR         int               `json:"activePr,omitempty"`
      ActivePRTitle    string            `json:"activePrTitle,omitempty"`
      QueueCount       int               `json:"queueCount"`
      ReviewedLastHour int               `json:"reviewedLastHour"`
      LastPollAt       string            `json:"lastPollAt,omitempty"`
      NextPollAt       string            `json:"nextPollAt,omitempty"`
      LastError        string            `json:"lastError,omitempty"`
      History          []AutoReviewEntry `json:"history,omitempty"` // últimas 20 revisões automáticas
  }
  ```
- **Ciclo de execução (1 PR por vez por projeto, em fila FIFO por antiguidade):**
  1. `tick()` chama `ListProjectPullRequests(ctx, workspace)`.
  2. Filtra elegíveis (`!IsMine`, `!IsDraft`, `NeedsAttention || ReviewRequested&&MyResponse==""`, fingerprint ainda não revisado, não está em cooldown).
  3. Verifica circuit breaker `ReviewedLastHour < MaxPRsPerHour`.
  4. Se houver PR elegível:
     - Se `run_tests == true`: prepara worktree efêmera `.hivemind/worktrees/_autoreview_pr_<N>` com `git fetch origin pull/<N>/head` + `git worktree add --detach` (e `defer` remove a worktree ao final!).
     - Se o fetch/worktree falhar (ex.: fork privado sem ref ou repo sem git local), faz fallback limpo para revisão estática no workspace sem travar.
     - Dispara `SessionManager.RunTurnWithOptions` na lane dedicada `"lane-autoreview"` com `runMode = "auto-review"`, `MaxTokens = effectiveMaxTokensPerPR`, `Confirm = autoReviewShellConfirmer` (permite apenas comandos da allowlist sem metacaracteres perigosos; recusa qualquer comando destrutivo automaticamente sem bloquear esperando clique!).
     - Ao concluir com sucesso: grava o fingerprint em `.hivemind/auto-review-state.json`, registra entrada em `History` (PR #, título, veredito, tokens, duração, timestamp), emite evento Wails `"hivemind:autoreview-status"` e dispara refresh da aba **Fluxo**.
  5. Ao desligar o toggle ou fechar a IDE (`shutdown()`): cancela imediatamente o contexto do loop + cancela a lane `"lane-autoreview"` + limpa qualquer worktree efêmera aberta.

### 4.4 Bindings Wails (`ide/app.go`)

- `GetAutoReviewStatus(workspace string) idecore.AutoReviewStatus`
- `SetAutoReviewConfig(workspace string, cfg team.AutoReviewSettings) (idecore.AutoReviewStatus, error)`
- `TriggerAutoReviewPoll(workspace string) (idecore.AutoReviewStatus, error)` *(botão "Verificar agora")*
- `StopActiveAutoReview(workspace string) bool` *(interrompe apenas o PR em andamento sem perder histórico)*
- Evento em tempo real: `"hivemind:autoreview-status"`

### 4.5 Interface do Usuário (`ide/frontend/src/git.ts`, `shell.ts`, `chat.ts`)

1. **Card compacto no topo da aba `Git → Fluxo`** (paleta Obsidian Graphite × Silver, zero roxo):
   - Switch **`Revisão Automática`** (`Ligada` / `Desligada`) + selo fixo **`Sem Merge`** (deixando claro visualmente que merge é bloqueado).
   - Status ao vivo: `Vigiando fluxo · próx. checagem em 1m 40s` ou `Revisando PR #718… (rodando testes)` com botão **`Verificar agora`** e **`Parar PR atual`**.
   - Mini-configuração expansível (chevron):
     - `[x] Rodar testes do PR em worktree isolada`
     - `Ação: [Parecer completo (Aprovar/Ajustes/Comentar) ▾ | Somente comentar]`
     - `Intervalo: [2 min ▾]` · `Limite: [6 PRs/hora]`
   - Log enxuto das últimas revisões automáticas (`#718 · Aprovado · 142k tok · 12:18`) clicável para abrir a sessão/parecer.
2. **Lane dedicada `auto-review` no painel AI direito:**
   - Criada automaticamente quando o primeiro PR automático começar, permitindo acompanhar as tool calls (`github_get_pr`, `search_text`, `run_shell` testes, `github_review_pr`) ao vivo **sem interferir na sua aba `principal`**.
3. **Indicador discreto na Status Bar:**
   - Chip clicável `auto-review: on (vigiando)` / `auto-review: #718` que abre direto a aba **Fluxo**.

---

## 5. Casos de Borda & Resiliência

1. **Comando perigoso ou travado durante testes no modo automático:**
   - No modo automático não existe humano clicando "Autorizar" — portanto `Confirm` retorna `false` para comandos perigosos (`rm -rf`, pipes destrutivos, etc.), e cada comando de teste tem timeout reduzido (`5 min` em vez de `20 min`) para nunca travar a fila.
2. **PR atualizado no meio da revisão:**
   - Se chegar um commit novo enquanto a revisão estava rodando, o fingerprint do estado novo será diferente do fingerprint capturado no início; no próximo ciclo, a fila detectará a novidade e fará uma reavaliação incremental.
3. **Sem autenticação GitHub ou sem quota LLM:**
   - Se o token GitHub expirar ou atingir 429/quota esgotada, o daemon entra em estado `paused_limit` com aviso claro no card do Fluxo (não fica retentando em loop).
4. **Múltiplos projetos abertos em abas:**
   - Cada workspace tem seu próprio estado e toggle independente (ligar no `IPG` não ativa automaticamente no `block-trail`).

---

## 6. Plano de Testes Obrigatórios

1. **`internal/tools/tools_test.go`**:
   - `TestAutoReviewToolsNeverExposeMerge`: com `DisableMerge: true, DisableGitWrite: true, AllowReadOnlyTestShell: true`, confirma que `github_merge_pr`, `git_commit`, `git_push`, `write_file` **NÃO existem**, enquanto `github_review_pr`, `github_comment_pr`, `search_text`, `find_files`, `read_file` e `run_shell` existem — e `run_shell` recusa `git merge`, `git push` e `gh pr merge`.
   - `TestReviewCommentOnlyBlocksApproveAndRequestChanges`: com `ReviewCommentOnly: true`, `github_review_pr` recusa `APPROVE` e `REQUEST_CHANGES` e aceita `COMMENT`.
2. **`internal/orchestrator/orchestrator_test.go`**:
   - `TestAutoReviewModeBlocksMergeAndDelegation`: executa `RunMode: "auto-review"` com modelo scriptado tentando chamar `github_merge_pr` e `delegate_to_squad` e verifica que ambas são impossíveis/bloqueadas.
3. **`ide/internal/idecore/autoreview_test.go`**:
   - `TestAutoReviewFilterAndFingerprintDedup`: ignora PR próprio e draft, seleciona PR pendente, grava fingerprint, ignora no 2º poll idêntico, re-enfileira quando `UpdatedAt`/commit muda.
   - `TestAutoReviewRateLimitAndBackoff`: respeita `max_prs_per_hour` e aplica cooldown após erro.
   - `TestAutoReviewEphemeralWorktreeCleanup`: cria worktree isolada para um PR em repo git temporário, executa comando de teste dentro dela e confirma que o checkout principal permanece intocado e a worktree é removida ao terminar.
4. **Frontend (`ide/frontend/src/`)**:
   - Testes Vitest de renderização/estado do controle de Auto-Review na aba Fluxo e badge na status bar.

---

## 7. Ordem de Implementação Sugerida

1. **Etapa 1 (Hard-Gates):** `team.AutoReviewSettings` + `tools.Deps` (`DisableMerge`, `ReviewCommentOnly`, `AllowReadOnlyTestShell`) + `RunMode: "auto-review"` no `orchestrator` + testes unitários.
2. **Etapa 2 (Daemon + Worktree Efêmera):** `ide/internal/idecore/autoreview.go` + persistência `.hivemind/auto-review-state.json` + bindings Wails em `ide/app.go` + testes unitários.
3. **Etapa 3 (UI na Aba Fluxo + Lane Dedicada):** card de Auto-Review em `git.ts`, lane dedicada `auto-review`, chip na status bar em `shell.ts`, evento `"hivemind:autoreview-status"` em `boot.ts`.
4. **Etapa 4 (Validação & Build):** `go test ./...`, `vitest`, `tsc && vite build` e novo instalador `HiveMindSetup-x64.exe`.
