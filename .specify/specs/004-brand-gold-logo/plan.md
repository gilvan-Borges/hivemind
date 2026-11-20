# Plano Técnico de Design — 004-brand-gold-logo

> Referência: `spec.md` · Constituição: `.specify/memory/constitution.md`

## 1. Arquivos Impactados
- `ide/frontend/src/styles.css` — violeta solto vira `var(--accent*)` ou `color-mix` do token.
- `ide/frontend/src/icons.ts`, `explorer.ts`, `git.ts`, `modals.ts` — SVG antigo e hex violeta.
- `ide/frontend/index.html` e `ide/frontend/public/` — favicon e ícone servido pela IDE.
- `ide/build/appicon.png`, `ide/build/windows/icon.ico` — ícone do Wails (exceção no `.gitignore`).
- `assets/brand/hivemind.ico` — fonte do instalador; `scripts/build-installer.ps1` e `scripts/installer/hivemind.iss`.
- `README.md` — wordmark `assets/brand/hivemind-logo-horizontal.png`.
- `internal/tui/theme.go` — matiz violeta do papel Pink nos temas noite e oceano.
- Remoção de `assets/hivemind-logo.png` e `assets/hivemind.ico`.

## 2. Mapeamento de Tokens e Compatibilidade
Não há token novo. Fundo violeta vira `--accent-subtle`, borda `--accent-border`, texto claro `--accent-strong`, preenchimento `--accent`. O ícone da janela no Windows continua o recurso que `applyWindowIcons` já carrega (`icon.ico`); não há bandeja separada no código.

## 3. Estratégia de Verificação
1. `npm run check:design` em `ide/frontend`.
2. `cd ide/frontend && npx tsc --noEmit && npm test && npm run build`.
3. `cd ide && go build ./... && go test ./...`.
4. Inspeção da tela inicial no Vite (a IDE desktop depende do WebView do Windows).
