# Matriz de Specs — HiveMind Design System (SDD)

| ID | Spec | Alvos | Status | Artefatos |
|---|---|---|---|---|
| **D-001** | [001-havrus-design-system](specs/001-havrus-design-system/spec.md) | `hivemind` (`ide/frontend`) + `hivemind-app` (`Theme.kt`) | Em Teste (IDE aplicada) | `spec.md`, `plan.md`, `tasks.md`, `docs/design/01-paleta-e-tokens-havrus.md` |
| **D-004** | [004-brand-gold-logo](specs/004-brand-gold-logo/spec.md) | `hivemind` (`ide/frontend`, ícone Windows, README) | Em Teste | `spec.md`, `plan.md`, `tasks.md` |

## Como Criar Nova Spec de UI
1. Leia `.specify/memory/constitution.md` (Artigos D1–D8).
2. Copie `.specify/templates/spec-design-template.md`, `plan-design-template.md` e `tasks-design-template.md` para `.specify/specs/NNN-nome/`.
3. Antes de abrir PR, execute:
   - `node ide/frontend/scripts/check-design-tokens.mjs`
   - `cd ide/frontend && npm test && npm run build`
