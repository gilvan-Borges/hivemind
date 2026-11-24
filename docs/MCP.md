# MCP e skills no HiveMind

O cliente MCP mora no produto. A CLI (`hivemind run` e a sessão interativa), a HiveMind IDE e o Remote Bridge usam o mesmo host. O app mobile não fala MCP direto: quando o agente pede para ligar um servidor, a IDE emite o mesmo `approval_required` de sempre, e o celular responde por esse fluxo.

O import de credenciais do Jira (`internal/jirax`, arquivos `.cursor/mcp.json`, `.mcp.json` e `.agents/mcp_config.json`) não foi alterado. A configuração daqui fica em outro arquivo.

## Onde grava

| Escopo | Caminho | O que tem |
| --- | --- | --- |
| Usuário | pasta de config do sistema, `HiveMind/mcp-servers.json` | interruptor que vale para todos os projetos |
| Projeto | `.hivemind/mcp-servers.json` | interruptor só daquele projeto. A pasta `.hivemind/` já está no gitignore |

Os padrões (comando, URL, toolsets) ficam no código. O arquivo gravado só guarda o que mudou, em geral `name` e `enabled`. Segredo não entra no Git: a config usa `${env:NOME}` e a expansão acontece na hora de conectar.

`hivemind mcp list` mostra o estado. `hivemind mcp enable nome` e `hivemind mcp disable nome` gravam o interruptor do usuário. `--project` limita ao projeto aberto.

Na IDE, a barra lateral tem a vista MCP, com o mesmo interruptor e a opção "só neste projeto".

## Ligados por padrão

- **context7** — HTTP `https://mcp.context7.com/mcp`. A conexão tenta sem chave. `CONTEXT7_API_KEY` (chave grátis no painel do Context7) entra no header só quando a variável existe. Se o servidor recusar a sessão anônima, o status fica `indisponível` e o texto pede para definir `CONTEXT7_API_KEY`. Isso não derruba a execução.
- **code-review-graph** — `uvx code-review-graph serve`, com o diretório de trabalho do projeto. Exige [uv](https://docs.astral.sh/uv/) e Python 3.10+. Sem `uvx` no PATH o processo não sobe: o status fica `indisponível` (não "ligado") e a mensagem diz para instalar o uv ou desligar o servidor. Na primeira vez, na pasta do projeto: `uvx code-review-graph build`. O banco `.code-review-graph/` não deve ir para o Git.

## Desligados até alguém pedir

Playwright, Chrome DevTools, a extensão Agent Hive (`hivemind-browser`), Toolbox, GitHub, Figma Context e Codebase Memory aparecem na lista, mas o processo não inicia e o esquema das ferramentas não entra no prompt. `hivemind-browser` não sobe um processo: ele mora neste host e só fala com a extensão em `127.0.0.1`. O guia está em [CHROME_EXTENSAO.md](CHROME_EXTENSAO.md).

O agente tem três ferramentas leves em toda execução:

- `mcp_list_servers` — nome, descrição e se está ligado. Desligado não traz ferramentas.
- `mcp_enable_server` — pede permissão (terminal, IDE ou app mobile) e liga só nesta tarefa. No fim da execução o processo cai e a preferência gravada não muda.
- `mcp_call` — chama uma ferramenta de um servidor que já está ligado.

Pares que não ficam ligados juntos: Playwright, Chrome DevTools e `hivemind-browser` (os três são controle de browser; só um por vez), e Code Review Graph com Codebase Memory. Ligar um desliga os outros do grupo na configuração; no meio da tarefa o segundo é recusado enquanto o primeiro estiver no ar.

O Toolbox só aceita `POSTGRES_HOST` em `127.0.0.1` ou `localhost`. O padrão já é `127.0.0.1`. Variáveis: `HIVEMIND_DEV_DB_NAME`, `HIVEMIND_DEV_DB_USER`, `HIVEMIND_DEV_DB_PASSWORD`. O HiveMind não usa Postgres para sessão. Não aponte isso para produção.

O GitHub MCP usa o servidor remoto oficial (`https://api.githubcopilot.com/mcp/`) com o header `X-MCP-Toolsets: repos,issues,pull_requests`. O token preferido é `GITHUB_MCP_TOKEN`, fino, só deste repositório, sem administração, segredos ou Actions. Não reuse um `GITHUB_TOKEN` amplo. Se `GITHUB_MCP_TOKEN` não existir e o `gh` desta máquina estiver autenticado (`gh auth login`), o host lê `gh auth token` na hora de conectar. Esse valor fica só na memória da conexão: não é gravado no `mcp-servers.json`, não entra no log e some quando o processo do host fecha. Sem a variável e sem `gh` autenticado, o status fica `indisponível` e pede um dos dois. O pacote npm antigo `@modelcontextprotocol/server-github` não é usado.

Figma usa `FIGMA_API_KEY`.

## Skills

Elas são semeadas no catálogo nativo (`%LOCALAPPDATA%\HiveMind\skills` no Windows; no Linux, a pasta de config do usuário). Uma skill que você já editou não é reescrita.

| Skill | Origem | Estado |
| --- | --- | --- |
| `ponytail` | Dietrich Gebert, MIT | ligada, modo padrão `lite` |
| `improve-codebase-architecture` | Matt Pocock | ligada; o Chefe só deve escolhê-la quando a tarefa for de arquitetura |
| `untangle-codebase` | caldoai/skills, MIT | desligada. O Chefe só a seleciona se você pedir essa limpeza pelo nome |

O painel de Skills mostra a desligada com o botão Ativar. O texto de origem está em `internal/skillx/seeds/`, com atribuição em `NOTICE.md`.

## O que este cliente ainda não faz

Não há editor de comando, URL ou header na IDE: o catálogo padrão e as variáveis de ambiente cobrem os servidores previstos. Não há OAuth interativo do Context7 nem do GitHub; a chave vai por variável. Um servidor que falha ao subir (binário ausente, rede, chave) não cancela a tarefa: o status fica no `mcp_list_servers` e as outras ferramentas seguem.
