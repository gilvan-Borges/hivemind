# Spec 09 — Segurança E2EE do Relay/LAN Bridge, Sandbox/Pool MCP e Runtime Único

> **Status:** Em Progresso (Onda 3 parcial: `W3-05` implementado — `internal/remote` e `internal/relayclient` promovidos para o módulo raiz; `W3-04` `internal/runtime/bootstrap.go` e pool `mcpx` pendentes)
> **Alvos:** `relaywire/`, `internal/relay/`, `ide/internal/relayclient/`, `ide/internal/remote/`, `internal/mcpx/`, `cmd/`, `internal/cli/`, `internal/tui/`, `ide/internal/idecore/runner.go`, `docs/MODELO_AMEACAS.md`

---

## 1. Diagnóstico do Estado Atual

### A. Relaywire, Relay Server & LAN Bridge
1. **Ausência de Anti-Replay/Idempotência em RPCs do Relay:**
   - `internal/relay/ws.go:261,285` zera `env.Seq = 0` ao encaminhar `TypeRPC`/`TypeRPCResult`, e `ide/internal/relayclient/client.go:640-660` (`Client.handleRPC`) não mantém dedupe de IDs, nonce ou janela de sequência/timestamp. Um frame `TypeRPC` replayed pode re-disparar `/api/v1/runs/start`, `/runs/redirect` ou `/approvals/resolve`.
2. **Segredos do Relay em Texto Plano (`relay.json`):**
   - `ide/internal/relayclient/config.go:14-26, 49-62` e `client.go:778-780` salvam `RefreshToken`, `DeviceToken` e chave privada `X25519` em JSON plano (`%LOCALAPPDATA%\HiveMind\relay.json`), contrariando `docs/MODELO_AMEACAS.md:14-15` (diferente do `lan.go:434-480` que já usa `secretx.NewOSVault()`).
3. **DoS por Wipe de Rate-Limiter e Pontos Sem Limite:**
   - `internal/relay/limiter.go:91-93,107-109` e `ide/internal/remote/lan.go:648-650` **apagam o mapa inteiro** ao atingir `> 4096` chaves (`l.hits = map[string]*rate.Limiter{key: lim}`), permitindo zerar todos os bloqueios de força bruta ativos enviando 4.097 requests.
   - `handleRedeemQRTicket` (`relay/server.go:229-266`) guarda `qrTickets` apenas em RAM e não verifica `LoginBlocked`; `handleRefresh` (`268-281`) não tem throttle de falhas; `clientIP` (`733-747`) lê o hop mais à esquerda do `X-Forwarded-For` (spoofável); `handleWS` aceita bearer em `Sec-WebSocket-Protocol` (`388-399`) sem rate-limit de frames por conexão e pode bloquear em `c.send` durante replay inicial de 500 eventos; presença online/offline da IDE não é propagada via `Hub.Attach`/`Detach`.
4. **LAN Bridge (`ide/internal/remote/`):**
   - `lan.go:285` grava `saveConfig` (via `os.WriteFile` não-atômico em `remote/config.go:119`) em **toda** requisição autenticada (até `GET`); `fr.Seq == dev.LastSeq+1` (`276-283`) impõe head-of-line blocking serializando todas chamadas do app mobile.

### B. MCP Host (`internal/mcpx/`)
1. **RCE de Workspace Sem Gate & Vazamento Completo de `os.Environ()`:**
   - `mcpx/config.go:106-122` carrega automaticamente `<workspace>/.hivemind/mcp-servers.json` ao iniciar o turno sem aprovação do usuário; `sdk.go:31` injeta **todo `os.Environ()`** (incluindo `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GITHUB_TOKEN`, `JIRA_API_TOKEN`) nos processos MCP de terceiros (`npx -y ...@latest` sem hash/versão pinada em `defaults.go`), sem nenhum `sandboxx`; Windows usa `cmd.exe /d /c` (`sdk.go:63-65`); `config.go:247` grava com `0644`.
2. **Cold Spawn por Turno & Timeouts Infinitos:**
   - Todos os servidores MCP são iniciados sequencialmente (20s timeout cada) e encerrados a cada turno (`idecore/runner.go:859,1091`, `runcmd.go:83`, `tui/model.go:399`); `Tools()` e `Call()` não têm timeout próprio e `cmd.Stderr` é descartado em `io.Discard` (`sdk.go:39`).

### C. Wiring Divergente CLI / TUI / IDE
- Quatro duplicações divergentes de construção do `Runner` (`cli/runcmd.go:83-108`, `tui/model.go:397-426`, `idecore/runner.go:856-919` e `1088-1143`). CLI e TUI ficam sem `Sessions`, `GitHub`, `Jira`, `EnableTriage`, `ApproveGate` e `RecordAIUsage`. Além disso, `hivemind serve` (`cli/servecmd.go:31-42`) depende de localizar/executar `hivemind-ide.exe`.

---

## 2. Comparação: HiveMind Atual vs Padrão Google Zero-Trust / Anthropic MCP

| Camada | HiveMind Atual | Padrão Google / Anthropic |
|---|---|---|
| **Anti-Replay Relay** | Relay zera `Seq`; IDE executa qualquer `TypeRPC` decifrado | `rpc_id` + `seq` monotônico/timestamp **autenticados dentro do envelope cifrado E2EE** + cache LRU anti-replay |
| **Rate Limiting** | Mapa apaga todos lockouts com >4096 IPs/chaves | Cache LRU com capacidade fixa + expiração passiva (nunca zera `failState` ativo) |
| **MCP Third-Party** | Executa config do repo automaticamente com todas env vars do host | **Workspace Trust Gate** por SHA-256 + env allowlist estrita + timeout por tool call + stderr em ring buffer + sandbox |
| **Bootstrap CLI/TUI/IDE** | 4 cópias manuais dessincronizadas + `serve` acoplado à GUI | Único `orchestrator.Bootstrap` + pacote `internal/remote` independente da GUI |

---

## 3. Especificação Técnica da Solução

### 3.1 Hardening Relaywire, Relay Server & LAN Bridge
1. **Protocolo E2EE Anti-Replay:** adicionar `Seq uint64` + `IssuedAtUnix int64` + `Nonce` dentro do payload cifrado `RPCRequest` (`relaywire/protocol.go`) e validar janela temporal (`±90s`) + LRU dedupe em `relayclient.Client.handleRPC`.
2. **Vault no `relayclient`:** mover `RefreshToken`, `DeviceToken` e `PrivateKey` X25519 para `secretx.NewOSVault()`, mantendo `relay.json` apenas com IDs públicos e URLs via `atomicx.WriteFile`.
3. **Relay Server & LAN:**
   - Substituir reset global `len > 4096` por eviction LRU das chaves inativas mais antigas (sem descartar lockouts em curso).
   - Persistir `qrTickets` no SQLite com remoção atômica (`DELETE ... RETURNING`) e validar `LoginBlocked` em `handleRedeemQRTicket`.
   - Configurar `HIVEMIND_RELAY_TRUSTED_PROXIES`, transmitir eventos `TypePeerPresence` no `Hub.Attach`/`Detach`, adicionar bucket de rate-limit por conexão WS e autenticar via `Authorization` header (ou ticket de curta duração) evitando token longo em `Sec-WebSocket-Protocol`.
   - Na LAN bridge, aceitar janela deslizante de 64 sequências (bitmap `uint64`) em `LanDevice` e gravar config atomicamente com debounce (ou apenas em rotação/pareamento).

### 3.2 Hardening e Pool do `internal/mcpx`
1. **Workspace Trust Gate:** exigir confirmação explícita (persistida em `%LOCALAPPDATA%\HiveMind\trusted-mcp.json` com hash SHA-256 de `.hivemind/mcp-servers.json`) antes de rodar servidores definidos no repositório.
2. **Isolamento de Ambiente e Comando:** limpar `cmd.Env` para baseline mínima + chaves declaradas em `server.Env`; resolver binários sem `cmd.exe /c`; capturar últimos `16 KiB` de `stderr` para diagnóstico; aplicar timeouts individuais (`Tools: 15s`, `Call: 60s`); fixar versões no `defaults.go`; gravar config com `0600`.
3. **Pool Persistente na IDE:** manter conexões MCP quentes por workspace com health-check e reconexão transparente ao invés de spawn síncrono por turno (paralelizado com `errgroup`).

### 3.3 Runtime Único & Desacoplamento do `serve`
1. Extrair `internal/runtime/bootstrap.go` construindo `Runner`, `GitHub`, `Jira`, `Sessions`, `mcpx`, `skillx` e `NewJevModelFactory` para CLI, TUI e IDE.
2. Mover `ide/internal/remote` e `ide/internal/relayclient` para `internal/remote` / `internal/relayclient` permitindo `hivemind serve` 100% headless sem `hivemind-ide.exe`.

---

## 4. Critérios de Aceite
- Replay do mesmo pacote cifrado `TypeRPC` é rejeitado por `relayclient` com erro idempotente/seguro.
- Envio de 5.000 IPs aleatórios não limpa o lockout de IP atacante no `Limiter`.
- Repo malicioso contendo `.hivemind/mcp-servers.json` é bloqueado até aprovação explícita do usuário e nunca recebe `ANTHROPIC_API_KEY`/`GITHUB_TOKEN` do ambiente pai.
- `hivemind run` em CLI cria/retoma sessão em `sessionx`, registra uso em `ai-usage` e respeita `ApproveGate`.
