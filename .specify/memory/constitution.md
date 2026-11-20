# Constituição de Design System (SDD) — HiveMind & HiveMind-App

> Versão 1.0.0 · Base comparativa: estrutura SDD rastreável (`Vibetex/ipg` `.specify`) + fundação visual e conferidores automáticos (`Havruss/havrus`).
>
> Este documento é a lei visual de **HiveMind IDE** (`ide/frontend/`) e **HiveMind-App** (`HiveMind-App/`). Toda spec de interface, plano, task e PR obedece a ela.

## 1. Fluxo SDD Obrigatório para Mudanças de UI

Nenhuma alteração estrutural de layout, tema ou componente nasce direto no código:

1. **Spec (`spec.md`)**: problema visual, superfícies afetadas (`ide/frontend` e/ou `HiveMind-App`), estados exigidos e pares de contraste.
2. **Plano (`plan.md`)**: mapeamento exato de tokens por papel, impacto em CSS / `Theme.kt` e verificação.
3. **Tasks (`tasks.md`)**: fatias testáveis e independentes com IDs (`D001`, `D002`...).
4. **Código + Conferidor**: implementação usando exclusivamente tokens semânticos + execução de `check-design-tokens` + `npm test` + `npm run build`.
5. **PR**: inclui evidência visual (ou smoke-check / capturas) e checklist de conformidade.

---

## 2. Artigos (Regras Invioláveis D1–D8)

### D1. Tokens por papel, nunca cor literal solta
- O nome do token descreve o **papel semântico** (`--surface-card`, `--accent`, `--color-danger`), nunca o matiz.
- `ide/frontend/src/styles.css` e `HiveMind-App/.../Theme.kt` devem espelhar os mesmos valores hexadecimais da paleta oficial (**Havrus Obsidian & Brand Gold**).
- Novos componentes não podem introduzir `#hex` ou `rgb(...)` fora de `:root` / `Theme.kt`.

### D2. Separação clara de camadas
| Camada | Papel | Superfície Havrus | Uso no HiveMind |
|---|---|---|---|
| 0. Fundo | Chão da aplicação | `#050505` | `--bg-frame`, `--bg-deep`, `ObsidianBg` |
| 1. Painel / Sidebar | Estrutura lateral e trilhos | `#0C0C0C` | `--bg-sidebar`, `--surface-panel` |
| 2. Conteúdo sólido | Cartões, listas, tabelas, chat | `#111111` | `--bg-card`, `--surface-card`, `ObsidianCard` |
| 3. Elevado / Flutuante | Modais, menus, popovers, dropdowns | `#1F1F1F` | `--bg-elevated`, `--surface-elevated` |
| 4. Campo | Inputs, selects, textareas | `#0A0A0A` | `--bg-input`, `--surface-input` |

### D3. Contraste mínimo WCAG 2.2 AA (>= 4.5:1 texto, >= 3:1 controles)
- Todo texto legível (`--text`, `--text-dim`, sinais `-text`) deve atingir pelo menos `4.5:1` sobre a superfície onde repousa.
- Bordas de controles interativos (`--border-control`) devem ser claramente distinguíveis (`>= 3:1` ou contraste nítido sobre `--surface-input`).

### D4. Hierarquia de Ação vs. Destaque (Sem competição visual)
- **Botão primário (`--action-primary`)**: alto contraste neutro (`#F3F2F0` com texto `#090909`).
- **Acento de marca e foco (`--accent`)**: Dourado Havrus (`#C6A15B` / `#E8C881`), reservado para foco de teclado (`:focus-visible`), destaques de IA/orquestração e acentos de seleção ativa.
- **Sinais semânticos**: nunca depender só da cor (sempre acompanhar ícone ou rótulo de estado).
  - Sucesso: `#10B981` | Erro/Perigo: `#E58C98` | Aviso/Aprovação: `#E2AE62` | Info: `#3B82F6`.

### D5. Quatro estados obrigatórios em telas com dados
Toda view que carrega dados assíncronos precisa tratar:
1. **Carregando** (skeleton/indicador estável sem layout shift)
2. **Erro** (mensagem clara em linguagem humana + ação de tentar novamente)
3. **Vazio** (estado inicial útil com próximo passo claro)
4. **Dados** (layout estável com `tabular-nums` para métricas, tokens e datas)

### D6. Família única de ícones e zero emojis estruturais
- Proibido usar emojis (`⚡`, `🚀`, `⚙️`) como ícones estruturais de UI; usar apenas a família vetorial/Material Symbols padronizada (`icons.ts` no desktop, Material Icons no app).

### D7. Acessibilidade, movimento reduzido e alvos de toque
- Respeitar `prefers-reduced-motion: reduce`.
- No `HiveMind-App` (mobile), alvos tocáveis devem ter no mínimo `44×44 dp` (`48×48 dp` recomendado).
- No desktop IDE, preservar anel de foco `:focus-visible` nítido (`--accent-strong`).

### D8. Paridade entre HiveMind IDE e HiveMind-App
- Toda mudança na paleta ou nos estados semânticos no `hivemind` precisa atualizar a tabela de paridade com o `HiveMind-App` (`Theme.kt`).
