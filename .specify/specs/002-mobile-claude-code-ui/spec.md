# Spec de Design — 002-mobile-claude-code-ui

> Status: Aprovado
> Alvos: [ ] HiveMind IDE (`ide/frontend`) · [x] HiveMind-App (`HiveMind-App`)
> Regras aplicáveis: D1–D8 (`.specify/memory/constitution.md`)

## 1. Contexto e Problema
A navegação atual do `HiveMind-App` usa uma `NavigationBar` fixa com 4 abas (`Projetos`, `Chat`, `Fluxo`, `Diffs`), fazendo o usuário passar por listas de projetos antes de conversar e ocupando espaço vertical constante em telas móveis. Além disso, o feed do chat encaixota todas as respostas do assistente em cartões com borda, exibe chips fixos de `RunModes` acima do campo de texto e usa emojis (`⚙`, `⇄`, `✓`) em eventos de ferramentas e conclusão (violando o Artigo D6).

O objetivo é transformar a experiência nativa do `HiveMind-App` para o padrão **Chat-first estilo Claude Code mobile**: moderno, fluido, com abertura direta no chat, menu lateral para projetos/conexão, blocos de ferramentas compactos e expansíveis, e composer em pill flutuante — preservando 100% da paleta oficial **Havrus Obsidian & Brand Gold** (`Theme.kt`).

## 2. Escopo e Não-Escopo
- **Inclui:**
  - Navegação **Chat-first** com `ModalNavigationDrawer` para seleção de projetos, nova sessão, status de conexão e gerenciamento de pareamento/conta.
  - Cabeçalho minimalista no Chat com acesso rápido a **Histórico**, **Fluxo** e **Diffs** (sub-views com retorno em 1 toque ao Chat).
  - Feed de conversa estilo Claude: bolhas sutis apenas para o usuário; respostas do assistente fluidas diretamente sobre o fundo `ObsidianBg`.
  - Cards de ferramentas/ações do agente compactos em 1 linha e expansíveis ao toque, usando ícones Material vetoriais (zero emojis estruturais).
  - Composer em pill/ilha flutuante (`RoundedCornerShape(24.dp)`) com anexo de imagem (`+`), seletor de modo compacto em dropdown e botão circular de envio (`ActionPrimary`).
  - Estado vazio acolhedor com sugestões clicáveis.
- **Não inclui:**
  - Alterações de protocolo LAN/Relay, endpoints da IDE ou mudança de paleta fora de `Theme.kt`.

## 3. Camadas e Tokens Utilizados
| Elemento | Camada (0–4) | Token CSS (IDE) | Token Compose (App) |
|---|---|---|---|
| Fundo do feed e scaffold | 0 | `--surface-page` | `ObsidianBg` (`#050505`) |
| Drawer lateral e top bar | 1 | `--surface-panel` | `SurfacePanel` (`#0C0C0C`) |
| Cards de aprovação, tool cards, sugestões | 2 | `--surface-card` | `ObsidianCard` (`#111111`) |
| Bolha do usuário, menus, pills elevadas | 3 | `--surface-elevated` | `ObsidianSurface` (`#1F1F1F`) |
| Campo interno do composer flutuante | 4 | `--surface-input` | `SurfaceInput` (`#0A0A0A`) |
| Botão principal enviar / aprovar | Ação | `--action-primary` | `ActionPrimary` (`#F3F2F0`) / `ActionPrimaryText` (`#090909`) |
| Destaque IA, seleção de projeto/modo | Acento | `--accent` | `BrandGold` (`#C6A15B`) / `BrandGoldSubtle` |

## 4. Estados Obrigatórios (D5)
- **Carregando:** Indicador discreto no drawer de projetos ao atualizar lista e spinner no botão de enviar/aprovar sem deslocar layout.
- **Erro:** `ErrorBanner` com mensagem legível e ação de tentar novamente no drawer/chat/fluxo.
- **Vazio:** Tela centralizada estilo Claude ("No que vamos trabalhar?") com ícone em `BrandGold` e chips de sugestões; ou CTA para escolher projeto no drawer caso nenhum projeto esteja ativo.
- **Dados:** Feed fluido com auto-scroll inteligente, botão flutuante para descer, contadores de tokens com boa legibilidade e tool cards expansíveis.

## 5. Pares de Contraste (D3 — >= 4.5:1)
| Texto / Sinal | Fundo | Razão mínima esperada |
|---|---|---|
| `TextPrimary` (`#FFFFFF`) | `ObsidianSurface` (`#1F1F1F`) | >= 15:1 |
| `TextBody` (`#E5E5E5`) | `ObsidianBg` (`#050505`) | >= 15:1 |
| `ActionPrimaryText` (`#090909`) | `ActionPrimary` (`#F3F2F0`) | >= 17:1 |
| `TextDim` (`#A3A3A3`) | `ObsidianCard` (`#111111`) | >= 7.0:1 |
| `BrandGold` (`#C6A15B`) | `ObsidianBg` (`#050505`) | >= 7.8:1 |

## 6. Critérios de Aceite
- [x] Zero cores `#hex` literais fora de `Theme.kt`.
- [x] Zero emojis estruturais de UI (D6): apenas `Icons.Filled.*` / `Icons.AutoMirrored.*`.
- [x] Alvos tocáveis >= 44×44 dp nas ações principais (D7).
- [x] Build Gradle `:app:assembleDebug` e instalação via USB (`installDebug`) concluídos sem erro.
