# Tasks — Agent Hive (Agente Residente Flutuante na Área de Trabalho)

> Referência: `spec.md` + `plan.md` · Constituição: `.specify/memory/constitution.md`

## Lista de Tarefas

### Fase 1 — Contrato e Ponte Backend (`ide/internal/orb` + `ide/`)
- [ ] **H001** Estender `OrbSnapshot` e `OrbAction` em `ide/internal/orb/hub.go` com `OrbResidentState`, `OrbResidentMessage` e ações `resident_send`, `resident_cancel`, `resident_approve`, `resident_clear`, `open_resident`.
- [ ] **H002** Escrever testes unitários em `ide/internal/orb/hub_test.go` validando snapshot do residente, streaming incremental e despacho das novas ações.
- [ ] **H003** Conectar `ide/app.go` e `ide/orb_host.go` ao `idecore.ResidentManager` para inicializar o snapshot da sessão ativa no startup (`ResidentStatus` + `ResidentSessionMessages` + workspace ativo) e propagar cada `LaneEvent` de `laneId == "resident"` em tempo real para o `orb.Hub`.
- [ ] **H004** Implementar no `orb_host.go` os handlers das ações vindas do Orb (`resident_send`, `resident_cancel`, `resident_approve`, `resident_clear`, `open_resident`), garantindo execução dentro da sandbox configurada e atualização sincronizada na UI da IDE.
- [ ] **H005** Expor métodos Go no `OrbApp` (`ide/orb_window.go`), ajustar janela expandida para `396×540` com clamp aos limites do monitor (`ScreenGetAll`) e garantir foco ao abrir o composer.

### Fase 2 — Interface Unificada do Agent Hive Flutuante (`ide/frontend/`)
- [ ] **D001 [D1/D2/D3]** Criar estilos `.orb-tabs`, `.orb-chat-list`, `.orb-msg`, `.orb-approval`, `.orb-composer`, `.orb-suggestions` em `ide/frontend/src/styles.css` usando apenas tokens semânticos (`--surface-*`, `--text-*`, `--accent*`, `--action-primary*`, `--color-*`).
- [ ] **D002 [D6]** Validar zero emojis estruturais (usar `icons.ts`: `icons.sparkle`, `icons.closeSmall`, `icons.chevronDown`, etc.).
- [ ] **H006 [D5]** Atualizar `ide/frontend/src/orb/view.ts` nomeando o agente flutuante como **Agent Hive**, colocando a aba **Conversa** como tela inicial ao clicar no mascote, com os 4 estados obrigatórios (carregando, erro, vazio com atalhos, dados com streaming) e barra fixa de aprovação pendente ancorada logo acima do composer.
- [ ] **H007** Preservar histórico/rascunho do input no Orb durante updates SSE parciais (`hivemind:orb-state`) sem perder foco ou apagar o que o usuário está digitando no `<textarea>`.
- [ ] **H008** Adicionar listener `hivemind:open-resident` em `ide/frontend/src/boot.ts` / `ide/frontend/src/resident.ts` para abrir a aba **Agent Hive** (`hive:resident`) ao clicar em **Abrir IDE** no flutuante e manter `residentState.messages` em sincronia quando mensagens nascerem pelo Orb.

### Fase 3 — Testes, Qualidade e Validação Final
- [ ] **H009** Adicionar testes unitários em `ide/frontend/src/` para formatação/estado da conversa do Agent Hive no Orb.
- [ ] **D003** Rodar `node ide/frontend/scripts/check-design-tokens.mjs` garantindo 0 falhas e contraste >= 4.5:1.
- [ ] **H010** Rodar `cd ide/frontend && npm test && npm run build`, `cd ide && go vet . && go test -count=1 ./internal/orb/... ./internal/notify/...`, compilar `build/hivemind-ide.exe` e `build/bin/hivemind-ide.exe` e validar `--smoke-check`.
- [ ] **H011** Validar manualmente com a IDE fechada na bandeja: enviar comando pelo Agent Hive flutuante, aprovar ferramenta, confirmar sincronia ao reabrir a aba **Agent Hive** na IDE.
