# Spec de Design — 002-icons-dedupe

> Status: Em Revisão
> Alvos: [x] HiveMind IDE (`ide/frontend`) · [ ] HiveMind-App (`HiveMind-App`)
> Regras aplicáveis: D1–D8 (`.specify/memory/constitution.md`)

## 1. Contexto e Problema

A activity bar e o status bar reutilizam o mesmo glifo Material Symbols em papéis diferentes (`hub` para swarm e MCP; `auto_awesome` para residente e uso de IA; `bolt` para tokens e bolt genérico). O IPG ([Vibetex/ipg](https://github.com/Vibetex/ipg)) usa Lucide (`@lucide/angular`) com ícones distintos por item de navegação. A constituição HiveMind (D6) fixa **Material Symbols** em `icons.ts` — não Lucide — então a solução é deduplicar glifos dentro da família já padronizada e favorecer outline (sem fill) onde o traço fino se aproxima do visual Lucide.

## 2. Escopo e Não-Escopo

- **Inclui:** remapeamento de glifos duplicados em `ide/frontend/src/icons.ts`; outline na activity bar; documentação desta spec.
- **Não inclui:** migrar para Lucide; alterar `Theme.kt` / HiveMind-App; redesenhar layout ou tokens de cor.

## 3. Camadas e Tokens Utilizados

Sem mudança de camada/token — apenas glifos vetoriais existentes via fonte Material Symbols.

## 4. Estados Obrigatórios (D5)

Não aplicável (sem nova superfície).

## 5. Pares de Contraste (D3 — >= 4.5:1)

Ícones herdam `currentColor` / tokens de texto já existentes.

## 6. Critérios de Aceite

- [x] Nenhuma chave de `icons` com o mesmo nome Material Symbols que outra chave de papel distinto (exceto variantes de tamanho do mesmo papel, ex.: `search` / `searchLarge`).
- [x] Família permanece Material Symbols (D6); zero Lucide/emoji estrutural.
- [x] `node ide/frontend/scripts/check-design-tokens.mjs` sem falhas.
- [x] `npm test` e `npm run build` em `ide/frontend` passando.
