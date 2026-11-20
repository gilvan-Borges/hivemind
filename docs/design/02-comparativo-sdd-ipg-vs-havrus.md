# Comparativo SDD (`Vibetex/ipg` vs `Havruss/havrus`) & Estrutura Adotada

## Por que combinar os dois modelos

| Dimensão | `Vibetex/ipg` | `Havruss/havrus` | Escolha para `HiveMind` + `HiveMind-App` |
|---|---|---|---|
| **Governança SDD** | `.specify/memory/constitution.md` + `spec-template.md` / `plan-template.md` / `tasks-template.md` + matriz de rastreabilidade | `docs/front/01-constituicao.md` + `docs/37` / `docs/105` | **`.specify/`** como estrutura formal de especificação + **`docs/design/`** como referência visual viva |
| **Roteiro de Execução IA** | `.claude/skills/...` por caso de uso técnico | `.claude/skills/tela-do-havrus/SKILL.md` com passos de camadas, peças e contraste | **`.agents/skills/hivemind-design-sdd/SKILL.md`** adaptado ao stack Wails/TS + Android Compose |
| **Verificação Automatizada** | Testes de domínio + checklist de PR | `frontend/fundacao/conferir:cores`, `pares-de-contraste.json`, `conferir-letra.mjs` | **`ide/frontend/scripts/check-design-tokens.mjs`** validando tokens obrigatórios e contraste WCAG >= 4.5:1 |
| **Paleta e Camadas** | Visual corporativo normativo | **Obsidian (`#050505` / `#111111` / `#1F1F1F`) + Branco (`#F3F2F0`) + Dourado (`#C6A15B`)** | Paleta exata do **Havrus** aplicada em `ide/frontend/src/styles.css` e espelhada para `HiveMind-App` |

## Estrutura de Arquivos Criada

- `.specify/memory/constitution.md` — Artigos D1–D8 (Constituição visual do HiveMind & HiveMind-App).
- `.specify/templates/spec-design-template.md` — Template de Spec de UI/Design.
- `.specify/templates/plan-design-template.md` — Template de Plano técnico de Design.
- `.specify/templates/tasks-design-template.md` — Template de Tasks rastreáveis (`D001`...).
- `docs/design/01-paleta-e-tokens-havrus.md` — Dicionário 1:1 de tokens Havrus (IDE CSS ↔ Android Compose).
- `docs/design/02-comparativo-sdd-ipg-vs-havrus.md` — Este documento comparativo e guia de governança.
- `.agents/skills/hivemind-design-sdd/SKILL.md` — Skill executável para qualquer agente que criar ou alterar telas.
- `ide/frontend/scripts/check-design-tokens.mjs` — Script conferidor de tokens Havrus e contraste WCAG.
