# Spec: Checklist de Tarefas Ao Vivo na Timeline (017-task-checklist)

## Problema
Durante execuções com múltiplas etapas (revisões, implementações, investigações), o usuário vê apenas blocos de pensamento e chamadas soltas de ferramentas, sem uma lista clara do que falta fazer, o que já foi concluído e em qual etapa o agente está agora.

## Superfícies afetadas
- `ide/frontend/` (`chat.ts`, `chat-timeline.ts`, `boot.ts`, `types.ts`, `styles.css`)
- Backend de ferramentas/orquestração (`internal/tools/tasks.go`, `internal/tools/policy.go`, `internal/orchestrator/runner.go`, `internal/orchestrator/triage_helpers.go`)

## Estados exigidos (D5)
- **Ativo (`in_progress`)**: cabeçalho com ícone `checklist`, contador `done/total` em `tabular-nums`, título da tarefa ativa e horário; itens com ícone de estado semântico.
- **Concluído**: todas as tarefas marcadas como `done` (`✓` em `--status-ok` / `--color-success`), bloco recolhível pelo usuário.
- **Falha / Pulado**: suporte visual a `failed` (`✕` em `--status-err`) e `skipped` (`–` em `--text-tertiary`).
- **Vazio**: se nenhuma tarefa foi emitida no turno, o componente não é renderizado.

## Pares de contraste e Tokens (D1–D4, D6)
- Apenas tokens semânticos de `ide/frontend/src/styles.css`: `--surface-panel`, `--border-divider`, `--border-emphasis`, `--accent-border`, `--accent-subtle`, `--accent-strong`, `--text-primary`, `--text-secondary`, `--text-tertiary`, `--status-ok`, `--status-err`.
- Apenas ícones de `icons.ts` (zero emojis estruturais).
