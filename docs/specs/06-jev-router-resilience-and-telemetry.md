# Spec 06 — Latência Zero no Roteamento Jev, Circuit Breakers Unificados, Erros Tipados e Telemetria

> **Status:** Proposta de Refatoração
> **Alvos:** `internal/jev/`, `internal/modelx/`, `internal/orchestrator/jev.go`, `internal/orchestrator/jev_wrapper.go`

---

## 1. Diagnóstico do Estado Atual

### Gargalos e Falhas Observadas
1. **Latência Síncrona no Hot-Path de Roteamento (`Score` / `BuildModel`)**:
   - `newOpenRouter` (`factory.go:322-340`) executa `openRouterCheckAuth` (`GET /key`, 12–15s timeout) de forma síncrona ao construir o modelo.
   - `OpenRouterModels` (`openrouter_models.go:136-234`) dispara até 21 chamadas HTTP síncronas em `enrichOpenRouterDailyDiscounts`.
   - `NewOpenCode` (`opencode.go:406`) bloqueia em `lookupOpenCodeRoute` (GET `/models`, 12s timeout) no cold-start; `ocRouteCache` (`118-120`) ignora `OPENCODE_BASE_URL` por perfil na chave de cache.
   - `orchestrator/jev.go:368-373` e `jev_wrapper.go:254-256` chamam `GetOpenRouterBalanceUSD(ctx)` (2 endpoints `/credits` + `/key`, 4s timeout) **sincronamente dentro do loop de decisão**; falhas não gravam cache negativo, repetindo os requests em toda iteração offline/rate-limited.
   - `JevCredentialPresence` (`jev_catalog.go:14-33`) e `JevCredentialFingerprints` (`jev_health.go:44-70`) executam `LookAgy()` e `LookCursor()` no `PATH` em todo passo de roteamento.
   - `ClassifyWithTypeSafeJev` (`typesafe.go:172`) cria `&http.Client{Timeout: 4*time.Second}` por chamada sem pool de conexões.
2. **Vazamento de `HalfOpenInFlight` e Circuit Breakers Split-Brain**:
   - `HealthStore.TryAcquireHalfOpen` (`jev/health.go:310-345`) trava `HalfOpenInFlight = true` na construção do modelo (`orchestrator/jev.go:433-462`). Se `GenerateContent` falhar com `ErrClassPolicy` (400 / cancelamento em `jev_wrapper.go:88-90`) ou sair antes da conclusão, **nem `RecordSuccess` nem `RecordFailure` são chamados**, travando o circuito em `half_open` com `HalfOpenSlots == 0` para sempre.
   - `jev.HealthStore` (`health.json`) e `modelx.accountPoolState` (`account-pool-health.json`, `account_pool.go:68-147`) operam circuitos desconexos; nenhum dos dois usa lock de arquivo entre processos ao salvar (`saveLocked` vs `lockUsageFile`), causando perda de estado e `DailySpentMicro` entre CLI e IDE.
   - `HealthStore.Summary` (`health.go:632`) muta `h.state.Providers` durante leitura, e `RecordSpend` (`384-394`) re-serializa o JSON inteiro em disco a cada chamada LLM.
3. **Classificação de Erro Frágil e Redação Incompleta**:
   - `jev.ClassifyError` (`health.go:31-96`) e `RecordAccountModelFailure` (`account_pool.go:245-309`) duplicam `strings.Contains` em textos PT/EN; casam substrings nuas `"401"`, `"402"`, `"429"` ou `"limite"`, causando falsos positivos; **omitem TypeSafe** (`"TYPESAFE_API_KEY não configurada"` cai como `ErrClassOutage`).
   - `FormatUpstreamError` (`provider_error.go:18-57`) só extrai HTTP body de `*openai.Error`, deixando `*anthropic.Error` e `*genai.APIError` crus; `stream.go:131-136` ignora `parseAgyError` e redatores.
   - 3 redatores divergentes (`jev/redact.go`, `cursor_stream.go`, `provider_scan.go`) deixam `ya29.`, `oc_` e `ts_` expostos em diferentes fluxos.
4. **Duplicação de Transports / CLI Runners & Cegueira de Telemetria**:
   - Múltiplos `&http.Client{}` ad-hoc; `newAnthropic` Vertex (`factory.go:163-167`) omite `retryHTTPClient()`; `newOpenCodeHTTPClient` (`opencode.go:61-91`) omite `promptCacheTransport`; `promptCacheTransport` + `retryTransport` fazem double-marshal de até 8 MiB por POST.
   - `agy.go`, `stream.go` e `cursor.go` duplicam spawn `.cmd`/`.bat` e leitores NDJSON; `Agy.GenerateContent` (`agy.go:230`) ignora `stream bool`.
   - `usage.go`, `jev_wrapper.go` e `score.go` ignoram `CachedContentTokenCount` (superestimando custo em até 10x), ignoram `DiscountPct` no spend real e zeram métricas de Cursor e execuções CLI (`RecordAIUsage` só existe no `idecore`). `flushUsageLocked` reescreve 31 dias de histórico a cada 2s; `OutcomeLog.NoteQA` usa chave só de `repo`, misturando lanes paralelas.
5. **Bypass de Orçamento & Duplicação no `Score`**:
   - `selectBestInTierWithOpts` (`score.go:354-624`) duplica ~100 linhas entre Pass 1 e Pass 2; safety net (`score.go:173-189`) ativa `ignoreDailyBudget: true` burlando silenciosamente `Policy.DailyUSD`; `InitOnce` em `orchestrator/jev.go:162-203` congela `policy.json` e `.hivemind/jev.yaml` para sempre.

---

## 2. Comparação: HiveMind Atual vs Engenharia Google SRE / Anthropic

| Aspecto | HiveMind Atual | Padrão Google SRE / Anthropic Routing |
|---|---|---|
| **Hot-Path de Decisão** | Faz chamadas `/credits`, `/key`, `/models` e `LookPath` dentro do loop de rota | Leitura 100% em memória (< 1ms); probes e catálogos atualizados por refreshers stale-while-revalidate + `singleflight` |
| **Half-Open Probe** | Flag booleana sem expiração nem `defer release` | **Lease com TTL** (`HalfOpenLeasedUntil`) + liberação automática em `ErrClassPolicy`, cancelamento ou panic |
| **Erros de Provedor** | Parsing por substring em strings PT/EN | **`ProviderError` estruturado** (`StatusCode`, `Code`, `Class`, `ResetAt`, `Detail`) por SDK + redação única |
| **Telemetria de Custo** | Ignora tokens em cache, Cursor e CLI | Contabiliza `CacheRead`/`CacheWrite` tokens, `DiscountPct` e unifica `RecordAIUsage` no orquestrador |
| **Budget Guard** | Bypass silencioso de `DailyUSD` se nenhum modelo couber | Cap diário inviolável sem aprovação explícita (`approveFn("budget", ...)`) |

---

## 3. Especificação Técnica da Solução

1. **Stale-While-Revalidate + Pool HTTP Único (`internal/modelx`):**
   - Criar `sharedHTTPTransport()` único com pool HTTP/2 e keep-alive em `modelx`.
   - Mover `GetOpenRouterBalanceUSD`, `openRouterCheckAuth`, `enrichOpenRouterDailyDiscounts`, `fetchOpenCodeCatalog` e descoberta de binários no PATH para caches com TTL positivo **e negativo** (ex.: falha guarda backoff de 60s) + `singleflight`, nunca bloqueando `Route()`.
   - Incluir `baseURL` na chave do `ocRouteCache`; aplicar `promptCacheTransport` no pipeline OpenCode e `retryHTTPClient` no Anthropic Vertex.
   - Extrair runner compartilhado de subprocesso + scanner NDJSON em `internal/modelx/cli_exec.go` unificando `agy.go`, `stream.go` e `cursor.go`, fazendo `Agy.GenerateContent(..., stream=true)` emitir deltas incrementais.
2. **Typed `ProviderError` e Redação Central (`internal/modelx` + `internal/jev`):**
   - Extrair erros estruturados de `*openai.Error`, `*anthropic.Error`, `*genai.APIError`, `agyErrorEnvelope`, TypeSafe e Cursor CLI.
   - Atualizar `jev.RedactSecrets` com padrões `ya29.`, `oc_`, `ts_`, chaves `refresh_token`/`access_token`, aplicando em todas as bordas (incluindo `stream.go`, `typesafe.go`, `agy_vault_windows.go`).
3. **HealthStore com Lease TTL, File Lock e Unificação (`internal/jev` + `modelx`):**
   - Substituir `HalfOpenInFlight bool` por `HalfOpenLeaseUntil time.Time` e adicionar `ReleaseHalfOpen(provider, model)` garantido via `defer` caso o probe não conclua com sucesso/falha classificável.
   - Unificar `(provider, accountID, model)` sob um contrato de saúde protegido por lock de arquivo OS e debouncing nas gravações de `RecordSpend`.
4. **Score Limpo, Policy Hot-Reload e Telemetria Completa:**
   - Unificar Pass 1/Pass 2 de `selectBestInTierWithOpts` com slice reutilizável; remover bypass silencioso `ignoreDailyBudget: true` do fallback automático (direcionar para fluxo de aprovação de orçamento).
   - Recarregar `policy.json` e `.hivemind/jev.yaml` por `mtime`; incluir `CachedContentTokenCount` e `DiscountPct` no cálculo de custo; chavear `OutcomeLog.NoteQA` por `(repo, laneID)` e mover `RecordAIUsage` para dentro de `orchestrator` + append-only JSONL em `usage.go`.

---

## 4. Critérios de Aceite
- Simulação de cancelamento/400 durante probe `half_open` libera o slot imediatamente sem travar o provedor.
- Roteamento Jev com rede offline retorna em < 5ms sem esperar múltiplos timeouts HTTP síncronos.
- `Policy.DailyUSD` bloqueia ou solicita aprovação explícita mesmo quando todos os candidatos excedem o teto diário.
- Execuções via CLI e turnos com Anthropic Prompt Cache refletem custo e economia reais em `ai-usage`.
