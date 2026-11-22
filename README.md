<p align="center">
  <img src="assets/brand/hivemind-logo-horizontal.png" alt="HiveMind" width="400" />
</p>

<h1 align="center">HiveMind</h1>

<p align="center">
  <em>Um time de agentes de software no seu PC — planeja, implementa, revisa e abre PR.</em><br />
  Local-first: motor no desktop, sandbox no SO, sem gateway que roda o seu código na nuvem.
</p>

<p align="center">
  <a href="https://github.com/gilvan-Borges/hivemind/releases/latest"><strong>Download</strong></a>
  · Windows
  · CLI Linux / macOS
  · <a href="https://github.com/gilvan-Borges/HiveMind-App">Android</a>
</p>

<p align="center">
  <a href="https://github.com/gilvan-Borges/hivemind/releases/latest"><img src="https://img.shields.io/github/v/release/gilvan-Borges/hivemind?label=latest&logo=github&color=C6A15B" alt="release" /></a>
  <a href="https://github.com/gilvan-Borges/hivemind/actions/workflows/ci.yml"><img src="https://github.com/gilvan-Borges/hivemind/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://github.com/gilvan-Borges/hivemind/actions/workflows/release.yml"><img src="https://github.com/gilvan-Borges/hivemind/actions/workflows/release.yml/badge.svg" alt="Release" /></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go" />
  <img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License" />
</p>

---

O HiveMind não é um chat colado no editor. É um **escritório multiagente**: Chefe (arquiteto), desenvolvedores em worktrees paralelas, QA com loop real, Git/Jira nativos e ferramentas MCP. Você descreve o objetivo; o time executa no repositório aberto na IDE ou na CLI.

Ele só oferece uma ação quando o papel pode fazê-la. Dev não mergeia PR. QA não inventa “testes passaram”. Commit e PR ficam com Chefe/Arquiteto — ou com você na UI.

```
 você ──▶ Chefe ─┬─ Auto ──────────────────────────────┐
                 ├─ Planejar (gate humano) ─────────────┤
                 └─ SDD (specs → Jira → ondas) ─────────┼─▶ Devs (worktrees) ─▶ QA ─▶ Fecho / PR
                                                        │
 IDE · CLI · MCP · Jev · sandbox SO ◀───────────────────┘
        │
        ├─ LAN :47890 ──▶ HiveMind-App (Android)
        └─ WSS saída ──▶ hivemind-relay ──▶ App (qualquer rede)
```

---

## IDE

Desktop Windows (Go + Wails). Abre na pasta do projeto, chat multimodal com prints, time ao vivo, diffs, commit/PR, Jira, terminal, MCP e página de **Uso & Limites das IAs** (uma conta do Pool = um cartão de cota).

- **Modos:** Auto (triagem), Direto, Planejar (feature / SDD com gates), Squad.
- **Quadro multiagente:** modelos por papel, lane paralela, handoffs e telemetria de tokens.
- **Git & Issues:** branch, diff, commit, push, PR, comentários Jira/GitHub — na barra e na paleta.
- **Pool de contas:** várias Antigravity (`agy`), OpenCode, Cursor CLI, OpenRouter… por organização Git.
- **Revisão automática de PR:** lane dedicada, opt-in (sem merge automático no modo auto).

Detalhes de build: [ide/README.md](ide/README.md).

---

## CLI

O mesmo orquestrador no terminal (Windows, Linux, macOS).

```bash
hivemind doctor
hivemind team create --name padrao
hivemind                          # TUI
hivemind run "adicionar healthcheck"
hivemind serve                    # Remote Bridge headless + QR
```

Comandos públicos: `run`, `team`, `agent`, `session`, `jira`, `git`, `mcp`, `doctor`, `serve`, `version`.

---

## Squad e orquestração

| Peça | Função |
| --- | --- |
| **Chefe / Arquiteto** | Triagem, plano, fecho, commit/PR |
| **Devs** | Worktrees isoladas, `edit_file`, sem commit |
| **QA** | Testes + `qa_approve` / `qa_request_changes` |
| **Jev** | Roteamento de modelo, circuit breaker, cotas (opt-in) |
| **Sandbox** | AppContainer/Job Object · bubblewrap · Seatbelt |
| **MCP** | Context7 e Code Review Graph ligados; outros opt-in — [docs/MCP.md](docs/MCP.md) |

Provedores: `agy`, OpenCode, Gemini/Vertex, Anthropic, OpenAI, OpenRouter, Cursor (`agent`). Sem `GOOGLE_API_KEY` e com `agy` no PATH, time novo prefere **agy**.

---

## Remote e mobile

**Android.** O [HiveMind-App](https://github.com/gilvan-Borges/HiveMind-App) controla a IDE. Na IDE, ligue o **Remote Bridge** (desligado por padrão) e escaneie o QR `hivemind://pair`. Token bearer de 256 bits; só dispositivos pareados. Na mesma Wi‑Fi não precisa de servidor.

**Fora da LAN.** IDE e celular abrem só **conexão de saída** para o `hivemind-relay`. O relay encaminha ciphertext; o motor continua no PC. Sem porta aberta no roteador de casa.

```
 App ──wss──▶ Relay (Caddy :443 → 127.0.0.1:8787) ◀──wss── IDE
```

Guia Oracle Always Free: [docs/GUIA_ORACLE_RELAY.md](docs/GUIA_ORACLE_RELAY.md) · Contrato: [docs/SPEC_RELAY.md](docs/SPEC_RELAY.md).

### Relay em 6 passos (resumo)

1. VM Ubuntu + Security List/`iptables` com TCP **80** e **443**
2. DNS `A` → IP público
3. Baixe `hivemind-relay-linux-amd64` (ou `arm64`) na [Latest Release](https://github.com/gilvan-Borges/hivemind/releases/latest) — não compile na Micro de 1 GB
4. Instale binário + `/etc/hivemind-relay.env` + unit [`deploy/relay/hivemind-relay.service`](deploy/relay/hivemind-relay.service)
5. Caddy com [`deploy/relay/Caddyfile`](deploy/relay/Caddyfile) → `curl https://relay.seudominio/api/v1/health`
6. IDE → Acesso remoto → login → ligar túnel; app na mesma conta escolhe a IDE `online`

---

## Install

A [latest release](https://github.com/gilvan-Borges/hivemind/releases/latest) traz:

| Asset | Uso |
| --- | --- |
| `HiveMindSetup-x64.exe` | Instalador Windows (IDE + CLI no PATH) |
| `hivemind-windows-amd64.exe` | CLI Windows avulsa |
| `hivemind-linux-*` / `hivemind-darwin-*` | CLI Unix |
| `hivemind-relay-linux-*` | Relay na VPS |
| `checksums.txt` / `manifest.json` | SHA-256 |

Todo push verde na `main` gera uma Release automática (tag `v{VERSION}-{sha}`, marcada Latest).

**Compilar local (Windows):**

```powershell
git clone https://github.com/gilvan-Borges/hivemind.git
cd hivemind
powershell -File .\scripts\build-installer.ps1
```

Go 1.26+, Node 20+, [Wails v2.17.1](https://wails.io/), Inno Setup 6.

**CLI só:**

```bash
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o hivemind ./cmd/hivemind
./hivemind version
```

---

## Primeiro uso

```powershell
cd C:\projetos\meu-app
hivemind doctor
hivemind team create --name padrao
hivemind
```

Ou abra a pasta na IDE e diga o objetivo. Exemplo de time: [examples/team.yaml](examples/team.yaml). Config do projeto: `.hivemind/team.yaml` (gitignore).

Chaves e Pool de Contas ficam na config do usuário (`%APPDATA%\HiveMind`), fora do Git. Variável de ambiente, se existir, prevalece.

---

## Privacidade e dados

Tudo que importa fica na máquina: repositório, modelos, cofre de segredos, telemetria local de tokens. O bridge e o cliente do relay **nascem desligados**. O relay não executa agentes e não lê o plaintext do plano de dados (NaCl box). Shell dos agentes limpa env de tokens/senhas. No Windows, segredos do residente usam Credential Manager + DPAPI.

Ver [SECURITY.md](SECURITY.md).

---

## Desenvolvimento

Módulo Go (`github.com/gilvan-Borges/HiveMind`), IDE em `ide/` (Wails + TypeScript/Vite), ADK Go no orquestrador. CI em Ubuntu e Windows; Release publica os binários.

```powershell
go test ./...
cd ide\frontend && npm ci && npm test && npm run build
cd .. && go test ./internal/idecore/...
```

| Doc | Assunto |
| --- | --- |
| [docs/MCP.md](docs/MCP.md) | MCP e skills |
| [docs/CURSOR_AGENTE.md](docs/CURSOR_AGENTE.md) | Provedor Cursor |
| [docs/SPEC_JEV_ORQUESTRADOR.md](docs/SPEC_JEV_ORQUESTRADOR.md) | Jev |
| [docs/SPEC_HIVE_AGENT.md](docs/SPEC_HIVE_AGENT.md) | Residente |
| [docs/SPEC_RELAY.md](docs/SPEC_RELAY.md) | Relay |
| [docs/GUIA_ORACLE_RELAY.md](docs/GUIA_ORACLE_RELAY.md) | Oracle Always Free |
| [docs/ROADMAP.md](docs/ROADMAP.md) | Roadmap |
| [ide/README.md](ide/README.md) | IDE |

---

## Licença

[Apache License 2.0](LICENSE).

<p align="center">
  <a href="https://github.com/gilvan-Borges/hivemind/releases/latest">→ Baixar a Latest Release</a>
</p>
