# Spec de Design — [NNN-nome-da-mudanca]

> Status: Rascunho | Em Revisão | Aprovado
> Alvos: [ ] HiveMind IDE (`ide/frontend`) · [ ] HiveMind-App (`HiveMind-App`)
> Regras aplicáveis: D1–D8 (`.specify/memory/constitution.md`)

## 1. Contexto e Problema
Descreva o problema visual, inconsistência ou nova superfície necessária.

## 2. Escopo e Não-Escopo
- **Inclui:** ...
- **Não inclui:** ...

## 3. Camadas e Tokens Utilizados
| Elemento | Camada (0–4) | Token CSS (IDE) | Token Compose (App) |
|---|---|---|---|
| ... | ... | `--surface-card` | `SurfaceCard` |

## 4. Estados Obrigatórios (D5)
- **Carregando:** ...
- **Erro:** ...
- **Vazio:** ...
- **Dados:** ...

## 5. Pares de Contraste (D3 — >= 4.5:1)
| Texto / Sinal | Fundo | Razão mínima esperada |
|---|---|---|
| `--text-primary` | `--surface-card` | >= 15:1 |

## 6. Critérios de Aceite
- [ ] Zero cores literais fora de `:root` / `Theme.kt`.
- [ ] `node ide/frontend/scripts/check-design-tokens.mjs` sem falhas.
- [ ] `npm test` e `npm run build` em `ide/frontend` passando.
- [ ] Paridade validada para `HiveMind-App` (quando aplicável).
