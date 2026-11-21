# Design System HiveMind (Havrus Obsidian & Brand Gold)

## Regras Inegociáveis (`D1–D9`)
- Zero hexadecimais literais fora do bloco `:root` em `ide/frontend/src/styles.css`.
- Superfícies em camadas escuras: `#050505` (page), `#0C0C0C` (panel), `#111111` (card), `#1F1F1F` (elevated), acento `#C6A15B`.
- Todos os valores numéricos críticos (USD, tokens, bytes, datas, linhas) usam `font-variant-numeric: tabular-nums`.
- Ícones via Material Symbols (`icons.ts`) — nunca emojis estruturais na interface.
- Estados obrigatórios em painéis assíncronos: carregando, erro com botão `Tentar novamente`, vazio e dados.
- Rode `node scripts/check-design-tokens.mjs` dentro de `ide/frontend` após alterações visuais.
