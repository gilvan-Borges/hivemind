# Spec 04 — Blindagem de Ferramentas, Path Security (`pathsec`) e Sandbox OS

> **Status:** Implementado (Onda 1 — 2026-11-21; `go test -count=1 ./internal/{pathsec,sandboxx,shellx,tools}/...` verde)
> **Alvos:** `internal/workspace/jail.go`, `internal/pathsec/sensitive.go`, `internal/tools/tools.go`, `internal/tools/search.go`, `internal/sandboxx/sandbox_windows.go`, `internal/sandboxx/sandbox_linux.go`, `internal/shellx/exec.go`

---

## 1. Diagnóstico do Estado Atual

### Vulnerabilidades e Lacunas Observadas
1. **Bypass de arquivos sensíveis nas ferramentas do Squad (`workspace.Resolve` vs `pathsec.IsSensitivePath`)**:
   - `readFileRange` (`tools.go:305-314`) e `runSearchText` (`search.go:98, 173`) chamam `pathsec.IsSensitivePath`.
   - Porém `workspace.Resolve` (`workspace/jail.go:24-80`) bloqueia apenas `.git` e `.hivemind` — **não verifica `pathsec.IsSensitivePath`**. Assim, `write_file` (`tools.go:440`), `edit_file` (`478`), `list_dir` (`394`) e `find_files` (`search.go:280-379`) no Squad **não bloqueiam `.env`, `credentials.json`, `api-keys.json`, `id_rsa` ou `*.pem`** (enquanto `internal/resident/broker.go:565, 662, 678` bloqueia todos eles).
   - `pathsec/sensitive.go:11-81` também deixa passar artefatos comuns de segredo: `.crt`, `.der`, `.kube/config`, `.docker/config.json`, `terraform.tfstate`, `.htpasswd` e `*secret*.toml/.ini/.properties`.
2. **Defeitos no Sandbox Windows `AppContainer` (`sandbox_windows.go`)**:
   - `runShellSandboxed` (`tools.go:772`) passa `AllowNetwork: true`, mas `sandbox_windows.go:376-380` zera `secCaps.CapabilityCount = 0` ignorando `opts.AllowNetwork`, quebrando testes/builds que acessam localhost/rede dentro do sandbox.
   - `grantAppContainerACL` (`198-263, 322-331`) grava permanentemente ACEs para `HiveMind.Resident.Sandbox.v2` nas pastas `WriteRoots`, `ReadRoots` e `filepath.Dir(resolvedExe)` (instalações do Go/Node). Em instalações não-elevadas onde `exeDir` está em `Program Files`, `SetNamedSecurityInfoW` falha e aborta o comando sandboxed; além disso as ACEs nunca são revogadas.
3. **Exposição excessiva no Sandbox Linux `bwrap` (`sandbox_linux.go:88-90`)**:
   - `--ro-bind / /` expõe toda a raiz do host (incluindo `~/.ssh`, `~/.aws`, `~/.config`, `/etc/shadow` legíveis pelo UID) em modo somente leitura dentro do sandbox, sem mascarar `$HOME` com `--tmpfs`.
4. **Ergonomia e Confiabilidade de `read_file`, `edit_file`, `search` e `run_shell`**:
   - `read_file` sem `start_line`/`end_line` (`tools.go:329-336`) despeja até 64 KiB **sem números de linha** (`L1:` só sai no modo range na linha 382).
   - `edit_file` (`tools.go:474-575`) e `write_file` (`447`) gravam com `os.WriteFile` não-atômico, não exigem leitura/staleness check antes de editar e não devolvem diff hunk.
   - `runShellSandboxed` (`tools.go:783`) e `shellx.Local.Run` (`shellx/exec.go:78`) truncam os **primeiros** 16 KiB (`head-truncate`), jogando fora os erros de compilação/teste que aparecem no **final** da saída.
   - `search_text` / `find_files` (`search.go`) ignoram `.gitignore`, varrem sequencialmente até 20.000 arquivos e param no primeiro corte de 24 KiB em ordem alfabética/walk sem limite por arquivo.

---

## 2. Comparação: HiveMind Atual vs Padrão Anthropic Claude Code / Google Sandbox

| Controle | HiveMind Atual | Padrão Anthropic / Google |
|---|---|---|
| **Fronteira de Segredos** | Checagem manual repetida em cada handler (esquecida em write/edit/find) | Política única centralizada (`ResolveSafe` com `workspace` jail + `pathsec.IsSensitivePath` expandido) |
| **Truncamento de Shell** | Mantém o início (banners/logs de setup) e descarta o erro no fim | **Head + Tail** (ex.: 4 KiB início + 12 KiB fim) + caminho do log completo quando truncado |
| **Segurança de `edit_file`** | Substituição cega sem hash/mtime check | Tracking de leitura/mtime na sessão (rejeita arquivo modificado externamente) + escrita atômica |
| **Sandbox Windows/Linux** | `bwrap` monta `/` cru; Windows altera ACLs do host e zera rede mesmo com `AllowNetwork` | Linux monta `--tmpfs $HOME` e expõe apenas binários/libs + workspace; Windows inclui SID `internetClient`/`privateNetworkClientServer` quando `AllowNetwork=true` e evita mutar `Program Files` |

---

## 3. Especificação Técnica da Solução

### 3.1 Bloqueio Centralizado em `workspace` + `pathsec`
1. Expandir `pathsec.IsSensitivePath` (`sensitive.go`) para cobrir `.crt`, `.der`, `.kube/config`, `.docker/config.json`, `terraform.tfstate`, `.htpasswd`, `*secret*.{toml,ini,properties,json,yaml,yml}`.
2. Adicionar `workspace.ResolveCode(root, rel)` (ou validar `pathsec.IsSensitivePath` por padrão em `workspace.Resolve`, permitindo bypass explícito apenas onde autorizado): garantir que **todas** as tools (`read_file`, `write_file`, `edit_file`, `list_dir`, `find_files`, `search_text`, e handlers do `resident`) bloqueiem paths sensíveis tanto no caminho relativo quanto após `EvalSymlinks`.

### 3.2 Correção de `sandbox_windows.go` e `sandbox_linux.go`
1. **Windows (`sandbox_windows.go`)**:
   - Quando `opts.AllowNetwork` for `true`, preencher `SID_AND_ATTRIBUTES` com os Well-Known Capabilities (`WinCapabilityInternetClientSid`, `WinCapabilityPrivateNetworkClientServerSid`) em `SECURITY_CAPABILITIES`.
   - Em `grantAppContainerACL`: não falhar se `filepath.Dir(resolvedExe)` já tiver leitura para `ALL APPLICATION PACKAGES` (verificar ACL efetiva antes de tentar `SetNamedSecurityInfoW` em diretórios protegidos como `Program Files`), e registrar/limpar ACEs temporárias criadas em `WriteRoots` temporários quando aplicável.
2. **Linux (`sandbox_linux.go`)**:
   - Adicionar `--tmpfs $HOME` antes do bind do workspace (caso o workspace esteja dentro de `$HOME`, re-montar o workspace por cima), e mascarar `~/.ssh`, `~/.aws`, `~/.gnupg`, `~/.kube`, `~/.docker` sempre que `$HOME` existir.

### 3.3 Hardening de `read_file`, `edit_file`, `search` e `run_shell`
1. **`run_shell` (`shellx/exec.go` + `tools.go`)**: implementar `strx.TruncateHeadTailBytes(s, headMax, tailMax)` preservando primeiros `4 KiB` + marcador `"... [N bytes omitidos] ..."` + últimos `12 KiB`, e salvar log integral em arquivo temporário do workspace/sessão quando truncado.
2. **`read_file` (`tools.go`)**: incluir prefixo `L<n>:` consistentemente no retorno padrão (ou faixa padrão 1..400) e metadado de linhas totais para facilitar `edit_file` preciso.
3. **`edit_file` / `write_file`**: usar `atomicx.WriteFile` + verificar se o arquivo foi modificado externamente desde a última leitura do agente na sessão + retornar preview compacto de diff (`±3` linhas).
4. **`search.go`**: respeitar `.gitignore` básico da raiz e limitar matches por arquivo (ex.: máx. 20 por arquivo) para impedir que um bundle/SQL monopolize os 24 KiB de resultado.

---

### 3.4 Carve-out Escopado para `.hivemind/spillover`
O spillover (Spec 02 §3.5) arquiva em `.hivemind/spillover/` payloads que as próprias tools do agente persistiram. Sem um carve-out, o ponteiro de recuperação seria ilegível para o `read_file` — o mesmo defeito do antigo spill em `os.CreateTemp`.

1. Nova opção `workspace.Options.AllowSpilloverRead` e o helper `pathsec.IsSpilloverPath` liberam **apenas** o subtree `.hivemind/spillover/<arquivo>`: exigem `.hivemind` como primeiro segmento + segmento literal `spillover` + pelo menos um segmento de arquivo.
2. O carve-out não vaza: `filepath.Clean` normaliza `..` **antes** do loop de segmentos (`.hivemind/spillover/../../team.yaml` continua bloqueado); symlinks são resolvidos antes da checagem (um link dentro do spillover apontando para `team.yaml` continua bloqueado); lookalikes aninhados (`docs/.hivemind/spillover/x.txt`) e sufixos (`.hivemind/spilloverx/`) não herdam a permissão.
3. `pathsec.IsSensitivePath` permanece intacto para todos os outros chamadores; o carve-out é aplicado explicitamente em `readFileRange` (`internal/tools/fs.go`), compartilhado pelo residente via `ReadFileReadOnly`.

## 4. Critérios de Aceite
- Testes negativos garantindo que `write_file`, `edit_file`, `list_dir` e `find_files` do Squad recusam `.env`, `.kube/config`, `terraform.tfstate` e `id_rsa`.
- Teste no Windows AppContainer validando `AllowNetwork=true` vs `AllowNetwork=false` e execução sob diretório read-only sem privilégio de admin.
- Teste unitário validando que falhas de compilação no final de um stdout de 64 KiB aparecem integralmente no retorno de `run_shell`.
