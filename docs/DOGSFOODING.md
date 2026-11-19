# Dogfooding — Protocolo de Validação (Fase 1)

A IDE nunca foi usada para valer numa tarefa real. Nenhuma fase do [ROADMAP](ROADMAP.md) avança sem fechar isto: **critério de saída da Fase 1 é completar uma tarefa real ponta-a-ponta sem abrir outro editor.**

## Regras

1. **A IDE é o único editor.** VS Code/whatever só pode ser aberto para comparar resultado no fim da tarefa.
2. **Registre tudo que quebrar.** Cada travamento, lentidão, confusão de UI, recurso ausente que fez você querer sair da IDE → uma linha na tabela abaixo (vira issue).
3. **Sem commits/PRs durante o teste** — o objetivo é validar o produto, não entregar código limpo.
4. Cada tarefa dura no máximo 1 dia. Se travar mais que isso, registre a causa e pare.

## Tarefas

Escolha 3 tarefas REAIS (não demos) de complexidade crescente:

- **T1 — Pequena:** uma alteração pontual num projeto seu (bugfix, endpoint, ajuste de configuração) com run do time (`squad` ou `direct`).
- **T2 — Média:** uma feature que toque 2–3 arquivos, com merge de branch, comentário de Jira ou PR no fim.
- **T3 — Grande:** uma tarefa multi-arquivo com devs paralelos, aprovando/reescrevendo diffs pelo painel Git e usando o terminal integrado para build/teste.

### Execução atual — Block-Trail (Kotlin/Compose, `core/` puro com JUnit)

- **T1 (CONCLUÍDA ✅ 18/11/2026, ~13 min):** `PlayerXpTest.kt` criado, +66 linhas, **PR #2 aberto** (github.com/gilvan-Borges/block-trail/pull/2). QA aprovou rodando o gradle de verdade (JDK 21). **Verificação independente: `:core:test` exit 0 rodado manualmente na worktree de integração.** Checkout do usuário intacto; branches `hivemind/adicionar-teste-junit-playerxpte/{dev_1,dev_2,entrega}` + `integracao` criadas e empurradas. Custo: **1.102.878 tokens** (input 1.03M / output 70k, 37 chamadas; dev_1 436k, chefe 261k, dev_2 261k, qa 145k).
- **T2 (CONCLUÍDA ✅ 18/11/2026, ~11,5 min):** multiplicador de sequência de vitórias. **PR #3** (+134/-2: `PlayerXp.kt` +29/-2 com `WinStreakXpConfig` data class, teste +76, doc de decisão +29). Valores em config conforme regra do projeto; defaults retrocompatíveis. **Verificação independente: `:core:test` exit 0.** Custo: **1.214.159 tokens** (input 1.15M / output 61k, 30 chamadas; **chefe 535k**, dev_2 260k, qa 214k, dev_1 205k).
  - **Instructions por agente funcionam:** com a dica do JDK 21 no `team.yaml`, ambos os devs acertaram o gradle na **primeira** tentativa (6 `run_shell` no run todo, vs ~12+ na T1). Fricção da quebra #5 eliminada por configuração — a correção de produto (executar `.bat` no Windows / memory no CLI) continua pendente.
  - **Custo não caiu, migrou:** total 1,21M (vs 1,1M na T1), agora dominado pelo **chefe (44%)** — triagem + plano + fechamento. Achado #9 abaixo.
- **T3 (FALHOU ✘ 18/11/2026, ~11 min, 1.245.144 tokens):** conquistas — morte por causa composta: **(a)** worktree de dev reutilizada entre runs carregou a implementação suja da T2 (`PlayerXp.kt` +29/-2 não commitado) e a integração da T3 consolidou esse lixo de estado; **(b)** o QA rejeitou certo ("apenas alteração não relacionada em PlayerXp.kt"), mas o loop girou ~6 `qa_request_changes` sem os devs conseguirem entregar; **(c)** a quota do agy estourou no meio do run (`429 RESOURCE_EXHAUSTED`) e matou tudo sem checkpoint. Sem PR. Sessão `9c62bd8220e5` registrada.

## Balanço final do dogfooding do motor (18/11/2026)

**T1 ✅ PR #2 · T2 ✅ PR #3 · T3 ✘ (2 tentativas — vazamento de worktree e janela de quota; motivou os fixes 1a/1b abaixo).** 3 entregas verificadas por inspeção independente. Custo total: **3.681.000 tokens** (~1,2M por run, tamanho-independente — o agy injeta ~14k por chamada e o contexto ADK cresce a cada turno; o chefe/coodenação come ~44% do run).

**O que funciona de verdade (verificado fora do run):** pipeline ponta-a-ponta, governança (devs nunca commitam; escopo limpo), QA que executa teste antes de aprovar, worktrees sem tocar no checkout do usuário, PRs reais abertos, instructions por agente respeitadas, sessões persistidas com telemetria por agente, triagem que escala devs.

**O que quebra (por severidade):**

| # | Severidade | Achado |
|---|---|---|
| 6, 9 | **trava (custo)** | ~1,2M tokens por run independentemente do tamanho; o agente de coordenação (chefe) é o maior consumidor (~44%) |
| 11 | **trava (corretude)** | worktrees de dev/entrega reutilizadas entre runs sem limpeza: mudança suja de um run vaza para o próximo e é consolidada como entrega → **corrigido (1a)** |
| 12 | **trava (resiliência)** | quota 429 do agy matava o run inteiro sem checkpoint; o envelope `AGY_ERROR` diz `"retryable":true` e era ignorado → **corrigido (1b)** |
| 14 | **trava p/ tarefas grandes** | **a janela de uso do agy não comporta um squad**: T1+T2 (2,3M) esgotaram; após ~40 min uma chamada isolada funciona, mas o burst do T3b estourou a janela em 2,5 min com 7 chamadas. Tarefa de tamanho T3 é inviável no agy sem esperar janela ou trocar de provider |
| 13 | **irrita (custo)** | loop do QA gira (`qa_request_changes`×6) quando os devs não entregam — queima tokens sem backpressure → **corrigido (18/11/2026): 3ª reprovação consecutiva sem alteração nova na entrega encerra o loop com motivo** |
| 4 | **irrita (custo)** | fatiamento duplicado em tarefas pequenas → **corrigido (18/11/2026): dedup mecânico de dev_tasks + reforço de prompt "1 item para tarefas pequenas" (incluído junto com os modos SDD)** |
| 1, 2, 7, 8 | irrita/feio | probe ignora `AGY_MODEL` → **corrigido**; mensagem enganosa do agy → **corrigida**; `MEMORY.md` só na IDE → **corrigido: CLI injeta em todo run**; CLI não imprime resumo de tokens → **corrigido** |

### Correções derivadas — placar final (18/11/2026)

| Achado | Status |
|---|---|
| #1, #2 (probe/AGY_MODEL, mensagem) | ✔ corrigido |
| #4 (fatiamento duplicado) | ✔ corrigido (dedup mecânico + prompt SDD) |
| #5 (wrappers `.bat` no Windows) | ✔ corrigido (`resolveLocalExecutable` no shellx) |
| #7 (memory só na IDE) | ✔ corrigido (injeção em todo run do CLI, starter criado) |
| #8 (tokens invisíveis no CLI) | ✔ corrigido (resumo no fim + uso até o erro) |
| #11 (vazamento de worktree) | ✔ corrigido + validado em produção |
| #12 (agy 429 mata o run) | ✔ corrigido + validado em produção |
| #13 (loop do QA girando) | ✔ corrigido (backpressure: 3ª reprovação sem alterações encerra) |
| #9 (custo do chefe ~44%) | aberto → Fase 5 (redesenho de prompt/coordenação; risco alto para mudança autônoma) |
| #6, #14 (custo/janela do agy) | abertos → Fase 5 (compactação de contexto entre turnos, roteamento de provider) |

**Fechamento:** o motor está validado no que importa — pipeline, governança, QA honesto, persistência, e agora limpeza de worktree e resiliência a 429. Os achados #6/#9/#14 (custo e janela) são limites do desenho atual: endereçar na Fase 5 com compactação de contexto entre turnos e roteamento/troca de provider ao detectar 429 persistente. **Restante da Fase 1: 1c — cockpit na IDE (seu, ~30 min).**

### T3 — as duas tentativas

- **1ª (21h20, 1,25M tokens):** morta por causa composta: worktree de dev carregou a implementação suja da T2 (`PlayerXp.kt` sem commit) e a integração consolidou esse lixo; o QA rejeitou certo, mas o loop girou 6× sem os devs entregarem; a quota estourou no meio (`429`), matando tudo sem checkpoint.
- **2ª (22h03, com o binário corrigido, 115k tokens, ~4 min):** **validou os dois fixes em produção**: (a) a worktree de dev amanheceu **limpa** — o `M PlayerXp.kt` da T2 foi descartado pelo `reset --hard` do 1a; (b) o run morreu com a **mensagem acionável nova** ("agy esgotou a quota/créditos (429)… fixe um modelo mais leve ou troque de provedor") após exercitar os retries de 30s+60s do 1b, em vez do "exit 3" genérico. Confirmado: a cota do agy é **janela de uso**, não saldo.

## Correções derivadas do dogfooding (18/11/2026) — VALIDADAS EM PRODUÇÃO

| Achado | Correção | Onde |
|---|---|---|
| #11 worktree suja entre runs | `AlignDeveloperWorktree` agora faz `reset --hard` + `checkout -f -B` + `clean -fd` (o `checkout -B` simples **preserva** modificações em arquivos tracked — raiz do vazamento). Regressão: `TestAlignDeveloperWorktreeDiscardsStaleEdits` (falha no código antigo, passa no novo; verificada com stash) | `internal/gitx/integrate.go` |
| #12 agy 429 mata o run | `generate()` parseia o envelope `AGY_ERROR` (`"retryable":true`) e faz retry com backoff (30s, 60s, cancelável); quota esgotada vira mensagem acionável ("429 … fixe agy:gemini-3.8-flash-low ou troque de provedor") em vez de "exit 3" genérico | `internal/modelx/agy.go` |
| #1 probe ignora `AGY_MODEL` | `ProbeAgyPrompt` constrói o agy via `NewAgy` — respeita `AGY_MODEL`/`AGY_EFFORT` | `internal/modelx/agy.go` |

Testes: `TestParseAgyError` (envelope real capturado na falha da T3) + `TestAgyRetriesQuota429`/`TestAgyQuotaErrorAfterRetries` (fake agy com modos `quota-once`/`quota-always`; skip no Windows local, cobertos no CI Linux junto com os demais fake-binary). Binário `bin/hivemind-dogfood.exe` rebuildado com as correções — **o próximo run contra o Block-Trail limpa as worktrees sujas automaticamente** (a própria correção fazendo o trabalho).

Time: `dogfood` (chefe=arquiteto, dev_1, dev_2, qa), provider `agy:gemini-3.8-flash-medium`, governed, worktrees ON, auto_commit ON, auto_pr ON (escolha do dono), allowlist com `gradlew`/`gradlew.bat`.

#### Resultado da T1 (checklist)

| Item | Resultado |
|---|---|
| Pipeline ponta-a-ponta (triage → devs paralelos → integração → QA → PR) | ✔ funcionou |
| Entrega correta e verificada | ✔ teste passa (exit 0 verificado fora do run) |
| QA aprovou executando teste, não "no olho" | ✔ rodou gradle com JDK 21 antes de `qa_approve` |
| Governança (devs não commitam; 1 arquivo consolidado; escopo limpo) | ✔ |
| Checkout do usuário preservado | ✔ (só `.gitignore` ganhou `.hivemind/`) |
| Duplicação de trabalho | ✘ 2 devs escreveram o MESMO arquivo numa tarefa pequena |
| Eficiência de tokens | ✘ 1,1M tokens/66 linhas; fricção de gradlew custou ~436k só no dev_1 |

| # | Tarefa | O que aconteceu | Severidade | Issue |
|---|--------|-----------------|------------|-------|
| 4 | T1 | Triagem escalou 2 devs para tarefa de 1 dev e ambos escreveram `PlayerXpTest.kt`; integração consolidou uma versão, mas ~261k tokens desperdiçados | **irrita (custo)** | fatiamento deve checar sobreposição; sugerir `direct` para tarefas pequenas |
| 5 | T1 | `run_shell` no Windows: `gradlew.bat` relativo e `cmd /c ...` (fora da allowlist) custaram ~6 tentativas + diagnóstico de JDK 25→21 (~436k tokens no dev_1) | **irrita (custo)** | shellx deveria executar `.bat`/wrapper do diretório de trabalho; e a allowlist recusa `cmd` |
| 6 | T1 | Custo absoluto: 1,1M tokens (~14k de injeção própria do agy **por chamada** + contexto ADK crescente a cada turno) | **trava p/ uso diário** | compactar contexto entre turnos; considerar `gemini-3.8-flash-low`; default `max_tokens` por run |
| 7 | T1 | `MEMORY.md` (recurso documentado como memória do time) é injetado **só na IDE** (`idecore/runner.go`); CLI `run` não lê — conhecimento caro descoberto num run não ajuda o próximo | **irrita** | injetar memory no orchestrator comum (CLI+IDE) |
| #8 | T1 | ~~CLI `run` não imprime resumo de tokens ao final~~ → **CORRIGIDO (18/11/2026):** `Runner.Run` imprime `Tokens: ↑N entrada · ↓N saída · N total` ao final (e `uso até o erro/parar:` no stderr quando o run morre no meio). TUI e IDE não são poluídas (`io.Discard`) | **feio → resolvido** | — |
| 5 | T1 | ~~`run_shell` no Windows: `gradlew.bat` relativo e `cmd /c ...` (fora da allowlist) custaram ~6 tentativas~~ → **CORRIGIDO (18/11/2026):** `resolveLocalExecutable` no shellx executa wrappers do diretório de trabalho após a policy aprovar (Windows sonda `.bat/.cmd/.exe`) | **irrita → resolvido** | — |
| 7 | T1 | ~~`MEMORY.md` injetado só na IDE~~ → **CORRIGIDO (18/11/2026):** `Runner.Run` injeta `.hivemind/MEMORY.md` (4 KiB) em todo run do CLI; cria starter no primeiro uso | **irrita → resolvido** | — |
| 4 | T1 | ~~Triagem escalou 2 devs para tarefa de 1 dev e ambos escreveram `PlayerXpTest.kt`~~ → **CORRIGIDO (18/11/2026):** dedup mecânico de `dev_tasks` idênticas + prompt endurecido (1 item para tarefas pequenas) | **irrita → resolvido** | — |
| 13 | T3 | ~~loop do QA gira sem os devs entregarem~~ → **CORRIGIDO (18/11/2026):** 3ª reprovação consecutiva sem alteração nova na entrega encerra o loop com motivo (state `qa_feedback` + stderr) | **irrita → resolvido** | — |
| 9 | T2 | Custo por run não caiu com a fricção eliminada (1,21M vs 1,1M): migrou para o **chefe**, que sozinho queimou 535k (44% do run) em triagem+plano+fechamento. O agente de coordenação é o maior gargalo de tokens em todas as tarefas | **trava p/ uso diário** | medir se o plano do chefe pode ser reduzido (menos leitura de arquivos na triagem, plano reaproveitado entre tarefas do mesmo projeto) |
| 10 | T2 | Instructions por agente no `team.yaml` são respeitadas no run e eliminaram a fricção de JDK/gradlew (correção por configuração); o recurso de memória que faltou no CLI (#7) pôde ser contornado com instructions | **informativo** | manter; ainda assim corrigir #7 (memory no CLI) |

## Checklist por tarefa

Para cada tarefa, note SIM/NÃO/PARCIAL:

- [ ] Abriu a pasta e o explorador refletiu o projeto inteiro
- [ ] Navegação por Ctrl+P encontrou os arquivos que você precisava
- [ ] O editor abriu, editou e salvou sem perda de conteúdo
- [ ] A conversa com o time fluiu com streaming legível
- [ ] As permissões/approval gates apareceram no momento certo
- [ ] O resultado do run apareceu como diff e permitiu aceitar/rejeitar
- [ ] Quando o agente editou um arquivo aberto na sua tela, a aba atualizou (file watcher)
- [ ] O terminal integrado rodou o build/teste da stack
- [ ] Commit/branch/PR funcionaram pela UI
- [ ] Retomar a sessão no dia seguinte funcionou
- [ ] Em nenhum momento você abriu outro editor

## Log de quebras

| # | Tarefa | O que aconteceu | Severidade (trava/irrita/feio) | Issue |
|---|--------|-----------------|-------------------------------|-------|
| 1 | setup | `doctor --probe` ignora `AGY_MODEL`: monta `&Agy{Bin}` sem `Model` em `ProbeAgyPrompt` (`modelx/agy.go:748`), testando sempre o modelo default do agy. Com o default fora de cota, o probe reporta "sem créditos" mesmo com slugs funcionando | **irrita** (falso negativo que bloqueia o usuário) | probe deve respeitar `AGY_MODEL`/`AGY_EFFORT` |
| 2 | setup | Modelo default do agy responde "Your AI credits balance is too low" enquanto slugs específicos (`gemini-3.8-flash-low/medium`) funcionam na mesma conta — comportamento do lado do agy, mas o HiveMind pode sugerir fixar slug no erro do probe | **irrita** | mensagem de erro do agy pode sugerir `--model`/spec `agy:<slug>` |
| 3 | setup | Falso alarme de encoding: `team.yaml` é UTF-8 válido, mas PowerShell 5 (sem BOM) exibe mojibake — não é bug do HiveMind; registrou para não repetir a investigação | — | nenhum |

### Custo por chamada do agy (medido)

Probe mínimo "responda apenas OK": **12.283 tokens de entrada** (agy injeta ~12k de contexto próprio) + 17 de saída. Um squad completo (chefe+devs+QA) multiplica isso por ~10-20 chamadas por run — dimensionar `max_llm_calls`/`max_tokens` com isso em mente.


## ~~Falha conhecida do ambiente de dev~~ → RESOLVIDA (18/11/2026)

Os testes que forjam o agy com script `/bin/sh` (`TestAgy*`, `TestPromptNeverInArgv`, `TestStreamJSONDeltas…`, `TestDoctorProbe`, `TestDefaultProviderModel`) pulam no Windows local com mensagem explícita e rodam no CI Linux; `TestLocalEchoAndDeny` foi reescrito cross-plataforma usando o wrapper local do achado #5. `go test ./...` agora fica verde de ponta a ponta no Windows — a suíte local voltou a ser sinal confiável.
