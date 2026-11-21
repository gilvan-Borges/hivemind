# Spec de Design — 005-equipe-ia-stream

> Status: Implementado
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [ ] HiveMind-App (`HiveMind-App`)
> Regras aplicáveis: D1–D8 (`.specify/memory/constitution.md`)

## 1. Contexto e Problema
O card da Equipe IA mostra o slug do agente (`chefe`) em vez do modelo que está respondendo. O stream do Cursor cola as palavras e mistura campos internos de ferramenta no texto. Com o editor vazio, o chat continua estreito na coluna direita.

## 2. Escopo e Não-Escopo
- **Inclui:** chip do modelo (papel + nome amigável, provedor e id no hover, atualiza no failover); parser do stream que preserva whitespace e separa ferramentas; grupo colapsável de ações; expandir o chat no centro quando não há abas, com a preferência persistida.
- **Não inclui:** mudar o roteamento do Jev, reexecutar as ferramentas do Cursor no ADK, nem redesenhar o Quadro da Equipe.

## 3. Camadas e Tokens Utilizados
| Elemento | Camada (0–4) | Token CSS (IDE) | Token Compose (App) |
|---|---|---|---|
| Chip do modelo | 3 Elevado | `--surface-elevated`, `--accent`, `--accent-strong` | — |
| Grupo de ações | 2 Card | `--surface-card`, `--text-secondary`, `--focus-ring` | — |
| Chat expandido | 1–2 | painel existente, largura de leitura no conteúdo | — |

## 4. Estados Obrigatórios (D5)
- **Carregando:** ferramentas do turno aberto ficam expandidas; o botão de expandir reflete a preferência.
- **Erro:** texto de erro do turno permanece fora do grupo de ferramentas.
- **Vazio:** boas-vindas do chat; expandir só quando não há abas.
- **Dados:** papel + chip; prosa em Markdown; "N ações" quando há duas ou mais ferramentas.

## 5. Pares de Contraste (D3 — >= 4.5:1)
| Texto / Sinal | Fundo | Razão mínima esperada |
|---|---|---|
| `--accent-strong` | `--surface-elevated` | >= 4.5:1 |
| `--text-secondary` | `--surface-card` | >= 4.5:1 |

## 6. Critérios de Aceite
- [x] Zero cores literais fora de `:root` / `Theme.kt`.
- [x] `node ide/frontend/scripts/check-design-tokens.mjs` sem falhas.
- [x] `npm test` e `npm run build` em `ide/frontend` passando.
- [x] Parser com fixture real do stream do Cursor.
