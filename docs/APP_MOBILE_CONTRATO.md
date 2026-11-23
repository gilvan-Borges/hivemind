# Contrato do app — Projetos, Conversas e Chat

O HiveMind-App fala com a IDE pelo bridge que já existe: LAN ou Tailscale em `GET/POST /api/v1/*`, e o mesmo JSON tunelado pelo relay (`relaywire.AllowedRPC`). Não há rota paralela. Campo novo é opcional: um app antigo ignora o que não conhece.

Voz não entra neste contrato.

## Status da IDE

`GET /api/v1/health` (público no LAN; no relay passa pela allowlist)

```json
{
  "ok": true,
  "online": true,
  "hostName": "pc",
  "version": "…",
  "time": "2026-11-23T00:00:00Z",
  "transport": "lan",
  "openProject": { "workspace": "/tmp/acme", "name": "acme" }
}
```

`transport` é `lan`, `tailscale` (IP do cliente em `100.64.0.0/10`) ou `relay` (chamada in-process do cliente de relay). `online: true` significa que este processo respondeu. `openProject` some quando não há projeto registrado.

## Projetos e modo Full

`GET /api/v1/projects`

Cada item ganha `permissionMode` quando a IDE está no processo (`governed`, `auto_edit` ou `full_access`). `runningLanes` e `pendingApprovals` continuam.

`POST /api/v1/project/permission`

```json
{ "workspace": "/tmp/acme", "permissionMode": "full" }
```

`full` e `full_access` gravam Full naquele projeto (`team.yaml`). Governado volta com `governed`. No Full daquele projeto o time e o Agent Hive não emitem pedido de aprovação. O toggle global do Agent Hive continua existindo; os dois liberam.

Host sem a IDE responde `501`.

## Conversas

`GET /api/v1/sessions?workspace=`

Cada sessão ganha:

- `preview`: última fala, no máximo 140 caracteres
- `unread`: falas novas desde a última leitura. A primeira lista marca a linha de base (não lidas = 0) para não badjar o histórico antigo
- `kind`: `hive` quando o fio nasceu no Agent Hive; vazio é equipe

`GET /api/v1/session?workspace=&id=` devolve o transcript (`messages`).

`POST /api/v1/session/read` com `{ "workspace", "sessionId" }` zera `unread`.

Continuar: `POST /api/v1/runs/start` com o `sessionId` dessa conversa.

- Equipe: `runMode` `auto` (ou `direct`, `plan`, `squad`) e `laneId` da lane.
- Agent Hive: `runMode` `resident`. A lane fica `resident`. O `sessionId` continua o mesmo fio.

Parar: `POST /api/v1/runs/cancel` com `{ "laneId" }`. Para o Hive, `laneId` é `resident`.

## Aprovações

Não há canal novo. O app recebe no fluxo de eventos (SSE no LAN, quadro `event` no relay) um `lane_event` cujo `event.kind` é `approval_required`, com `approvalId`, `toolName`, `text` e `gate`.

`GET /api/v1/approvals?workspace=` lista os cartões ainda abertos, para o caso de o app ter perdido o evento.

`POST /api/v1/approvals/resolve`

```json
{ "approvalId": "…", "approved": true }
```

`approved: false` nega. No modo Full do projeto (ou no Full global do Hive) esses cartões não são criados.

## Ao vivo

Além de `lane_event`, cada `tool_call` também sai como `activity`:

```json
{
  "laneId": "lane-1",
  "team": "principal",
  "author": "dev",
  "tool": "edit_file",
  "phase": "file",
  "target": "main.go",
  "sessionId": "abc"
}
```

`phase` é `file`, `command`, `test` ou `other`.

## Diff, commit e PR

`GET /api/v1/git/diffs?workspace=` segue igual (`files[]`).

`POST /api/v1/git/commit`

```json
{ "workspace": "/tmp/acme", "message": "explica a mudança", "push": false }
```

`POST /api/v1/git/pr`

```json
{ "workspace": "/tmp/acme", "title": "título", "body": "texto", "baseBranch": "main" }
```

A resposta do PR traz `url`. Host sem a IDE responde `501`.

## Rotinas do Agent Hive

`GET /api/v1/routines` → `{ "routines": [{ "id", "expr", "timezone", "text", "workstream", "enabled" }] }`

`POST /api/v1/routines/enabled`

```json
{ "id": "rot_1", "enabled": false }
```

`enabled: false` pausa. `true` retoma. Não cria nem apaga rotina por aqui.

Uma rotina pode trazer `loopId` quando ela dispara um loop. O app não cria essa rotina por aqui.

## Loops

O app v1 acompanha e para. Não cria loop e não grava `allow_full_unattended` (isso só existe na tela de Configurações da IDE).

`GET /api/v1/loops?workspace=`

```json
{
  "loops": [
    {
      "id": "fix-auth",
      "name": "Deixar os testes verdes",
      "kind": "goal",
      "status": "running",
      "outcome": "",
      "iteration": 2,
      "phase": "verify",
      "lastVerdict": "continue",
      "lastReason": "unit exit=1",
      "spentMicroUsd": 0,
      "prUrl": ""
    }
  ]
}
```

`status` é `running`, `paused`, `waiting`, `stopped` ou `finished`. `outcome`, quando o loop acabou, é `done`, `impossible`, `stalled`, `budget_exhausted`, `blocked` ou `stopped`.

`GET /api/v1/loops?workspace=&id=` devolve o mesmo objeto, sem a lista. Id desconhecido responde 404.

`POST /api/v1/loops/stop`

```json
{ "workspace": "/tmp/acme", "id": "fix-auth" }
```

Para o loop. Não faz merge.

`POST /api/v1/loops/pause` e `POST /api/v1/loops/resume` usam o mesmo corpo. Resume é uma retomada acompanhada. O app v1 só precisa de listar, ver e parar.

No fluxo de eventos, além de `lane_event`, um `loop_iteration` também sai como `activity` com `loopId`, `iteration` e `phase` (`plan`, `execute`, `verify` ou `decide`). `loop_finished` traz o `outcome` no `lane_event`.
