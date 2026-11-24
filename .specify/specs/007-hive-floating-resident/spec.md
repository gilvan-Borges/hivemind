# Spec — Agent Hive (Agente Residente Flutuante na Área de Trabalho)

> Status: Implementado (Onda 4 — W4-03 / W4-04)
> Identidade unificada: **Agent Hive** (mesmo cérebro, sessão e histórico do agente Residente dentro da IDE e flutuando fora dela)
> Alvos: [x] HiveMind IDE (`ide/`) · [x] HiveMind Orb (`ide/frontend/src/orb/`) · [ ] HiveMind-App (paridade semântica de estado mantida)
> Regras aplicáveis: D1–D8 (`.specify/memory/constitution.md`)

---

## 1. Contexto e Problema

Hoje existem duas experiências separadas para o mesmo conceito:
1. Dentro da HiveMind IDE (`ide/frontend/src/resident.ts`), existe o **Agent Hive** (aba `Hive Residente`), com conversa contínua, workstreams (`geral`, etc.), roteamento de modelo (`cursor/cursor:auto`, `openrouter/...`), leitura auditada do workspace, execução em sandbox com aprovação (`approval_required`), memória pessoal e trilha de auditoria.
2. Fora da IDE, na janela flutuante transparente (`ide/orb_window.go` + `ide/frontend/src/orb/view.ts`), o mascote (**HiveBee** / **HiveStriker**) hoje exibe apenas **avisos passivos** e botões de abrir a IDE/trocar visual. Não dá para mandar um comando ou conversar com o **Agent Hive** direto dali sem abrir a janela completa da IDE.

**Objetivo:** Unificar os dois em uma única entidade chamada **Agent Hive**:
- O agente flutuante na área de trabalho passa a se chamar **Agent Hive** e representa diretamente o **Agent Hive** (Residente) da IDE.
- Ao clicar no mascote flutuante, abre-se o **chat e barra de comando do Agent Hive** fora da IDE (mesmo com a janela principal da IDE fechada na bandeja do sistema).
- Toda mensagem enviada pelo **Agent Hive flutuante**, resposta em streaming, chamada de ferramenta e **cartão de aprovação de risco** usa exatamente a mesma instância `idecore.ResidentManager`, o mesmo `workstream`, o mesmo workspace ativo e o mesmo histórico de conversa (`ResidentSessionMessages`) do **Agent Hive** dentro da IDE — abrir a IDE depois mostra exatamente a mesma conversa em tempo real (e vice-versa).

---

## 2. Escopo e Não-Escopo

### Inclui
- **Identidade unificada:** Nomear o agente e o título/cabeçalho tanto do painel flutuante quanto da aba residente como **Agent Hive** (ex.: `Agent Hive · <workspace>` + modelo/rota ativa e chip de estado `ocioso` / `pensando` / `aguardando você`).
- **Chat bidirecional direto no Orb flutuante (aba padrão ao clicar no mascote):**
  - Timeline compacta da conversa do **Agent Hive** (mensagens do usuário, respostas em markdown/texto do Agent Hive e chips de ferramentas executadas com nível de risco).
  - **Barra fixa de aprovação pendente logo acima do campo de chat (`composer`)**: sempre que houver uma solicitação de aprovação (`approval_required`), os botões **Aprovar / Recusar** + resumo e selo de risco ficam ancorados imediatamente acima da caixa de texto, sem depender de rolagem do histórico.
  - Composer no rodapé para digitar um comando/pergunta (`Enter` envia, `Shift+Enter` quebra linha, botão `Enviar` e botão `Parar` quando `running === true`).
  - Seletor rápido de **workstream** (`geral` ou ativo) no cabeçalho/sublinha ou leitura sincronizada com `ResidentStatus()`.
- **Alternância limpa com Avisos e Configuração do Mascote:**
  - Navegação em 3 modos compactos dentro do painel flutuante **Agent Hive**:
    1. **Conversa** (padrão ao clicar no mascote — falar com o Agent Hive direto da área de trabalho).
    2. **Avisos (`N`)** (feed atual de notificações de GitHub PRs, Equipe IA e Rotinas do Agent Hive, com badge numérico).
    3. **Visual / Agente** (escolher mascote `HiveBee` / `HiveStriker`, paleta/tema e visor tático).
- **Sincronização em tempo real IDE <-> Agent Hive flutuante:**
  - Ponte estendida em `ide/internal/orb/hub.go` + `ide/orb_host.go` para espelhar `ResidentState` (mensagens recentes da sessão, `running`, status da sandbox/rota, workstream ativo, eventos parciais `text` streaming, `tool_call`, `tool_result`, `approval_required`, `approval_resolved`, `error`, `finish`).
  - Ações no `OrbAction`: `resident_send`, `resident_cancel`, `resident_approve`, `resident_clear`, `open_resident` (para abrir a IDE já na aba do Agent Hive).
- **Respeito estrito à segurança do Residente:**
  - Comandos enviados pelo Agent Hive flutuante continuam passando 100% pelo mesmo gate de sandbox (`appcontainer+jobobject` no Windows), allowlist de binários/hosts e cartões de aprovação humana (`RespondApproval`). Nenhum atalho sem sandbox é criado.

### Não inclui (neste ciclo)
- Edição avançada de Políticas, Cofre de Segredos do SO, Memória Pessoal completa e Auditoria de 80 linhas dentro da janelinha flutuante — esses permanecem na aba **Agent Hive** dentro da IDE (com botão rápido `"Abrir IDE"` no cabeçalho do Agent Hive flutuante que abre a IDE já focada na aba `hive:resident`).
- Alterações no `HiveMind-App` Android (o app mobile já conversa com a IDE via Bridge/Relay).

---

## 3. Fluxo de Interação do Usuário

1. **IDE fechada no `X` (rodando na bandeja) ou minimizada:** o mascote **Agent Hive** continua flutuando sem fundo na área de trabalho.
2. **Usuário clica no mascote Agent Hive:**
   - O painel flutuante expande (`~396×540 px`, ancorado sem sair da tela) diretamente na view **Conversa com o Agent Hive**.
   - Se já houver conversa ativa na aba **Agent Hive** (`hive:resident`) da IDE, ela aparece imediatamente ali.
3. **Usuário digita um comando** (ex.: `"lista os últimos commits e vê o que falhou no build"`) e aperta `Enter`:
   - O `OrbApp` envia `resident_send` via bridge HTTP loopback para o processo principal da IDE.
   - O backend executa `a.resident.RunTurn(..., workstream, message, resident.OriginLocal)` usando o workspace ativo.
   - O mascote entra em estado `working` enquanto o Agent Hive responde em tempo real (streaming) no chat flutuante **e** atualiza o chat **Agent Hive** dentro da IDE simultaneamente.
4. **Se o Agent Hive precisar de aprovação (`write` / `exec` / `network`):**
   - O cartão de aprovação aparece **dentro do próprio chat do Agent Hive flutuante** com selo de risco (`Risco Médio · Escrita`, `Risco Alto · Execução em Sandbox`, etc.) + botões **Aprovar** e **Recusar**.
   - O mascote entra em estado `notify`/`attention`, emite o alarme/pulso redondo configurado e aguarda a decisão sem obrigar abrir a IDE.
5. **Se o usuário clicar em "Abrir IDE" no Agent Hive flutuante:**
   - A janela principal da IDE vem para frente já abrindo/focando a aba **Agent Hive** (`hive:resident`) com toda a conversa sincronizada.
6. **Clique fora da caixa** (depois que o painel esteve em primeiro plano) recolhe o Agent Hive ao mascote.
7. **Arrastar a borda direita, a base ou o canto** muda a largura e a altura. O próximo abrir usa esse tamanho, com mínimo 320×420.
8. **Notificação do sistema** só quando a janela da IDE está oculta ou minimizada. Com a IDE aberta na tela, não há toast nem balão.

---

## 4. Camadas e Tokens Utilizados (D1 / D2)

| Elemento | Camada (0–4) | Token CSS (IDE & Orb) | Papel |
|---|---|---|---|
| Casca do painel flutuante (`orb-panel`) | 1 | `--surface-panel` (`#0C0C0C`) | Estrutura do widget expandido |
| Cabeçalho (`orb-head`) e rodapé (`orb-foot`) | 2 | `--surface-card` (`#111111`) | Barra superior e barra de modos |
| Balões de resposta do Agent Hive / cartões de tool / aprovação | 2 | `--surface-card` (`#111111`) | Cartões sólidos da timeline |
| Balão do usuário / destaque de ação | 3 | `--surface-elevated` (`#1F1F1F`) | Elevação sutil da fala do usuário |
| Campo de comando (`textarea`) do Agent Hive | 4 | `--surface-input` (`#0A0A0A`) | Input com foco `--accent-strong` |
| Botão primário (`Enviar` / `Abrir IDE` / `Aprovar`) | — | `--action-primary` (`#F3F2F0`) / `--action-primary-text` (`#090909`) | Ação principal neutra |
| Selo ativo / foco de teclado / badge do Agent Hive | — | `--accent` (`#C6A15B`) / `--accent-strong` (`#E8C881`) | Marca Havrus Gold |
| Erro / Parar / Recusar | — | `--color-danger` / `--color-danger-border` | Semântica de erro/parada |
| Sucesso / Leitura / Sandbox ativa | — | `--color-success` / `--color-success-border` | Estado seguro |

---

## 5. Estados Obrigatórios (D5)

- **Carregando:** Ao expandir o painel pela primeira vez antes do snapshot inicial chegar, exibir skeleton estável na área do chat e manter o composer desabilitado com placeholder `"Conectando ao Agent Hive..."`. Quando `running === true`, exibir indicador discreto `"Agent Hive pensando..."`, manter input liberado para visualização e trocar botão `Enviar` por `Parar`.
- **Erro:** Falha de envio, erro de execução do modelo ou perda de conexão com o processo principal exibe cartão de erro legível (`--color-danger-border`) com ação `"Tentar novamente"` ou `"Abrir IDE"`.
- **Vazio:** Quando a conversa do workstream atual ainda não tem mensagens, exibir estado inicial limpo com ícone vetorial (`icons.sparkle`), título `"Agent Hive pronto"`, subtítulo explicando o workspace/rota ativos e 2–3 chips clicáveis de atalho rápido (ex.: `"Resumo do projeto"`, `"Status do Git"`, `"Últimos PRs"`).
- **Dados:** Timeline com scroll automático suave para a última mensagem, diferenciando visualmente usuário, resposta do Agent Hive (Markdown leve), tool calls com risco e aprovações pendentes/resolvidas; métricas/horários com `tabular-nums`.

---

## 6. Pares de Contraste (D3 — >= 4.5:1)

| Texto / Sinal | Fundo | Razão mínima esperada |
|---|---|---|
| `--text-primary` (`#F3F2F0`) | `--surface-card` (`#111111`) / `--surface-input` (`#0A0A0A`) | >= 15:1 |
| `--text-secondary` (`#A1A1AA`) | `--surface-card` (`#111111`) / `--surface-panel` (`#0C0C0C`) | >= 6.8:1 |
| `--action-primary-text` (`#090909`) | `--action-primary` (`#F3F2F0`) | >= 17:1 |
| `--accent-strong` (`#E8C881`) | `--surface-card` (`#111111`) | >= 10:1 |

---

## 7. Critérios de Aceite

- [ ] Clicar no mascote flutuante abre o painel **Agent Hive** já pronto para conversar/comandar sem precisar abrir a janela principal da IDE.
- [ ] Enviar um comando pelo Agent Hive flutuante executa no `ResidentManager` do processo principal e atualiza a conversa em tempo real tanto no flutuante quanto na aba **Agent Hive** (`hive:resident`) dentro da IDE (e vice-versa).
- [ ] Pedidos de aprovação (`write` / `exec` / `network`) aparecem no chat flutuante com nível de risco e botões **Aprovar / Recusar** funcionais, disparando aviso/alarme no mascote.
- [ ] A janela flutuante pode alternar entre **Conversa**, **Avisos** e **Agente (Visual)**, e o botão **Abrir IDE** traz a IDE já na aba **Agent Hive**.
- [ ] Funciona integralmente com a IDE fechada no `X` (rodando em segundo plano na bandeja).
- [ ] Zero cores `#hex` literais fora de `:root` em `ide/frontend/src/styles.css`.
- [ ] `node ide/frontend/scripts/check-design-tokens.mjs`, `npm test`, `npm run build`, `go test ./internal/orb/... ./internal/idecore/...` e `hivemind-ide.exe --smoke-check` sem falhas.
