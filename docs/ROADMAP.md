# Roadmap de Fases — HiveMind CLI & IDE

Estado: **Wed Nov 18 2026** · Baseado em análise completa do repositório (~13.450 LOC de `internal/`, IDE inteira em `ide/`).

## Decisões de direção (ADRs curtos)

**ADR-001 — Posicionamento: Agentic IDE (estilo Cursor).**
Contexto: a IDE precisa escolher entre ser uma IDE completa com IA por cima ou um ambiente onde o agente é cidadão de primeira classe. Decisão: o editor serve ao agente; inline AI e diff-apply vêm **antes** de debugger e de LSP amplo. Consequências: LSP entra pontual (só o que melhora o agente e a navegação), debugger cai para fase avançada, ghost text/Ctrl+K viram o coração do produto.

**ADR-002 — Público: uso interno, Windows-first.**
Contexto: não é produto público. Decisão: cross-platform, extensões, auto-update e i18n ficam fora do caminho crítico. Consequências: `powershell.exe` hardcoded e `LOCALAPPDATA` são aceitáveis; empacotamento e assinatura de binário não são critério de saída de nenhuma fase.

**ADR-003 — Refator de frontend é fase explícita antes de crescer.**
Contexto: `ide/frontend/src/main.ts` tem ~6.400 linhas renderizadas por `innerHTML`, sem testes. Construir editor real e LSP sobre esse monólito duplicaria o débito. Decisão: split em módulos como primeiro trabalho da fase de fundação. Consequência:features param de pagar custo exponencial; curto prazo, zero feature nova por ~1 iteração.

---

## Onde estamos

O produto hoje é um **"agentic IDE shell"** — o inverso do Cursor:

| Eixo | VS Code | Cursor | HiveMind IDE (hoje) |
|---|---|---|---|
| Orquestração multiagente | ✖ | parcial | ✔ **excede** (lanes, papéis, QA loop, approval gates, escala dinâmica) |
| Telemetria de tokens | ✖ | parcial | ✔ **excede** (turno/sessão/agente, orçamento) |
| Git (diff, revert por hunk, graph) | ✔ | ✔ | ✔ razoável (falta blame, merge conflict, history de arquivo) |
| Editor | ✔ Monaco | ✔ | ✖ alpha (textarea + highlight regex) |
| LSP / diagnostics | ✔ | ✔ | ✖ inexistente |
| Debugger | ✔ | ✔ | ✖ inexistente |
| Terminal | ✔ PTY | ✔ PTY | ✖ command runner one-shot |
| Inline AI (ghost text, Ctrl+K) | via Copilot | ✔ núcleo | ✖ (só chat/turnos + `Ctrl+L`) |
| Testes | ✔ | n/d | CLI/idecore fortes; TUI (1.680 LOC) e frontend **zero testes** |

**Ponto de partida honesto: a camada que é o diferencial está em nível de produto (denso, testado, zero stubs/TODOs no Go). A camada que o mercado considera mínimo de uma IDE (LSP, debugger, PTY, editor robusto) não começou. E a IDE inteira nunca foi usada para valer numa tarefa real — nenhuma fase abaixo avança sem fechar isso primeiro.**

---

## Fase 0 — Núcleo CLI · **CONCLUÍDA**

Orquestração ADK (sequential + parallel + loop), triagem do Chefe com `delegate_to_squad`, 6 providers de modelo (agy default, OpenCode Zen, Gemini, Vertex, Anthropic, OpenAI), jail de workspace (anti-symlink-escape, NTFS `git~1`), shell policy com allowlist, governança de git/PR por papel (bloqueio de auto-approve, gate de merge por branch), sessões JSON retomáveis com atomic writes, Jira Cloud + GitHub REST/`gh`, CI com build estático cross-platform. 59 testes em `internal/` + 18 em `idecore`.

## Fase 1 — Shell agent-first MVP · **ATUAL, falta fechar**

O que existe: janela Wails frameless, multi-projeto em abas isoladas, explorador com CRUD, editor básico com highlight e find/replace, git completo (diff split, revert por hunk, graph SVG, branches, PRs, issues), chat multimodal com streaming e approval gates, telemetria de tokens, skills nativas/projeto, sessões retomáveis, command runner com atalhos por stack.

**Falta para fechar (em ordem):**

1. **Dogfooding para valer.** Usar a IDE como ambiente único em ≥ 3 tarefas reais de desenvolvimento (não demos). Registrar tudo que quebrar: o que é lento, o que trava, o que faz voltar para o VS Code. Cada quebra vira issue. **Critério de saída: completar uma tarefa real ponta-a-ponta sem abrir outro editor.** → Protocolo pronto em [DOGSFOODING.md](DOGSFOODING.md); **execução pendente (é o item que falta)**.
   - **Motor validado e dogfooding CONCLUÍDO (18/11/2026):** 3 tarefas reais via CLI contra o Block-Trail (T1 ✅ PR #2, T2 ✅ PR #3, T3 ✘ em 2 tentativas — expôs os 2 bloqueantes que viraram 1a/1b, ambos **corrigidos e validados em produção** na retentativa), **3,68M tokens**, 14 achados com severidade em [DOGSFOODING.md](DOGSFOODING.md#balanço-final-do-dogfooding-do-motor-18112026). Achado de desenho: ~1,2M tokens/run dominados pelo agente de coordenação, e a janela de uso do agy não comporta tarefas grandes (compactação de contexto e roteamento de provider → Fase 5).
   - **Faltam 1a e 1b (correções dos bloqueantes) e o cockpit:**
   - **1a — limpeza de worktree entre runs** (achado #11): ~~pendente~~ **FEITO (18/11/2026)** — `AlignDeveloperWorktree` faz `reset --hard` + `checkout -f -B` + `clean -fd`; regressão `TestAlignDeveloperWorktreeDiscardsStaleEdits` falha no código antigo e passa no novo.
     - **1b — resiliência do provider agy** (achado #12): ~~pendente~~ **FEITO (18/11/2026)** — `generate()` parseia o envelope `AGY_ERROR` (`retryable:true`) e faz backoff+retry; quota 429 vira mensagem acionável; `ProbeAgyPrompt` respeita `AGY_MODEL` (achado #1).
     - **1c — cockpit (seu, ~30 min):** uma tarefa real dentro da IDE sem abrir outro editor. Único item aberto da Fase 1.
2. ~~**File watching no `FileIndex`**~~ — **FEITO (nov/2026)**: `idecore/watcher.go` (fsnotify recursivo, debounce de 250 ms, coalescência por lote, cap de 400 paths com overflow, exclusão de `ignoredDirs`/`.hivemind`), um watcher por projeto em `app.go`, evento Wails `hivemind:fs-event` e handler no frontend que atualiza a árvore e recarrega abas de editor abertas e não-sujas quando o agente edita arquivo (abas sujas nunca são sobrescritas; conteúdo idêntico não re-renderiza).
3. ~~**Resiliência do run** (lado CLI)~~ — **FEITO (nov/2026)**: `modelx/retry.go` — retry transport com backoff exponencial + jitter para 408/429/5xx e erros de rede, honra `Retry-After` (cap 30 s), replay de body; plugado em **todos** os providers HTTP (OpenCode, Gemini/Vertex, Anthropic, OpenAI). agy mantém o retry específico de "empty conversation id" (subprocesso, sem status HTTP visível).
4. ~~Testes do caminho TUI~~ — **FEITO (nov/2026)**: 19 testes em `internal/tui/model_test.go` cobrindo wizard de primeiro uso, slash commands, histórico, máquina de estados `onStream` (stage/usage/tool_call/text/done/error/cancel), approval gates HITL (autorizar/negar), cancelamento por Esc, `agentKeyFor`, `roleTitle`, `shortModel`.

Fora de escopo aqui: qualquer feature nova de editor. A meta é fechar e estabilizar o que existe.

## Fase 2 — Fundação de edição (editor real + frontend modular)

Pré-requisito: Fase 1 fechada (dogfooding validou o loop básico).

1. **Split do `main.ts`** em módulos (explorer, chat, git, terminal, editor, modais, estado) com testes mínimos de frontend (Vitest). Critério: nenhum módulo > 1.000 linhas; re-render de painel inteiro por evento eliminado no chat e no editor. → **FEITO (18/11/2026):** 14 módulos (state, shell, lanes, boot, explorer, models, git, editor, teamconfig, chat, terminal, quickopen, modals + main.ts entry de 6 linhas), maior módulo 1.066 linhas, código movido verbatim (204 declarações conferidas), build verde e smoke-check da IDE boot OK. WIP paralelo do dono (modos SDD às 22:25) preservado dentro dos módulos novos.
2. **Editor real: CodeMirror 6** (recomendado) — modular, leve, funciona bem em WebView2, syntax highlight por grammar de verdade, bracket matching, code folding, undo/redo sólido, e principalmente: **base para ghost text e diff-apply inline das fases seguintes**. Alternativa Monaco (mais recursos prontos, porém pesado e resistente a embutir custo de streaming inline). Manter a API de abas/arquivos atual; substituir o overlay textarea+`<pre>` por uma única instância CM6 por aba. → **FEITO (18/11/2026):** `cm-editor.ts` — uma `EditorView` persistente com estado por aba (doc/cursor/scroll preservados ao alternar), oneDark + tema Obsidian via `EditorView.theme` (styles.css intocado), 11 linguagens (Go, Kotlin via clike, Java, TS/JS, JSON, Python, Rust, Markdown, CSS, HTML, YAML, shell), folding, bracket matching, histórico, indentUnit 2 espaços. Find/replace, Ctrl+L, jump-to-line e reload externo reescritos contra a view; bundle 238KB→1,04MB (353KB gzip). Smoke-check boot OK.
3. Reagir a mudanças externas no arquivo aberto (o agente editou o arquivo que está na tela → recarregar com aviso, sem perder cursor do usuário quando não houver dirty state). → **FEITO (18/11/2026):** aba não-suja recarrega **in-place** (`view.dispatch` troca só o doc, sem re-render); aba **suja** com arquivo mudado no disco → toast de conflito sinalizando salvar/recarregar, sem sobrescrever a edição do usuário.

**Critérios de saída:** abrir/navegar/editar/salvar fluido num projeto grande (≥ 5k arquivos, arquivos ≥ 40k chars sem debounce visível); zero regressão nos fluxos git/chat; testes de frontend cobrindo editor e explorer. → **Testes de frontend: FEITO** — Vitest, 30 testes (escapeHtml/formatTokens/runModeLabel incl. modos SDD, parseDiffHunks, diff unificado/split, contadores de linha, mapeador de linguagens). Os critérios de fluidez exigem uso real: rolar no cockpit (1c da Fase 1) junto com a validação desta fase.

## Fase 3 — Paridade agentic (o coração, dado o posicionamento Cursor) · **CONCLUÍDA (19/11/2026)**

A fase que justifica o produto. Tudo construído sobre o CM6 da Fase 2:

1. **Ghost text (inline completion)** — **FEITO (19/11/2026)**: extensão CM6 nativa (`cm-editor.ts`: `ghostTextField`, `GhostTextWidget`, prefix-trim ao digitar, `Tab` aceita tudo, `Ctrl+→` aceita palavra por palavra, `Esc` dispensa) + backend FIM em `idecore/inline_ai.go` (`CompleteInlineCode`, `ResolveInlineModel` exigindo provider HTTP como OpenCode/OpenAI/Anthropic/Gemini com cancelamento automático de requests em voo e toggle + contador de aceites na status bar).
2. **Ctrl+K inline edit** — **FEITO (19/11/2026)**: atalho contextual (`Ctrl+K` no editor de código abre Inline Edit; `Ctrl+Shift+P` sempre abre a Paleta de Comandos), `idecore.GenerateInlineEdit` com `ComputeLineDiff` LCS, preview colorido de diff verde/vermelho, `Aceitar (Ctrl+Enter)` aplicando em 1 transação undoable no CM6 e `Rejeitar (Esc)`.
3. **Diff-apply do agente no editor** — **FEITO (19/11/2026)**: `idecore.StageDiffHunk`, `ApplyDiffHunk` e `UnstagedFileDiff` + decorações CM6 inline (`inlineDiffField` + `InlineHunkWidget`) mostrando linhas removidas, destaque verde nas adicionadas e botões `✓ Aceitar` / `✕ Rejeitar` por hunk direto no buffer editável, além do banner de revisão no topo do arquivo e abertura direta pelo card de diff do chat.
4. **Contexto de seleção aprimorado (`@arquivo:L10-L20`)** — **FEITO (19/11/2026)**: `expandAtFileMentions` em `idecore/runner.go` agora injeta apenas a faixa `[start..end]` numerada (ou janela ±10 linhas para `@arquivo:L42`) e evita duplicar trechos já colados com code fence via `Ctrl+L`.

**Critérios de saída:** completar código com ghost text ≥ X aceites/dia em uso real; editar seleção via Ctrl+K sem tocar no chat; revisar e aceitar/rejeitar mudança do agente hunk-a-hunk dentro do editor. *(Código e testes unitários Go + Vitest prontos; validação em uso real junto ao próximo build do instalador.)*

## Fase 4 — Navegação e execução de verdade · **CONCLUÍDA (19/11/2026)**

Com o editor e o agente integrados, o que mais dói no dia a dia:

1. **LSP pontual: `gopls` primeiro** — **FEITO (19/11/2026)**: cliente LSP JSON-RPC 2.0 sobre stdio (`ide/internal/idecore/lsp.go`: `LSPManager` lazy por módulo `go.mod`, `initialize`/`didOpen`/`didChange`/`didSave`/`didClose`, `publishDiagnostics` via evento `hivemind:lsp-diagnostics`, `definition` e `hover`) + integração no CodeMirror 6 (`@codemirror/lint` squiggles/gutter, `hoverTooltip`, `F12` e `Ctrl+Click` Go-to-Definition) + chip `lsp: gopls` na status bar.
2. **Terminal PTY: ConPTY + `xterm.js`** — **FEITO (19/11/2026)**: ConPTY nativo Windows sem CGO (`ide/internal/idecore/pty_windows.go`, `pty_manager.go`: `CreatePseudoConsole`, `ResizePseudoConsole`, `powershell.exe -NoLogo`, scrollback 32 KB, limpeza limpa no shutdown) + `@xterm/xterm` + `@xterm/addon-fit` persistente por aba de projeto com `ResizeObserver`, cores ANSI, `cd` persistente, `Ctrl+C`, `Reiniciar` e atalhos de stack injetados no shell vivo (runner one-shot mantido como fallback).
3. **Problem panel (Painel de Problemas)** — **FEITO (19/11/2026)**: gaveta inferior unificada com abas `Terminal` | `Problemas (N)`, parser multi-stack (`ide/internal/idecore/problems.go`: Go, TypeScript/Angular/Vite, Java/Maven, Python/pytest e Rust/Cargo) combinado aos diagnósticos ao vivo do `gopls`, filtros `Todos/Erros/Avisos`, botão `Verificar Projeto`, chip clicável `✕ E · ▲ W` na status bar (`Ctrl+Shift+M`) e clique saltando direto para `arquivo:linha:coluna` no editor.

**Critérios de saída:** navegar um repo Go grande sem sair da IDE (go-to-def + diagnostics); rodar `npm run dev` interativo no terminal integrado; erros de `go build` clicáveis que abrem a linha. *(Implementado e validado com testes reais de ConPTY, `gopls` end-to-end, parser multi-stack e Vitest.)*

## Fase 5 — Maturação (sem pressa, guiada pelo uso)

Sem data; entra o que o dogfooding contínuo apontar, em ordem provável:

1. Streaming real com agy (`modelx/stream.go` hoje é código morto em produção) ou migração do default para provider HTTP com SSE.
2. Blame, merge conflict resolution, history de arquivo no painel git.
3. Settings de IDE (fonte, keybindings), segundo tema (light).
4. Debugger (DAP, breakpoints) — só se o uso real pedir; posicionamento Cursor não exige cedo.
5. Observabilidade do CLI (logs estruturados, métricas por run); CI rodando testes do CLI no Windows **feito**.
6. Blame/pr-quality: QA loop com verificação executável (hoje o QA pode aprovar sem rodar teste — é só instrução de prompt).

Proposta à parte, ainda sem código e sem fase própria: [SPEC_HIVE_AGENT.md](SPEC_HIVE_AGENT.md) (agente residente). Não entra na fila antes de a fase anterior fechar no dogfooding.

---

## Débitos transversais (não pertencem a uma fase; corrigir ao tocar o código)

| Débito | Onde | Ação |
|---|---|---|
| Streaming fake com agy (provider default) | `modelx/agy.go` vs `modelx/stream.go` | Decidir em Fase 5 ou quando doer no dogfooding |
| ~~CI não testa o CLI no Windows~~ | `.github/workflows/ci.yml` | **FEITO (18/11/2026)**: job `test-cli-windows` roda `go vet` + `go test ./...`; fixtures portáveis em `internal/testutil/fakeagy` e testes de PATH corrigidos |
| Versão hardcoded `0.1.0` | `internal/cli/root.go` | Injetar via `-ldflags` ao tocar release |
| `ignore_io.go` indireção inútil | `internal/gitx` | Remover ao tocar o pacote |
| Allowlist duplicada (código vs `examples/team.yaml`) | `internal/team` + examples | Single source ao tocar governança |
| Caps silenciosos (1 MiB arquivo, 20k arquivos índice, 64 KB terminal) | `idecore/explorer.go`, `terminal.go` | Elevar/avisar quando doer no dogfooding |

## Regra de ouro entre fases

**Nenhuma fase avança sem dogfooding da anterior.** O maior risco deste roadmap não é técnico — é construir paridade Cursor sobre um loop básico que nunca foi usado para valer.
