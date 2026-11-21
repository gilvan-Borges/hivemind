# Plan 010 — Git Blame, File History & Guided Conflict Resolution

## Backend (`ide/internal/idecore/git_history.go` + `ide/app_git.go`)
- `BlameLine` e `GitBlameFile(ctx, workspaceDir, relPath)` com `git -c core.quotePath=false blame --line-porcelain -- <rel>`
- `FileCommitEntry` e `GitFileHistory(ctx, workspaceDir, relPath, limit)` com `git -c core.quotePath=false log -n <limit> --follow`
- `ConflictBlock`, `ConflictedFile`, `ListConflictedFiles`, `ResolveConflictBlock(ours|theirs|both)`, `MarkConflictResolved`
- Bindings `*App` em `ide/app_git.go` + DTOs em `ide/frontend/src/types.ts`

## Frontend (`ide/frontend/src/git-history.ts`, `editor.ts`, `git.ts`, `styles.css`)
- Novo módulo `git-history.ts` + testes Vitest `git-history.test.ts` com renderizadores puros de Blame, Histórico de Arquivo e Cartões de Conflito (4 estados: loading/empty/error/data)
- Botões `Blame` e `Histórico` na barra do editor (`editor.ts`) com painel contextual acoplado à aba de arquivo ativa
- Card "Conflitos de Merge" no topo da aba Alterações (`git.ts`) quando houver arquivos em estado unmerged
- Estilos dentro de `@layer components` usando apenas variáveis CSS (`var(--...)`)
