# Plano 001 — Aplicação SDD & Paleta Havrus

> **Spec:** `spec.md` · **Constituição:** `.specify/memory/constitution.md`

## 1. Arquitetura Escolhida (Comparativo `Vibetex/ipg` vs `Havruss/havrus`)
- **Do `Vibetex/ipg`**: adotamos a camada de governança `.specify/` (`memory/constitution.md`, `templates/*-design-template.md`, `specs/NNN-*/{spec,plan,tasks}.md` e matriz em `.specify/README.md`) para garantir rastreabilidade de requisitos antes do código.
- **Do `Havruss/havrus`**: adotamos a fundação de design tokens por papel (`--surface-*`, `--text-*`, `--border-*`, `--action-primary*`, `--accent*`, `--color-*`), a separação de camadas (fundo `#050505` / conteúdo sólido `#111111` / elevado `#1F1F1F`), a skill prática de execução (`.agents/skills/hivemind-design-sdd/SKILL.md`) e o script conferidor automático de tokens + contraste WCAG (`ide/frontend/scripts/check-design-tokens.mjs`).

## 2. Arquivos Criados/Atualizados neste Worktree
- `.specify/memory/constitution.md`
- `.specify/README.md`
- `.specify/templates/{spec,plan,tasks}-design-template.md`
- `.specify/specs/001-havrus-design-system/{spec,plan,tasks}.md`
- `docs/design/01-paleta-e-tokens-havrus.md`
- `.agents/skills/hivemind-design-sdd/SKILL.md`
- `ide/frontend/scripts/check-design-tokens.mjs`
- `ide/frontend/src/styles.css` (aplicação da paleta Havrus + remoção de `button.primary` azul hardcoded + `:focus-visible` dourado + `prefers-reduced-motion`)

## 3. Verificação
1. `node ide/frontend/scripts/check-design-tokens.mjs`
2. `cd ide/frontend && npm test && npm run build`
3. `cd ide && go vet ./... && go test -count=1 ./...`
4. `go build -tags desktop,production -ldflags "-s -w -H windowsgui" -o build/hivemind-ide.exe .` e `.\build\hivemind-ide.exe --smoke-check`
