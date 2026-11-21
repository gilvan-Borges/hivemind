# Plano Técnico de Design — 008-equipe-sidebar-layout

> Referência: `spec.md` · Constituição: `.specify/memory/constitution.md`

## 1. Arquivos Impactados
- `ide/frontend/src/explorer.ts`
- `ide/frontend/src/styles.css`

## 2. Mapeamento de Tokens e Compatibilidade
- Preservar todos os IDs e atributos existentes (`#team-select`, `#btn-new-team-sb`, `#btn-delete-team-sb`, `#sb-permission-select`, `#btn-team-memory`, `#team-agents-list`, `data-agent-card`, `data-sb-agent-model`) para compatibilidade total com `bindTeamSidebarEvents()`, `updateTeamStatusPills()` e listeners em `chat.ts` / `boot.ts` / `models.ts`.
- Substituir estilos inline em `explorer.ts` por classes semânticas (`.team-sb-*`, `.agent-card-*`) usando exclusivamente tokens CSS definidos em `:root` (`ide/frontend/src/styles.css`).
- Respeitar `@media (prefers-reduced-motion: reduce)` no pulso do indicador `rodando`.

## 3. Estratégia de Verificação
1. `node ide/frontend/scripts/check-design-tokens.mjs`
2. `cd ide/frontend && npm test && npm run build`
3. Build e `--smoke-check` da IDE Windows (`hivemind-ide.exe --smoke-check`)
