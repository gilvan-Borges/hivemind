# Design System SDD — HiveMind & HiveMind-App

Esta pasta centraliza a especificação orientada por spec (**SDD**) de layout, cores, tipografia e componentes do **HiveMind IDE** (`ide/frontend/`) e do **HiveMind-App** (`HiveMind-App`, Kotlin/Jetpack Compose).

## Comparativo de Estrutura (Por que este formato)

Comparamos dois repositórios de referência para montar a estrutura ideal para o ecossistema HiveMind:

| Aspecto | `Vibetex/ipg` | `Havruss/havrus` | Estrutura adotada no HiveMind |
|---|---|---|---|
| Governança SDD | `.specify/memory/constitution.md` + templates (`spec`, `plan`, `tasks`) + rastreabilidade por ID (`P1–P10`, `T001`) | `docs/front/01-constituicao.md` a `08-checklist-producao.md` | **`.specify/memory/constitution.md`** + templates dedicados em `.specify/templates/*-design-template.md` com IDs (`D1–D8`, `D001`) |
| Execução por agentes | Skills operacionais em `.claude/skills/` | Skill focada de tela (`.claude/skills/tela-do-havrus/SKILL.md`) | **`.agents/skills/hivemind-design-sdd/SKILL.md`** cobrindo desktop IDE e app mobile |
| Qualidade contínua | Testes de domínio antes do código | Conferidores automáticos em `frontend/fundacao/` (`conferir:cores`, `pares-de-contraste.json`, `conferir-letra.mjs`) | **`ide/frontend/scripts/check-design-tokens.mjs`** (`npm run check:design`) validando tokens obrigatórios e contraste WCAG >= 4.5:1 |
| Paleta e estética | Sistema técnico de engenharia | **Preto profundo (`#050505`), grafite (`#111111`), branco/off-white (`#F3F2F0`) e Dourado Havrus (`#C6A15B` / `#E8C881`)** | **Paleta Havrus Obsidian & Brand Gold** aplicada na IDE (`ide/frontend/src/styles.css`) e especificada em paridade 1:1 para `HiveMind-App` (`Theme.kt`) |

## Índice

- [Constituição SDD de Design](../../.specify/memory/constitution.md) — Artigos invioláveis `D1–D8`
- [01 — Paleta e Tokens (Havrus Obsidian & Brand Gold)](./01-paleta-e-tokens-havrus.md) — Mapeamento CSS (IDE) ↔ Compose (`HiveMind-App`) + matriz WCAG
- [02 — Arquitetura de Layout e Componentes](./02-arquitetura-layout-e-componentes.md) — Camadas, pontos de quebra, 4 estados obrigatórios e checklist de PR
- [Skill de Execução para Agentes](../../.agents/skills/hivemind-design-sdd/SKILL.md)
- Templates SDD: [Spec](../../.specify/templates/spec-design-template.md) · [Plano](../../.specify/templates/plan-design-template.md) · [Tasks](../../.specify/templates/tasks-design-template.md)
