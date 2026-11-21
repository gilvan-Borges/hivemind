# Plano Técnico de Design — 009-onda6-ide-ux

> Referência: `spec.md` · Constituição: `.specify/memory/constitution.md`

## 1. Arquivos Impactados
- `ide/frontend/src/styles.css` (`@layer components`)
- `ide/frontend/src/types.ts`, `state.ts`, `utils.ts`, `boot.ts`
- `ide/frontend/src/chat.ts`, `lanes.ts`, `terminal.ts`, `modals.ts`, `git.ts`, `resident.ts`
- `ide/frontend/src/worktrees.ts` (novo painel Worktrees & Checkpoints)
- `ide/frontend/src/design-mode.ts` (novo seletor Design Mode via CDP)

## 2. Mapeamento de Tokens e Compatibilidade
- Todas as novas classes reutilizam exclusivamente variáveis semânticas existentes em `:root` (`--surface-*`, `--text-*`, `--border-*`, `--accent*`, `--action-primary*`, `--color-*`).
- Nenhum `#hex` ou `rgb(...)` novo é inserido em regras de componentes.
- Números de custo USD (`$0.0000`), tokens, timestamps e tamanhos em bytes usam `font-variant-numeric: tabular-nums`.
- Botões interativos preservam `:focus-visible` com `--accent-strong` e zero emojis estruturais (somente `icons.ts` / Material Symbols).

## 3. Estratégia de Verificação
1. `node ide/frontend/scripts/check-design-tokens.mjs`
2. `cd ide/frontend && npm test && npm run build`
3. `go vet` + `go test` em `ide/internal/idecore/...` e `ide/internal/orb/...`
