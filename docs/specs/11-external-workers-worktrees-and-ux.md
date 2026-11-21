# Spec 11 — Workers CLI Externos (Piloto Cursor), Ergonomia de Worktrees, Residente Event-Driven e UX Avançada

> **Status:** Proposta de Implementação (Onda 6)
> **Alvos:** `internal/orchestrator/`, `internal/modelx/cursor.go`, `internal/gitx/`, `internal/workspace/`, `internal/resident/`, `internal/skillx/`, `ide/internal/idecore/`, `ide/frontend/src/`

---

## 1. Contexto e Motivação

A análise comparativa **Orca vs HiveMind** mostrou que o HiveMind já está à frente no **motor interno** (review loop com `reviewx`, `TeamBoard`, `dev_guard`, failover Jev e sandbox por OS `AppContainer`/`bwrap`), mas existem ganhos estratégicos de alto retorno em:
1. **Extensibilidade de execução (P1):** permitir que CLIs externos atuem como workers orquestrados pelo Chefe dentro de worktrees isoladas, começando pelo **Cursor CLI (`agent`)** — que já está integrado no HiveMind (`internal/modelx/cursor.go`, `cursor_stream.go`, `WithAgentDir`).
2. **Ergonomia operacional de worktrees (P2):** eliminar worktrees "vazias" em monorepos grandes, criar em background com progresso e dar rollback rápido por turno.
3. **Residente alcançável externamente (P3):** `SOUL.md`, webhooks event-driven e conector Slack mantendo o modelo de segurança HITL + `WrapUntrustedData`.
4. **Visibilidade financeira imediata (P4):** custo USD acumulado por run, agente e papel.
5. **UX avançada da IDE (P5–P7):** alternância Chat ↔ Terminal (PTY), split panes de lanes, Design Mode via CDP (Chrome DevTools MCP), hybrid skill stubs e Race Mode.

---

## 2. Especificação Técnica por Frente

### 2.1 P1 — Workers CLI Externos com Worker Contract (Piloto: Cursor CLI `agent`)
- **Por que começar pelo Cursor CLI:** o HiveMind já resolve binário (`LookCursor`), diretório de trabalho (`WithAgentDir`), stream NDJSON (`cursor_stream.go`) e política `--trust`/`--force` (`cursor.go:46`). Não exige assinatura de Claude Code ou Codex para validar o protocolo completo.
- **Worker Contract (`internal/workerx/` ou `internal/orchestrator/worker_contract.go`):**
  - `dispatch`: entrega ao worker CLI o escopo sanitizado (`HandoffPackage`) + preâmbulo estruturado contendo `taskId`, `dispatchId`, diretório da worktree e instruções de protocolo.
  - `heartbeat`: sinalização periódica em tarefas longas para distinguir worker ativo de processo travado.
  - `ask(question, options)`: gate bloqueante que sobe como card de decisão HITL na IDE/Mobile/Orb em vez de travar esperando stdin local do terminal.
  - `worker_done(outcome, filesModified, taskId, dispatchId)`: evento terminal **idempotente** (aceito exatamente uma vez por `dispatchId`) que devolve autoridade de conclusão ao Chefe/orquestrador.
- **Segurança & Governança Pós-Hoc:**
  - Como um CLI externo não passa pelo `dev_guard` nem pelo `pathsec` em tempo real (e no Windows o Cursor CLI não possui sandbox nativa), o adaptador deve:
    1. Executar em worktree isolada e respeitar o modo de permissão (`--trust`/`--force` apenas em `Full Acesso`).
    2. Validar o `git diff --name-only` pós-hoc antes de integrar em `_integracao`, rejeitando automaticamente alterações fora de `owned_paths` ou em arquivos sensíveis (`pathsec.IsSensitivePath`).
- **Extensibilidade:** definir interface `ExternalWorkerAdapter` com implementação piloto `CursorWorkerAdapter` (e extensível depois para OpenCode, Claude Code, Codex).

### 2.2 P2 — Ergonomia de Worktrees, Checkpoints & Painel de Gestão
- **`.worktreeinclude` (raiz do repo):** lista de caminhos literais (sem globs) de arquivos gitignored copiados para novas worktrees (ex.: `.vscode/settings.json`, `.nvmrc`, `local.properties`).
  - **Restrição de segurança obrigatória:** todo caminho listado passa por `pathsec.IsSensitivePath`; arquivos sensíveis (`.env`, `.pem`, `.key`, credenciais) são **recusados por padrão** para não furar a blindagem da Onda 1.
- **`sharedDirectories`:** vinculação via symlink/junction (sem cópia) para árvores grandes rebuildáveis (`node_modules`, `.gradle`, `.cache`) configuráveis por projeto, com aviso/proteção contra escrita destrutiva no cache compartilhado.
- **Criação assíncrona com progresso:** `git fetch` / `git worktree add` fora da thread de UI emitindo eventos de progresso e ação de **Retry** em caso de erro.
- **Worktree Checkpoints:** snapshot barato antes de cada turno de Dev (`git stash create` / ref interna `.git/refs/hivemind/checkpoints/<runId>`) permitindo rollback com 1 clique.
- **Painel de Administração de Worktrees na IDE:** listar worktrees ativas/órfãs, tamanho/branch, `archive` e `prune`.

### 2.3 P3 — Residente Event-Driven (`SOUL.md`, Webhooks e Slack)
- **`SOUL.md`:** arquivo Markdown editável ao lado de `MEMORY.md` definindo tom, propósito e preferências pessoais do usuário no residente (com cap de bytes e injeção estável).
- **Webhooks event-driven:** receptor HTTP usando `standard-webhooks` (já presente no `go.mod`) com validação de assinatura HMAC para eventos GitHub (PR aberto, review solicitada, check falho) disparando lane do residente sem depender só de polling.
- **Conector Slack (opcional):** menção `@bot` em canal abre uma tarefa/lane no residente e responde com link de PR/resumo.
- **Mitigação de risco:** toda entrada externa (webhook/Slack) entra obrigatoriamente encapsulada em `WrapUntrustedData` e **nunca** executa escrita, shell ou abertura de PR sem aprovação HITL explícita do usuário.

### 2.4 P4 — Custo USD Acumulado por Run, Agente e Papel
- Reaproveitar os preços do catálogo Jev (`PromptMicroUSD`, `CompletionMicroUSD`, `CachedContentTokenCount` e `DiscountPct` consolidados na Spec 06) para calcular USD real por chamada.
- Agregar e exibir custo USD acumulado por **run**, por **agente** e por **papel** (`Chefe`, `Dev`, `QA`) no resumo da CLI/TUI, no cabeçalho da lane na IDE e na página **Uso & Limites**.

### 2.5 P5–P7 & Extras de Produtividade
- **P5 — Toggle Chat ↔ Terminal (PTY) na IDE:** alternar a mesma lane entre a UI de chat e o terminal interativo (TUI/CLI) reaproveitando o mesmo `sessionID` sem perder contexto.
- **P6 — Split Panes de Lanes:** permitir abrir 2+ lanes/agentes lado a lado na IDE (além do split de diff existente) para acompanhar devs paralelos simultaneamente.
- **P7 — Design Mode via CDP:** reutilizar o servidor Chrome DevTools MCP (`mcpx/defaults.go`) conectado ao browser real do usuário para selecionar um elemento na página e enviar seletor + HTML + estilos computados direto para o composer do chat (sem embutir browser por worktree).
- **Hybrid Skill Stubs:** comando `hivemind skills get <topic>` servindo guias version-matched embutidos no binário em execução, eliminando drift entre skills e flags reais do app.
- **Race Mode:** preset sobre `parallelagent` que dispara N agentes independentes na mesma task (em worktrees isoladas) e apresenta comparação de diffs/testes para o humano ou QA escolher o vencedor.

---

## 3. Fora de Escopo (Decisões Explícitas)
- Runtime SSH/VM/cloud por worktree (conflita com o threat model local de sandbox OS + HITL).
- Marketplace público de plugins, integração Linear e app iOS nativo.

---

## 4. Critérios de Aceite
- Um Dev configurado como worker externo **Cursor CLI (`agent`)** executa uma task em worktree isolada, respeita `dispatch`/`heartbeat`/`ask`/`worker_done`, passa na validação pós-hoc de `owned_paths` + `pathsec` e integra no fluxo normal do QA.
- `.worktreeinclude` copia arquivos literais não-sensíveis e bloqueia `.env`/chaves; checkpoints permitem rollback completo do turno do Dev.
- Custo USD acumulado aparece no resumo de execução (CLI e IDE) batendo com o catálogo Jev + tokens em cache.
- Entradas de webhook/Slack chegam com `WrapUntrustedData` e exigem aprovação HITL antes de qualquer ação de escrita.
