# ADR 0002 — Skills com triggers (injeção condicional) e cache hit na telemetria de uso

## Contexto

Benchmark de práticas consolidadas de orquestração em assistentes de código
identificou duas lacunas no HiveMind:

1. **Skills inflando o prompt por turno**: skills escolhidas pelo Chefe
   injetam o corpo completo (até 4 KiB/skill, 12 KiB/agente) no prompt dos
   executores **sempre**, mesmo quando a tarefa da vez não tem relação com o
   tema da skill. Assistentes de referência usam *keyword routing*: a skill
   declara `triggers:` e o corpo só entra quando uma keyword casa com o
   objetivo da execução; sem match, apenas um stub de 1 linha aponta o
   caminho. A Spec 12 já aprovou esse modelo ("Hybrid Skill Stubs" e orçamento
   estrito de KV-cache) — este ADR implementa a fatia mínima antecipando o
   router completo.
2. **Cache de prompt medido mas invisível**: o Runner já registra
   `CachedReadTokens` por evento de uso e no ledger `ai-usage.jsonl`, mas não
   calcula nem exibe a taxa de acerto de cache (cache hit %) — a métrica que
   mostra se o prompt caching está efetivamente amortizando o custo de
   entrada.

## Decisão

- `internal/skillx`: `Skill` ganha `Triggers []string` lidos do frontmatter
  (`triggers: [a, b]` inline ou bullets). `GuidanceBlock` passa a receber o
  objetivo da execução: skill com triggers e sem match vira stub de 1 linha
  (nome, descrição, caminho para `read_file`); skill sem triggers mantém o
  corpo completo (retrocompatível — skills existentes não declaram triggers).
  O orquestrador passa `r.objectiveText` em `guidanceForRole`/`nativeSkillGuide`.
- `internal/orchestrator`: evento `usage` ganha `CacheHitPct`
  (`cachedReadTokens/promptTokens` por chamada); `Result` ganha
  `CachedReadTokens` acumulado por execução e a linha de resumo de uso exibe
  `cache read: N (P%)`.

## Consequências

- **Positivas**: skills temáticas deixam de consumir tokens por turno quando
  irrelevantes ao objetivo; o dev ainda sabe que existem (stub com caminho);
  a eficácia do prompt caching passa a ser observável por sessão.
- **Negativas**: skills com triggers mal calibradas podem não injetar quando
  deviam (mitigação: o stub instrui o dev a ler a skill com `read_file` se a
  tarefa exigir; skills críticas simplesmente não declaram triggers e mantêm
  injeção incondicional).
- Campos novos (`Skill.Triggers`, `Event.CacheHitPct`, `Result.CachedReadTokens`)
  são aditivos no JSON: consumidores existentes ignoram.

## Emenda — Progressive Disclosure 100% On-Demand via `read_skill` (PR #55)

Na integração do PR #55 (OpenClaw) sobre a `main`, optamos por substituir a
injeção inline do `GuidanceBlock` em `guidanceForRole`/`nativeSkillGuide` por
`skillx.StubBlockForObjective` + ferramenta dedicada `read_skill`:

- **Motivo da troca:**
  1. **Economia de tokens em turnos longos:** mesmo com `triggers:`, skills sem
     triggers continuavam injetando até 12 KiB no system prompt em *todas* as
     chamadas de LLM da rodada (20–80 chamadas/dev). Com `StubBlockForObjective`,
     o bloco cai para ~0,4 KiB fixos e o corpo carregado via `read_skill` (até
     16 KiB, sem truncamento silencioso em 4 KiB) é micro-compactado pelo
     `contextx` conforme o turno avança.
  2. **Acesso real a skills nativas:** o stub anterior apontava para `read_file`,
     que falha em `NativeSkillsRoot` por estar fora do jail da worktree. A tool
     `read_skill` resolve ambos os catálogos (priorizando a versão nativa quando
     explicitamente aprovada/selecionada).
  3. **Preservação dos `triggers:` (ADR 0002):** `Skill.Triggers` e
     `MatchTrigger` continuam ativos — `StubBlockForObjective` marca com
     `[recomendada para esta tarefa]` as skills cujos triggers casam com o
     objetivo, orientando o agente a priorizar `read_skill` nelas.
- **Alinhamento do Bounded Intake com o jail (PR #54):** `IntakeDir` foi
  ajustado de `.hivemind/intake` para `.hivemind/spillover/intake` para
  reaproveitar o carve-out `AllowSpilloverRead` sem enfraquecer o isolamento
  de `.hivemind/team.yaml`, sessões e worktrees.

## Emenda 2 — Subagentes Efêmeros (`spawn_subagent`), Deduplicação de Reminders e Unificação em `read_skill` (PR #56)

Ao integrar o PR #56 (OpenCode) sobre a `main`:

- **O que entrou do PR #56:**
  1. **Subagentes efêmeros somente-leitura (`spawn_subagent` / scouts)**: sessão
     in-memory isolada, toolset estritamente read-only (`tools.ForReadOnly`),
     profundidade 1 por construção, cotas `max_subagent_calls` e
     `max_subagent_llm_calls`, herdando rota, papel de custo e desconto do
     chamador com contadores de tokens sincronizados (`costMu`).
  2. **Deduplicação de `<system-reminder>` (`filterReminderBlocks`)**: blocos
     grandes (`chief_plan`, dossiê, `dev_reports`, `qa_report`, `git status`,
     `TeamBoard`) só são re-injetados quando o hash mudou ou quando o texto
     atual deixou de constar no transcript pós-compactação.
- **O que foi descartado por conflito/sobreposição (`load_skill` / `skill_tools.go`):**
  - O PR #56 propunha uma segunda ferramenta `load_skill` acoplada ao
    `orchestrator.Runner` e mantinha skills fixadas (`skill_assignments`) com
    corpo inline.
  - **Motivo da escolha por `read_skill` (PR #55):** `read_skill` vive em
    `internal/tools` (reutilizável fora do orquestrador), já participa da
    micro-compactação in-turn do `contextx` (`compactableInTurnTools`), conta
    com bloqueio anti-loop na 2ª leitura idêntica (`context_guard`), preserva o
    sinal dos `triggers:` em `StubBlockForObjective` e evita expor duas tools
    sinônimas (`read_skill` e `load_skill`) ao modelo.
