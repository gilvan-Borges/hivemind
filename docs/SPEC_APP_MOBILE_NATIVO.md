# Spec — HiveMind Mobile Nativo (Flutter Android + Remote Bridge Go)

**Status:** Proposta pronta para execução  
**Ambiente verificado:** Flutter `3.44.6` · Dart `3.12.2` · Android SDK (`adb`) · Tailscale instalado · Go `1.26`

---

## 1. Objetivo

Permitir **continuar e comandar os trabalhos do HiveMind diretamente pelo celular (App Nativo Android em Flutter)**:

- Acompanhar equipes rodando em tempo real (streaming de texto, ferramentas, estágios `Chefe → Dev → QA → Fecho` e tokens).
- **Continuar exatamente a mesma sessão/conversa** iniciada no PC — e vice-versa (o que você faz no celular atualiza o chat da IDE no PC em tempo real).
- Responder **Approval Gates** pelo celular (aprovar plano, gates SDD, commit/merge em `main`, comandos de shell).
- Enviar novas tarefas, **Redirecionar** ou **Parar** equipes em andamento, inclusive anexando fotos/prints.
- Ver **Diffs**, **Fluxo de PRs / Auto-Review (Sem Merge)** e **Minhas Tasks do Jira** (`assignee = currentUser()`) com botão 1-toque **Resolver c/ Equipe**.

---

## 2. Arquitetura (PC Host ↔ App Nativo)

```
┌─────────────────────────────────┐        HTTPS / HTTP + SSE (Bearer Token)        ┌───────────────────────────────────────┐
│  App Flutter Nativo (Android)   │ <=============================================> │  HiveMind IDE (PC Host - Go)          │
│  mobile/                        │        Tailscale (100.x.x.x) ou Wi-Fi LAN       │  ide/internal/remote/                 │
│                                 │                                                 │                                       │
│  • Pareamento via QR Code       │   GET  /api/v1/projects, /sessions, /lanes      │  • Reutiliza idecore.SessionManager   │
│  • Projetos & Lanes ao vivo     │   POST /api/v1/runs/{start,cancel,redirect}     │  • Reutiliza AutoReviewManager        │
│  • Chat + Approval Gates        │   POST /api/v1/approvals/resolve                │  • Espelha hivemind:lane-event (SSE)  │
│  • Minhas Tasks Jira & Fluxo    │   GET  /api/v1/git/{diffs,prs,issues}           │  • Espelha hivemind:autoreview-status │
│  • Visualizador de Diffs        │   GET  /api/v1/events  (SSE stream)             │  • Mantém snapshot vivo de lanes      │
└─────────────────────────────────┘                                                 └───────────────────────────────────────┘
```

### Por que essa arquitetura?
1. **Zero duplicação do motor:** repositórios Git, worktrees, compiladores, `agy`, OpenCode, GitHub e credenciais ficam **100% no seu PC** — nenhum segredo de repo/API é copiado para o celular.
2. **Tailscale já instalado no seu PC:** o Bridge detecta automaticamente seu IP privado **Tailscale (`100.x.x.x`)** e o IP **Wi-Fi LAN** — assim o app funciona tanto no Wi-Fi de casa quanto no 4G/5G da rua, sem abrir portas no roteador e com criptografia WireGuard ponta a ponta.
3. **SSE (Server-Sent Events) para tempo real:** nativo na biblioteca padrão Go (`net/http`), reconexão automática super estável em trocas 4G ↔ Wi-Fi e consumo mínimo de bateria.

---

## 3. Segurança & Pareamento

- **Desligado por padrão** ou ativável por 1 clique na barra inferior da IDE (`mobile: off` / `mobile: on`).
- **Token forte de 256 bits** (`crypto/rand`, 32 bytes hex) salvo fora de qualquer repositório Git em `%LOCALAPPDATA%\HiveMind\remote-bridge.json` (permissão `0600`).
- **QR Code na IDE:** exibe payload JSON compactado (`hivemind://pair?urls=http://100.x.x.x:47890,http://192.168.x.x:47890&token=...&host=...`) — basta apontar a câmera no app Flutter para conectar em 2 segundos.
- **Fallback multi-endpoint:** o app salva ambas as URLs (Tailscale + LAN) e escolhe automaticamente a que responder primeiro.
- **Revogação instantânea:** botão **"Rotacionar Token / Desconectar Celulares"** no modal da IDE invalida imediatamente todas as conexões ativas.
- Todas as rotas `/api/v1/*` (exceto `GET /api/v1/health`) exigem `Authorization: Bearer <token>` (comparação em tempo constante `subtle.ConstantTimeCompare`).

---

## 4. Sincronização Bidirecional (Continuar Trabalhos)

Hoje, o estado da conversa em andamento fica no frontend da IDE enquanto o histórico gravado fica em `.hivemind/sessions/*.json`. Para o celular **continuar exatamente de onde o PC parou** (e vice-versa):

1. **`remote.Hub` em Go (`ide/internal/remote/hub.go`):**
   - Intercepta todo `idecore.LaneEvent` emitido pelo `SessionManager` (além de repassar para o Wails frontend no PC).
   - Mantém um buffer vivo por `(workspace, laneId)` com:
     - `running`, `stage`, `teamName`, `sessionId`, `turnTokens`, `startedAt`
     - Últimos eventos do turno ativo (texto, chamadas de ferramenta, handoffs)
     - **Aprovações pendentes** (`approvalId`, `prompt`, `createdAt`) — resolvidas no celular ou no PC desaparecem instantaneamente nos dois lados!
   - Quando o celular envia `POST /api/v1/runs/start`, o backend também emite um evento `user_message` no `SessionManager` para que o chat aberto na IDE do PC exiba a mensagem enviada pelo celular em tempo real.
   - Para conversas já encerradas ou retomadas, `GET /api/v1/projects/session?workspace=...&id=...` carrega o histórico salvo em `.hivemind/sessions/*.json`, permitindo continuar a mesma `sessionId` de qualquer dispositivo.

---

## 5. Contrato da API HTTP + SSE (`/api/v1`)

| Método | Rota | Descrição |
|---|---|---|
| `GET` | `/api/v1/health` | Status mínimo do host (`ok`, `hostName`, `version`, `time`) |
| `GET` | `/api/v1/projects` | Projetos abertos/recentes com branch, stack, equipe ativa, contagem de lanes rodando e aprovações pendentes |
| `GET` | `/api/v1/lanes?workspace=...` | Estado vivo das lanes + aprovações pendentes |
| `GET` | `/api/v1/sessions?workspace=...` | Lista sessões salvas + consumo total de tokens |
| `GET` | `/api/v1/session?workspace=...&id=...` | Carrega mensagens de uma sessão salva para continuar |
| `POST` | `/api/v1/runs/start` | Inicia/continua execução (`laneId`, `workspace`, `team`, `objective`, `jiraKey`, `sessionId`, `runMode`, `images`) |
| `POST` | `/api/v1/runs/cancel` | Para execução ativa na lane (`laneId`) |
| `POST` | `/api/v1/runs/redirect` | Cancela execução ativa e já dispara nova instrução na mesma lane |
| `POST` | `/api/v1/approvals/resolve` | Aprova ou recusa um gate pendente (`approvalId`, `approved`) |
| `GET` | `/api/v1/git/diffs?workspace=...` | Lista arquivos alterados e hunks de diff |
| `GET` | `/api/v1/git/prs?workspace=...` | Lista PRs abertos + status de revisão (Fluxo) |
| `GET` | `/api/v1/git/issues?workspace=...` | Lista **suas tasks do Jira** (`assignee = currentUser()`) + issues GitHub |
| `GET` | `/api/v1/autoreview/status?workspace=...` | Status do Auto-Review do projeto |
| `POST` | `/api/v1/autoreview/poll` | Dispara verificação imediata do Auto-Review |
| `GET` | `/api/v1/events` | Stream SSE em tempo real (`lane_event`, `autoreview_status`, `approval_resolved`, `ping`) |

---

## 6. Design do App Nativo Flutter (`mobile/`)

- **Identidade visual:** mesma linguagem **Obsidian Graphite × Silver + Steel Blue** da HiveMind IDE (`#0B0D10` fundo, `#12151B` cards, `#1A1F28` superfícies elevadas, `#4C8DFF` acento primário, `#34D399` sucesso, `#F87171` erro/parar, `#FBBF24` aprovação pendente — zero roxo).
- **Navegação inferior limpa (4 abas):**
  1. **Projetos:** cards com branch, equipes ativas, badge pulsante de aprovações pendentes e atalho para trocar de projeto.
  2. **Chat (Orquestrador):** seletor de lane + histórico de sessões, streaming ao vivo, **cards de Aprovação no topo**, barra de modos (`Auto`, `Direto`, `Planejar`, `Squad`), anexo de imagem, botões **Parar** e **Redirecionar**.
  3. **Fluxo & Jira:** sub-abas **Minhas Tasks Jira** (com filtro por status, busca e botão **Resolver c/ Equipe**), **Fluxo de PRs** e card de controle do **Auto-Review (Sem Merge)**.
  4. **Diffs:** lista de arquivos modificados com `+add / -rem` e visualizador colorido de diff legível em tela mobile.
- **Tela de Pareamento:** scanner de QR Code nativo (`mobile_scanner`) + modo manual (URL + Token) + indicador de latência e rota ativa (`Tailscale` vs `Wi-Fi`).

---

## 7. Marcos de Entrega

### Marco 1 — Remote Bridge Go + Pareamento QR Code na IDE
- Pacote `ide/internal/remote/` (servidor HTTP+SSE, auth Bearer, `Hub` de snapshot vivo + histórico, detecção automática de IP Tailscale + LAN, persistência do token em `%LOCALAPPDATA%\HiveMind\remote-bridge.json`).
- Integração em `ide/app.go` + chip `mobile: on/off` na status bar + modal **App Mobile (QR Code)** com liga/desliga, QR Code SVG nativo, lista de URLs (Tailscale/LAN) e botão revogar token.
- Testes unitários Go cobrindo auth, SSE streaming, start/cancel/redirect, resolução de aprovações e isolamento de workspaces.

### Marco 2 — App Flutter Nativo (`mobile/`) + APK Android
- Projeto Flutter em `c:\hivemind-go\mobile\` com arquitetura em camadas (`core/api`, `core/models`, `features/pairing`, `features/projects`, `features/chat`, `features/flow`, `features/diffs`).
- Compilação e validação de `flutter analyze`, `flutter test` e `flutter build apk --release` gerando `dist/HiveMindMobile.apk` (e instalação direta via `adb install` se o celular estiver conectado no USB!).

### Marco 3 — Modo Daemon Headless (`hivemind serve`) & Notificações
- Subcomando `hivemind serve` na CLI para manter o Bridge rodando leve em segundo plano no PC sem precisar deixar a janela gráfica da IDE aberta.
- Notificações locais no Android ao receber aprovação pendente ou término de equipe + reconexão resiliente em background.
