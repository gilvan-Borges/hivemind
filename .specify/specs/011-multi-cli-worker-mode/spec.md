# Spec de Design — 011-multi-cli-worker-mode

> Status: Aprovado para Implementação
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [x] HiveMind-App (`HiveMind-App`)
> Regras aplicáveis: D1–D9 (`.specify/memory/constitution.md`)
> Spec Técnica Vinculada: [`docs/specs/13-multi-cli-worker-adapters-and-runtime-selector.md`](../../../docs/specs/13-multi-cli-worker-adapters-and-runtime-selector.md)

## 1. Contexto e Problema
Os desenvolvedores do time (`role: developer`) podem rodar em dois modos:
1. **Motor Nativo HiveMind (`worker: ""`)**: o próprio HiveMind executa o loop ADK e as ferramentas nativas, usando o provedor configurado apenas como LLM.
2. **Worker CLI Externo (`worker: "agy" | "claude" | "codex" | "cursor" | "opencode"`)**: o HiveMind atua como camada de governança (Chefe, Worktree isolada, `/ask` HITL, `PostHocGuard`, QA e Fecho) e delega a execução da tarefa na worktree ao agente CLI completo escolhido pelo usuário.

Hoje essa escolha não está exposta visualmente no **Quadro de Controle Multiagente** nem no modal **Configurar Equipe**, obrigando edição manual de `.hivemind/team.yaml` e suportando apenas `cursor` no registro padrão.

## 2. Escopo e Não-Escopo
- **Inclui:**
  - Seletor **"Modo / Worker de Execução"** nos cards de `Desenvolvedor` do **Quadro de Controle Multiagente** (`ide/frontend/src/multiagent-board.ts`) e na tabela de **Configurar Equipe** (`ide/frontend/src/team.ts`).
  - Indicador visual de disponibilidade do binário CLI no host (`Instalado` / `Ausente no PATH`) via `ListAvailableWorkers()`.
  - Badge `Worker CLI · <agy|claude|codex|cursor|opencode>` na timeline do chat e no card do agente (IDE e `HiveMind-App`).
- **Não inclui:**
  - Alteração do papel do Chefe (`project_manager` / `architect`) ou do QA (`qa`), que continuam rodando no motor de governança nativo do HiveMind para garantir a integridade dos gates de segurança e de aprovação.

## 3. Camadas e Tokens Utilizados
| Elemento | Camada (0–4) | Token CSS (IDE) | Token Compose (App) |
|---|---|---|---|
| Card do Agente no Quadro | 2 (Cartão sólido) | `--surface-card` (`#111111`) | `ObsidianCard` (`#111111`) |
| Select / Dropdown de Modo Worker | 4 (Campo) | `--surface-input` (`#0A0A0A`) + `--border-subtle` | `SurfaceInput` (`#0A0A0A`) + `ObsidianBorder` |
| Foco no Select de Worker | 4 (Campo focado) | `--border-focus` / `--accent` (`#C6A15B`) | `BrandGold` (`#C6A15B`) |
| Badge `Worker CLI · <nome>` | 3 (Elevado / Destaque IA) | `--accent-subtle` + `--accent` (`#C6A15B`) | `BrandGoldSubtle` + `BrandGold` |
| Status CLI Instalado / Ausente | 2 (Sinal semântico) | `--status-ok` (`#10B981`) / `--status-warn` (`#E2AE62`) | `SuccessGreen` / `ApprovalAmber` |

## 4. Estados Obrigatórios (D5)
- **Carregando:** Enquanto `ListAvailableWorkers()` resolve os binários do PATH, o seletor exibe o valor atual de `agent.worker` habilitado com sufixo `(verificando…)`.
- **Erro:** Se o usuário selecionar um Worker CLI cujo binário não está instalado no PATH (ex.: `codex` ausente), exibe aviso inline em `--status-warn` (`#E2AE62`) com o comando de instalação/verificação (`hivemind doctor`) sem travar a UI.
- **Vazio (`worker: ""`):** Exibe `HiveMind Nativo (Tools + Jev/LLM)` como opção padrão selecionada.
- **Dados:** Lista as 6 opções (`HiveMind Nativo`, `Antigravity CLI (agy)`, `Claude Code (claude)`, `OpenAI Codex (codex)`, `Cursor Agent (agent)`, `OpenCode (opencode)`) com status de instalação e persiste a troca imediatamente via `SaveTeam`.

## 5. Pares de Contraste (D3 — >= 4.5:1)
| Texto / Sinal | Fundo | Razão mínima esperada |
|---|---|---|
| `--text-primary` (`#FFFFFF`) | `--surface-input` (`#0A0A0A`) | >= 19:1 |
| `--accent` (`#C6A15B`) | `--surface-card` (`#111111`) | >= 6.8:1 |
| `--status-ok` (`#10B981`) | `--surface-card` (`#111111`) | >= 7.2:1 |
| `--status-warn` (`#E2AE62`) | `--surface-card` (`#111111`) | >= 8.1:1 |

## 6. Critérios de Aceite
- [ ] Zero cores literais fora de `:root` / `Theme.kt`.
- [ ] Família única de ícones (SVG inline na IDE / Material Icons no App) e zero emojis estruturais (D6).
- [ ] `node ide/frontend/scripts/check-design-tokens.mjs` sem falhas.
- [ ] `npm test` e `npm run build` em `ide/frontend` passando.
