# Spec: Mídia Inline, Prints de Agente e Chips de Arquivo/PDF no Chat (018-chat-inline-media)

## Problema
Hoje o chat exibe apenas imagens anexadas pelo usuário no composer. Prints gerados por ferramentas do agente (Playwright/browser), imagens referenciadas em Markdown (`![alt](caminho)`), PDFs e links `file://` de arquivos aparecem como texto bruto.

## Superfícies afetadas
- Backend: `internal/orchestrator/runner.go`, `internal/orchestrator/events.go`, `ide/internal/idecore/media.go`, `ide/app_project.go`
- Frontend: `ide/frontend/src/markdown.ts`, `ide/frontend/src/boot.ts`, `ide/frontend/src/chat.ts`, `ide/frontend/src/types.ts`, `ide/frontend/src/styles.css`

## Estados exigidos (D5)
- **Carregando**: placeholder compacto `.md-img-loading` enquanto `ReadLocalMedia` resolve imagem local.
- **Dados**: imagem inline clicável com modal de zoom (`data-preview-img`), chip de arquivo abrindo no editor (com suporte a `#L<linha>`), chip de PDF abrindo no visualizador do sistema.
- **Erro**: fallback discreto informando indisponibilidade quando a imagem não puder ser lida.
- **Segurança**: leitura restrita ao workspace, temp do sistema ou `%LOCALAPPDATA%\HiveMind`, bloqueando arquivos sensíveis (`pathsec.IsSensitivePath`) e aceitando apenas formatos de imagem raster seguros.
