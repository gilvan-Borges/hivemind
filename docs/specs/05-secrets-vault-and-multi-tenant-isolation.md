# Spec 05 — Cofre Unificado (`secretx`), Isolamento do Antigravity e Multi-Tenant Pool

> **Status:** Implementado (Onda 1 — 2026-11-21; `go test -count=1 ./internal/{secretx,modelx}/...` + `ide/internal/relayclient/...` verde)
> **Alvos:** `internal/modelx/keys.go`, `profiles.go`, `opencode.go`, `agy_vault_windows.go`, `account_pool.go`, `jev_catalog.go`, `jev_health.go`, `internal/secretx/`

---

## 1. Diagnóstico do Estado Atual

### Problemas Observados no Código
1. **Split-Brain do Vault e Segredos em Texto Plano**:
   - `internal/secretx` (`vault.go`, `vault_windows.go` com DPAPI + Windows Credential Manager e chunking `maxCredBlobBytes = 2048`) é usado **apenas por `internal/resident`**.
   - `internal/modelx` ignora `secretx` e grava credenciais brutas em JSON plano (`0o600` via `writeSecretFile` em `keys.go:228-268`):
     - `HiveMind/api-keys.json` (`keys.go:43-53, 164-213`)
     - `HiveMind/provider-profiles.json` (`profiles.go:96-106, 219-244`, campo `ProviderProfile.Env`)
     - `HiveMind/credentials.json` (`opencode.go:127-136, 174-201`)
     - `HiveMind/agy-profiles/<id>/oauth-session.json` (`agy_vault_windows.go:223-233, 256-301` com `access_token` e `refresh_token` Google OAuth crus).
   - `agy_vault_windows.go:70-219` duplicou P/Invoke Win32 `advapi32.dll` (`CredWriteW`/`CredReadW`) ao invés de usar `internal/secretx`.
   - Precedência inconsistente entre `EnvOrStored` (`keys.go:102-114`), `LoadOpenCodeCredentials` (`opencode.go:141-170`, que perde o contexto chamando `resolveEnvForContext(context.Background(), ...)`) e `NewOpenCode` (`378-394`).
2. **Mutação do Slot Global `gemini:antigravity` e Contenção por Lock In-Process**:
   - `WithAgyVaultLease` (`agy_vault_windows.go:359-421`) e `fetchAllAgyAccountQuotas` (`usage.go:727-775`) sobrescrevem o slot compartilhado `gemini:antigravity` no Windows Credential Manager, protegidos apenas por um `sync.Mutex` **em memória do processo** (`agyVaultSwitchMu` em `agy_vault_windows.go:92`).
   - Dois processos HiveMind (ou uma sessão externa do Antigravity IDE) entram em race no slot global; crash durante `fn()` deixa a conta errada no Credential Manager do usuário; chamadas simultâneas para **contas diferentes** (`Conta 1` e `Conta 2`) ficam serializadas no mesmo mutex; e `fetchAllAgyAccountQuotas` segura o mutex global durante subprocessos sequenciais de até 15s por conta.
3. **Vazamento Cross-Organization no Failover do Pool (`account_pool.go`)**:
   - Em `ListAccountPoolForProvider` (`account_pool.go:350-439`), se um workspace casa com uma organização (`OrgPatterns`), `CredentialIsolated(ctx, prov)` retorna `false` e o bloco `364-375` carrega **todos** os perfis prontos daquele provedor (incluindo contas de *outras* organizações e pessoais) dentro de `allForProv`. Em caso de 429/cota na Conta 1, `pooledAccountLLM.GenerateContent` (`501-581`) faz failover silencioso para a conta de outra empresa/organização!
   - `JevCredentialPresence` (`jev_catalog.go:14-33`) e `JevCredentialFingerprints` (`jev_health.go:44-70`) chamam `resolveEnvForContext` com `context.Background()`, descartando o workspace/tenant ativo.

---

## 2. Comparação: HiveMind Atual vs Engenharia Google IAM / Zero-Trust

| Dimensão | HiveMind Atual | Padrão Google Cloud / Zero-Trust |
|---|---|---|
| **Armazenamento de Chaves/OAuth** | JSONs em texto plano no `%LOCALAPPDATA%` fora do `secretx` | Metadados não-sensíveis em JSON + **segredos exclusivamente no `secretx.Vault`** (DPAPI/CredMan/Keychain/SecretService) |
| **Execução Multi-Conta CLI** | Swapping destrutivo do slot global `gemini:antigravity` com mutex de processo | Isolamento por perfil via env/diretório próprio (`CLOUDSDK_CONFIG`/`HOME` por conta) ou, quando o CLI exigir slot único, **lock nomeado de SO entre processos** + restauração garantida fora de probes |
| **Failover Multi-Tenant** | Pool mistura todas as contas configuradas após matched org | Particionamento estrito por **Tenant/Org**: workspace da Org A só enxerga contas da Org A (ou opt-in explícito de fallback pessoal) |
| **Contexto de Credenciais** | `context.Background()` em scanners/presence | Contexto do workspace propagado de ponta a ponta |

---

## 3. Especificação Técnica da Solução

### 3.1 Migração Total de Segredos para `internal/secretx`
1. Estender `secretx.Vault` com namespaces tipados (`modelx/api-keys`, `modelx/profiles/<id>`, `modelx/agy-oauth/<id>`, `modelx/opencode`) mantendo chunking automático quando > 2048 bytes no CredMan.
2. Migrar automaticamente arquivos legados em texto plano (`api-keys.json`, `provider-profiles.json`, `credentials.json`, `oauth-session.json`) na primeira leitura: gravar o segredo no `secretx.Vault`, substituir valores sensíveis por referências `"vault:<key>"` nos arquivos JSON de metadados e remover os tokens crus do disco.
3. Remover as duplicações de Win32 P/Invoke em `agy_vault_windows.go`, delegando toda leitura/escrita de Credential Manager para `secretx`.

### 3.2 Isolamento Multi-Conta e Lock Cross-Process no AGY
1. Verificar/priorizar execução da CLI `agy` com `IsolateEnv` por conta (variável de diretório de credenciais por perfil sempre que suportado) eliminando dependência do slot compartilhado `gemini:antigravity` durante execução paralela de agentes.
2. Para operações que ainda dependam do slot `gemini:antigravity`:
   - Proteger com **Named Kernel Mutex no Windows** (`Local\HiveMind.AgyVaultSlot`) além do `sync.Mutex` local.
   - Registrar recovery marker no vault antes do lease para restaurar o backup da sessão do usuário na inicialização caso o processo anterior tenha sofrido crash.
   - Nunca rodar `fetchAllAgyAccountQuotas` segurando o lock de execução se uma execução ativa precisar do slot; usar probe não-bloqueante (`TryLock`) e cache stale.

### 3.3 Barreira Multi-Org no `ListAccountPoolForProvider`
1. Em `ListAccountPoolForProvider(ctx, prov)`:
   - Se `ctx` resolve uma organização ativa (`orgPatterns` casam com o workspace): filtrar o pool **apenas** para perfis que pertencem àquela mesma organização (`allowCrossOrgPool` só via flag explícita no perfil/config). Nunca incluir contas de outros clientes/organizações no failover automático.
2. Propagar o `ctx` com workspace em `JevCredentialPresence`, `JevCredentialFingerprints` e `LoadOpenCodeCredentials`.

---

## 4. Critérios de Aceite
- Após migração, nenhuma chave de API (`sk-`, `AIza`, `oc_`, `ts_`) ou `refresh_token` existe em texto plano em `%LOCALAPPDATA%\HiveMind\*.json`.
- Teste de isolamento multi-org: workspace da Org A com `agy` da Org A esgotado **nunca** faz failover para conta da Org B nem conta pessoal quando `OrgPatterns` estão configurados.
- Teste cross-process / crash recovery garantindo restauração do slot original do Antigravity IDE.
