# Plano Técnico de Design — [NNN-nome-da-mudanca]

> Referência: `spec.md` · Constituição: `.specify/memory/constitution.md`

## 1. Arquivos Impactados
- `ide/frontend/src/styles.css`
- `ide/frontend/src/...`
- `HiveMind-App/app/src/main/java/dev/hivemind/mobile/theme/Theme.kt` (se aplicável)

## 2. Mapeamento de Tokens e Compatibilidade
Descreva como os tokens semânticos serão adicionados/atualizados sem quebrar componentes existentes.

## 3. Estratégia de Verificação
1. `node ide/frontend/scripts/check-design-tokens.mjs`
2. `cd ide/frontend && npm test && npm run build`
3. Build e `--smoke-check` da IDE Windows (`hivemind-ide.exe --smoke-check`)
4. Captura ou inspeção visual em `1440×900` (desktop) e `390×844` (mobile / estreito).
