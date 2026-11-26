# Plan: Checklist de Tarefas Ao Vivo (017-task-checklist)

1. **Tool `update_tasks` (`internal/tools/tasks.go` + `policy.go`):**
   - Expor `update_tasks` quando `Deps.OnTasks != nil`, validando 1–12 itens com status `pending | in_progress | done | failed | skipped`.
2. **Evento `tasks_updated` (`internal/orchestrator/runner.go` + `triage_helpers.go`):**
   - Adicionar `Tasks []tools.TaskItem` em `orchestrator.Event` e emitir `Kind: "tasks_updated"` via `OnTasks`.
3. **Estado e UI (`ide/frontend/src/types.ts`, `boot.ts`, `chat.ts`, `chat-timeline.ts`, `styles.css`):**
   - Atualizar o cartão `kind: "tasks"` no turno atual sem duplicar cartões, mantendo-o visível fora do fold genérico de ferramentas.
   - Estilizar `.task-checklist` com tokens Havrus existentes e validar com `check-design-tokens`, `npm test` e `npm run build`.
