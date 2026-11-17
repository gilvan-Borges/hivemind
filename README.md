# HiveMind CLI

Time de software no terminal. Você abre o prompt na pasta do projeto, descreve um objetivo, e um grupo de agentes — gerente de projeto, tech lead, arquiteto, desenvolvedores e QA — se organiza para implementar, testar e, se você quiser, abrir o pull request.

O binário é estático (`hivemind` no Linux e no macOS, `hivemind.exe` no Windows). Os identificadores do código ficam em inglês; os textos do programa e este guia estão em português.

Esta branch (`hivemind-cli-go`) é um projeto novo. Ela não contém o IDE da branch `main`.

## Sessão interativa

`hivemind` sem argumentos, dentro da pasta do projeto, abre a tela cheia (Bubble Tea). No primeiro uso, um assistente monta o time: nome, quantidade de desenvolvedores, modelo de cada agente a partir de `agy models` (atalho “mesmo para todos”), esforço e as integrações (worktree, commit, pull request, GitHub e Jira).

A conversa fica no centro, com markdown. A barra da esquerda mostra cada agente (gerente, líder, arquiteto, dev e QA), o modelo e o estado ao vivo: aguardando, pensando, usando ferramenta, concluído ou erro. A barra da direita lista os arquivos alterados. Embaixo, o campo aceita várias linhas (Enter envia, Shift+Enter ou Alt+Enter quebra a linha). `/ajuda` descreve os comandos; `/time`, `/modelo`, `/agentes`, `/sessoes`, `/jira`, `/git`, `/pr`, `/limpar`, `/tema` e `/sair` também. `@arquivo` anexa o conteúdo. Esc cancela a execução (os processos `agy` são encerrados). Ctrl+C duas vezes sai.

Uma saudação ou uma pergunta curta passa só pelo gerente. Um pedido de mudança percorre o time inteiro, com o texto chegando em fluxo (`agy --output-format stream-json`). O checkout da sua branch não é trocado em silêncio: no fim, a tela pergunta se você quer trazer a integração. Diagnósticos vão para `.hivemind/logs/`, não para a tela. A pasta `.hivemind/` entra no `.gitignore`.

## O que o time faz

1. O gerente de projeto lê o objetivo (ou a issue do Jira) e monta o plano. Se o Jira estiver ligado, ele pode criar subtarefas.
2. O tech lead divide o trabalho entre os desenvolvedores, sem dois agentes no mesmo arquivo.
3. O arquiteto define a abordagem mínima.
4. Os desenvolvedores trabalham **em paralelo** (goroutines do ADK). Em um repositório Git com pelo menos um commit, cada um recebe a própria branch e uma git worktree em `.hivemind/worktrees/`.
5. O QA revisa e roda testes. Se reprovar, o ciclo volta para os desenvolvedores. Se aprovar, o ciclo para. O número de voltas tem limite (`max_qa_loops`).

A orquestração usa os agentes de workflow do ADK Go: `sequentialagent`, `parallelagent` e `loopagent`.

## Requisitos

Para **usar** o binário pronto: Git, e as chaves dos provedores que o time usar. Go não precisa estar instalado.

Para **compilar**: Go **1.26.10 ou mais novo**. O ADK Go v2 exige essa versão.

## Instalação no Windows (PowerShell)

### Baixar o binário

No GitHub, abra a branch `hivemind-cli-go`, entre em **Actions**, abra a execução do workflow **HiveMind CLI** e baixe o artefato `hivemind-windows-amd64`. O arquivo é `hivemind.exe`.

Coloque-o numa pasta que esteja no `PATH`, ou chame-o pelo caminho completo.

### Compilar

```powershell
winget install --id GoLang.Go -e
```

O `winget` pode instalar uma versão antiga. Confira com `go version`. Se for menor que 1.26.10, baixe o zip em [https://go.dev/dl/](https://go.dev/dl/) (`go1.26.10.windows-amd64.zip`), extraia e coloque `go\bin` no `PATH`.

```powershell
git clone --branch hivemind-cli-go --single-branch https://github.com/gilvan-Borges/HiveMind.git
cd HiveMind
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w" -o hivemind.exe .\cmd\hivemind
.\hivemind.exe --help
.\hivemind.exe version
```

## Instalação no Linux e no macOS

```bash
git clone --branch hivemind-cli-go --single-branch https://github.com/gilvan-Borges/HiveMind.git
cd HiveMind
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o hivemind ./cmd/hivemind
./hivemind --help
```

No Actions também há artefatos `hivemind-linux-amd64`, `hivemind-darwin-amd64` e `hivemind-darwin-arm64`.

## Sem chave de API: Antigravity CLI

O padrão é o `agy` (Google Antigravity CLI) quando ele está no `PATH` e `GOOGLE_API_KEY` não está definida. A conta é a Google Pro com a qual você já entrou no `agy`. O HiveMind não pede `GOOGLE_API_KEY`.

No PowerShell, uma vez por máquina:

```powershell
agy
agy models
hivemind doctor
```

`agy` abre o login, se ainda não houver sessão. `agy models` lista os slugs. `hivemind doctor` diz se o executável foi encontrado (por `HIVEMIND_AGY_PATH`, pelo `PATH` ou em `%LOCALAPPDATA%\agy\bin\agy.exe`) e se `agy models` autenticou. `hivemind doctor --probe` manda `responda apenas OK` pelo mesmo caminho de uma execução real: texto UTF-8 no stdin (`--input-format text`) e `--json-schema` apontando para um arquivo temporário. O prompt nunca vai no argv, porque as aspas do Windows quebram `agy -p`. Nada disso imprime segredo nem a lista de modelos.

```powershell
cd C:\projetos\meu-app
hivemind team create --name padrao
hivemind run "adicionar um endpoint de healthcheck"
```

`team create` grava `provider: agy` e `model: agy` nesse caso. O modelo em si é o que o `agy` escolher. Para fixar um slug e o esforço:

```powershell
hivemind agent edit --name dev_1 --model "agy:gemini-3.8-flash-medium:high" --provider agy
```

O formato é `agy`, `agy:<slug>` ou `agy:<slug>:<effort>`, com effort `low`, `medium`, `high`, `xhigh` ou `max`. `AGY_MODEL` e `AGY_EFFORT` valem quando o agente fica só em `agy`.

O `agy` não edita o repositório. Cada chamada roda numa pasta temporária, sem `--dangerously-skip-permissions`. O prompt vai em UTF-8 pelo stdin (`--input-format text`), nunca em `-p`. O `--json-schema` é o caminho de um arquivo temporário com a decisão (`text` ou `tool_calls`). Quem grava arquivo, roda comando, fala com o git, o GitHub e o Jira é o HiveMind.

## Chaves e variáveis

Gemini, Vertex, Anthropic e OpenAI continuam disponíveis por agente. Sem `GOOGLE_API_KEY`, eles não são o padrão.

```powershell
# Opcional. Se existir, team create volta a sugerir Gemini.
$env:GOOGLE_API_KEY = "sua-chave"
# alternativa aceita: $env:GEMINI_API_KEY

# Vertex AI, em vez da API key
$env:GOOGLE_GENAI_USE_VERTEXAI = "true"
$env:GOOGLE_CLOUD_PROJECT = "seu-projeto"
$env:GOOGLE_CLOUD_LOCATION = "us-central1"

# Anthropic
$env:ANTHROPIC_API_KEY = "sua-chave"
# Claude no Vertex: $env:ANTHROPIC_BACKEND = "vertex"

# OpenAI, ou um endpoint compatível
$env:OPENAI_API_KEY = "sua-chave"
$env:OPENAI_BASE_URL = "https://api.openai.com/v1"

# Jira Cloud
$env:JIRA_BASE_URL = "https://sua-empresa.atlassian.net"
$env:JIRA_EMAIL = "voce@empresa.com"
$env:JIRA_API_TOKEN = "seu-token"

# GitHub. Se o token existir, a CLI usa a API REST.
# Sem token, usa o executável gh autenticado.
$env:GITHUB_TOKEN = "seu-token"
```

`hivemind doctor` diz se cada variável está definida, sem mostrar o valor.

## Configuração do time

Tudo fica em `.hivemind/team.yaml`, na pasta do projeto. Pode haver vários times; um deles é o ativo.

```powershell
cd C:\projetos\meu-app
hivemind team create --name padrao --devs 2
# com chave do Gemini, o mesmo comando usa gemini-flash-latest
# para forçar o modelo: --provider gemini --model gemini-flash-latest
hivemind team list
hivemind team show
hivemind team use padrao
```

Sem `--name` e com o terminal interativo, `hivemind team create` abre um assistente.

```powershell
hivemind agent add --role dev --name dev_back --model agy:gemini-3.8-flash-medium
hivemind agent add --role qa --name qa_api --model claude-sonnet-4-5 --provider anthropic
hivemind agent edit --name dev_back --model gemini-2.5-pro
hivemind agent remove --name dev_back
hivemind agent list
```

Papéis: `pm` (gerente), `tech_lead`, `architect`, `dev`, `qa`.

Um exemplo completo está em [examples/team.yaml](examples/team.yaml). Campos úteis em `settings`:

| Campo | Efeito |
| --- | --- |
| `jira_enabled` | Liga ou desliga as ferramentas de Jira e o comentário final |
| `auto_commit` | Commit em cada worktree ao terminar |
| `auto_pr` | `git push` e abertura do pull request |
| `max_qa_loops` | Máximo de voltas desenvolvedor/QA |
| `max_llm_calls` | Limite de chamadas de modelo. `0` usa o padrão do ADK (500). `-1` não limita |
| `base_branch` | Branch de destino do pull request |
| `shell_allowlist` | Executáveis que `run_shell` pode chamar |
| `confirm_shell` | Pede confirmação para comando destrutivo |

Comandos de shell fora da lista são recusados. `git push --force`, `git reset --hard`, `rm` e pipes pedem confirmação. Fora de um terminal a confirmação é negada, a menos que você passe `--yes`.

## Uso

Na pasta do projeto:

```powershell
# sessão interativa
hivemind

# uma execução, com a saída de cada agente
hivemind run "adicionar um endpoint de healthcheck"

# a issue vira o objetivo; a chave entra na branch, no commit e no PR
hivemind run --jira PROJ-123

# objetivo extra junto da issue, retomando uma sessão
hivemind run "não mude o contrato público" --jira PROJ-123 --session abc123def456

hivemind session list
hivemind session show abc123def456
hivemind session resume abc123def456

hivemind jira search "project = PROJ AND statusCategory != Done ORDER BY updated DESC"
hivemind jira show PROJ-123

hivemind git status
hivemind git diff
hivemind git branch
```

Dentro da sessão interativa: escreva o objetivo, `/jira PROJ-123`, `/time`, `/sessoes`, `/ajuda`, `/sair`.

A branch de cada desenvolvedor tem a forma `hivemind/proj-123/dev_1`. Sem issue, o trecho do meio é um resumo do objetivo. O commit automático usa `feat(PROJ-123): ...`.

As sessões ficam em `.hivemind/sessions/` e sobrevivem ao fechar o terminal. A pasta `.hivemind/` guarda configuração e worktrees; vale a pena ignorá-la no Git do seu projeto.

## Ferramentas dos agentes

Todas as leituras e gravações ficam presas ao diretório de trabalho do agente (o projeto, ou a worktree daquele desenvolvedor). Um link simbólico que aponte para fora é recusado, e `.git` não é lido nem escrito por essas ferramentas.

| Papel | Ferramentas |
| --- | --- |
| Gerente | ler, listar, `git_status`, Jira (ler, JQL, subtarefa, transição, comentário), comentários e CI do GitHub |
| Tech lead | ler, listar, status, diff, comentário no Jira |
| Arquiteto | ler, listar, gravar, status |
| Desenvolvedor | ler, listar, gravar, shell, git (status, diff, commit, push, pull), pull request e CI |
| QA | ler, listar, shell, diff, `qa_approve`, `qa_request_changes`, comentário no Jira |

`qa_approve` encerra o loop do ADK. `qa_request_changes` devolve o texto do ajuste aos desenvolvedores na volta seguinte.

## Testes e CI

```bash
go test ./...
```

Os testes não chamam Gemini, GitHub nem Jira de verdade. O modelo é um `model.LLM` scriptado, o Jira é um servidor HTTP falso, o GitHub REST também, e o git dos testes é um repositório temporário local. O shell é exercitado com `echo` e com recusa de comando perigoso.

O workflow [`.github/workflows/ci.yml`](.github/workflows/ci.yml) roda esses testes e compila os binários de Windows, Linux e macOS a cada push em `hivemind-cli-go`.

## Limitações do ADK Go (v2.8.0)

O que o ADK entrega e o que esta CLI completa:

- **Sequential, parallel e loop existem** em `google.golang.org/adk/v2/agent/workflowagents` e são o que o pipeline usa. No código Go 2.8.0 eles não emitem aviso de depreciação. A documentação do ADK 2.0 descreve grafos (`workflow`) como o estilo mais novo e marca sequential/parallel/loop como depreciados no TypeScript; no Go os três construtores continuam a API pública. Não há um predicado “até os testes passarem”: o `loopagent` para quando um evento vem com `Actions.Escalate` ou quando `MaxIterations` acaba. O QA liga `Escalate` em `qa_approve`.
- **Não há serviço de sessão em arquivo.** O ADK oferece memória, banco via GORM/SQLite e Vertex AI. Para o binário estático continuar sem CGO e a sessão poder ser retomada depois de fechar o terminal, `.hivemind/sessions/*.json` implementa `session.Service`.
- **Não há ferramentas de Git, GitHub ou Jira** no ADK. O CLI as implementa. GitHub usa a API REST quando `GITHUB_TOKEN` ou `GH_TOKEN` existe e, na ausência do token, o executável `gh`. Jira é só Jira Cloud (REST v3), com `JIRA_BASE_URL`, `JIRA_EMAIL` e `JIRA_API_TOKEN`.
- **Não há o conceito de time.** `.hivemind/team.yaml` é desta CLI. O ADK também não escolhe o modelo por agente sozinho: cada `llmagent` recebe o `model.LLM` que a fábrica cria (agy, Gemini, Vertex, Anthropic ou OpenAI). Outro provedor entra se falar a API compatível com OpenAI (`OPENAI_BASE_URL`). O provedor `openai-responses` usa a Responses API; o padrão `openai` usa Chat Completions.
- **O ADK não fala com o Antigravity CLI.** O provedor `agy` implementa `model.LLM` chamando `agy` em modo headless (`--output-format json`). As ferramentas nativas do agy ficam de fora: o processo usa um diretório temporário e não recebe `--dangerously-skip-permissions`. A decisão de ferramenta volta em `--json-schema` e o ADK executa as ferramentas do HiveMind. A resposta chega inteira; o agy nesse modo não entrega token por token. `--conversation` do agy não é usado, porque o histórico e o resultado das ferramentas ficam na sessão do HiveMind.
- **Confirmação de ferramenta (HITL) do ADK** pausa a execução à espera de um cliente que entenda `RequestedToolConfirmations` (o launcher e a UI web). No PowerShell isso não aparece sozinho. O `run_shell` confirma no próprio terminal e devolve erro ao modelo se você recusar.
- **Agentes paralelos não leem a conversa uns dos outros.** O ADK isola a branch de cada um. O plano compartilhado viaja no estado da sessão (`OutputKey`), que é o mecanismo do ADK para isso.
- **Worktree exige um commit.** Sem repositório ou sem commit, os desenvolvedores compartilham a pasta do projeto e a CLI avisa. Escrita paralela no mesmo arquivo pode colidir.
- **Streaming** depende do provedor. Com `StreamingModeSSE` a CLI imprime os eventos parciais que o runner entrega. O teste usa o modo sem streaming para o modelo simulado.
- **Não há teto de concorrência.** Cada desenvolvedor é uma goroutine, como o `parallelagent` do ADK faz. Muitos desenvolvedores significam muitas chamadas de modelo ao mesmo tempo. A saída de ferramenta é truncada para a sessão não crescer sem limite, e só a sessão ativa fica na memória.
- O nome de agente `user` é reservado pelo ADK. A CLI reescreve nomes para identificadores (`dev-back` vira `dev_back`).

## Layout

```
cmd/hivemind            comando principal
internal/cli            comandos em português
internal/orchestrator   sequential + parallel + loop do ADK
internal/tools          arquivos, shell, git, GitHub, Jira
internal/modelx         agy, Gemini, Vertex, Anthropic, OpenAI e o modelo dos testes
internal/sessionx       sessões em JSON
internal/team           .hivemind/team.yaml
internal/gitx           git
internal/githubx        API REST e gh
internal/jirax          Jira Cloud
internal/shellx         lista de comandos e confirmação
internal/workspace      a escrita não sai da pasta do agente
```
