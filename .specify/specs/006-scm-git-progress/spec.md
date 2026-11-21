# Spec de Design — 006-scm-git-progress

> Status: Implementado
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [ ] HiveMind-App
> Regras aplicáveis: D1–D8

## 1. Contexto e Problema
Pull, Push, Sync e Fetch só escreviam "Executando git pull…" na status bar. A operação parecia travada, sem saída, sem cancelar e podia esperar um prompt de credencial invisível.

## 2. Escopo e Não-Escopo
- **Inclui:** progresso no painel (spinner, rótulo, menu desabilitado), saída ao vivo do stderr com `--progress`, timeout configurável, Cancelar que mata o processo, resultado ou erro com ação sugerida, git não interativo.
- **Não inclui:** um formulário novo de configuração além de `hivemind.gitTimeoutSec` e `HIVEMIND_GIT_TIMEOUT`.

## 3. Camadas e Tokens
| Elemento | Camada | Token |
|---|---|---|
| Faixa de progresso | 2 Card | `--surface-card`, `--text-secondary`, `--color-danger-text` |
| Log | 4 Campo | `--surface-input`, `--text-body`, `--focus-ring` |

## 4. Estados
- **Carregando:** "Puxando de origin/main…", spinner, log aberto, Cancelar.
- **Erro:** texto com a ação sugerida (stash, auth, pull antes do push).
- **Vazio:** a faixa some quando não há operação nem resultado.
- **Dados:** "Atualizado, N commits" ou "Já estava atualizado".

## 5. Contraste
| Texto | Fundo | Mínimo |
|---|---|---|
| `--text-body` | `--surface-input` | >= 4.5:1 |
| `--color-danger-text` | `--surface-card` | >= 4.5:1 |

## 6. Aceite
- [x] Sem cor literal nova fora de `:root`.
- [x] `check-design-tokens` ok.
- [x] Testes Go cobrem progresso, cancelamento, resultado e auth.
