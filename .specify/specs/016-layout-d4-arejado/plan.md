# Plano Técnico de Design — 016 Layout D4 arejado

> Referência: `spec.md` · Constituição: `.specify/memory/constitution.md`

## 1. Arquivos Impactados
- `ide/frontend/src/styles.css` — tokens `--space-*`, `--radius-*`, `--shadow-soft` e a camada D4.
- `ide/frontend/src/shell.ts` — título, faixa da equipe, trilho, status e palco central.
- `ide/frontend/src/chat.ts` — cabeçalho e composer.
- `ide/frontend/src/lanes.ts` — trilha `chefe › dev › qa › fecho`.
- `ide/frontend/src/explorer.ts` — move o conteúdo Git para o centro.
- `ide/frontend/src/git.ts` — PRs, Jira e Fluxo.
- `ide/frontend/src/quickopen.ts` — paleta cobre lateral, painel e área Jira.
- `ide/frontend/src/state.ts`, `ide/frontend/src/types.ts` — aba `jira` e menus.
- `ide/frontend/preview.html` e `src/layout-preview.ts` — capturas com dados de exemplo. Não entram no bundle do `index.html` embutido pelo Wails.

## 2. Mapeamento de Tokens e Compatibilidade
Os hex da paleta Havrus permanecem. O respiro novo aponta para `--space-*` e `--radius-*`. O bloco D4 fica fora de `@layer` para vencer refrescos anteriores sem apagar regras de diff, CodeMirror e árvore. `Theme.kt` não muda.

## 3. Estratégia de Verificação
1. `node ide/frontend/scripts/check-design-tokens.mjs`
2. `cd ide/frontend && npm test && npm run build`
3. Capturas em `1440×900` via Vite + Chrome headless, antes (`main`) e depois, em `docs/screenshots/layout-d4-*.png`.
4. `hivemind-ide.exe --smoke-check` não roda neste ambiente Linux; a build do frontend cobre o tipo e o bundle.
