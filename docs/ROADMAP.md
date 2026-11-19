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
2. ~~**File watching no `FileIndex`**~~ — **FEITO (nov/2026)**: `idecore/watcher.go` (fsnotify recursivo, debounce de 250 ms, coalescência por lote, cap de 400 paths com overflow, exclusão de `ignoredDirs`/`.hivemind`), um watcher por projeto em `app.go`, evento Wails `hivemind:fs-event` e handler no frontend que atualiza a árvore e recarrega abas de editor abertas e não-sujas quando o agente edita arquivo (abas sujas nunca são sobrescritas; conteúdo idêntico não re-renderiza).
3. ~~**Resiliência do run** (lado CLI)~~ — **FEITO (nov/2026)**: `modelx/retry.go` — retry transport com backoff exponencial + jitter para 408/429/5xx e erros de rede, honra `Retry-After` (cap 30 s), replay de body; plugado em **todos** os providers HTTP (OpenCode, Gemini/Vertex, Anthropic, OpenAI). agy mantém o retry específico de "empty conversation id" (subprocesso, sem status HTTP visível).
4. ~~Testes do caminho TUI~~ — **FEITO (nov/2026)**: 19 testes em `internal/tui/model_test.go` cobrindo wizard de primeiro uso, slash commands, histórico, máquina de estados `onStream` (stage/usage/tool_call/text/done/error/cancel), approval gates HITL (autorizar/negar), cancelamento por Esc, `agentKeyFor`, `roleTitle`, `shortModel`.

Fora de escopo aqui: qualquer feature nova de editor. A meta é fechar e estabilizar o que existe.

## Fase 2 — Fundação de edição (editor real + frontend modular)

Pré-requisito: Fase 1 fechada (dogfooding validou o loop básico).

1. **Split do `main.ts`** em módulos (explorer, chat, git, terminal, editor, modais, estado) com testes mínimos de frontend (Vitest). Critério: nenhum módulo > 1.000 linhas; re-render de painel inteiro por evento eliminado no chat e no editor.
2. **Editor real: CodeMirror 6** (recomendado) — modular, leve, funciona bem em WebView2, syntax highlight por grammar de verdade, bracket matching, code folding, undo/redo sólido, e principalmente: **base para ghost text e diff-apply inline das fases seguintes**. Alternativa Monaco (mais recursos prontos, porém pesado e resistente a embutir custo de streaming inline). Manter a API de abas/arquivos atual; substituir o overlay textarea+`<pre>` por uma única instância CM6 por aba.
3. Reagir a mudanças externas no arquivo aberto (o agente editou o arquivo que está na tela → recarregar com aviso, sem perder cursor do usuário quando não houver dirty state).

**Critérios de saída:** abrir/navegar/editar/salvar fluido num projeto grande (≥ 5k arquivos, arquivos ≥ 40k chars sem debounce visível); zero regressão nos fluxos git/chat; testes de frontend cobrindo editor e explorer.

## Fase 3 — Paridade agentic (o coração, dado o posicionamento Cursor)

A fase que justifica o produto. Tudo construído sobre o CM6 da Fase 2:

1. **Ghost text (inline completion)** — sugestão inline no editor, aceitar com Tab. Rota técnica: providers HTTP (OpenCode Zen / OpenAI-compat) têm caminho natural; **agy não entrega token-a-token — risco conhecido: ghost text pode exigir provider HTTP fixo para completions**, com agy mantido para orquestração.
2. **Ctrl+K inline edit** — seleção + instrução → diff aplicado no buffer com aceitar/rejeitar, reutilizando o parser de unified diff que já existe em `idecore/diff.go`.
3. **Diff-apply do agente no editor** — hoje o resultado do run chega como resumo no chat; passa a chegar como diff clicável por arquivo que aplica no buffer aberto (aceitar/rejeitar por hunk — o revert por hunk já existe, é estender para o lado de aplicação).
4. Contexto de seleção aprimorado: `@arquivo` vira `@arquivo:L10-L20` com conteúdo injetado por linhas, não arquivo inteiro truncado.

**Critérios de saída:** completar código com ghost text ≥ X aceites/dia em uso real; editar seleção via Ctrl+K sem tocar no chat; revisar e aceitar/rejeitar mudança do agente hunk-a-hunk dentro do editor.

## Fase 4 — Navegação e execução de verdade

Com o editor e o agente integrados, o que mais dói no dia a dia:

1. **LSP pontual: gopls primeiro** (o projeto em si é Go; maior ROI imediato). Bridge Go ↔ LSP client sobre stdin/stdout, diagnostics no editor (squiggles), go-to-definition, hover. Depois, por demanda real: TypeScript/JAVA só quando o dogfooding pedir.
2. **Terminal PTY: ConPTY + xterm.js** — substituir o command runner one-shot por shell persistente e interativo (`cd` persiste, watchers, REPLs, servidores de dev, ANSI rendering). O runner atual continua como fallback não-interativo.
3. **Problem panel** — saída de build/teste parseada em lista clicável (integra com os TestHints de stack que já existem).

**Critérios de saída:** navegar um repo Go grande sem sair da IDE (go-to-def + diagnostics); rodar `npm run dev` interativo no terminal integrado; erros de `go build` clicáveis que abrem a linha.

## Fase 5 — Maturação (sem pressa, guiada pelo uso)

Sem data; entra o que o dogfooding contínuo apontar, em ordem provável:

1. Streaming real com agy (`modelx/stream.go` hoje é código morto em produção) ou migração do default para provider HTTP com SSE.
2. Blame, merge conflict resolution, history de arquivo no painel git.
3. Settings de IDE (fonte, keybindings), segundo tema (light).
4. Debugger (DAP, breakpoints) — só se o uso real pedir; posicionamento Cursor não exige cedo.
5. Observabilidade do CLI (logs estruturados, métricas por run), CI rodando testes do CLI no Windows.
6. Blame/pr-quality: QA loop com verificação executável (hoje o QA pode aprovar sem rodar teste — é só instrução de prompt).

---

## Débitos transversais (não pertencem a uma fase; corrigir ao tocar o código)

| Débito | Onde | Ação |
|---|---|---|
| Streaming fake com agy (provider default) | `modelx/agy.go` vs `modelx/stream.go` | Decidir em Fase 5 ou quando doer no dogfooding |
| CI não testa o CLI no Windows | `.github/workflows/ci.yml` | Adicionar job windows quando tocar CI. Evidência (nov/2026): `TestProbeAgy*`, `TestAgyAuthAndMissingBinary`, `TestStreamJSONDeltasStayOutOfArgv` e `TestLocalEchoAndDeny` falham localmente no Windows (binários fake sem `.exe` e `echo` inexistente) |
| Versão hardcoded `0.1.0` | `internal/cli/root.go` | Injetar via `-ldflags` ao tocar release |
| `ignore_io.go` indireção inútil | `internal/gitx` | Remover ao tocar o pacote |
| Allowlist duplicada (código vs `examples/team.yaml`) | `internal/team` + examples | Single source ao tocar governança |
| Caps silenciosos (1 MiB arquivo, 20k arquivos índice, 64 KB terminal) | `idecore/explorer.go`, `terminal.go` | Elevar/avisar quando doer no dogfooding |

## Regra de ouro entre fases

**Nenhuma fase avança sem dogfooding da anterior.** O maior risco deste roadmap não é técnico — é construir paridade Cursor sobre um loop básico que nunca foi usado para valer.
