# Spec de Design — 016-layout-d4-arejado

> Status: Aprovado
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [ ] HiveMind-App (`HiveMind-App`)
> Regras aplicáveis: D1–D9 (`.specify/memory/constitution.md`)

## 1. Contexto e Problema
O chrome da IDE empilha controles sem hierarquia: a barra de título carrega cerca de onze itens e uma segunda fileira de abas de projeto; o trilho lateral repete Quadro e Equipe entre onze ícones; a barra de status mostra cerca de doze chips cinza; o chat repete trilha, tokens e ações. Branch e tokens aparecem em mais de um lugar. A opção escolhida é o layout D com o estilo D4 (visual arejado): menos cromo visível, o mesmo conjunto de funções, atalhos intactos e acesso pelo Ctrl+K.

## 2. Escopo e Não-Escopo
- **Inclui:** uma única linha de título (logo com menu Arquivo, abas de projeto, busca/paleta, controles da janela); faixa da equipe sob o título (`Equipe · modo | chefe › dev › qa › fecho | N tok · Quadro · Configurar`); abas compactas no topo da lateral (Explorador, Busca, Git, Sessões e `⋯` para Skills, MCP, Agent Hive, Uso das IAs e Equipe); chat só com a aba da conversa, mensagens e composer reduzido; barra de status com branch, stack, problemas, Terminal e `sistema ▴`; estilo D4 (mais respiro, cantos maiores, separação por tom, dourado só em acento/ativo). Preview `?layout=d4` e capturas em `docs/screenshots/`.
- **Não inclui:** HiveMind-App, contratos Wails/Go, novos atalhos, mudança de paleta Havrus, regras de proteção de branch ou configurações do repositório.

## 3. Camadas e Tokens Utilizados
| Elemento | Camada (0–4) | Token CSS (IDE) | Token Compose (App) |
|---|---|---|---|
| Título e moldura | 0 Fundo | `--surface-page` | — |
| Faixa da equipe e laterais | 1 Painel | `--surface-panel` | — |
| Abas ativas, mensagens, composer | 2 Card | `--surface-card` | — |
| Menus, paleta, popover sistema | 3 Elevado | `--surface-elevated` | — |
| Campo do composer | 4 Campo | `--surface-input` | — |
| Aba/etapa ativa e foco | Acento | `--accent`, `--accent-strong`, `--focus-ring` | — |

## 4. Estados Obrigatórios (D5)
- **Carregando:** chips de sistema continuam com reticências (`provedor: …`, `jira: …`) dentro do popover.
- **Erro:** problemas na barra de status e mensagens `.role-error` no chat permanecem com ícone ou rótulo, não só cor.
- **Vazio:** sem projeto, a busca e o explorador mantêm o convite para abrir uma pasta; o menu Arquivo lista recentes vazios.
- **Dados:** faixa, abas, chat e status mostram o projeto ativo com `tabular-nums` nos tokens.

## 5. Pares de Contraste (D3 — >= 4.5:1)
| Texto / Sinal | Fundo | Razão mínima esperada |
|---|---|---|
| `--text-primary` | `--surface-card` | >= 15:1 |
| `--text-secondary` | `--surface-panel` | >= 4.5:1 |
| `--accent-strong` | `--surface-card` | >= 4.5:1 |
| `--text-primary` | `--surface-elevated` | >= 4.5:1 |

## 6. Critérios de Aceite
- [x] Zero cores literais novas fora de `:root`.
- [x] Branch só na barra de status; tokens só na faixa da equipe.
- [x] Atalhos existentes (Ctrl+K, Ctrl+P, Ctrl+J, Ctrl+,, Ctrl+Shift+A, Ctrl+Shift+F, Ctrl+Shift+E, Ctrl+Shift+O, Ctrl+Shift+M) continuam no mesmo lugar do código.
- [x] `npm run check:design`, `npm test` e `npm run build` em `ide/frontend` passando.
- [x] Capturas antes/depois em `docs/screenshots/layout-d4-*.png`, incluindo 1024px.
