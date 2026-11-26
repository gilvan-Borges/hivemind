# Plan: Anotação Estruturada de Planos (019-plan-annotator)

1. Criar `ide/frontend/src/plan-annotator.ts` com `splitPlanBlocks` e `formatPlanAnnotationsFeedback`.
2. Cobrir com testes unitários em `ide/frontend/src/plan-annotator.test.ts`.
3. Integrar botão `Anotar`, editor inline por linha e envio em 1 clique em `ide/frontend/src/chat.ts` (`m.kind === "plan"`).
4. Estilizar com tokens Havrus em `ide/frontend/src/styles.css` e validar com `check:design`, `npm test` e `npm run build`.
