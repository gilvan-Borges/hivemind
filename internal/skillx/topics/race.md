# Race Mode (`--mode race` / `--race`)

## Funcionamento
- Dispara `N` execuções paralelas em paralelo (`2 <= N <= 4`, padrão `2`) para o mesmo objetivo em worktrees Git isoladas.
- Flags equivalentes: `hivemind run --mode race`, `hivemind run --mode race:3`, ou `hivemind run --race 3` (ou seletor `Race` na IDE).

## Critério de Seleção & Segurança
- Cada candidato registra arquivos alterados, status de conclusão e custo acumulado (`CostMicroUSD` + `TotalTokens`).
- Candidatos bem-sucedidos e com alterações válidas são ranqueados deterministicamente (menor custo em caso de empate).
- Apenas arquivos não sensíveis (`!pathsec.IsSensitivePath`) do vencedor são aplicados ao workspace principal, seguidos de limpeza automática das worktrees temporárias.
