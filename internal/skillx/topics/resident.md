# Agente Residente (`SOUL.md`, Webhooks & Slack)

## Persona & Preferências (`SOUL.md`)
- O arquivo `SOUL.md` (limitado em tamanho) define tom, convenções de revisão e prioridades operacionais do Residente sem sobrepor regras de segurança.

## Gatilhos Orientados a Eventos (`OriginWebhook`, `OriginSlack`)
- Webhooks GitHub (`POST /api/v1/webhooks/github`) validam HMAC (`X-Hub-Signature-256`) ou `standard-webhooks`.
- Eventos Slack (`POST /api/v1/webhooks/slack`) validam `X-Slack-Signature` + timestamp anti-replay.
- Todo payload externo entra embrulhado com `WrapUntrustedData` para mitigar prompt injection.
- Ações de escrita, shell, rede ou merge disparadas por eventos externos exigem aprovação humana obrigatória (HITL Ask).
