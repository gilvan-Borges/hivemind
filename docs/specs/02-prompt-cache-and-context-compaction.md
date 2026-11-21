# Spec 02 — Estabilidade de Prompt Cache (KV-Cache), Compactação Token-Aware e Schemas

> **Status:** Proposta de Refatoração
> **Alvos:** `internal/orchestrator/orchestrator.go`, `pr_dossier.go`, `context_guard.go`, `handoff.go`, `internal/sessionx/store.go`, `internal/tools/mcp.go`, `internal/resident/prompt.go`, `fence.go`

---

## 1. Diagnóstico do Estado Atual

### O que foi observado no código
1. **Quebra sistemática de KV-Cache por mutação do prefixo (`SystemInstruction`)**:
   - `Runner.llmCore` (`orchestrator.go:1159-1182`) usa `InstructionProvider` antes de **toda** chamada de modelo injetando `stateKeys` e `r.sharedBoard.SummaryFor(adkName)` diretamente no `SystemInstruction`.
   - Sempre que qualquer Dev toca um arquivo (`newTeamBoardTouchCallback` em `team_board.go:334-355`), publica via `share_team_note`, ou um irmão em `parallelagent` grava sua `OutputKey` (`dev_dev_1_report` presente nas `stateKeys` de `dev_2`, `orchestrator.go:883-884`), o **System Prompt muda no meio do turno**, invalidando 100% do cache KV de prefixo na Anthropic/OpenAI/Gemini.
2. **Mutação do array `Tools` no meio do turno (`pr_dossier.go:601-729`)**:
   - `newChiefToolStrippingModelCallback` remove 15 ferramentas de exploração de `req.Config.Tools` e altera `cfgCopy.SystemInstruction` quando `exploreCalls >= maxExploreCalls`, garantindo miss completo de cache nas chamadas finais do Chefe.
3. **Duplicação de memória e bloat de schemas**:
   - `teamMemorySnippet()` é injetado na mensagem inicial do usuário (`orchestrator.go:402`) **e** armazenado em `sess.State()["team_memory"]` (`507-511`) para ser re-injetado no `SystemInstruction` de cada subagente (`1165-1175`).
   - `internal/tools/mcp.go:77-105` registra `mcp_call` **e mais um wrapper** `mcp_<server>__<name>` por ferramenta MCP expondo `remote.Schema` serializado dentro da `Description` com um único campo `arguments: string` (forçando JSON dentro de JSON).
   - Tools Git sem argumentos (`tools.go:810-812, 822-824, 859-861, 878-880`) declaram `Unused string json:"unused,omitempty"` em vez de `struct{}`.
4. **Compactação cega a tokens, destrutiva e duplicada**:
   - `compactLLMRequestInTurn` (`context_guard.go:67-219`) dispara apenas por contagem fixa (`keepRecent = 8`, `threshold = 600` bytes), só compacta chaves `"text"` e `"entries"` (`278-309`) e não considera o limite de janela do modelo escolhido nem gera resumo estruturado.
   - `internal/resident/prompt.go:139-216` (`compactInTurnRequest`) reimplementa uma versão reduzida divergente que ignora `FunctionCall.Args` de `write_file`/`edit_file`.
   - `sessionx.CompactOldToolOutputs` (`sessionx/store.go:277-347`) altera destrutivamente eventos históricos no JSON da sessão em disco (inclusive no meio do run em `orchestrator.go:2436, 2455`).
5. **Cortes UTF-8 inseguros**:
   - `handoff.go:80, 97-98, 108, 136-137, 145-146` e `fence.go:75` fatiam strings por índice cru de bytes (`s[:N]`), podendo quebrar sequências UTF-8 multibyte (acentuação em PT-BR) e gerar erro 400 nos provedores.

---

## 2. Comparação: HiveMind Atual vs Engenharia Anthropic / Google

| Mecanismo | HiveMind Atual | Padrão Anthropic Harness / Google |
|---|---|---|
| **Prefixo Cacheável** | `SystemInstruction` e `Tools` variam entre chamadas de um mesmo turno | Prefixo (`SystemInstruction` + `Tools`) **100% imutável** byte-a-byte durante todo o turno; `cache_control` aproveita 90% de desconto na leitura |
| **Contexto Dinâmico** | Re-escreve `SystemInstruction` a cada edição de colega | Injetado como bloco `<system-reminder>` anexo à **mensagem trailing** (`user` / último `tool_result`), preservando todo o prefixo anterior |
| **Controle de Orçamento de Tools** | Remove declarações de tools do schema em tempo de execução | Mantém schema idêntico; bloqueia no handler da tool retornando erro estruturado acionável ("Orçamento de busca esgotado; edite ou conclua") |
| **Compactação de Contexto** | Contagem arbitrária (`keepRecent = 8`), muta arquivo histórico | Disparada por % real da janela (`~75%` de `ContextWindow`), preserva transcrição original intacta e mantém marcador/resumo não-destrutivo |
| **Schemas MCP** | JSON Schema enfiado como texto na `Description` + `arguments: string` | `jsonschema` nativo no `functiontool` (ou catálogo sob demanda via `mcp_search` + `mcp_call` sem inflar o prompt raiz) |

---

## 3. Especificação Técnica da Solução

### 3.1 Prefixo Estático + `<system-reminder>` na Cauda
1. Alterar `llmCore` (`orchestrator.go:1159`) para que `StaticInstruction` (role prompt, governança fixa, skill guide estática) seja constante durante toda a execução do agente.
2. Todo dado dinâmico entre turnos (`sharedBoard.SummaryFor(adkName)`, deltas de irmãos em `stateKeys`, alertas de orçamento restante) passa a ser anexado via `BeforeModelCallback` **apenas ao último `genai.Content` da requisição** como bloco XML `<system-reminder>...</system-reminder>`, nunca no `SystemInstruction`.
3. Remover `team_memory` da duplicação: manter memória do time em um único lugar estável no início da sessão (ou bloco estático no system prompt sem duplicar no user prompt).

### 3.2 Remoção de Tool Stripping Dinâmico
1. Substituir a remoção de tools de `req.Config.Tools` em `newChiefToolStrippingModelCallback` (`pr_dossier.go:682-727`) por um guardrail no `BeforeToolCallback`: quando `exploreCalls >= maxExploreCalls`, bloquear a execução da tool retornando resposta curta orientando síntese imediata, mantendo `req.Config.Tools` e `SystemInstruction` idênticos.

### 3.3 Compactador Único Token-Aware & Não-Destrutivo
1. Unificar `context_guard.go` e `resident/prompt.go` em um pacote compartilhado `internal/contextx`:
   - Estimação rápida de tokens do `LLMRequest` comparada ao `ContextWindow` do modelo roteado (via catálogo Jev).
   - Limiar adaptativo: micro-compactação de tool results antigos ao passar de `55%` do `ContextWindow`, e sumarização/corte estruturado ao passar de `75%`.
   - Compactação aplicada em **projeção de leitura em memória** antes do envio ao modelo; **nunca** sobrescrever argumentos/respostas históricas na gravação principal de `sessionx` (ou salvar apenas snapshots de resumo como evento append-only de compactação).
2. Substituir todo slice `s[:N]` em `handoff.go` e `fence.go` por `strx.TruncateBytes` (ou corte por limite de runa UTF-8 válido).

### 3.4 Schemas Limpos (`mcp.go` e `tools.go`)
1. Remover o campo `Unused` das structs vazias em `internal/tools/tools.go` (usar `struct{}`).
2. Em `internal/tools/mcp.go`, passar o `InputSchema` real do servidor MCP ou restringir o registro raiz a `mcp_list_tools` + `mcp_call` sob demanda quando houver > 5 tools MCP externas, evitando inflar todo turno com schemas textuais gigantes.

---

## 4. Critérios de Aceite
- Em uma execução de 10 turnos de ferramentas, `hash(SystemInstruction + Tools)` permanece **idêntico** do turno 1 ao turno 10 (teste unitário verificando `BeforeModelCallback` no `orchestrator_test.go`).
- Zero chamadas de rede extras de cache-miss causadas por `TeamBoard` ou stripping de tools.
- Textos em PT-BR truncados em `handoff.go` e `fence.go` passam em testes com strings contendo emojis e acentos exatamente na fronteira `N-1..N+1` (`utf8.ValidString(out) == true`).
- `session.Events()` preserva o histórico bruto original após compactação em turno.
