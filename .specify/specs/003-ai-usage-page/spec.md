# Spec de Design — 003-ai-usage-page

> Status: Aprovado
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [ ] HiveMind-App
> Regras aplicáveis: D1–D8

## 1. Contexto e Problema
Uso & Limites das IAs abre em modal (camada elevada). Deve abrir como página no centro (editor), e listar **cada conta** do Pool (ex.: 3× agy), não um único cartão agregado.

## 2. Escopo e Não-Escopo
- **Inclui:** aba/página central `ai:usage`; uma seção/cartão por conta agy do pool; demais provedores como hoje.
- **Não inclui:** HiveMind-App; mudar Pool de Contas; migrar outros modais.

## 3. Camadas e Tokens Utilizados
| Elemento | Camada | Token |
|---|---|---|
| Página | 2 | `--surface-card` |
| Cartões de provedor | 2 | `--surface-card` / `--border-subtle` |
| Barras de cota | sinais | `--color-success` / `--color-warning` / `--color-danger` |

## 4. Estados Obrigatórios (D5)
- **Carregando:** spinner na página.
- **Erro:** mensagem dim + toast.
- **Vazio:** cartões mínimos agy/opencode-go.
- **Dados:** N cartões agy (um por perfil) + demais provedores.

## 5. Critérios de Aceite
- [ ] Clique em Uso & Limites abre aba no centro (não modal).
- [ ] Com 3 contas agy no pool, aparecem 3 cartões de cota.
- [ ] `check-design-tokens`, `npm test`, `npm run build` OK.
