# Spec de Design — 009-onda6-ide-ux

> Status: Aprovado  
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [ ] HiveMind-App (`HiveMind-App`, fora de escopo nesta onda)  
> Regras aplicáveis: D1–D9 (`.specify/memory/constitution.md`)

## 1. Contexto e Problema
A Onda 6 (Spec 11) introduz quatro melhorias operacionais e visuais de alta densidade na IDE sem alterar a linguagem visual **Havrus Obsidian & Brand Gold**:
1. **Custo USD Acumulado (`W6-01`):** cabeçalho da lane (`#token-bar`) e página **Uso & Limites** precisam exibir gasto USD acumulado por run, agente e papel (`Chefe`, `Dev`, `QA`) com `tabular-nums`.
2. **Painel de Administração & Checkpoints de Worktrees (`W6-03`):** sub-aba **Worktrees** na barra Git para inspecionar worktrees ativas/órfãs, criar com progresso/Retry, arquivar, podar (`prune`) e restaurar checkpoints por turno.
3. **Toggle Chat ↔ Terminal (PTY) e Split Panes de Lanes (`W6-06`):** alternar uma lane entre chat estruturado e PTY compartilhando o mesmo `sessionID`, além de abrir 2+ lanes lado a lado.
4. **Design Mode via CDP (`W6-07`):** seletor de página/elemento conectado ao Chrome DevTools MCP direto no composer.

## 2. Escopo e Não-Escopo
- **Inclui:** componentes em `ide/frontend/src/` (`chat.ts`, `lanes.ts`, `modals.ts`, `worktrees.ts`, `terminal.ts`, `design-mode.ts`, `git.ts`, `styles.css`).
- **Não inclui:** cores hex literais fora de `:root`, emojis estruturais ou alterações no `HiveMind-App`.

## 3. Camadas e Tokens Utilizados
| Elemento | Camada (0–4) | Token CSS (IDE) | Papel Semântico |
|---|---|---|---|
| Base da barra / Split Lanes | 0–1 | `--surface-page`, `--surface-panel` | Moldura e fundo recuado |
| Cards de custo, worktrees e panes | 2 | `--surface-card`, `--border` | Conteúdo sólido e separação |
| Popover Design Mode / breakdown tooltip | 3 | `--surface-elevated`, `--border-strong` | Superfície flutuante |
| Inputs de seletor / branch / agente | 4 | `--surface-input`, `--border-control` | Campos editáveis |
| Métricas monetárias e badges ativos | — | `--accent`, `--accent-strong`, `--text-primary`, `--text-secondary` | Foco Havrus Gold e contraste AA |
| Estados sucesso / alerta / perigo | — | `--color-success`, `--color-warning`, `--color-danger-text` | Semântica com ícone/rótulo |

## 4. Estados Obrigatórios (D5)
- **Carregando:** spinner vetorial (`icons.ts`) + texto estável em painéis assíncronos (Worktrees, Design Mode, Uso & Limites).
- **Erro:** banner/card com `--color-danger-text` + ícone + botão **Tentar novamente** (`Retry`).
- **Vazio:** instrução clara com próximo passo (ex.: habilitar `chrome-devtools` ou criar primeira worktree).
- **Dados:** listas e tabelas com `font-variant-numeric: tabular-nums` e ícones Material Symbols (`icons.ts`).

## 5. Pares de Contraste (D3 — >= 4.5:1)
| Texto / Sinal | Fundo | Razão mínima esperada |
|---|---|---|
| `--text-primary` (`#F3F2F0`) | `--surface-card` (`#111111`) | >= 15:1 |
| `--text-secondary` (`#B5B4B0`) | `--surface-card` (`#111111`) | >= 8:1 |
| `--accent-strong` (`#E8C881`) | `--surface-card` (`#111111`) | >= 10:1 |
| `--color-danger-text` (`#EBA2AC`) | `--surface-card` (`#111111`) | >= 7:1 |
| `--action-primary-text` (`#090909`) | `--action-primary` (`#F3F2F0`) | >= 18:1 |

## 6. Critérios de Aceite
- [x] Zero cores literais fora de `:root`.
- [ ] `node ide/frontend/scripts/check-design-tokens.mjs` sem falhas.
- [ ] `npm test` e `npm run build` em `ide/frontend` passando.
