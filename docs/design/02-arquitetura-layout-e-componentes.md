# 02 — Arquitetura de Layout, Camadas e Componentes (HiveMind + HiveMind-App)

## 1. Arquitetura de Camadas

Inspirada na separação rígida do Havrus ("conteúdo sólido, cromo/overlay elevado") combinada com o modelo de ilhas de trabalho da IDE:

1. **Camada 0 (`--surface-page: #050505`)**: moldura externa da IDE (titlebar, activity rail, barra de status) e fundo raiz do `HiveMind-App`.
2. **Camada 1 (`--surface-panel: #0C0C0C`)**: sidebars, explorador de arquivos, trilhos de lista, barras de filtro.
3. **Camada 2 (`--surface-card: #111111`)**: toda área de leitura e trabalho contínuo (editor de código, cards de agente, chat, auditoria, cartões de aprovação). **Sempre opaco/sólido**.
4. **Camada 3 (`--surface-elevated: #1F1F1F`)**: popovers, dropdowns, paleta de comandos (`Ctrl+K`/`Ctrl+P`), modais e folhas inferiores (`BottomSheet` no app).
5. **Camada 4 (`--surface-input: #0A0A0A`)**: campos de formulário, composer de chat e filtros de busca com borda nítida (`--border-control` ou `--border-emphasis`).

## 2. Pontos de Quebra Padronizados

Use apenas estes pontos de quebra em CSS (`ide/frontend`):

| Breakpoint | Contexto | Comportamento esperado |
|---|---|---|
| `max-width: 1100px` | Janela estreita no computador | Compacta barras laterais e prioriza editor + composer |
| `max-width: 760px` | Viewport mobile / painel lateral reduzido | Tabelas viram listas agrupadas; modais viram folha cheia |
| `max-width: 480px` | Tela estreita (celular) | Ações primárias empilhadas com alvo >= 44px |

## 3. Tipografia e Telemetria

- **Interface**: `--sans` (`Inter`, `Segoe UI Variable`, `Segoe UI`, `system-ui`).
- **Código / Tokens / Diffs / Timestamps**: `--mono` (`JetBrains Mono`, `Cascadia Code`, `Consolas`) com `font-variant-numeric: tabular-nums`.
- **Piso de legibilidade**: nenhum texto informativo abaixo de `11px` (`11sp` no app).

## 4. Checklist Obrigatório antes de Abrir PR de UI

- [ ] Nenhum `#hex` literal novo adicionado fora de `:root` (`styles.css`) ou `Theme.kt`.
- [ ] `npm run check:design` em `ide/frontend` passando (tokens presentes + contraste WCAG >= 4.5:1).
- [ ] `npm test` e `npm run build` em `ide/frontend` passando sem erros.
- [ ] Os 4 estados (carregando, erro, vazio, dados) verificados em telas com dados.
- [ ] Sem emojis estruturais na interface; apenas ícones vetoriais/Material Symbols.
- [ ] `prefers-reduced-motion: reduce` preservado.
