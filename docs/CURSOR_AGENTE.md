# Provedor Cursor (agente CLI)

A Cursor não publica uma API de chat completions. A chave do painel (`crsr_…`, em Cursor Dashboard → API Keys) autentica o CLI oficial e a Cloud Agents API, que executam um agente inteiro. O HiveMind usa só o CLI oficial, no modo headless documentado.

Não dá para tratar essa chave como OpenRouter ou OpenCode. Aqueles falam HTTP de modelo. Este provedor inicia `agent` na máquina, no workspace daquele agente (a worktree do desenvolvedor, ou a pasta do projeto para o Chefe).

## Como ligar

1. Instale o CLI.
   - Windows (PowerShell): `irm 'https://cursor.com/install?win32=true' | iex`
   - Linux e macOS: `curl https://cursor.com/install -fsS | bash`
   - Confira com `agent --version`. Se o executável não estiver no `PATH`, defina `HIVEMIND_CURSOR_AGENT_PATH`.
2. Na IDE, abra **Chaves de API** e cole `CURSOR_API_KEY`. O arquivo é `api-keys.json` na pasta de config do usuário (`%APPDATA%\HiveMind` no Windows), fora do Git. Se a variável de ambiente existir, ela ganha da chave salva. `hivemind doctor` só diz se a chave está definida, sem mostrar o valor.
3. No seletor de modelo, escolha **Cursor (agente CLI)**. O padrão é `auto`: o CLI escolhe o modelo da conta. `agent models` preenche o restante da lista quando a chave e o CLI existem.

O rótulo deixa claro que isto é um agente, não um modelo cru. Ele grava arquivos nesse workspace. Commit, push e PR seguem a governança do time: Chefe/Arquiteto podem; Dev e QA não.

## O que o processo faz

```text
agent --model <id> -p --output-format stream-json --stream-partial-output --sandbox enabled --workspace <pasta> "…"
```

`auto` omite `--model`. `--trust` e `--force` só entram quando o modo de permissão do time é `full_access`. Nos outros modos o CLI headless recusa o que exigiria aprovação. A sandbox do CLI permanece ligada onde o binário a suporta (macOS e Linux). No Windows o CLI não tem sandbox (`--sandbox disabled`, senão o processo aborta) e a IDE mostra esse aviso junto da escolha de permissão: sem `full_access`, edição que pediria aprovação é recusada. A chave vai no ambiente do processo, nunca nos argumentos. O pedido longo fica em `.hivemind/cursor-task-<id>.md` (a pasta já está no `.gitignore`), um arquivo por turno para lanes paralelas não apagarem o pedido uma da outra, e é apagado ao terminar. Uma imagem anexada no chat é gravada em `.hivemind/cursor-images/` e o caminho entra no pedido, para o CLI poder lê-la. Esses arquivos também são apagados ao terminar.

O texto e o nome das ferramentas (sem o corpo dos arquivos) entram no mesmo fluxo de eventos da execução. A IDE e o app mobile, pelo Remote Bridge, mostram esses eventos. O modo headless não consegue parar para perguntar: o que exigiria aprovação é recusado pelo CLI.

## Limitações

- Sem o CLI instalado, o provedor não sobe. A mensagem traz o comando de instalação.
- A chave de admin do Grok Bot (ligar o bot, rede, auto-review) não serve como modelo. A chave precisa ser uma user API key aceita pelo CLI.
- O Chefe, o dev e o QA, se usarem este provedor, cada um dispara o próprio agente Cursor no diretório daquele papel. Não é o loop de ferramentas do HiveMind por baixo.
- Não há endpoint oficial para listar modelos fora do CLI. Se `agent models` falhar, fica só `auto`.
