---
name: ide-dark-islands-ui
description: >-
  Use this skill when designing, styling, refactoring, or reviewing dark-mode IDE interfaces,
  desktop developer tools, code editors, or multi-panel workspaces in HiveMind (aligned with
  .specify/memory/constitution.md and Havrus Obsidian & Brand Gold palette).
---

# HiveMind Dark Islands UI (SDD × Havrus Obsidian & Brand Gold)

> Constituição completa e obrigatória: [`.specify/memory/constitution.md`](../../../.specify/memory/constitution.md)  
> Paleta e paridade com `HiveMind-App`: [`docs/design/01-paleta-e-tokens-havrus.md`](../../../docs/design/01-paleta-e-tokens-havrus.md)  
> Roteiro SDD passo a passo: [`.agents/skills/hivemind-design-sdd/SKILL.md`](../hivemind-design-sdd/SKILL.md)

## 1. Hierarquia de Superfícies (Camadas 0–4)
- **Camada 0 · Fundo (`--surface-page: #050505`)**: Moldura externa (`--bg-frame`), barra de título e base.
- **Camada 1 · Painéis (`--surface-panel: #0C0C0C`)**: Sidebars e trilhos recuados (`--bg-sidebar`, `--bg-deep`).
- **Camada 2 · Conteúdo sólido (`--surface-card: #111111`)**: Cartões, listas, tabelas e mensagens (`--bg-card`).
- **Camada 3 · Elevado (`--surface-elevated: #1F1F1F`)**: Modais, popovers, menus e seleções (`--bg-elevated`).
- **Camada 4 · Campos (`--surface-input: #0A0A0A`)**: Inputs, selects e textareas (`--bg-input`).

## 2. Ação, Acento e Sinais (WCAG >= 4.5:1)
- **Ação primária**: `--action-primary: #F3F2F0` com texto `--action-primary-text: #090909`.
- **Destaque de marca e foco**: Dourado Havrus (`--accent: #C6A15B`, `--accent-strong: #E8C881`, `--focus-ring`).
- **Sinais semânticos**: Sucesso `--color-success: #10B981`, Perigo `--color-danger: #E58C98` (`--color-danger-text: #EBA2AC`), Aviso `--color-warning: #E2AE62`, Info `--color-info: #3B82F6` (`--color-info-text: #60A5FA`). Nunca depender só da cor.

## 3. Disciplina e Conferência
- Zero emojis estruturais (`⚡`, `🚀`, `⚙️`) — usar exclusivamente os ícones vetoriais de `icons.ts`.
- `font-variant-numeric: tabular-nums` em contadores de tokens, linhas e métricas.
- Antes de abrir PR: rodar `npm run check:design`, `npm test` e `npm run build` em `ide/frontend`.
