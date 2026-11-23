# Spec de Design — 015 painel de loops

> Status: Aprovado
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [ ] HiveMind-App
> Regras aplicáveis: D1–D8 (`.specify/memory/constitution.md`)

## 1. Contexto e Problema

O Agent Hive passa a rodar loops. A pessoa precisa ver, na IDE, o estado, a iteração, o último veredito e um botão para parar. O interruptor de Full desacompanhado fica só nas Configurações.

## 2. Escopo e Não-Escopo

- **Inclui:** um bloco no chat do Agent Hive e um toggle na seção Agent Hive das Configurações.
- **Não inclui:** tela nova, cor nova, nem a aba do app mobile.

## 3. Camadas e Tokens Utilizados

| Elemento | Camada | Token |
|---|---|---|
| Texto do painel | conteúdo | `--text-secondary` via `.dim` |
| Botão Parar | ação | classe `.mini` já existente |
| Aviso do toggle | sinal | `.tc-error` só no Full geral, com texto |

## 4. Estados Obrigatórios (D5)

- **Vazio:** "Nenhum loop neste projeto."
- **Dados:** nome, status, iteração e veredito, com Parar enquanto corre ou espera.
- **Erro:** o toast já usado pela IDE ao falhar a chamada de parar.

## 5. Pares de Contraste (D3 — >= 4.5:1)

O bloco reusa `.dim` e `.mini` sobre o fundo do painel do Agent Hive. Nenhum hex novo.

## 6. Critérios de Aceite

- [x] Zero cores literais novas.
- [x] O toggle `ide-hive-full-unattended` só aparece nas Configurações.
- [x] O botão Parar leva o id do loop.
