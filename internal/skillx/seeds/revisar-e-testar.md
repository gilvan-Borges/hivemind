---
name: revisar-e-testar
description: >
  Use when reviewing a change, when the user says revisa, review, testa isso,
  or before declaring a code change done. Classifies the diff as IDE, CLI,
  mobile companion, Chrome extension, or orb and runs that surface's tests.
  A review without command output is not a review.
---

# Revisar e testar

Use ao revisar, ao terminar código, ou quando pedirem revisa, review ou testa isso.
Classifica o diff em IDE, CLI, mobile, extensão ou orb e roda o teste dessa superfície. Sem saída de comando, não é revisão.

Revisão sem comando executado e saída lida não conta. "Li o diff" é NÃO TESTADO.

## 1. Classifique pelo diff

`git diff --name-only` (e os arquivos ainda não commitados). Cada linha que bater entra na rodada. Superfície que não bate, não rode.

| Sinal no diff | Superfície | Comando (cwd) |
|---|---|---|
| `ide/frontend/**` | IDE, tela | `ide/frontend`: `npm test`. Se mexeu em `styles.css` ou token: `node scripts/check-design-tokens.mjs`. Se mexeu em tipo exportado: `npx tsc --noEmit` |
| `ide/**/*.go` | IDE, processo | `ide`: `go test -count=1 ./internal/<pacote>/...` do pacote tocado. Orb (`ide/orb*.go`, `ide/internal/orb`): `go test -count=1 ./internal/orb/...` |
| `internal/**/*.go` fora de `internal/remote` | CLI | raiz: `go test -count=1 ./internal/<pacote>/...` |
| `internal/remote/**`, `relaywire/**`, `ide/frontend/src/companion/**` | Mobile, protocolo | raiz: `go test -count=1 ./internal/remote/... ./relaywire/...` |
| `HiveMind-App/**` ou checkout irmão do app | Mobile, tela Android | no app: `./gradlew --no-daemon testDebugUnitTest lintDebug`. Sem o checkout: **NÃO TESTADO**. Vitest da IDE não cobre o app |
| `extension/chrome/**` | Extensão Chrome | `extension/chrome`: `npm test` |

Não rode `./...` no monorepo inteiro para um pacote. Não troque o comando da superfície por um teste de outra.

## 2. Rode e leia

- PowerShell não aceita `&&`. Um comando por chamada, com `working_directory`.
- O teste tem que afirmar o comportamento pedido (sessão nova não reusa a ativa, aba do projeto aparece). Teste que só importa o módulo não conta.
- Falhou: a revisão para nesse ponto. Corrija só se a pessoa pediu correção; senão reporte o comando e o trecho da falha.
- Não deu para rodar (sem Android, sem device, pacote fora deste clone): escreva **NÃO TESTADO** e o motivo. Nunca vire OK.

## 3. Relatório

```
Revisão — <o que mudou>
Superfícies: IDE tela, CLI residente
Não é: mobile, extensão

✅ IDE tela — npm test src/resident.test.ts — exit 0
✅ IDE processo — go test ./internal/idecore/ -run TestFreshHive — exit 0
⚠️ Mobile tela — NÃO TESTADO — HiveMind-App não está neste clone

Achado: <arquivo:linha, o que quebra, só se viu no teste ou no diff>
```

Uma linha por comando, com exit code. Sem exit code, a linha não entra.
