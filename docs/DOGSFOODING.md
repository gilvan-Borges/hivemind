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

**3 tarefas, 2 entregas verificadas e aprovadas por inspeção independente, 1 falha real que expôs os bugs mais valiosos da noite. Custo total: 3.561.181 tokens** (input 3.37M — o agy injeta ~14k por chamada e o contexto ADK cresce a cada turno; ~1,2M por run independentemente do tamanho da tarefa).

**O que funciona de verdade (verificado fora do run):** pipeline ponta-a-ponta, governança (devs nunca commitam; escopo limpo), QA que executa teste antes de aprovar, worktrees sem tocar no checkout do usuário, PRs reais abertos, instructions por agente respeitadas, sessões persistidas com telemetria por agente, triagem que escala devs.

**O que quebra (por severidade):**

| # | Severidade | Achado |
|---|---|---|
| 6 | **trava (custo)** | ~1,2M tokens por run, tamanho-independente; chefe/coodenação ≈ 44% |
| 9 | **trava (custo)** | — |
| 11 | **trava (corretude)** | worktrees de dev/entrega **reutilizadas entre runs sem limpeza**: mudança suja de um run vaza para o próximo e é consolidada como entrega |
| 12 | **trava (resiliência)** | quota 429 do agy mata o run inteiro sem checkpoint; o envelope `AGY_ERROR` do agy diz `"retryable":true` e o HiveMind ignora (provider agy não faz retry de 429) |
| 13 | **irrita (custo)** | loop do QA gira (`qa_request_changes`×6) quando os devs não entregam — queima tokens sem backpressure |
| 4 | **irrita (custo)** | fatiamento duplicado em tarefas pequenas |
| 1, 2, 7, 8 | irrita/feio | probe ignora `AGY_MODEL`; mensagem de erro enganosa; `MEMORY.md` só na IDE; CLI não imprime tokens |

**Pendente:** o critério original da Fase 1 ("tarefa real sem abrir outro editor") exige o **cockpit** — 30 min suas na IDE com uma tarefa dessas. O motor está validado: funciona, e agora sabemos exatamente onde ele quebra. A quota do agy ficou exaurida (reset sozinho); as worktrees sujas em `.hivemind/worktrees/` ficaram como estavam (evidência) e não afetam seu checkout.

## Correções derivadas do dogfooding (18/11/2026)

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
| 8 | T1 | Telemetria existe no `sessions/*.json` (`usageMetadata` por evento), mas o CLI `run` não imprime resumo de tokens ao final (só a IDE tem painel) | **feio** | resumo de uso no fim do `run` |
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


## Falha conhecida do ambiente de dev (não é da IDE)

Os testes `TestProbeAgy*`, `TestAgyAuthAndMissingBinary`, `TestStreamJSONDeltasStayOutOfArgv` (modelx) e `TestLocalEchoAndDeny` (shellx) **falham no Windows local** mesmo sem nenhuma mudança de código: gravam binários fake sem extensão `.exe` e invocam `echo` real. Passam no CI (Linux). Débito registrado no ROADMAP (transversais) — job de CI no Windows cobriria isso.
