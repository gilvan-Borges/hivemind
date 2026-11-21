# Worktrees, Checkpoints & Ergonomia Git

## Arquivo `.worktreeinclude`
- Liste arquivos ignorados pelo Git (um caminho literal relativo por linha, ex.: `config/dev.settings.json`).
- Bloqueios automáticos (`pathsec.IsSensitivePath`): segredos (`.env*`, `*.pem`, `*.key`, `credentials.json`), padrões glob (`*`), e path traversal (`..`).

## Diretórios Compartilhados (`shared_directories`)
- Configure `settings.shared_directories` em `.hivemind/team.yaml` (ex.: `node_modules`, `.next`, `vendor`).
- Cada worktree recebe link simbólico / NTFS junction reutilizando caches grandes sem duplicar disco.

## Checkpoints & Rollback
- Antes de edições destrutivas, o HiveMind grava refs em `refs/hivemind/checkpoints/<id>` via `git stash create`.
- Na IDE (Aba Git -> Worktrees) é possível listar órfãs/ativas, arquivar, podar (`prune`) e restaurar um checkpoint em 1 clique.
