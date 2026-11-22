# ADR 0001 — Telemetria de microcompact, custo de re-cache no failover e verificação real pelo Chief/QA

## Contexto

Benchmark de práticas consolidadas de orquestração em assistentes de código
identificou três mecanismos que o HiveMind não tinha, todos ligados a consumo
de tokens e qualidade de resposta:

1. **Telemetria de microcompact**: agentes de referência emitem um evento por
   pass de compactação com `toolsCleared`/`tokensSaved` — a economia de
   contexto é observável. O HiveMind compactava (`internal/contextx`) mas
   descartava o retorno: ninguém sabia quanto estava sendo poupado.
2. **Custo de re-cache estimado antes de trocar de modelo**: trocar de modelo no
   meio de um turn invalida o cache de prefixo do provedor; assistentes de
   referência superficiam esse custo na decisão. O failover do Jev
   (`jevModelWrapper`) trocava de modelo silenciosamente.
3. **Heurísticas de delegação no orquestrador**: (a) responder diretamente o que
   não precisa de implementação, (b) specs auto-contidas para workers (eles não
   veem a conversa do coordenador), (c) confiar mas verificar — o relatório do
   worker descreve intenção, o diff mostra o que foi feito, (d) não usar um
   worker para conferir outro.

## Decisão

- `contextx.Budget` ganha `OnCompact func(Stats)`; `NewCompactionCallback`
  reporta `Stats{Compacted, BytesSaved, TokensSaved}` por pass que efetivamente
  compactou (pass no-op fica silencioso). O orquestrador emite um evento
  `compact` no stream (`Runner.compactionStatsCallback`).
- `provider_failover` agora reporta a estimativa de tokens de entrada que serão
  reprocessados sem cache (`InputTokens` + texto do evento), calculada pelo
  estimador já existente.
- Prompts default de Chefe/Arquiteto/QA (`team/defaults.go`) incorporam as
  heurísticas (1)-(4) em PT-BR, sem código novo.

Fora de escopo (registrados para specs futuras): side questions (agente leve
sem tools para perguntas do usuário durante trabalho em andamento), rota de
modelo barato para títulos/resumos, checkpoint gracioso em vez de abort seco
no dev guard e isolamento de execução de comandos por micro-VM (hoje `run_shell`
roda no host atrás de allowlist; ambientes de referência isolam a execução em
micro-VM com rede virtual).

## Consequências

- **Positivas**: economia de tokens vira métrica visível por agente; o failover
  expõe seu custo oculto; menos delegações desperdiçadas e QA menos
  "rubber-stamp" tendem a reduzir turns e retrabalho.
- **Negativas**: um evento a mais no stream por pass de compactação (mitigado:
  só quando compacta de fato); textos de system prompt ~15% maiores para
  Chief/Arquiteto/QA (com cache de prompt, o custo é marginal e é compensado
  pela primeira heurística).
- Os novos campos são aditivos no JSON de eventos: consumidores antigos ignoram.
