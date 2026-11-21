# Plano Técnico de Design — 005-companion-mascote

> Referência: `spec.md` · Constituição: `.specify/memory/constitution.md`

## 1. Arquivos Impactados
- `ide/frontend/src/companion/` — lógica, mascote SVG, vista e navegação até o card
- `ide/frontend/src/styles.css` — superfície flutuante só com tokens
- `ide/frontend/src/boot.ts`, `resident.ts`, `shell.ts` — eventos e o item de menu para religar o companion
- `ide/internal/notify/` — envio nativo com interface substituível em teste
- `ide/desk.go` — liga o envio à janela Wails (trazer para frente e emitir o id clicado)
- `internal/orchestrator/orchestrator.go` e `ide/internal/idecore/resident.go` — origem `routine` no evento
- `ide/internal/idecore/runner.go` — confirmação de shell identificada como `run_shell`

## 2. Mapeamento de Tokens e Compatibilidade
Nenhum token novo. O balão usa `--surface-elevated`, `--text-primary`, `--text-secondary`, `--accent-border` e `--island-shadow`. Sinais usam `--color-danger` e `--color-warning` junto com texto. O PNG da logo não é ampliado: o mascote é SVG com `currentColor`.

## 3. Estratégia de Verificação
1. `node ide/frontend/scripts/check-design-tokens.mjs`
2. `cd ide/frontend && npm test && npm run build`
3. `go test` do pacote `ide/internal/notify` e `go vet` da IDE e da raiz
4. Inspeção do mascote em `1440×900`. O HiveMind-App não muda.
