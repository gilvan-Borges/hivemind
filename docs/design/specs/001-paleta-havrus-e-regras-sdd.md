# Spec 001 — Fundação SDD de Design e Paleta Havrus (Teste)

> Status: Implementado para avaliação visual
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [x] Especificação de paridade para `HiveMind-App` (`Theme.kt`)
> Regras aplicáveis: D1–D8 (`.specify/memory/constitution.md`)

## 1. Contexto e Problema
O HiveMind precisava de uma regra SDD formal para governar layout, cores e componentes tanto na IDE desktop quanto no `HiveMind-App`, e aplicar a paleta **Havrus Obsidian & Brand Gold** (`Havruss/havrus`) para validação visual prática na IDE.

## 2. Escopo e Não-Escopo
- **Inclui:**
  - Constituição SDD `.specify/memory/constitution.md` e templates em `.specify/templates/*-design-template.md` (combinando rastreabilidade SDD formal com disciplina visual e conferidores automáticos da paleta Havrus).
  - Documentação de referência em `docs/design/` com mapeamento 1:1 CSS ↔ Jetpack Compose (`Theme.kt`).
  - Skill de execução `.agents/skills/hivemind-design-sdd/SKILL.md`.
  - Conferidor automatizado `ide/frontend/scripts/check-design-tokens.mjs` (`npm run check:design`).
  - Aplicação imediata da paleta Havrus em `ide/frontend/src/styles.css`.
- **Não inclui:**
  - Alteração direta do repositório externo `HiveMind-App` neste PR (o mapeamento Kotlin pronto está documentado em `docs/design/01-paleta-e-tokens-havrus.md` para abertura de PR sincronizado no repo do app).

## 3. Critérios de Aceite
- [x] `npm run check:design` valida presença dos tokens canônicos e contraste WCAG >= 4.5:1.
- [x] `npm test` e `npm run build` em `ide/frontend` passam sem regressão.
- [x] Build desktop Windows (`hivemind-ide.exe --smoke-check`) e `go test` da IDE passam.
