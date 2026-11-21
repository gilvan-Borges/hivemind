# Spec de Design — 008-equipe-sidebar-layout

> Status: Aprovado
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [ ] HiveMind-App (`HiveMind-App`)
> Regras aplicáveis: D1–D8 (`.specify/memory/constitution.md`)

## 1. Contexto e Problema
No painel lateral **Equipe**, os controles superiores (seletor de time, permissão, aviso longo de sandbox do Cursor CLI, botão de memória e resumo de fluxo) ficam empilhados de forma solta e densa, com texto corrido ocupando múltiplas linhas. Nos cartões de agentes (`.agent-card`), o chip do modelo compete horizontalmente com o nome do papel e o status, o estado `rodando` usa cinza neutro sem destaque semântico, e a telemetria (`Jev` + tokens) usa `word-break: break-all` com CSS inline sem separação estrutural.

## 2. Escopo e Não-Escopo
- **Inclui:** reorganizar o painel **Equipe** em duas zonas claras (ilha de Governança/Workspace + lista hierárquica de Agentes), transformar o aviso do Cursor CLI em `<details>` compacto expansível sob demanda, melhorar o destaque semântico de `.agent-card` por estado (`running`, `done`, `error`, `idle`), estruturar rodapé de telemetria (`tabular-nums`) e separar visualmente título do agente, chip do modelo e seletores.
- **Não inclui:** alterar regras do orquestrador, contratos Wails/Go ou telas do `HiveMind-App`.

## 3. Camadas e Tokens Utilizados
| Elemento | Camada (0–4) | Token CSS (IDE) | Token Compose (App) |
|---|---|---|---|
| Painel lateral Equipe | 1 Painel | `--surface-panel` | — |
| Ilha de governança e cards de agente | 2 Card | `--surface-card`, `--border-divider` | — |
| Seletores (`team`, `permission`, `provider`, `model`) | 4 Campo | `--surface-input`, `--border-control`, `--focus-ring` | — |
| Estado ativo (`rodando`) | Acento | `--accent`, `--accent-strong`, `--accent-subtle`, `--accent-border` | — |
| Aviso sandbox & telemetria | Sinais / Texto | `--color-warning`, `--color-warning-subtle`, `--color-success-text-soft`, `--text-secondary`, `--text-tertiary` | — |

## 4. Estados Obrigatórios (D5)
- **Carregando / Rodando:** card `.is-running` recebe filete dourado (`--accent`), badge `rodando` com ponto pulsante (`prefers-reduced-motion` respeitado) e telemetria ao vivo em `tabular-nums`.
- **Erro:** card `.is-error` usa borda `--color-danger-border` e badge `--color-danger-text`.
- **Vazio:** quando não há agentes na equipe, exibe cartão vazio orientando clicar em **Config**.
- **Dados:** ilha superior com equipe, permissão, memória (`.hivemind/MEMORY.md`) e resumo de fluxo, seguida pela lista estruturada de agentes.

## 5. Pares de Contraste (D3 — >= 4.5:1)
| Texto / Sinal | Fundo | Razão mínima esperada |
|---|---|---|
| `--text-primary` | `--surface-card` | >= 15:1 |
| `--text-secondary` / `--text-tertiary` | `--surface-card` | >= 4.5:1 |
| `--accent-strong` | `--surface-card` | >= 4.5:1 |
| `--color-warning` | `--surface-card` | >= 4.5:1 |

## 6. Critérios de Aceite
- [x] Zero cores literais fora de `:root`.
- [x] `node ide/frontend/scripts/check-design-tokens.mjs` sem falhas.
- [x] `npm test` e `npm run build` em `ide/frontend` passando.
- [x] `hivemind-ide.exe --smoke-check` passando após build de produção.
