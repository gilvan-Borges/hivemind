# Spec de Design — 016 Layout D4 arejado

> Status: Aprovado
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [ ] HiveMind-App (`HiveMind-App`)
> Regras aplicáveis: D1–D9 (`.specify/memory/constitution.md`)

## 1. Contexto e Problema
A IDE acumulou chrome na barra de título (~11 itens), no trilho vertical (11 ícones, com Quadro e Equipe repetidos), na barra de status (~12 chips) e no cabeçalho do chat. Branch e tokens apareciam em mais de um lugar. PRs, Jira e Fluxo ficavam espremidos na lateral.

O dono escolheu o padrão D4 — visual arejado: mais respiro, cantos maiores, quase sem borda (separação por tom de fundo) e dourado `#C6A15B` só em acento e estado ativo. Nada de função sai; o que deixa de ser um botão permanente continua na paleta `Ctrl+K`, no menu Arquivo (logo) ou no menu `⋯`.

## 2. Escopo e Não-Escopo
- **Inclui:** barra de título com logo, abas de projeto, busca e paleta; faixa da equipe; trilho como abas no topo da lateral; chat e composer reduzidos; status com branch, stack, problemas, Terminal e `sistema ▴`; GitHub, Jira e Fluxo no centro; o mesmo respiro em Quadro, Sessões, Skills, MCP, Agent Hive, Uso das IAs, Configurações, paleta, modais, terminal, editor e estado vazio.
- **Não inclui:** HiveMind-App (a paleta hexadecimal não muda), regras de proteção de branch e qualquer atalho removido.

## 3. Camadas e Tokens Utilizados
| Elemento | Camada (0–4) | Token CSS (IDE) | Token Compose (App) |
|---|---|---|---|
| Moldura, título, status | 0 | `--surface-page` | sem mudança |
| Lateral, centro, chat | 1 | `--surface-panel` | sem mudança |
| Mensagem, card, composer | 2 | `--surface-card` | sem mudança |
| Menus, paleta, modal | 3 | `--surface-elevated` | sem mudança |
| Campo | 4 | `--surface-input` | sem mudança |
| Aba e passo ativos | acento | `--accent` / `--accent-strong` | sem mudança |
| Respiro e raio | — | `--space-*`, `--radius-*` | sem mudança |

## 4. Estados Obrigatórios (D5)
- **Carregando:** PRs, tasks e Fluxo continuam com o indicador já existente.
- **Erro:** falha de importação Jira e de vínculo seguem em toast; o drawer do PR mantém “Tentar novamente”.
- **Vazio:** “Minhas tasks & issues” e a área Jira usam um cartão com o próximo passo.
- **Dados:** listas em cards; tokens e contagens com `tabular-nums`.

## 5. Pares de Contraste (D3 — >= 4.5:1)
| Texto / Sinal | Fundo | Razão mínima esperada |
|---|---|---|
| `--text-primary` | `--surface-card` | >= 15:1 |
| `--text-secondary` | `--surface-card` | >= 4.5:1 |
| `--accent-strong` | `--surface-card` | >= 4.5:1 |
| `--action-primary-text` | `--action-primary` | >= 4.5:1 |

## 6. Critérios de Aceite
- [x] Zero cores literais novas fora de `:root`.
- [x] `node ide/frontend/scripts/check-design-tokens.mjs` sem falhas.
- [x] `npm test` e `npm run build` em `ide/frontend` passando.
- [x] Paridade de paleta do `HiveMind-App` inalterada (nenhum hex de papel mudou).
