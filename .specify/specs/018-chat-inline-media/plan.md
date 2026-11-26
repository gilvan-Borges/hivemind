# Plan: Mídia Inline e Chips de Arquivo/PDF (018-chat-inline-media)

1. `internal/orchestrator/runner.go` + `events.go`: adicionar `Images []string` em `Event` e extrair imagens base64 (`data:image/...`) de `tool_result`.
2. `ide/internal/idecore/media.go` + `ide/app_project.go`: expor `ReadLocalMedia(workspace, rawPath)` e `OpenLocalFile(workspace, rawPath)` com guardas de segurança.
3. `ide/frontend/src/markdown.ts`: suportar `![alt](src)` (data URI e local), links `file://` e caminhos relativos com chips `.md-file-chip` / `.is-pdf`.
4. `ide/frontend/src/boot.ts` + `chat.ts` + `styles.css`: renderizar prints de `tool_result`, hidratar `[data-local-img]` com cache em memória, abrir arquivos no editor e PDFs no sistema usando apenas tokens Havrus.
