# HiveMind IDE (Go + Wails v2)

IDE desktop para Windows construída em **Go + Wails v2** (HTML/TypeScript/CSS com tema escuro `#12121a`), reutilizando diretamente o núcleo do CLI Go (`internal/team`, `internal/orchestrator`, `internal/modelx`, `internal/gitx`, `internal/githubx`, `internal/jirax`, `internal/sessionx`).

## Pré-requisitos

- **Go 1.26+**
- **Node.js 20+** e `npm`
- **Wails CLI v2**:
  ```powershell
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  ```
- **WebView2 Runtime** (já incluído no Windows 10/11)
- **Antigravity CLI (`agy`)** autenticado com conta Google Pro (`agy models`)

## Build com um comando

Na pasta `ide/`:

```powershell
wails build -o ..\build\hivemind-ide.exe
```

Ou via `go build` (após `cd ide\frontend && npm install && npm run build`):

```powershell
cd ide
go build -tags desktop,production -ldflags "-s -w -H windowsgui" -o build\hivemind-ide.exe .
```

## Uso

Abra um terminal na pasta de qualquer projeto e execute:

```cmd
C:\caminho\para\HiveMind\bin\hivemind-ide.cmd
```

Ou adicione a pasta `bin\` ao `PATH` do Windows e rode:

```cmd
hivemind-ide
```

### Funcionalidades principais

1. **Abertura na pasta atual**, menu **Abrir Pasta...** e **Recentes** persistidos em `%LOCALAPPDATA%\HiveMind\ide-recent.txt`.
2. **Explorador de arquivos completo** (criar arquivo, criar pasta, renomear e excluir), **Busca Rápida (`Ctrl+P`)**, **Busca Global no Código (`Ctrl+Shift+F`)** com clique direto na linha e **editor ao vivo** com syntax highlighting, `Ctrl+S` e **`Ctrl+L`** (enviar seleção ao Chefe).
3. **Chat Multimodal com o Chefe** com colagem de fotos/prints (`Ctrl+V`, Drag & Drop ou botão `Foto`), streaming ao vivo, handoffs expansíveis, resumo de diffs pós-execução, `@arquivo` e botão **Cancelar**.
4. **Telemetria de Tokens & Histórico de Sessões (`Sessões & Tokens`)**: contador em tempo real do turno (`↑entrada / ↓saída`), total acumulado da sessão, consumo individual por agente, orçamento/alerta de tokens (`settings.max_tokens`), rodapé de tokens por mensagem e retomada/exclusão de sessões salvas em `.hivemind/sessions/`.
5. **Painel inteligente do time** (`Chefe (Triagem) → Dev (fatias sem conflito) → QA (loop real) → Chefe (Fecho)`), modelos de `agy models` e **+ Equipe Paralela**.
6. **Modos de execução com planejamento**:
   - **Auto** — o Chefe tria sozinho e, em tarefa grande/arriscada, pode pausar para sua aprovação (`pause_for_approval`) antes de disparar o Squad;
   - **Planejar ▾ · Feature / Task Grande** (`plan`) — plano arquitetural + fatiamento paralelo, pausa em *Aprovar & Executar Squad*;
   - **Planejar ▾ · Projeto do Zero (SDD)** (`plan:sdd`) — fluxo Spec-Driven Development travado por gates: **Entrevista/Discovery → Docs & Arquitetura** (`specs/01..05`, ADRs em `docs/adr/`, gravação só via `write_doc`) → *Gate 1: Aprovar Docs & Criar Tasks no Jira* → **Backlog Jira** (Epics/Tasks/Subtasks via `jira_create_issue`, chaves gravadas de volta na spec) → *Gate 2: Aprovar Backlog & Disparar Squad* → **Squad por ondas**. As skills nativas `sdd-discovery`, `sdd-architecture-docs`, `sdd-jira-planner`, `sdd-dev-executor`, `sdd-qa-verifier` e `plan-feature-architect` são semeadas em `%LOCALAPPDATA%\HiveMind\skills` e editáveis no painel de Skills (edições suas nunca são sobrescritas).
7. **Git, Branches & Issues**: seletor/criador de branches na barra de status, diffs coloridos, **Commitar**, **Commit + Push**, **Abrir PR**, **Comentar Jira**, **Descartar** e listagem de **Issues abertas (Jira / GitHub)** com botão **Resolver c/ Equipe**.
8. **Conectores nativos**: **Conector GitHub Nativo** (PAT/API REST) e **Conector Jira Nativo** (Base URL, e-mail, API token e projeto padrão, salvos em `%AppData%\HiveMind\jira-connector.json`, fora do repo) — menu *Controle de Código → Mais ações* ou paleta de comandos.
9. **Terminal integrado (`Ctrl+J`)** com atalhos automáticos de teste da stack detectada (Go, Java/Maven/Gradle, Node/Angular/React, Python, Rust, .NET).
10. **Tema escuro frameless** (`#12121a` / `#e2e0ec`) estilo Cursor/VS Code, sem janelas extras de console no Windows (`CREATE_NO_WINDOW`).

## Testes

```powershell
cd ide
go test ./...
```
