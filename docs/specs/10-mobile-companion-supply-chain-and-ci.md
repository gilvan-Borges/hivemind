# Spec 10 — App Mobile (`HiveMind-App`), Cadeia de Suprimentos, Auto-Update e CI/CD

> **Status:** Implementado (Onda 5 — `HiveMind-App` offline Room/TOFU/R8/Theme, `VERSION` + `-ldflags`, `internal/updater`, `scripts/build-installer.ps1`, `.github/workflows/ci.yml` e hardening `deploy/relay/`)
> **Alvos:** `HiveMind-App/`, `scripts/`, `.github/workflows/ci.yml`, `deploy/relay/`, `ide/wails.json`, `docs/SPEC_ATUALIZACAO.md`

---

## 1. Diagnóstico do Estado Atual

### A. `HiveMind-App` (Kotlin / Jetpack Compose)
1. **Lacunas de Paridade de Protocolo e Modos:**
   - `ChatScreen.kt:110-115` (`RunModes`) só oferece `auto`, `direct`, `plan`, `squad` — faltam `plan:sdd`, `plan:sdd-jira` e `resident` já suportados no `ide/internal/remote/server.go:289` e `deps.go:51-75`.
   - `HubState.kt:207-224` (`onStreamEvent`) descarta eventos `"provider_health"` e `"relay_gap"`, e `Models.kt:74-88` (`OrchestratorEvent`) não desserializa `provider`, `model`, `fromProvider`, `fromModel`, `tier`, `reasons`, `estimatedMicroUsd`, `decisionMicros`, `health` e `gate`.
   - `RelayController.kt:402` e `ApiClient.kt:191` bloqueiam todas as chamadas exceto `/approvals/resolve` quando `ideOnlineFlow.value == false` (atualizado só por polling de 20s em `RelayController.kt:285-291`), impedindo enfileiramento no `rpc_queue` do relay.
2. **Zero Cache Offline:**
   - `HubState.kt:39-41` guarda `projects`, `lanes` e `autoreview` apenas em memória (`MutableStateFlow`); `app/build.gradle.kts` não possui Room/SQLite — reiniciar o app ou perder sinal apaga transcrições, diffs e status.
3. **Criptografia, Transporte e Paridade de Design Tokens:**
   - `AndroidManifest.xml:21` habilita `android:usesCleartextTraffic="true"` globalmente em vez de restringir cleartext apenas a faixas LAN/Tailscale via `network_security_config.xml`.
   - `RelayController.kt:554-559` derruba o WebSocket (`opened.close(1000, "")`) a cada ~14.5m ao renovar token; `KeystoreAccountVault.kt:75,86` e `KeystoreLanVault.kt:44` retornam `null` silencioso em erro de decriptação; `RelayController.kt:270-273` rotaciona `appPublicKey` TOFU sem confirmação do usuário.
   - `Theme.kt:39` mantém alias legado `SteelBlue = BrandGold`; `Theme.kt:80` define `surface = ObsidianCard (#111111)` divergindo de `--surface-panel (#0C0C0C)` (`ide/frontend/src/styles.css:4`); `Theme.kt:110-121` não carrega `Inter` nem `JetBrains Mono`; `app/build.gradle.kts:24-34` configura build `release` com `isMinifyEnabled = false` e assinatura de `debug`.

### B. Supply Chain, Versionamento, Auto-Update e CI/CD
1. **Drift entre Binário do Workspace vs Instalado (`AppData\Local\Programs\HiveMind`):**
   - Versão `0.1.0` hardcoded em `internal/cli/root.go:22`, `ide/wails.json:15`, `internal/relay/run.go:17` e `ide/internal/remote/config.go:24`; `scripts/build-installer.ps1:103` injeta `-X` apenas em `hivemind.exe` (ignorando `hivemind-ide.exe` e `hivemind-relay`), gerando binários locais vs instalados fora de sincronia e sem mecanismo de auto-update in-app apesar de `AppUpdatesURL` em `scripts/installer/hivemind.iss:31`.
   - Além disso, `ide/build/hivemind-ide.exe` e `ide/build/bin/hivemind-ide.exe` são cópias duplicadas que divergem facilmente durante builds manuais.
2. **Artefatos Sem Assinatura / Checksum e Gaps no CI:**
   - `scripts/build-installer.ps1:118-122` cai silenciosamente para `npm install` se `package-lock.json` faltar e não gera `.sha256` nem assinatura Authenticode (`signtool.exe`).
   - `.github/workflows/ci.yml` (`test-ide:80-89`) **não testa `./internal/remote/...`**, não executa `-race` no Windows nem `./gradlew testDebugUnitTest lintDebug` em `HiveMind-App/`, e não executa `govulncheck`.
   - `deploy/relay/Dockerfile:15` usa tag mutável `alpine:3.20` sem SHA digest nem `HEALTHCHECK`; `hivemind-relay.service:8-9` usa `User=hivemind` enquanto `Dockerfile:17` cria `relay` e faltam limites cgroup/seccomp.

---

## 2. Comparação: HiveMind Atual vs Engenharia Google Mobile & Release

| Área | HiveMind Atual | Padrão Google Release & Android Engineering |
|---|---|---|
| **Offline-First Mobile** | Estado volátil em RAM + bloqueio por polling de 20s | Banco Room local com sincronização incremental + fila outbox para comandos/aprovações |
| **Rede & Release Android** | `usesCleartextTraffic="true"` global + R8 off + debug key | `network_security_config.xml` restrito a RFC1918/CGNAT + R8 ativo + keystore dedicado |
| **Auto-Update Desktop** | Cópia manual ou reinstalador sem manifesto nem checagem | Manifesto assinado (`releases/latest` + `checksums.txt.sig`) + `hivemind update` / notificação na IDE |
| **Qualidade Contínua (CI)** | Ignora `remote/`, Android Gradle, `-race` Windows e `govulncheck` | Matrix completa Go (`-race` + `remote/`), Frontend (`check:design` + test + build), Android (`testDebugUnitTest`) e `govulncheck` |

---

## 3. Especificação Técnica da Solução

### 3.1 Evolução do `HiveMind-App`
1. **Paridade de Protocolo:** adicionar `plan:sdd`, `plan:sdd-jira` e `resident` ao seletor em `ChatScreen.kt`; mapear `provider_health`, `relay_gap` e metadados Jev (`provider`, `model`, `tier`, `estimatedMicroUsd`, `gate`) em `Models.kt` e `HubState.kt`, exibindo badge de failover/saúde na UI.
2. **Cache Room + Presença Real:** persistir último snapshot de projetos, lanes e mensagens em banco Room local; atualizar `ideOnlineFlow` instantaneamente via evento push `peer_presence` do Spec 09 (mantendo polling só como fallback) e diferenciar RPCs somente-leitura vs comandos enfileiráveis no relay.
3. **Hardening & Tokens:** substituir `usesCleartextTraffic="true"` por `res/xml/network_security_config.xml` (cleartext só para `localhost`, LAN RFC1918 e `100.64.0.0/10`); exigir confirmação explícita antes de aceitar mudança de `appPublicKey`/`idePublicKey`; ativar R8 (`isMinifyEnabled = true`, `isShrinkResources = true`) + config de assinatura via env/gradle properties; corrigir `surface = SurfacePanel`, remover alias `SteelBlue` e embutir fontes `Inter` e `JetBrainsMono`.

### 3.2 Pipeline de Build, Auto-Update e CI/CD
1. **Fonte Única de Versão + Manifesto:** criar arquivo raiz `VERSION` (+ commit SHA + data de build injetados via `-ldflags` em `hivemind`, `hivemind-ide`, `hivemind-relay` e lidos pelo `build-installer.ps1` e Gradle). Unificar destino único de build da IDE (`ide/build/bin/hivemind-ide.exe`) evitando cópia manual divergente em `ide/build/`.
2. **Auto-Updater Verificável:** implementar verificador opcional de atualização (conforme `docs/SPEC_ATUALIZACAO.md`) consultando GitHub Releases, validando `SHA-256` em `checksums.txt` antes de executar upgrade silencioso do instalador; gerar `.sha256` em `build-installer.ps1`, remover fallback `npm install` (exigir `npm ci`) e suportar hook de `signtool.exe`.
3. **Endurecimento CI & Deploy:**
   - Em `.github/workflows/ci.yml`: incluir `./internal/remote/...` e `go test -race` em `test-ide`, job Android (`./gradlew testDebugUnitTest lintDebug`) e step `govulncheck ./...`.
   - Em `deploy/relay/`: fixar digest `@sha256:...` da imagem Alpine + `HEALTHCHECK`, alinhar usuário (`relay`) entre `Dockerfile` e `hivemind-relay.service`, adicionar `MemoryMax=512M`, `CPUQuota=100%`, `SystemCallFilter=@system-service` e headers HSTS/CSP no `Caddyfile`.

---

## 4. Critérios de Aceite
- `HiveMind-App` preserva histórico das conversas após encerrar o processo offline e recebe eventos Jev/Sdd/Resident completos.
- APK release compila com `isMinifyEnabled = true` e proíbe HTTP em hosts públicos.
- `ci.yml` valida `internal/remote/...`, `HiveMind-App` unit tests/lint e vulnerabilidades (`govulncheck`).
- `hivemind version` e diálogo "Sobre" na IDE reportam exatamente a mesma versão/commit injetada no build.
