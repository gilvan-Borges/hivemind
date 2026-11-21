# Matriz de Specs — HiveMind Design System (SDD)

| ID | Spec | Alvos | Status | Artefatos |
|---|---|---|---|---|
| **D-001** | [001-havrus-design-system](specs/001-havrus-design-system/spec.md) | `hivemind` (`ide/frontend`) + `hivemind-app` (`Theme.kt`) | Implementado | `spec.md`, `plan.md`, `tasks.md`, `docs/design/01-paleta-e-tokens-havrus.md` |
| **D-002** | [002-icons-dedupe](specs/002-icons-dedupe/spec.md) | `hivemind` (`ide/frontend/src/icons.ts`) | Implementado | `spec.md`, `plan.md`, `tasks.md` |
| **D-002M** | [002-mobile-claude-code-ui](specs/002-mobile-claude-code-ui/spec.md) | `HiveMind-App` (`MainActivity.kt`, `ChatScreen.kt`) | Implementado | `spec.md`, `plan.md`, `tasks.md` |
| **D-003** | [003-ai-usage-page](specs/003-ai-usage-page/spec.md) | `hivemind` (`ide/frontend`, `internal/modelx`) | Implementado | `spec.md`, `plan.md`, `tasks.md` |
| **D-004** | [004-brand-gold-logo](specs/004-brand-gold-logo/spec.md) | `hivemind` (`ide/frontend`, ícone Windows, README) | Implementado | `spec.md`, `plan.md`, `tasks.md` |
| **D-005A** | [005-companion-mascote](specs/005-companion-mascote/spec.md) | `hivemind` (`ide/frontend/src/companion/`, `notify/`) | Implementado | `spec.md`, `plan.md`, `tasks.md` |
| **D-005B** | [005-equipe-ia-stream](specs/005-equipe-ia-stream/spec.md) | `hivemind` (`ide/frontend`, `internal/modelx/cursor_stream.go`) | Implementado | `spec.md` |
| **D-006** | [006-scm-git-progress](specs/006-scm-git-progress/spec.md) | `hivemind` (`ide/internal/idecore`, `ide/frontend/src/git.ts`) | Implementado | `spec.md` |
| **D-007** | [007-hive-floating-resident](specs/007-hive-floating-resident/spec.md) | `hivemind` (`ide/internal/orb`, `ide/frontend/src/orb/`) | **Pendente (Onda 4)** | `spec.md`, `plan.md`, `tasks.md` |
| **D-008** | [008-equipe-sidebar-layout](specs/008-equipe-sidebar-layout/spec.md) | `hivemind` (`ide/frontend/src/explorer.ts`, `styles.css`) | Implementado | `spec.md`, `plan.md`, `tasks.md` |

> **Plano Mestre consolidado (Design + Engenharia):** [`docs/specs/PLANO_IMPLEMENTACAO.md`](../docs/specs/PLANO_IMPLEMENTACAO.md)

## Como Criar Nova Spec de UI
1. Leia `.specify/memory/constitution.md` (Artigos D1–D8).
2. Copie `.specify/templates/spec-design-template.md`, `plan-design-template.md` e `tasks-design-template.md` para `.specify/specs/NNN-nome/`.
3. Antes de abrir PR, execute:
   - `node ide/frontend/scripts/check-design-tokens.mjs`
   - `cd ide/frontend && npm test && npm run build`
