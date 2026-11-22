# ADR 0003 — Orquestração econômica: ciclo de vida de subagentes, compactação observável, orçamento em custo, navegação de workspace e leitura web do squad

## Contexto

O benchmark contínuo de práticas de orquestração em assistentes de código
apontou cinco lacunas do HiveMind, todas ligadas ao eixo custo/fluxo do
time de agentes:

1. **Ciclo de vida de subagente opaco**: o spawn do residente devolvia só o
   texto final calado. Assistentes de referência tratam o subagente como um
   recurso gerenciado — o spawn recebe um brief de contexto (o filho não
   re-lê o que o pai já sabe), pode ser travado em modo leitura (pesquisa
   barata, zero risco de escrita) e o encerramento reporta telemetria
   (`toolCalls`, tokens, duração) para o pai decidir se vale spawnar outro.
2. **Compactação invisível no gatilho**: o evento `compact` dizia quanto
   poupou, mas não *quando nem por quê* — a ocupação da janela que disparou
   o pass e a marcação da primeira compactação do agente são o sinal de
   pressão de contexto que faltava para diagnóstico de runs longos.
3. **Orçamento só em tokens**: com um catálogo multi-modelo (Jev), dois
   runs com o mesmo `max_tokens` podem custar centavos ou dólares
   dependendo da rota. Assistentes de referência limitam uso em unidade
   monetária, não em tokens.
4. **Navegação de workspace cara**: `list_dir` plano força o agente a uma
   cauda de chamadas cegas em repositórios grandes; cada saída lota a
   janela e vira alvo da compactação depois. Referência: esqueleto de
   diretórios com orçamento de caracteres e ramos marcados como
   *não explorados* — o agente navega em 1-2 chamadas e refina só onde
   importa.
5. **Sem leitura web no squad**: para ler uma página de docs, o agente
   ligava um servidor MCP de browser inteiro (schema inflado em TODAS as
   chamadas seguintes do turno) ou caía no shell. Não existia o caminho
   barato: um GET bounded, cercado como dado não confiável.

## Decisão

- **`resident.SpawnRequest`** (novo contrato de spawn): `objective`,
  `roots`, `classes` (como antes), mais `context_brief` (cap 2 KiB, viaja
  com o objetivo sob a etiqueta "fatos já apurados") e `readonly`
  (hard-gate: interseção das classes pedidas com `read`; teto do filho vira
  `TierRead` — escrita/exec falham fechado no broker, não por instrução).
  O retorno ganha cabeçalho de telemetria
  (`[subagente (readonly) · N chamadas · M tokens · Xs]`) e um evento
  `subagent` no stream com o mesmo resumo, inclusive em falha.
- **`contextx.Stats`** ganha `ContextTokens` (estimativa capturada ANTES do
  pass — `CompactRequest` muta a projeção de leitura) e `ContextWindow`;
  `UsagePercent()` deriva a ocupação. O evento `compact` passa a exibir
  `janela N% ocupada no gatilho (T/W tokens)` e marca a 1ª compactação do
  agente no run.
- **`team.Settings.MaxCostMicroUSD`** (`max_cost_micro_usd`, 0 = off,
  piso 10.000 = US$ 0,01): o runner aborta com evento `Gate: "budget"`
  quando `turnCostMicroUSD` (já computado em micro-USD inteiro) estoura.
  Complementa `max_tokens`: tokens medem volume, custo mede valor.
- **`tools.list_tree`** (todos os papéis com leitura): esqueleto por
  profundidade (default 3, teto 6) com orçamento de caracteres (default
  6.000, teto 20.000); ao esgotar, marca `(+N entradas não exploradas)`
  no diretório exato onde parou. Pula `.git`/`.hivemind`/`.worktrees`/
  `node_modules` e mantém o mesmo filtro de caminhos sensíveis do
  `list_dir`.
- **`tools.web_fetch`** (PM/TL/Arquiteto/Dev/QA): GET read-only com
  esquema http/https apenas, sem userinfo, guard SSRF em dois níveis
  (privado/link-local/metadata sempre recusado; loopback só com carve-out
  controlado pelo host, nunca pelo modelo), validação por hop de
  redirect (`CheckRedirect = ErrUseLastResponse` — um redirect para
  `169.254.169.254` morre no hop), teto de 64 KiB head+tail, content-type
  textual only, e corpo cercado como conteúdo externo não confiável com
  neutralização de fechamento falsificado.

## Consequências

- **Positivas**: subagentes de pesquisa ficam baratos e seguros (readonly
  + brief elimina re-leituras); a pressão de contexto vira observável no
  gatilho; orçamento expresso na unidade que o usuário sente no bolso;
  navegação de repo cai de dezenas de chamadas para um esqueleto + refinos;
  docs/APIs públicas sem inflar o schema de tools do turno.
- **Negativas**: mais um evento por encerramento de subagente (um por
  spawn, aceitável); `list_tree` em árvores gigantes marca ramos não
  explorados (é o comportamento desenhado, não perda silenciosa);
  `web_fetch` conscientemente NÃO renderiza JS nem faz login — é leitor
  de texto, não browser; páginas SPA continuam exigindo os MCPs de browser.
- Campos novos são aditivos: `SpawnRequest` muda a assinatura do hook
  `BrokerConfig.Spawn` (interno, com testes atualizados no mesmo PR);
  eventos novos (`subagent`) e campos estendidos (`compact`) são ignorados
  por consumidores antigos.
- Follow-ups registrados: histórico de revisões de skills nativas com
  revert (snapshots por hash), rota de modelo barato por spawn
  (pin de rota Jev no filho), e `read_skill` com dedup entre agentes do
  mesmo run.
