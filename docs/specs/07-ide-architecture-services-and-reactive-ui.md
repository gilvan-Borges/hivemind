# Spec 07 — Desacoplamento da IDE (`idecore`), Ciclo de Vida de Projetos e UI Reativa

> **Status:** Parcialmente Implementado (Onda 4 concluiu `W4-06`/`W4-07`: render guards, markdown incremental, `safeImageSrc`, CSP, ARIA focus-trap e CSS `@layer`; pendentes `W4-01`, `W4-02`, `W4-05`, `W4-08`)
> **Alvos:** `ide/app.go`, `ide/internal/idecore/`, `ide/frontend/src/`, `ide/frontend/index.html`

---

## 1. Diagnóstico do Estado Atual

### Backend Go (`ide/app.go` & `ide/internal/idecore/`)
1. **God-Objects:**
   - `ide/app.go` (1.834 linhas) expõe ~90 métodos Wails no único struct `App` misturando janela/orb/tray, FS, Git/diff, GitHub/Jira, OAuth multi-conta, PTY, LSP, Jev monitor, LAN bridge, Relay client e Resident.
   - `ide/internal/idecore/diff.go` (2.668 linhas) aglomera 5 domínios não relacionados (parser de diff, sync worktree->workspace, Git hunk/commit/graph, heurísticas/JQL do Jira e GraphQL/REST do GitHub).
   - `ide/internal/idecore/runner.go` (1.188 linhas) duplica closures de roteamento/uso entre `RunTurnWithOptions` (`864-885`) e `RunAutoReviewTurn` (`1096-1117`), além de subir/derrubar `mcpx.Start` em todo turno.
2. **Leaks ao Fechar Projeto:**
   - `App.OpenProject` (`ide/app.go:433-491`) inicia `FileWatcher`, `autoReview.SyncWorkspace` e `githubMonitor.EnsureWatching`, mas **não existe `CloseProject(workspace)`**; ao fechar uma aba no frontend (`shell.ts:44-95`), apenas `CancelRun` é chamado, deixando watchers `fsnotify`, pollers GitHub e processos LSP (`gopls`) vazando até o encerramento da IDE.
3. **Polling Duplicado & Cegueira a `.git`:**
   - `GitHubMonitor` (`github_watch.go:16`) faz polling de PRs a cada `60s` e `AutoReviewManager` (`autoreview.go:446`) faz polling independente do mesmo endpoint a cada `120s`.
   - `FileWatcher` (`watcher.go:193, 215`) ignora `.git`; commits/checkouts feitos no terminal não atualizam a aba Git automaticamente.

### Frontend TypeScript (`ide/frontend/src/`)
1. **Bindings Wails Ignorados:**
   - Wails gera tipagens em `ide/frontend/wailsjs/go/main/App.d.ts`, mas zero arquivos em `src/` as importam — `types.ts` (1.221 linhas) duplica manualmente `WailsBackendApp` com risco de drift silencioso.
2. **Re-renderização Total via `innerHTML`, Streaming `O(N²)` e Perda de Foco/Input:**
   - `renderLeftSidebar()` (65 call sites) e `renderModalLayer()` (81 call sites) substituem `.innerHTML` completo e re-registram listeners do zero.
   - `boot.ts:438, 476` e `git.ts:273` disparam `renderLeftSidebar()` em eventos assíncronos de fundo (polling de 60s!), destruindo foco e texto digitado em inputs de commit/busca; `boot.ts:523, 539, 545` e `modals.ts:117, 131` chamam `renderModalLayer()` assincronamente apagando tokens recém-colados pelo usuário (`#gh-api-token`, `#ak-*`).
   - `scheduleChatFlush()` (`chat.ts:866-896`) -> `updateMessageElement` re-parseia **todo** `m.text` acumulado via `renderMarkdownLite` (`markdown.ts:17-57`) a cada frame de streaming (`O(N²)` em respostas longas).
   - `refreshExplorerTree()` (`explorer.ts:98-117`) limpa `state.dirCache` e executa `await ListDirectory` sequencial para toda pasta expandida em cada evento de filesystem.
3. **Higiene XSS, Acessibilidade e CSS Monolítico:**
   - `modals.ts:1162` renderiza `<img src="${escapeHtml(pr.qrDataUrl)}">` sem `safeImageSrc()`; `markdown.ts` usa regex frágil em HTML escapado e não roteia links por `OpenExternalURL`; `index.html` não declara CSP estrita.
   - Linhas interativas em `explorer.ts`, `editor.ts`, `shell.ts`, `lanes.ts`, `git.ts` são `<div>` sem `tabindex`/`role`/teclado; modais não fazem focus trap nem devolvem foco ao fechar.
   - `styles.css` possui **7.128 linhas (153,9 KB)** e **30 `!important`** sem camadas `@layer` ou escopo por componente.

---

## 2. Comparação: HiveMind Atual vs Engenharia Google / Anthropic Desktop IDEs

| Camada | HiveMind Atual | Padrão Moderno (Cursor / Antigravity / VS Code) |
|---|---|---|
| **Contrato Backend ↔ UI** | Struct `App` única + tipagem TS manual duplicada | Serviços coesos por domínio + import direto de `wailsjs` (type-safety garantido no compile) |
| **Ciclo de Workspace** | Recursos anexados no `OpenProject` vazam ao fechar aba | Registro ref-counted `OpenWorkspace`/`ReleaseWorkspace` encerrando watchers, pollers e LSPs |
| **Renderização & Estado** | `state.ts` mutável + `innerHTML` global apagando inputs | Store reativa granular (signals/VDOM leve) + listas virtualizadas; inputs preservam estado independentemente de atualizações de status |
| **Streaming Markdown** | Re-parse completo do markdown a cada chunk (`O(N²)`) | **Parser incremental por bloco**: blocos fechados são congelados/memoizados; só o bloco ativo na cauda re-renderiza |
| **Segurança WebView** | `innerHTML` direto + regex markdown sem CSP | CSP restrita no `index.html` + sanitizador estrito (`DOMPurify` ou construção DOM) |

---

## 3. Especificação Técnica da Refatoração

### 3.1 Backend `ide/`
1. Particionar `ide/app.go` em controladores de serviço ligados no startup do Wails (`WorkspaceService`, `GitService`, `AgentService`, `ConnectorsService`, `RemoteService`) e quebrar `diff.go` em `git_diff.go`, `worktree_sync.go`, `git_ops.go`, `jira_discovery.go`, `github_prs.go`.
2. Implementar `CloseProject(workspace string)` em `ide/app.go` chamado por `closeProjectTab()` (`shell.ts`): quando a contagem de abas abertas para aquele diretório chega a 0, interromper `FileWatcher`, remover de `AutoReviewManager`/`GitHubMonitor` e encerrar LSPs associados.
3. Unificar o cache de PRs entre `GitHubMonitor` e `AutoReviewManager` (single poller com barramento pub/sub interno); observar `.git/HEAD` e `.git/index` (debounced 250ms) no `FileWatcher` para emitir evento `hivemind:git-changed` (inclusive para sub-repositórios aninhados!).

### 3.2 Frontend `ide/frontend/`
1. Gerar e importar exclusivamente `wailsjs/go/main/App` (eliminando declarações manuais duplicadas de métodos em `types.ts`).
2. **Isolamento de Render & Preservação de Inputs (curto prazo antes/durante migração reativa):**
   - Nunca chamar `renderLeftSidebar()` nem `renderModalLayer()` de listeners assíncronos quando um input dentro da região alvo está focado/sujo: atualizar apenas os nós de status/lista (`#team-agents-list`, `#pr-list-box`, badges) via patches direcionados.
   - Migrar gradualmente os painéis críticos (`chat.ts`, `modals.ts`, `git.ts`, `explorer.ts`) para renderização granular (Signals/Preact ou DOM reconciliation leve) com renderização **incremental por blocos** no `markdown.ts` e virtualização do chat/árvore/graph.
3. **XSS & Acessibilidade:** aplicar `safeImageSrc` em `modals.ts:1162`, adicionar CSP em `index.html`, rotear links markdown externos via `OpenExternalURL`, converter nós clicáveis em `<button>`/`role="treeitem"` com navegação por teclado e implementar focus trap + restauração de foco nos modais.
4. **Modularização CSS:** dividir `styles.css` em tokens base + `@layer base, layout, components, utilities` (zero `!important` novos).

---

## 4. Critérios de Aceite
- Abrir e fechar 10 abas de projeto mantém estável a contagem de goroutines, handles `fsnotify` e processos `gopls`.
- Digitar uma mensagem de commit ou colar um PAT durante eventos `hivemind:github-sync` / `bootstrapAsync` preserva 100% do foco, seleção e valor digitado.
- Streaming de uma resposta de 5.000 linhas mantém tempo de frame `rAF < 8ms` (parser incremental de blocos).
- `npx tsc --noEmit` usando `wailsjs` gerado, `check-design-tokens.mjs`, `npm test` e `npm run build` passando sem erros.
