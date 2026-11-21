# Plano Técnico — Agent Hive (Agente Residente Flutuante na Área de Trabalho)

> Referência: `spec.md` · Constituição: `.specify/memory/constitution.md`

---

## 1. Arquitetura de Sincronização (IDE <-> Orb)

Hoje `ide/internal/orb/hub.go` já mantém um servidor HTTP/SSE em `127.0.0.1:0` autenticado por token de sessão entre o processo principal (`App` em `ide/app.go`) e o processo satélite `--orb` (`OrbApp` em `ide/orb_window.go`).

Em vez de criar outro daemon ou duplicar o `ResidentManager` no processo `--orb` (o que quebraria isolamento de estado, aprovações e histórico SQLite/cofre), **todo o cérebro continua no processo principal** e a ponte `orb.Hub` passa a carregar um snapshot leve do estado do **Agent Hive**:

### 1.1 Extensão de `ide/internal/orb/hub.go`
Adicionar ao `OrbSnapshot`:
```go
type OrbResidentMessage struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"` // user | chief | tool | approval | error | system
	Label      string `json:"label,omitempty"`
	Text       string `json:"text"`
	ToolName   string `json:"toolName,omitempty"`
	ToolDetail string `json:"toolDetail,omitempty"`
	RiskTier   string `json:"riskTier,omitempty"`
	ApprovalID string `json:"approvalId,omitempty"`
	Resolved   string `json:"resolved,omitempty"` // "" | approved | denied
	Active     bool   `json:"active,omitempty"`
}

type OrbResidentState struct {
	State            string               `json:"state"`            // ocioso | pensando | aguardando você
	Workstream       string               `json:"workstream"`       // ex: "geral"
	Workspace        string               `json:"workspace,omitempty"`
	Provider         string               `json:"provider,omitempty"`
	Model            string               `json:"model,omitempty"`
	SandboxAvailable bool                 `json:"sandboxAvailable"`
	SandboxMechanism string               `json:"sandboxMechanism,omitempty"`
	Running          bool                 `json:"running"`
	Messages         []OrbResidentMessage `json:"messages"`
}
```
E no `OrbSnapshot`:
```go
Resident OrbResidentState `json:"resident"`
```

Métodos no `orb.Hub`:
- `SetResidentState(st OrbResidentState)` — substitui o snapshot inicial / pós-turno e faz `broadcast()`.
- `ApplyResidentEvent(ev idecore.LaneEvent)` (ou via helper em `ide/orb_host.go` para manter `internal/orb` sem dependência cíclica) — aplica streaming parcial `text`, `user_message`, `tool_call`, `tool_result`, `approval_required`, `approval_resolved`, `stage`, `error`, `finish` e dispara `broadcast()`.

### 1.2 Novas ações em `OrbAction` (`orb.Hub` + `ide/orb_host.go` + `ide/orb_window.go`)
- `resident_send` (`Text`, `Workstream`): dispara goroutine no processo principal chamando `a.ResidentStartTurn(workspace, workstream, text)` (publica `user_message` antes para feedback imediato tanto no Orb quanto na aba `Hive Residente` da IDE).
- `resident_cancel`: chama `a.ResidentCancel()`.
- `resident_approve` (`ApprovalID`, `Approved: bool`): chama `a.RespondApproval(approvalID, approved)`.
- `resident_clear` (`Workstream`): chama `a.ResidentClearSession(workstream)` e atualiza snapshot.
- `open_resident`: traz a IDE para frente e emite evento `hivemind:open-resident` para abrir/focar a aba `hive:resident`.

Métodos Wails no `OrbApp` (`ide/orb_window.go`):
- `SendHiveMessage(text, workstream string) error`
- `CancelHiveTurn() error`
- `RespondHiveApproval(approvalId string, approved bool) error`
- `ClearHiveChat(workstream string) error`
- `OpenHiveInIDE() error`

### 1.3 Sincronização com `ide/frontend/src/resident.ts`
- Quando um turno é iniciado pelo **Orb**, o backend também emite `hivemind:lane-event` com `laneId = "resident"` (ou `hivemind:resident-sync`), fazendo com que `resident.ts` receba o mesmo `user_message`, streaming, `tool_call`, `approval_required` e `finish`.
- Quando um turno é iniciado pela **aba Agent Hive na IDE**, o mesmo callback de `LaneEvent` no backend alimenta o `orb.Hub` automaticamente, mantendo o Agent Hive flutuante 100% espelhado sem precisar de polling.

---

## 2. UI do Painel Expandido (`ide/frontend/src/orb/view.ts`)

### 2.1 Geometria da janela (`ide/orb_window.go`)
- Estado colapsado: `152×152` (mascote flutuante puro, sem fundo, aura circular).
- Estado expandido (`SetOrbExpanded(true)`): `396×540` (com clamp automático para nunca sair dos limites do monitor ativo via `ScreenGetAll`).

### 2.2 Estrutura do Painel Expandido **Agent Hive**
1. **Cabeçalho (`orb-head`, arrastável)**
   - Mini mascote ativo + título `Agent Hive` (+ projeto ativo se houver).
   - Sublinha compacta: `<estado> · <provider>/<model> · <workstream>`.
   - Ações: botão `"Abrir IDE"` (abre a aba `Agent Hive` na IDE) + botão colapsar (`chevronDown`).
2. **Barra de Abas / Modos (3 abas enxutas, camada 1/2)**
   - **Conversa** (padrão)
   - **Avisos** (com contador numérico quando > 0)
   - **Agente** (seletor `HiveBee` / `HiveStriker` + temas + visor)
3. **Corpo da Aba `Conversa`**
   - Timeline rolável com:
     - Estado vazio: ícone `icons.sparkle` + `"Agent Hive pronto"` + workspace/sandbox badge + 3 sugestões clicáveis (`"Resumo do workspace"`, `"Git status e branch"`, `"Revisar alterações"`).
     - Mensagens (`user`, `chief` com `renderMarkdownLite`, `tool` com badge de risco, `approval` com botões **Aprovar** / **Recusar**, `error`).
   - **Barra fixa de aprovação perto do chat**: sempre que existir aprovação pendente (`!resolved`), renderizar dock compacto imediatamente acima do `<textarea>` do composer com selo de risco, ação e botões **Aprovar / Negar**.
   - Composer fixo no rodapé do chat:
     - `<textarea>` auto-ajustável (`--surface-input`, placeholder `"Fale com o Agent Hive... (Enter envia)"`).
     - Botão primário `Enviar` (`--action-primary`) ou `Parar` (`--color-danger`) quando `resident.running === true`.
4. **Rodapé (`orb-foot`)**
   - Esquerda: `Limpar conversa` (na aba Conversa) ou `Limpar avisos` (na aba Avisos) + indicador discreto `Sandbox ativa` / `IDE em 2º plano`.
   - Direita: `Sair de vez`.

---

## 3. Arquivos Impactados

| Arquivo | Mudança |
|---|---|
| `ide/internal/orb/hub.go` | Adicionar `OrbResidentState`, `OrbResidentMessage`, ações `resident_*` e `open_resident`, com testes em `hub_test.go`. |
| `ide/orb_host.go` | Alimentar `hub` com eventos/snapshot do `ResidentManager`, handlers de envio/cancelamento/aprovação/limpeza e `OpenHiveInIDE`. |
| `ide/orb_window.go` | Ajustar `orbExpandedW = 396`, `orbExpandedH = 540`, clamp de tela ao expandir e expor métodos `SendHiveMessage`, `CancelHiveTurn`, `RespondHiveApproval`, `ClearHiveChat`, `OpenHiveInIDE`. |
| `ide/app.go` | Encaminhar `LaneEvent` do residente (`laneId == "resident"`) e workspace ativo ao `orb.Hub`; sincronizar workspace atual no `OrbSnapshot`. |
| `ide/frontend/src/boot.ts` / `resident.ts` | Escutar `hivemind:open-resident` para focar a aba `Agent Hive`; evitar duplicação de mensagens quando originadas no Orb; atualizar rótulos visuais para `Agent Hive`. |
| `ide/frontend/src/orb/view.ts` | UI com abas (`Conversa` / `Avisos` / `Agente`), chat em tempo real do Agent Hive, cartões de aprovação e composer com `Enter`. |
| `ide/frontend/src/styles.css` | Estilos `.orb-tabs`, `.orb-chat-*`, `.orb-composer`, `.orb-approval*` usando 100% tokens Havrus (`--surface-*`, `--accent*`, `--action-primary*`, `--color-*`). |

---

## 4. Estratégia de Verificação

1. **Testes Go:**
   - `cd ide && go test -count=1 ./internal/orb/... ./internal/idecore/... ./internal/notify/...`
   - Novos testes unitários em `ide/internal/orb/hub_test.go` cobrindo `SetResidentState`, streaming de mensagens, aprovação inline e `OrbAction` (`resident_send`, `resident_approve`, `resident_cancel`).
2. **Design System & Frontend:**
   - `node ide/frontend/scripts/check-design-tokens.mjs` (zero `#hex` fora de `:root`, 12 pares WCAG).
   - `cd ide/frontend && npm test && npm run build` (testes novos para helpers de renderização/estado do chat do Agent Hive).
3. **Build & Smoke Check Windows:**
   - `cd ide && go vet . && go build -tags desktop,production -ldflags "-s -w -H windowsgui" -o build\hivemind-ide.exe .`
   - `.\build\hivemind-ide.exe --smoke-check`
4. **Teste E2E Manual com IDE oculta na bandeja:**
   - Fechar IDE no `X` -> clicar no mascote Agent Hive -> enviar comando -> validar resposta, tool call e aprovação sem abrir a IDE -> clicar em `Abrir IDE` e confirmar conversa idêntica na aba `Agent Hive`.
