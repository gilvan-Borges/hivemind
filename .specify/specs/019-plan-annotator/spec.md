# Spec: Anotação Estruturada de Planos (019-plan-annotator)

## Problema
Ao revisar um plano do Chefe antes de aprovar (`role-plan`), o usuário precisava digitar do zero no composer quais trechos mudar. Falta uma UX para anotar linhas específicas do plano e devolver o feedback estruturado em 1 clique (estilo `backnotprop/plannotator`).

## Superfície afetada
- `ide/frontend/` (`plan-annotator.ts`, `plan-annotator.test.ts`, `chat.ts`, `styles.css`)

## Estados exigidos (D5)
- **Leitura**: cartão de plano padrão com botão `Anotar` e botão de aprovação.
- **Anotando**: lista interativa linha a linha (`L1`, `L2`...) com botão `+` para comentar cada trecho.
- **Com anotações**: chips abaixo de cada linha anotada com opção de remover (`×`) e rodapé com botão `Enviar feedback ao Chefe`.
- **Enviado / Aprovado**: ao enviar feedback, o modo anotação fecha e o Chefe recebe `[L<n>] "<trecho>" → <comentário>`.

## Tokens Havrus (D1–D4, D6)
- Exclusivamente tokens de `styles.css` (`--surface-card`, `--surface-elevated`, `--surface-input`, `--border-divider`, `--accent-border`, `--accent-subtle`, `--accent-strong`, `--text-primary`, `--text-secondary`, `--text-tertiary`) e ícones de `icons.ts`.
