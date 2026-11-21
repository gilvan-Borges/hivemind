# Plano de Implementação — 002-mobile-claude-code-ui

## 1. Arquitetura de Navegação (`MainActivity.kt`)
- Substituir a `NavigationBar` inferior por `ModalNavigationDrawer` com `ModalDrawerSheet(drawerContainerColor = SurfacePanel)`.
- Abrir diretamente no `ChatScreen` quando conectado/pareado (selecionando automaticamente o primeiro projeto disponível caso `selectedWorkspace` ainda seja `null`).
- Gerenciar sub-telas secundárias (`Chat`, `Flow`, `Diffs`) via estado local no `MainScaffold`, exibindo `TopAppBar` compacta com botão de voltar ao Chat quando em `Flow` ou `Diffs`.
- Mover gestão de projetos, nova sessão, status de conexão (`ConnectionBadge`) e ação de trocar dispositivo/desparear para o Drawer lateral.

## 2. Feed e Composer (`ChatScreen.kt`)
- **Top Bar do Chat:** botão menu (`48.dp`), nome do projeto + seletor de lane discreto, e ícones de ação (`44.dp`) para Histórico, Fluxo e Diffs.
- **Feed:**
  - Usuário: `Surface` em `ObsidianSurface` (`RoundedCornerShape(18.dp)`), alinhado à direita.
  - Assistente: texto direto em `ObsidianBg` com rótulo discreto em `BrandGold` apenas na mudança de autor.
  - Tool calls: `ToolCallCard` compacto em `ObsidianCard` + `ObsidianBorder`, 1 linha com ícone vetorial (`Terminal`, `Edit`, `Build`), nome e contador; expansível ao toque via `AnimatedVisibility`.
  - Stage, Handoff e Finish: sem emojis (`⚙`, `⇄`, `✓`); usar `Icons.Filled.*` / `Icons.AutoMirrored.*`.
- **Empty State:** layout centralizado estilo Claude com ícone `AutoAwesome` em `BrandGold`, título "No que vamos trabalhar?" e chips de sugestão.
- **Composer:** ilha/pill flutuante (`RoundedCornerShape(24.dp)`) em `SurfaceInput` com borda `ObsidianBorder` (ou `BrandGoldBorder` no foco), input transparente sem borda dupla, linha inferior interna com botão `+` (imagem), chip dropdown de modo (`Auto`, `Direto`, `Planejar`, `Squad`) e botão circular `ActionPrimary` com ícone de seta para cima.

## 3. Verificação
- `./gradlew :app:testDebugUnitTest :app:assembleDebug` com JDK 21.
- `./gradlew installDebug` no dispositivo conectado via USB.
- Conferência manual de ausência de `#hex` fora de `Theme.kt` e zero emojis estruturais em `ChatScreen.kt` e `MainActivity.kt`.
