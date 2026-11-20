# Paleta e Tokens Havrus — Guia de Paridade (`hivemind` ↔ `hivemind-app`)

> Fonte da paleta: `Havruss/havrus` (`frontend/src/styles.scss` — tema escuro padrão: preto profundo, grafite, branco e dourado de marca).

## 1. Tabela Completa de Tokens

| Papel Semântico | CSS (`ide/frontend/src/styles.css`) | Valor Hex / RGBA | Kotlin (`HiveMind-App/.../Theme.kt`) |
|---|---|---|---|
| Fundo da página (camada 0) | `--surface-page`, `--bg-frame`, `--bg-deep` | `#050505` | `ObsidianBg = Color(0xFF050505)` |
| Painel / Sidebar (camada 1) | `--surface-panel`, `--bg-sidebar`, `--bg` | `#0C0C0C` | `SurfacePanel = Color(0xFF0C0C0C)` |
| Campo / Input (camada 4) | `--surface-input`, `--bg-input` | `#0A0A0A` | `SurfaceInput = Color(0xFF0A0A0A)` |
| Cartão sólido (camada 2) | `--surface-card`, `--bg-card` | `#111111` | `ObsidianCard = Color(0xFF111111)` |
| Hover em cartão/linha | `--surface-hover` | `#1B1B1B` | `SurfaceHover = Color(0xFF1B1B1B)` |
| Superfície elevada (camada 3) | `--surface-elevated`, `--bg-elevated` | `#1F1F1F` | `ObsidianSurface = Color(0xFF1F1F1F)` |
| Opção ativa em seletor | `--surface-option-active` | `#353535` | `SurfaceOptionActive = Color(0xFF353535)` |
| Texto primário | `--text-primary`, `--text` | `#FFFFFF` | `TextPrimary = Color(0xFFFFFFFF)` |
| Texto corpo | `--text-body` | `#E5E5E5` | `TextBody = Color(0xFFE5E5E5)` |
| Texto secundário | `--text-secondary`, `--text-dim` | `#A3A3A3` | `TextDim = Color(0xFFA3A3A3)` |
| Texto terciário | `--text-tertiary`, `--text-muted` | `#8A8A8A` | `TextTertiary = Color(0xFF8A8A8A)` |
| Divisória / Borda sutil | `--border-divider`, `--border` | `#262626` | `ObsidianBorder = Color(0xFF262626)` |
| Borda enfatizada | `--border-emphasis`, `--border-strong` | `rgba(255, 255, 255, 0.12)` | `BorderEmphasis = Color(0x1FFFFFFF)` |
| Borda de controle | `--border-control` | `#707070` | `BorderControl = Color(0xFF707070)` |
| Ação primária (fundo) | `--action-primary` | `#F3F2F0` | `ActionPrimary = Color(0xFFF3F2F0)` |
| Ação primária (hover) | `--action-primary-hover` | `#FFFFFF` | `Color.White` |
| Ação primária (texto) | `--action-primary-text` | `#090909` | `ActionPrimaryText = Color(0xFF090909)` |
| Destaque / Foco (Dourado) | `--accent` | `#C6A15B` | `BrandGold = Color(0xFFC6A15B)` |
| Destaque forte / Anel foco | `--accent-strong`, `--focus-ring` | `#E8C881` | `BrandGoldStrong = Color(0xFFE8C881)` |
| Destaque suave | `--accent-muted` | `#8C6E33` | `BrandGoldMuted = Color(0xFF8C6E33)` |
| Destaque fundo sutil | `--accent-subtle`, `--accent-bg` | `rgba(198, 161, 91, 0.12)` | `BrandGoldSubtle = Color(0x1FC6A15B)` |
| Destaque borda | `--accent-border` | `rgba(198, 161, 91, 0.25)` | `BrandGoldBorder = Color(0x40C6A15B)` |
| Sucesso | `--color-success`, `--green` | `#10B981` | `SuccessGreen = Color(0xFF10B981)` |
| Perigo / Erro | `--color-danger`, `--red` | `#E58C98` | `ErrorRed = Color(0xFFE58C98)` |
| Texto de perigo | `--color-danger-text` | `#EBA2AC` | `ErrorText = Color(0xFFEBA2AC)` |
| Aviso / Aprovação | `--color-warning`, `--amber` | `#E2AE62` | `ApprovalAmber = Color(0xFFE2AE62)` |
| Informação | `--color-info`, `--blue` | `#3B82F6` | `InfoBlue = Color(0xFF3B82F6)` |
| Texto de informação | `--color-info-text` | `#60A5FA` | `InfoText = Color(0xFF60A5FA)` |

## 2. Snippet de Paridade para `HiveMind-App` (`Theme.kt`)

> Como `HiveMind-App` vive em repositório separado (`gilvan-Borges/HiveMind-App`), aplique este bloco em `app/src/main/java/dev/hivemind/mobile/theme/Theme.kt` para alinhar 100% os dois clientes:

```kotlin
// Havrus Obsidian & Brand Gold
val ObsidianBg = Color(0xFF050505)
val SurfacePanel = Color(0xFF0C0C0C)
val SurfaceInput = Color(0xFF0A0A0A)
val ObsidianCard = Color(0xFF111111)
val SurfaceHover = Color(0xFF1B1B1B)
val ObsidianSurface = Color(0xFF1F1F1F)
val ObsidianBorder = Color(0xFF262626)

val ActionPrimary = Color(0xFFF3F2F0)
val ActionPrimaryText = Color(0xFF090909)
val BrandGold = Color(0xFFC6A15B)
val BrandGoldStrong = Color(0xFFE8C881)

val SuccessGreen = Color(0xFF10B981)
val ErrorRed = Color(0xFFE58C98)
val ApprovalAmber = Color(0xFFE2AE62)
val InfoBlue = Color(0xFF3B82F6)
val TextPrimary = Color(0xFFFFFFFF)
val TextDim = Color(0xFFA3A3A3)
```
