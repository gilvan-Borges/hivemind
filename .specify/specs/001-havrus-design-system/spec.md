# Spec 001 — Sistema Visual & Paleta Havrus (HiveMind + HiveMind-App)

> **Status:** Aprovado para Teste na IDE  
> **Alvos:** `ide/frontend/src/styles.css` (HiveMind IDE) · `HiveMind-App/.../Theme.kt` (paridade especificada)  
> **Atende:** Constituição D1, D2, D3, D4, D6, D7, D8 (`.specify/memory/constitution.md`)

## 1. Contexto e Objetivo
Unificar a identidade visual do **HiveMind** e do **HiveMind-App** sob uma fundação SDD única, combinando:
- **Estrutura SDD rastreável** (`.specify/memory/constitution.md`, `spec.md`, `plan.md`, `tasks.md`, matriz em `.specify/README.md`).
- **Fundação visual, camadas, tokens por papel e conferidores automáticos** (paleta **Havrus Obsidian & Brand Gold**).
- **Paleta Havrus Obsidian & Brand Gold** aplicada para teste imediato na IDE (preto profundo `#050505`, grafite sólido `#111111`, elevado `#1F1F1F`, branco `#FFFFFF`/`#E5E5E5`, ação primária `#F3F2F0`, dourado de marca `#C6A15B`/`#E8C881` e sinais WCAG >= 4.5:1).

## 2. Camadas e Tokens por Papel (D1, D2)

| Camada | Papel | Token CSS (IDE) | Token Compose (App) | Hex (Havrus) |
|---|---|---|---|---|
| 0. Fundo | Chão principal | `--surface-page` (`--bg-frame`, `--bg-deep`) | `ObsidianBg` | `#050505` |
| 1. Painel | Sidebar, trilhos, cabeçalho | `--surface-panel` (`--bg-sidebar`) | `SurfacePanel` | `#0C0C0C` |
| 2. Conteúdo sólido | Cartões, mensagens, listas | `--surface-card` (`--bg-card`) | `ObsidianCard` | `#111111` |
| 3. Elevado | Popovers, menus, modais, hover ativo | `--surface-elevated` (`--bg-elevated`) | `ObsidianSurface` | `#1F1F1F` |
| 4. Campo | Inputs, textareas, selects | `--surface-input` (`--bg-input`) | `SurfaceInput` | `#0A0A0A` |
| Ação primária | Botão principal (Enviar, Salvar, Confirmar) | `--action-primary` / `--action-primary-text` | `ActionPrimary` / `OnActionPrimary` | `#F3F2F0` / `#090909` |
| Destaque / Foco | Marca IA, anel de foco, seleção ativa | `--accent` / `--accent-strong` | `BrandGold` / `BrandGoldStrong` | `#C6A15B` / `#E8C881` |
| Sinais | Sucesso / Perigo / Aviso / Info | `--color-success` / `--color-danger` / `--color-warning` / `--color-info` | `SuccessGreen` / `ErrorRed` / `ApprovalAmber` / `InfoBlue` | `#10B981` / `#E58C98` / `#E2AE62` / `#3B82F6` |

## 3. Pares de Contraste Obrigatórios (D3 — >= 4.5:1)

| Texto / Sinal | Superfície | Razão mínima |
|---|---|---|
| `--text-primary` (`#FFFFFF`) | `--surface-card` (`#111111`) | >= 15:1 |
| `--text-body` (`#E5E5E5`) | `--surface-card` (`#111111`) | >= 13:1 |
| `--text-secondary` (`#A3A3A3`) | `--surface-card` (`#111111`) | >= 7:1 |
| `--text-tertiary` (`#8A8A8A`) | `--surface-card` (`#111111`) | >= 5:1 |
| `--action-primary-text` (`#090909`) | `--action-primary` (`#F3F2F0`) | >= 16:1 |
| `--accent-strong` (`#E8C881`) | `--surface-card` (`#111111`) | >= 10:1 |
| `--color-danger-text` (`#EBA2AC`) | `--surface-card` (`#111111`) | >= 8:1 |
| `--color-success` (`#10B981`) | `--surface-card` (`#111111`) | >= 6:1 |
| `--color-warning` (`#E2AE62`) | `--surface-card` (`#111111`) | >= 8:1 |
| `--color-info-text` (`#60A5FA`) | `--surface-card` (`#111111`) | >= 6:1 |

## 4. Critérios de Aceite
1. Estrutura `.specify/`, `docs/design/` e `.agents/skills/hivemind-design-sdd/SKILL.md` criada e documentada em PT-BR.
2. `ide/frontend/src/styles.css` atualizado com todos os tokens Havrus em `:root` e aliases preservados sem quebrar nenhuma tela.
3. Botões primários (`button.primary`), foco (`:focus-visible`) e destaques de seleção usam a hierarquia Havrus (neutro claro `#F3F2F0` para ação principal + dourado `#C6A15B`/`#E8C881` para foco/IA, removendo a sobrescrita azul `#4c8dff`).
4. `node ide/frontend/scripts/check-design-tokens.mjs` passa verificando presença de tokens e contraste WCAG >= 4.5:1.
5. `npm test`, `npm run build` e build Windows `hivemind-ide.exe --smoke-check` passam.
