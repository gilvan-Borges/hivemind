# Spec 03 — Concorrência, Eliminação de Data Races e Persistência Atômica

> **Status:** Proposta de Refatoração
> **Alvos:** `internal/sessionx/store.go`, `internal/resident/routine.go`, `internal/resident/resident.go`, `internal/resident/lock_windows.go`, `internal/orchestrator/orchestrator.go`, `internal/orchestrator/jev_wrapper.go`, `internal/workspace/storage.go`, `internal/reviewx/review.go`, `internal/gitx/integrate.go`

---

## 1. Diagnóstico do Estado Atual

### Data Races e Corrupções Observadas
1. **`sessionx/store.go` — Ponteiros Compartilhados e Lock de Disco Global**:
   - `fileSession.Events()` (`store.go:596-601`) copia apenas a slice de topo (`cp := append([]*session.Event(nil), s.events...)`), devolvendo ponteiros `*session.Event` compartilhados.
   - `CompactOldToolOutputs` (`store.go:277-347`) altera `part.FunctionCall.Args[...]` e `part.FunctionResponse.Response[k]` **in-place** sob `fs.mu.Lock()`, criando data race com qualquer leitor iterando `Events()` após a liberação do mutex.
   - `Store.Create` (`store.go:78-103`) mantém `Store.mu` bloqueado durante gravação completa em disco (`writeLocked`, `ensureIndexLocked`, `writeIndexLocked`), serializando todas as conversas e abas abertas.
2. **`internal/resident/routine.go` — Lost-Update em `routines.json`**:
   - `Scheduler.fire()` (`routine.go:390-396`) roda em goroutine `time.AfterFunc` chamando `ClaimDue` (`177-237`: read -> modify -> `writeRoutines`), enquanto `Broker.handleCronAdd` (`broker.go:955-975`) chama `SaveRoutine` (`107-137`) concorrentemente sem nenhum mutex ou file lock compartilhado.
3. **Estado Mutável Sem Mutex em Orquestração e Resident**:
   - `runTriageChief` (`orchestrator.go:2576-2826`): callback `delegate_to_squad` muta `delegated`, `planCardEmitted`, `r.chiefPlan`, `r.devSlices`, etc. sem proteção caso o modelo emita tool calls paralelas.
   - `jevModelWrapper.GenerateContent` (`jev_wrapper.go:60-127`): libera `w.mu` entre leitura de `w.current` (`60-63`) e atribuição `w.current = nextLLM` (`124-127`).
   - `resident.Runner.RunTurn` (`resident.go:174`) grava `r.LastBroker = broker` sem lock enquanto `runSpawn` executa até 2 subagentes simultâneos (`424-477`).
4. **TOCTOU e Escritas Não-Atômicas**:
   - `internal/workspace/storage.go` (`SessionsDir:85`, `MemoryPath:104`, `AutoReviewStatePath:113`) dispara `migrateLegacyFile` / `migrateLegacySessions` em todo getter sem `sync.Mutex` / `sync.Once`, gerando corrida TOCTOU quando múltiplos workers abrem o mesmo workspace.
   - `internal/reviewx/review.go` (`Add:38-70`, `Send:73-107`, `rewriteNotes:167`), `internal/gitx/integrate.go` (`copyBetweenTrees:379-401`) e `internal/modelx/openrouter_models.go:384` usam `os.WriteFile` direto em vez de escrita atômica (`temp` + `Sync` + `Rename`). `review.go:145-153` usa scanner de 64 KiB e ignora erros de linha silenciosamente.
   - `internal/resident/lock_windows.go:16-44` (`TryAcquireLock`) usa arquivo PID sujeito a TOCTOU em vez de kernel named mutex (`CreateMutexW`), e `ide/main.go` / `remote/host.go` não compartilham trava de instância única GUI vs `hivemind serve`.

---

## 2. Comparação: HiveMind Atual vs Engenharia Google (Go `-race` Clean)

| Área | HiveMind Atual | Padrão Google Go Engineering |
|---|---|---|
| **Eventos de Sessão** | Slice rasa de `*session.Event` mutada in-place | Eventos imutáveis após persistência (ou deep-clone na fronteira de leitura) + compactação como evento derivado |
| **I/O sob Mutex** | Mutex global do Store travado durante `MarshalIndent` + FS write | Locking por sessão (`fs.mu`), criação de registro sob `Store.mu` e flush de disco fora do lock global |
| **Persistência Compartilhada** | `os.WriteFile` sem lock entre goroutines/processos | Pacote utilitário `internal/atomicx` (`TempFile` + `Sync` + `Rename` + flock/OS mutex quando multi-processo) |
| **Instância Única Windows** | Arquivo PID com janela de corrida | Named Kernel Mutex Win32 (`CreateMutexW` com prefixo `Local\HiveMind...`) liberado automaticamente pelo SO em crash |

---

## 3. Plano Técnico de Refatoração

1. **Criar `internal/atomicx` (helper compartilhado único):**
   - `WriteFile(path string, data []byte, perm os.FileMode) error`: cria arquivo temporário no mesmo diretório, `Write`, `Sync`, `Close`, `os.Rename` (com retry curto no Windows para locks transitórios de antivírus/indexador).
   - Substituir escritas diretas em `reviewx`, `gitx/integrate.go`, `openrouter_models.go`, `remote/config.go`, `routine.go` e `sessionx`.
2. **Corrigir `internal/sessionx/store.go`:**
   - Remover `CompactOldToolOutputs` in-place (ou fazê-lo criar clones de evento). Em `Events()`, retornar clones imutáveis ou marcar eventos append-only como somente leitura.
   - Mover I/O de disco em `Store.Create` para fora de `Store.mu`.
3. **Serializar `routines.json` e `workspace/storage.go`:**
   - Adicionar `mu sync.Mutex` (+ lock de arquivo quando compartilhado) no `Scheduler`/repositório de rotinas protegendo `ClaimDue`, `SaveRoutine`, `DeleteRoutine` e `SetRoutineEnabled`.
   - Proteger `migrateLegacyFile` / `migrateLegacySessions` em `workspace/storage.go` com mapa de `sync.Once` ou mutex por `(src, dst)` path.
4. **Corrigir races em `orchestrator.go`, `jev_wrapper.go`, `resident.go` e `lock_windows.go`:**
   - Proteger closure `delegate_to_squad` em `runTriageChief` com mutex local; atomic/CAS geracional em `jevModelWrapper.current`; remover campo mutável global `LastBroker` (retornar/passar no contexto do turno).
   - Substituir PID file em `resident/lock_windows.go` por `CreateMutexW` real e adicioná-lo no startup de `remote/host.go` / `ide/main.go`.

---

## 4. Critérios de Aceite
- `go test -race -count=1 ./internal/sessionx/... ./internal/resident/... ./internal/orchestrator/... ./internal/workspace/... ./internal/reviewx/...` executando sem **nenhum** warning de data race.
- Testes de estresse concorrente com 16 goroutines chamando `Scheduler.ClaimDue` + `SaveRoutine` e `workspace.SessionsDir` em paralelo passando 100%.
