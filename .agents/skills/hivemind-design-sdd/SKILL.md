---
name: hivemind-design-sdd
description: >-
  Roteiro SDD obrigatório antes de criar ou alterar layout, cores, componentes,
  modais ou telas no HiveMind IDE (ide/frontend) e no HiveMind-App (Compose).
---

# HiveMind Design SDD

Antes de escrever CSS, TypeScript de UI ou Compose no `HiveMind-App`:

1. **Leia a Constituição:** [`.specify/memory/constitution.md`](../../../.specify/memory/constitution.md) (Artigos D1–D8).
2. **Crie/atualize a Spec:** use [`.specify/templates/spec-design-template.md`](../../../.specify/templates/spec-design-template.md) + `plan-design-template.md` + `tasks-design-template.md` em `.specify/specs/NNN-nome/`.
3. **Separe as camadas:**
   - Fundo (`#050505` / `--surface-page`)
   - Painéis laterais (`#0C0C0C` / `--surface-panel`)
   - Conteúdo sólido (`#111111` / `--surface-card`)
   - Flutuante/Elevado (`#1F1F1F` / `--surface-elevated`)
   - Campos (`#0A0A0A` / `--surface-input`)
4. **Use apenas tokens por papel** (`docs/design/01-paleta-e-tokens-havrus.md`). Nunca escreva `#hex` novo fora de `:root` (`ide/frontend/src/styles.css`) ou `Theme.kt` (`HiveMind-App`).
5. **Respeite a hierarquia de cores:**
   - Ação principal: `--action-primary` (`#F3F2F0`) + `--action-primary-text` (`#090909`).
   - Foco de teclado (`:focus-visible`) e destaque de marca/IA: Dourado Havrus (`--accent: #C6A15B` / `--accent-strong: #E8C881`).
   - Sinais: sempre acompanhados de ícone ou rótulo de texto.
6. **Valide proporcionalmente ao impacto visual antes de entregar/PR:**
   - **Mudança visual pontual/pequena** (1–2 componentes isolados, ajuste de texto/classe): rode `node ide/frontend/scripts/check-design-tokens.mjs` e os testes/build do pacote (`cd ide/frontend && npm test && npm run build` ou testes direcionados no `HiveMind-App`). **Não** abra browser nem tire screenshots.
   - **Mudança visual grande/estrutural** (nova tela, refactor de layout/shell, tokens globais em `styles.css`/`Theme.kt`, múltiplos componentes ou responsividade):
     1. Rode a **suíte completa** (`node ide/frontend/scripts/check-design-tokens.mjs`, `cd ide/frontend && npm test && npm run build`, e `hivemind-ide.exe --smoke-check` quando aplicável).
     2. Inspecione a tela renderizada (preview local / browser via `playwright` ou `browser_snapshot`) e **tire print apenas do que ficar ruim ou quebrado** (overflow, sobreposição, contraste, desalinhamento) para corrigir antes de finalizar — nunca tire prints em massa de telas que já estão íntegras.
