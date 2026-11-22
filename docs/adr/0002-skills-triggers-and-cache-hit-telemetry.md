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
