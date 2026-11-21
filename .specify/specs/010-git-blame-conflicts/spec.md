# Spec 010 — Git Blame, Histórico de Arquivo & Resolução Guiada de Conflitos (W4-08)

- **Status:** Implementado (W4-08)
- **Relacionado:** `docs/specs/07-ide-architecture-services-and-reactive-ui.md`, `W4-08`, `.specify/memory/constitution.md` (`D1–D9`)

## 1. Objetivo
Entregar na IDE HiveMind:
1. **Git Blame por linha** (`GitBlameFile`) e **Histórico de commits do arquivo** (`GitFileHistory` com `--follow`), acionáveis direto na barra do editor de arquivos e visíveis num painel lateral/inferior contextual sem tirar o usuário do código aberto.
2. **Resolução guiada de conflitos de merge/rebase** (`ListConflictedFiles`, `ResolveConflictBlock`, `MarkConflictResolved`) no painel Git (aba Alterações), permitindo escolher *Manter atual (Ours)*, *Aceitar recebida (Theirs)* ou *Manter ambas*, impedir `git add` enquanto houver marcadores `<<<<<<<` / `=======` / `>>>>>>>`, e marcar como resolvido quando zerar os blocos.

## 2. Regras de Design & Segurança
- **Zero cores hex literais fora de `:root` (`D1`):** usar exclusivamente `var(--...)` existentes em `ide/frontend/src/styles.css`.
- **Ícones (`D6`):** Material Symbols via `./icons` (nenhum SVG inline novo ou emoji como ícone).
- **Números tabulares:** hashes curtos, números de linha e datas em listas devem aplicar `font-variant-numeric: tabular-nums`.
- **Estados assíncronos explícitos (`D8`):** Loading, Empty (sem histórico / sem conflitos), Error (com retry) e Data.
- **Path containment:** todos os caminhos relativos passam por `workspace.Resolve`.
- **Privacidade (`AGENTS.md`):** testes e exemplos utilizam apenas repositórios e autores sintéticos (`dev@example.com`).
