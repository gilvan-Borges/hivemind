# HiveMind CLI (`hivemind`)

## Comandos principais
- `hivemind run "objetivo"`: executa o squad configurado no repositório atual.
- `hivemind run --mode auto|direct|squad|plan|race`: escolhe o modo de execução.
- `hivemind run --race 3 "objetivo"`: abre 3 candidatos paralelos em worktrees e aplica o vencedor.
- `hivemind run --jira PROJ-123`: carrega a issue como objetivo e mantém contexto por sessão.
- `hivemind skills list` / `hivemind skills get <topic> [--json]`: lê documentação compacta offline alinhada à versão instalada.
- `hivemind doctor` / `hivemind serve`: valida provedores locais e inicia o daemon/API remota.

## Flags úteis
- `--workspace <pasta>`: aponta para o repositório alvo (ex.: `C:\projetos-acme\app-web`).
- `--permission-mode governed|auto_edit|full_access`: define o nível de gate humano.
- `--max-tokens <n>`: sobrescreve o teto de tokens por turno nesta execução.
