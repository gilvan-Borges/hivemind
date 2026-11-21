# Workers Externos (Contrato & Post-hoc Guard)

## Contrato HTTP Local (`workerx`)
- `POST /dispatch`: envia objetivo, `dispatchId`, diretório da worktree e `owned_paths`.
- `POST /heartbeat`: reporta progresso periódico e uso de tokens/custo.
- `POST /ask`: bloqueia até resposta humana (HITL) quando uma operação protegida precisa de autorização.
- `POST /done`: idempotente por `dispatchId`; sinaliza conclusão e resumo do worker.

## Piloto Cursor CLI
- Configure um agente no `.hivemind/team.yaml` com `worker: cursor` para delegar fatias a subagentes externos CLI mantendo telemetria centralizada.

## Post-hoc Diff Guard
- Ao término do worker externo, o orquestrador valida `git status --porcelain -z` contra `owned_paths` e `pathsec.IsSensitivePath`, bloqueando arquivos fora da fatia ou sensíveis.
