# Política de Segurança (Security Policy) — HiveMind

A segurança do **HiveMind** (CLI, IDE Desktop, Relay e HiveMind-App) é tratada como prioridade máxima, especialmente nos subsistemas de isolamento de execução (`sandboxx`, AppContainer/Bubblewrap), jail de workspace (`workspace.Resolve`, `pathsec`), armazenamento em cofre do SO (`secretx`) e comunicação E2EE do Relay.

## Versões Suportadas

As correções de segurança são aplicadas continuamente na branch `main` e empacotadas na versão mais recente disponível em [GitHub Releases](https://github.com/gilvan-Borges/hivemind/releases).

| Versão | Suportada |
| :--- | :--- |
| Última release (`latest`) / `main` | ✅ Sim |
| Releases anteriores | ❌ Não (atualize para a versão mais recente) |

## Como Reportar uma Vulnerabilidade

**Por favor, NÃO abra uma Issue pública nem um Pull Request público para relatar vulnerabilidades de segurança.**

Utilize o canal oficial privado do GitHub:

1. Acesse a aba **[Security → Advisories](https://github.com/gilvan-Borges/hivemind/security/advisories/new)** deste repositório e clique em **"Report a vulnerability"** (Private Vulnerability Reporting).
2. Inclua no relato:
   - Componente afetado (ex.: `internal/workspace`, `internal/pathsec`, `internal/sandboxx`, `cmd/hivemind-relay`, `ide/`, `HiveMind-App`);
   - Passo a passo mínimo para reprodução (PoC);
   - Impacto estimado (ex.: escape de worktree/jail, execução fora da allowlist sem confirmação, vazamento de credencial em log/spillover).

## O que Esperar

- **Confirmação de recebimento:** em até **48 horas**.
- **Triagem e plano de correção:** em até **7 dias**, com classificação de severidade e criação de branch privada no GitHub Security Advisory quando aplicável.
- **Divulgação coordenada:** após a publicação da release corrigida, o advisory é publicado com os devidos créditos ao pesquisador (salvo pedido de anonimato).

## Escopo Principal de Segurança

- Escape de diretório / symlink / path traversal em `internal/workspace` e `internal/pathsec`;
- Bypass de bloqueio de arquivos sensíveis (`.env`, chaves privadas, `.hivemind/team.yaml`, credenciais);
- Bypass de confirmação humana ou sandbox em `internal/tools/shell.go` e `internal/sandboxx`;
- Vazamento de tokens/segredos em logs, telemetria, sessões ou arquivos de spillover;
- Autenticação, pareamento e criptografia ponta a ponta (E2EE) no `hivemind-relay` e `HiveMind-App`.
