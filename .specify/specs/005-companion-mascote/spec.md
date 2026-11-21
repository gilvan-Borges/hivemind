# Spec 005 — Companion com mascote

> Status: Implementado
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [ ] HiveMind-App (`HiveMind-App`)
> Regras aplicáveis: D1, D2, D3, D4, D6, D7 (`.specify/memory/constitution.md`)

## 1. Contexto e Problema
A IDE só avisava dentro do painel. Com a janela fora de foco o usuário perdia fim de turno, plano para aprovar, confirmação de shell/navegador/desktop, erro e rotina do Residente. O aviso precisa aparecer em foco (companion flutuante) e fora de foco (toast do sistema), sem os dois ao mesmo tempo.

## 2. Escopo e Não-Escopo
- **Inclui:** mascote SVG do cérebro dourado no hexágono, estados animados, balão com `aria-live`, arrastar e minimizar com posição persistida, clique que abre o card na Equipe IA ou no Residente, toast nativo (Windows primeiro, macOS e Linux no mesmo contrato), ajustes persistidos (companion, sistema, som, não perturbe).
- **Não inclui:** paleta nova, HiveMind-App, histórico longo de avisos, som do toast do macOS separado do som do sistema.

## 3. Camadas e Tokens Utilizados
| Elemento | Camada (0–4) | Token CSS (IDE) | Token Compose (App) |
|---|---|---|---|
| Mascote (fundo) | 0 | `--surface-page` | fora deste repositório |
| Balão e ajustes | 3 | `--surface-elevated` | — |
| Texto do balão | 3 | `--text-primary` / `--text-secondary` | — |
| Traço do mascote | 3 | `--accent`, `--accent-strong` | — |
| Erro / atenção | 3 | `--color-danger`, `--color-warning` | — |
| Foco | 3 | `--focus-ring` | — |

Nenhum hex novo fora de `:root`. O mascote usa `currentColor`.

## 4. Estados Obrigatórios (D5)
- **Carregando:** o mascote fica ocioso (respiração). Não há pedido de rede.
- **Erro:** tremor curto, cor de perigo e balão "Erro no QA" ou "Erro na equipe".
- **Vazio:** sem balão; o mascote continua no canto.
- **Dados:** balão curto com o aviso e o card correspondente no painel.

## 5. Pares de Contraste (D3 — >= 4.5:1)
| Texto / Sinal | Fundo | Razão mínima esperada |
|---|---|---|
| `--text-primary` | `--surface-elevated` | >= 4.5:1 |
| `--text-secondary` | `--surface-elevated` | >= 4.5:1 |
| `--accent-strong` | `--surface-page` | >= 4.5:1 |

O estado não depende só da cor: o balão traz texto e o botão do mascote atualiza o `aria-label`.

## 6. Critérios de Aceite
- [x] Zero cores literais novas fora de `:root`.
- [x] `node ide/frontend/scripts/check-design-tokens.mjs` sem falhas.
- [x] `npm test` e `npm run build` em `ide/frontend` passando.
- [x] `prefers-reduced-motion` desliga as animações. Animações só com `transform` e `opacity`.
- [x] Paridade com `HiveMind-App`: sem token novo, nada a espelhar em `Theme.kt`.
