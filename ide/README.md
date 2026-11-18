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
2. **Explorador de arquivos** e **abas de editor** com syntax highlighting e salvamento (`Ctrl+S`).
3. **Chat com o Chefe** com streaming ao vivo, auto-scroll inteligente, badges distintos (`Usuário`, `Chefe`, `Sistema`, `Ferramenta`), `@arquivo` e botão **Cancelar** habilitado apenas durante execução.
4. **Painel do time** (`Chefe → Dev → QA → Chefe`), modelos vindos de `agy models` e botão **+ Equipe Paralela** para rodar múltiplos times em paralelo.
5. **Diffs coloridos** com ações nativas de **Commitar**, **Commit + Push**, **Abrir PR**, **Comentar Jira** e **Descartar**.
6. **Montagem interativa do time** gravando em `.hivemind/team.yaml` sem alterar o `.gitignore` raiz do projeto.
7. **Detecção automática da stack** (Go, Java/Maven/Gradle, Node/Angular/React, Python, Rust, .NET) enviada automaticamente ao Chefe.
8. **Tema escuro completo** (`#12121a` / `#e2e0ec`), incluindo inputs, combos e scrollbars.

## Testes

```powershell
cd ide
go test ./...
```
