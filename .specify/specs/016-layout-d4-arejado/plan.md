# Plano Técnico de Design — 016-layout-d4-arejado

> Referência: `spec.md` · Constituição: `.specify/memory/constitution.md`

## 1. Arquivos Impactados
- `ide/frontend/src/shell.ts` — título, faixa, abas laterais, status e menu Arquivo.
- `ide/frontend/src/chat.ts` — cabeçalho do chat e composer reduzido.
- `ide/frontend/src/lanes.ts` — token some da aba da conversa; a trilha segue em `#pipeline-bar`.
- `ide/frontend/src/styles.css` — bloco D4 no fim do arquivo, só com tokens já definidos.
- `ide/frontend/src/quickopen.ts` — comandos da paleta para o que saiu da barra de título.
- `ide/frontend/src/state.ts` — menu do composer aceita `tools`; largura inicial da lateral cabe as abas.
- `ide/frontend/src/layout-preview.ts` e `ide/frontend/src/boot.ts` — dados sintéticos só com `?layout=d4`.
- `ide/frontend/src/boot.ts` — chama o preview quando não há runtime Wails.

## 2. Mapeamento de Tokens e Compatibilidade
Nenhum token novo e nenhum hex novo. O dourado existente (`--accent` / `--accent-strong`) fica restrito a aba ativa, etapa ativa da trilha e anel de foco. Separação por `--surface-page`, `--surface-panel` e `--surface-card`. Popover de sistema usa `--surface-elevated`. `Theme.kt` não muda (D8: paleta intacta).

Os ids que o resto do app já procura continuam no DOM: `status-branch`, `status-tokens`, `status-providers`, `status-jira`, `status-ghost`, `status-autoreview`, `status-lsp`, `status-mobile`, `status-resident`, `pipeline-bar`, `btn-header-swarm`, `act-ide-settings`, `btn-file-menu`, `jira-key-input`, `git-badge`, `data-act`.

## 3. Estratégia de Verificação
1. `npm run check:design` em `ide/frontend`.
2. `npm test` e `npm run build` em `ide/frontend`.
3. Vite + Chrome headless em `1440×900` (antes e depois) e `1024×640` (depois), URL `/?layout=d4`.
4. Smoke da IDE Windows fica de fora neste ambiente Linux.
