# Tasks: Checklist de Tarefas Ao Vivo (017-task-checklist)

- [ ] `D001` Criar `internal/tools/tasks.go` e registrar `update_tasks` em `internal/tools/policy.go`.
- [ ] `D002` Emitir `Event{Kind: "tasks_updated"}` em `internal/orchestrator/runner.go` e `triage_helpers.go`.
- [ ] `D003` Tratar `tasks_updated` em `ide/frontend/src/boot.ts` e `types.ts`.
- [ ] `D004` Renderizar `.task-checklist` em `ide/frontend/src/chat.ts` e `styles.css` usando apenas tokens Havrus.
- [ ] `D005` Validar com `go test`, `node scripts/check-design-tokens.mjs`, `npm test` e `npm run build`.
