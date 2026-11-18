---
name: ide-dark-islands-ui
description: >-
  Use this skill when designing, styling, refactoring, or reviewing dark-mode IDE interfaces,
  desktop developer tools, code editors, or multi-panel workspaces inspired by Cursor 2.0
  and JetBrains Islands Dark (IntelliJ New UI). Synthesizes anthropics/skills (frontend-design)
  and nextlevelbuilder/ui-ux-pro-max-skill dark-mode standards.
---

# Dark Islands IDE Design System (Cursor 2.0 × JetBrains Islands UI)

Apply these principles whenever creating or refining UI components, palettes, layouts, icons, or interactions in the HiveMind IDE.

## 1. Architectural Surface Hierarchy ("Islands in Obsidian")

Inspired by **JetBrains Islands Dark** (`MainWindow.background` vs `ToolWindow.background`) and **Cursor 2.0 Abyssal Dark**:

- **Outer Frame Moat (`--bg-frame: #07080c`)**: Deepest obsidian canvas anchoring the frameless titlebar, left activity rail, and bottom status bar without heavy divider lines.
- **Floating Workspace Islands (`border-radius: 10px`)**: Separate the Left Tool Window, Center Editor/Mission-Control Canvas, and Right Multi-Agent Composer with a `5px` dark moat (`#07080c`), `1px solid rgba(255, 255, 255, 0.065)` crisp hairline border, and subtle top specular edge (`inset 0 1px 0 rgba(255, 255, 255, 0.04)`).
- **Editor & Code Canvas (`--bg-deep: #0b0c12`)**: Ultra-low-glare abyssal surface optimized for syntax contrast during long coding sessions.
- **Tool Windows & Sidebars (`--bg-sidebar: #0f111a`)**: Slightly warmer/lifted dark slate so side panels feel like cohesive islands rather than flat voids.
- **Interactive Cards & Composer (`--bg-card: #141724`, `--bg-elevated: #1b1f30`)**: Reserved for agent cards, floating composer box, command palette, and active tabs.

## 2. Semantic Accent & Contrast Rules (`ui-ux-pro-max`)

- **Body & Code Contrast (`>= 4.5:1`)**:
  - Primary foreground (`--text`): `#ecebf5`
  - Secondary readable label (`--text-dim`): `#9196b0` (never gray-on-gray below `4.5:1`)
  - Muted metadata (`--text-muted`): `#626784`
- **Dual IDE Accents**:
  - **AI / Multi-Agent Accent (Cursor Violet-Indigo)**: `--accent: #a78bfa`, `--accent-strong: #7c5cff`, `--bg-active: rgba(139, 92, 246, 0.15)`
  - **IDE Run / VCS Accent (IntelliJ Blue & Play Green)**: `--jb-blue: #4c8dff`, `--green: #34d399` (Run/Test widget & QA approved), `--amber: #fbbf24` (warnings/budget), `--red: #fb7185` (errors/deletions)

## 3. Typography & Anti-Template Discipline (`anthropics/frontend-design`)

- **No Structural Emojis**: Never use emojis (`⚡`, `🚀`, `⚙️`, `●`) as structural UI icons. Always use crisp `1.75px`-stroke vector SVGs aligned to text baselines.
- **Sentence-Case Section Titles**: Prefer clean, high-legibility labels and subtle pill counters over tracked-out ALL-CAPS eyebrows everywhere.
- **Tabular Monospace Telemetry**: Always apply `font-variant-numeric: tabular-nums` on token counters, line gutters, diff stats, and durations so numbers never jitter during live streaming.
- **Stable Micro-Interactions**: Keep hover/press transitions within `90–140ms` using background, border, or opacity — never layout-shifting transforms. Always preserve visible `:focus-visible` rings.
