# Privacidade

O que este repositório grava no disco, lido no código em 2026-11-20. A raiz do perfil é `AppDataRoot`: `%LOCALAPPDATA%\HiveMind` no Windows, ou o diretório de configuração do usuário mais `HiveMind`.

## Fica no disco

| Arquivo | O que é |
| --- | --- |
| `remote-bridge.json` | Porta, opt-in, `tokenHash`, `boxPub` e dispositivos pareados. Sem o token de sessão cru. |
| `relay.json` | Conta e vínculo do relay neste PC. |
| `ai-usage.json` | Ledger local de uso. |
| `api-keys.json` | Chaves de provedor. |
| `credentials.json` | Credenciais de provedor que o código grava nesse caminho. |
| `provider-profiles.json` | Perfis de provedor. |
| `github-connector.json` | Conector do GitHub. |
| `jira-connector.json` | Conector do Jira. |
| `mcp-servers.json` | Servidores MCP configurados. |
| `ide-recent.txt` | Projetos recentes da IDE. |
| `jev/account-pool-health.json` | Saúde do pool de contas. |
| `resident/resident.json` | Política do residente. Não leva valor de segredo. |
| `resident/<conta>/memory.jsonl` | Texto que a pessoa pediu para lembrar (`Source: user`). |
| `resident/audit.jsonl` | Decisão da ferramenta: classe do alvo e 8 bytes de SHA-256. Sem caminho e sem argv. |
| `resident/vault/*.dpapi` | Embrulho do cofre no Windows. O valor não fica em JSON. |
| `agy-profiles/<id>/oauth-session.json` | Sessão OAuth gravada por perfil. |
| `skills/` | Skills locais. |
| `workspaces/<slug>/sessions/` | Transcript do projeto, fora do worktree. |
| `workspaces/<slug>/worktrees/` | Worktrees de entrega. |
| `.hivemind/team.yaml` | Time do projeto. |
| `.hivemind/MEMORY.md` | Memória de time no projeto. |
| `.hivemind/handoff/*.md` | Dossiê atual, modo `0644`, ainda dentro do worktree. |

## Não fica

- Valor de segredo do cofre do sistema (`internal/secretx`).
- Token de sessão da LAN. O disco guarda só o hex de SHA-256.
- Chave privada X25519 de longo prazo do bridge (`lan-bridge-box` no cofre).
- Comando, caminho ou corpo do pedido de permissão na linha durável de `audit.jsonl`.

O cartão de aprovação, na memória do processo, pode mostrar o alvo redigido enquanto a pessoa decide. Esse texto não é a linha do `audit.jsonl`.

## Retenção

`audit.jsonl` gira ao passar de 20 MiB. A linha antiga não é reescrita: se ainda tiver `target`, a leitura para a UI omite esse campo. Apagar o arquivo é ação da pessoa.

A oferta de pareamento vive só na memória, por 5 minutos, e some no resgate. Sessões em `workspaces/<slug>/sessions/` permanecem até serem apagadas. Esta fase não as cifra.

## Rede

Com o bridge ligado, o portador HTTP mostra IP, tamanho e tempo. O corpo autenticado e o SSE são caixa `naclbox`. `GET /api/v1/health` não leva segredo. O relay, quando usado, continua sem a chave privada: vê IP, tamanho e tempo, não o texto.
