# Cursor neste repositório (rascunho)

Configuração proposta para o agente do Cursor no HiveMind. Ainda não há pull request: a relação com a HiveMind IDE precisa ser confirmada antes de adotar isto.

O Cursor lê `.cursor/mcp.json`, `.cursor/rules/*.mdc` e as skills em `.cursor/skills/` e em `.agents/skills/`. A documentação usada foi a de [MCP](https://cursor.com/docs/mcp), [Rules](https://cursor.com/docs/rules) e [Agent Skills](https://cursor.com/docs/skills).

## O que este repositório é

Não há um repositório separado chamado Hive IDE. A **HiveMind IDE** é o app desktop em [`ide/`](../ide/README.md) (Go + Wails, binário `hivemind-ide`). Ela reutiliza o núcleo da CLI (`internal/orchestrator`, `internal/team`, e o resto). O catálogo de skills do projeto, para o time da IDE, é só `.agents/skills/` (`internal/skillx`). O Chefe recebe esse catálogo no prompt e pode pedir uma skill pelo nome ao delegar.

Por isso as skills novas deste rascunho ficam em `.cursor/skills/`, não em `.agents/skills/`. Assim o painel e o Chefe da IDE não passam a ver Ponytail nem a varredura de arquitetura até alguém decidir o contrário.

As skills que já estavam em `.agents/skills/` (`ide-dark-islands-ui`, `multi-agent-ide-patterns`) não foram movidas.

## Ligado por padrão

| Peça | Onde | Para quê |
| --- | --- | --- |
| Context7 | `context7` em `.cursor/mcp.json` | Documentação atual de biblioteca |
| Code Review Graph | `code-review-graph` em `.cursor/mcp.json` | Grafo local e impacto de uma mudança |
| improve-codebase-architecture | `.cursor/skills/improve-codebase-architecture/` | Varredura de arquitetura, sob pedido |
| Ponytail | `.cursor/skills/ponytail/` | Mudança mínima. Modo padrão: `lite` |

A regra [`.cursor/rules/ai-tooling.mdc`](../.cursor/rules/ai-tooling.mdc) vale em todo chat do Cursor (`alwaysApply: true`) e fixa o modo `lite`.

### Context7

Servidor remoto oficial: `https://mcp.context7.com/mcp`, pacote npm `@upstash/context7-mcp` (a config usa o remoto, que é o caminho recomendado para o Cursor). A chave vai no header `Authorization`, interpolada do ambiente. Crie uma chave grátis em [context7.com/dashboard](https://context7.com/dashboard?tab=settings).

```bash
# Linux / macOS
export CONTEXT7_API_KEY="sua-chave"
# PowerShell
$env:CONTEXT7_API_KEY = "sua-chave"
```

Sem a variável, o header sai como `Bearer ` vazio e a chamada falha. Não cole a chave no arquivo. Em Cloud Agents, o `mcp.json` do repositório não configura a conexão HTTP pessoal do backend; nesse ambiente a chave entra pela UI de MCP do agente.

### Code Review Graph

Pacote PyPI `code-review-graph` (série 2.4). Exige Python 3.10+ e [`uv`](https://docs.astral.sh/uv/getting-started/installation/) (`uvx` no PATH). O Cursor sobe `uvx code-review-graph serve --repo <raiz do workspace>`. O campo `type: stdio` segue a tabela atual da doc do Cursor e o instalador do próprio projeto, que marca Cursor como `needs_type`.

Na primeira vez, na raiz do workspace:

```bash
uvx code-review-graph build
```

O banco fica em `.code-review-graph/` e está no `.gitignore`. Não rode `code-review-graph install` neste rascunho: esse comando também escreve hooks e arquivos de instrução, e grava um `cwd` absoluto da máquina.

### Skills ligadas

Instaladas com a CLI de skills e depois movidas para `.cursor/skills/`:

```bash
npx skills add mattpocock/skills --skill improve-codebase-architecture --agent cursor -y --copy
npx skills add DietrichGebert/ponytail --skill ponytail --agent cursor -y --copy
```

Os commits de origem estão em `skills-lock.json`. A CLI, com `--agent cursor`, gravou em `.agents/skills/`. O rascunho moveu as pastas para o Cursor não alterar o catálogo da IDE. Um `npx skills update` posterior pode procurar de novo em `.agents/skills/` e não enxergar a cópia movida.

`improve-codebase-architecture` tem `disable-model-invocation: true`: o agente não deve disparar sozinho. A skill cita outra skill, `codebase-design`, do mesmo repositório, que não foi instalada. Também espera `GLOSSARY.md` e `docs/adr/`, que este repo não usa como glossário do time.

Ponytail upstream assume modo `full` e, no Cursor, oferece hooks (`node scripts/cursor-hooks.js install --project`) como alternativa à regra. Os hooks apontam para um checkout local e não rodam `sessionStart` em cloud agents. Enquanto a regra `ponytail.mdc` existe, os hooks não injetam o modo. Este rascunho não instala os hooks: a regra `ai-tooling.mdc` segura o `lite`. As outras skills do mesmo repo (`ponytail-review`, `ponytail-audit`, `ponytail-debt`, `ponytail-gain`, `ponytail-help`) ficaram de fora.

## Desligado, pronto para copiar

O Cursor não tem `disabled` em `mcp.json`. Um bloco em `mcpServers` liga o processo. A seção `disabledServers` não faz parte do formato e é ignorada. O desligar da interface (Customize) e o `agent mcp disable` ficam na máquina, não no Git.

O equivalente neste rascunho é [`.cursor/mcp.optional.json`](../.cursor/mcp.optional.json): JSON válido, mesmo formato, arquivo que o Cursor não abre. Para ligar um servidor, pergunte antes e copie só o objeto dele para `.cursor/mcp.json`. Para desligar, apague o objeto.

| Servidor | Pacote ou imagem conferida | Quando ligar |
| --- | --- | --- |
| `playwright` | npm `@playwright/mcp` | Teste ou automação da UI web em `ide/frontend` |
| `chrome-devtools` | npm `chrome-devtools-mcp` | Depurar uma página já aberta no Chrome |
| `toolbox` | npm `@toolbox-sdk/server` | Postgres local de desenvolvimento, só `127.0.0.1` |
| `github` | `https://api.githubcopilot.com/mcp/` | Issue, PR ou API do GitHub, com toolsets restritos |
| `figma-context` | npm `figma-developer-mcp` | Implementar um frame do Figma |
| `codebase-memory` | npm `codebase-memory-mcp` | Trocar o Code Review Graph, nunca somar a ele |

Playwright e Chrome DevTools não ficam ligados juntos. Code Review Graph e Codebase Memory também não.

`untangle-codebase` (caldoai/skills) está em `.cursor/optional-skills/untangle-codebase/`, fora das pastas que o Cursor varre. O passo a passo está em `COMO-ATIVAR.md` nessa pasta. É uma limpeza do repo inteiro: só com pedido explícito, e o diff se revisa.

### Variáveis

Nenhum segredo entra no Git. Defina no ambiente da máquina:

| Variável | Quem usa | Observação |
| --- | --- | --- |
| `CONTEXT7_API_KEY` | Context7 | Chave da conta, header `Authorization: Bearer` |
| `GITHUB_MCP_TOKEN` | GitHub MCP | PAT fino, só deste repositório. Conteúdo leitura, issues e pull requests. Sem administração, segredos, Actions ou apagar repositório. Não reuse o `GITHUB_TOKEN` amplo do app |
| `FIGMA_API_KEY` | Figma Context | Token pessoal do Figma com leitura |
| `HIVEMIND_DEV_DB_NAME` | Toolbox | Banco local |
| `HIVEMIND_DEV_DB_USER` | Toolbox | Usuário local |
| `HIVEMIND_DEV_DB_PASSWORD` | Toolbox | Senha local |

O host do Toolbox está fixo em `127.0.0.1` e a porta em `5432`. O HiveMind guarda sessão em JSON, não em Postgres. Só ligue o Toolbox se a tarefa for de fato um banco local que você subiu. Não aponte para produção.

No GitHub MCP, o header `X-MCP-Toolsets` já vale `repos,issues,pull_requests`. Para só leitura, acrescente `"X-MCP-Readonly": "true"` no mesmo objeto. A imagem Docker `ghcr.io/github/github-mcp-server` é o servidor local oficial. O pacote npm `@modelcontextprotocol/server-github` está abandonado desde abril de 2025 e não entra nesta config. O remoto foi o escolhido porque o guia de instalação do Cursor o recomenda e não exige Docker.

## O que ficou de fora

Mobile MCP não entra. Não há app mobile no código. [`docs/SPEC_APP_MOBILE_NATIVO.md`](SPEC_APP_MOBILE_NATIVO.md) é especificação de um app Flutter futuro. Também ficam de fora n8n-mcp, Desktop Commander, Serena, Windows MCP, Blender MCP e Apify.

## Limitações do formato

- Presença em `.cursor/mcp.json` significa ligado. Não dá para versionar “instalado porém desligado” no mesmo arquivo.
- JSON não aceita comentário. O “quando ligar” está nesta página e na regra.
- A tabela de STDIO do Cursor marca `type` como obrigatório. Vários READMEs de MCP omitem o campo. Os servidores locais deste rascunho incluem `"type": "stdio"`. Os remotos usam `url`, como nos exemplos do Cursor.
- A interpolação `${env:NOME}` vale em `command`, `args`, `env`, `url` e `headers`. Header com underscore pode ser descartado no proxy de Cloud Agents. Os headers daqui usam hífen ou o nome `Authorization`.
- `${workspaceFolder}` no `--repo` do grafo acompanha a pasta aberta no Cursor, inclusive uma worktree, se ela for a raiz do workspace.
