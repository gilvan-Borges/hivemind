# Spec 12 — Motor de Decisão de Skills, Mente Coletiva do Projeto (Graphify), Agent Hive Conectado e Kits Mobile & GameDev

> **Status:** Proposta Aprovada (Planejada — **Ondas 7 a 10**)
> **Alvos:** `internal/skillrouter/`, `internal/skillx/`, `internal/brain/`, `internal/resident/`, `internal/mcpx/`, `internal/modelx/`, `internal/jev/`, `internal/orchestrator/`, `internal/kits/`, `ide/internal/idecore/`, `ide/frontend/src/`

---

## 1. Contexto, Visão e Decisões Alinhadas

Com as Ondas 1–6 concluídas, o HiveMind já possui núcleo seguro (`pathsec`, `sandboxx`, `secretx`, KV-cache estável e Jev router), orquestrador modular (`runner`/`triage`/`pipeline`/`delivery`), *Hybrid Skill Stubs*, Agent Hive (Residente) e IDE reativa. O próximo salto transforma a forma como **skills**, **ferramentas externas**, **memória compartilhada** e **pilhas especializadas** são selecionadas e operadas sem inflar o prompt nem sacrificar segurança:

1. **Bloqueio de Rede do Residente Configurável (`strictNetworkGuard`):** adicionar configuração explícita no Residente para controlar a filtragem por `AllowedHosts` no `netguard`. **Valor padrão obrigatório: `false`** (sem bloqueio de domínios públicos por padrão). Mesmo com `strictNetworkGuard: false`, continuam sempre ativos os invariantes críticos de segurança: `--obey-robots` no `obscura`, **zero acesso** ao cofre `secretx` em tools web/browser, encapsulamento obrigatório de toda resposta externa em `WrapUntrustedData` (anti-prompt-injection), proteção contra SSRF em IPs privados/loopback/metadata, e exclusão mútua `obscura` vs `playwright`/`chrome-devtools` na mesma task.
2. **Mente Coletiva do Projeto com Graphify Oficial Primeiro:** testar **`graphify-labs/graphify`** (`graphifyy[mcp]`) como motor estrutural padrão atrás de uma interface interna `ProjectBrain`, instrumentando latência, uso de RAM e tempo de indexação. Se em uso real o processo Python/Graphify ficar lento ou pesado além dos gatilhos objetivos definidos na §2.3, chavear para um motor nativo em **Go + SQLite** sob o mesmo contrato.
3. **Armazenamento Local Agora, Sync via Git no Futuro:** na v1, a Mente do Projeto vive **exclusivamente local na máquina**, isolada por projeto em `%LOCALAPPDATA%\HiveMind\brains\<project-hash>\` (zero ruído no `git status`). Fica prevista em arquitetura a **Atualização Futura (pós-Onda 10)** de snapshot/sync opcional em `.hivemind/brain/` versionado no Git para compartilhamento entre membros do time.
4. **Automação do Motor de Decisão Regida pela Permissão Ativa:**
   - Modo **`full_access` (Permissão Total):** o Motor de Decisão aplica automaticamente as skills primárias + toolkit escolhidos, exibindo apenas um chip discreto no composer para trocar ou remover com 1 clique.
   - Modos **`governed` / `auto_edit`:** o Motor **sugere** o plano de skills/kit antes de iniciar e pede confirmação rápida com 1 clique (exceto quando o usuário invocar diretamente um slash command explícito como `/layout`, `/sec` ou `/game`, que vale como autorização imediata).
5. **Stacks Prioritárias — Mobile & Games Grátis:**
   - **Mobile:** prioridade total em **Android com Kotlin & Jetpack Compose** (reaproveitando padrões já validados no companion `HiveMind-App`: Gradle, ADB, emulador, `lintDebug`/`testDebugUnitTest`, hierarquia de UI/screenshots).
   - **GameDev (Gratuitas & Open-Source):** kit cobrindo **Godot** (`Coding-Solo/godot-mcp`), **Unreal Engine** (`Natfii/UnrealClaude`), engines Web gratuitas (**Phaser / Three.js / PixiJS / Bevy-WASM**), estrutura de estúdio multi-agente (**`Donchitos/Claude-Code-Game-Studios`**) e geração local de áudio/trilhas musicais (**`ace-step/ACE-Step-1.5`** / `acestep.cpp`, licença MIT, rodando abaixo de 4 GB de VRAM).
6. **Segurança da Cadeia de Skills & Fontes de Dados/Provedores:**
   - Gate automático antes de instalar/ativar skills externas combinando **scanner estático nativo em Go** e ponte opcional para **`NVIDIA/SkillSpector`** (`SKILL.md` + scripts + `OSV.dev`).
   - Navegação web leve do Agent Hive via **`h4ckf0r0day/obscura`** + canais públicos read-only inspirados no **`Agent Reach`** (Markdown reader, transcrição YouTube, RSS), **sem** extração de cookies do navegador pessoal do usuário.
   - Expansão **Jev Free-Tier** (Groq, Cerebras, NVIDIA NIM, Mistral + endpoint OpenAI-compatible customizado) absorvendo o conceito do `FreeLLMAPI` diretamente no `internal/modelx` + `internal/jev` sem subir um proxy externo redundante.

---

## 2. Arquitetura Técnica por Pilar

### 2.1 Motor de Decisão (`internal/skillrouter/`) & Matriz `Domínio × Intenção × Plataforma`

Em vez de depender da boa vontade do usuário lembrar nomes de skills ou injetar dezenas de guias no prompt (destruindo o KV-cache da Spec 02), a triagem passa por um roteador determinístico + semântico leve:

#### A. Perfil do Projeto (`.hivemind/project-profile.yaml`)
Detectado na primeira abertura (`OpenProject` / CLI) e editável pelo usuário na IDE:
```yaml
version: 1
domain: finance_erp        # saas_product | finance_erp | services | sales_ecommerce | marketing_landing | devtools | game | custom
platform: web              # web | android_kotlin | backend_edge | game_godot | game_unreal | game_web | multi
frameworks: [go, wails, typescript]
design_direction: precision_ui
default_toolkit: web_preview
```

#### B. Classificador de Dois Eixos (Intenção × Domínio/Plataforma)
1. **Passo 1 (< 1 ms, heurística local Go):** detecta sinais lexicais no texto do usuário (ex.: *"landing page"*, *"hero"*, *"conversão"* → `marketing_landing`; *"checkout"*, *"carrinho"*, *"catálogo"* → `sales_ecommerce`; *"fluxo de caixa"*, *"conciliação"*, *"tabela"* → `finance_erp`; *"agendamento"*, *"OS"*, *"atendimento"* → `services`; *"auth"*, *"webhook"*, *"CSRF"*, *"Cloudflare"* → `security`) + extensões dos arquivos citados via `@arquivo`.
2. **Passo 2 (opcional quando ambíguo):** micro-classificação via rota ultra-barata do Jev com JSON estrito (sem carregar código no contexto), retornando `{domain, intent, confidence}`.
3. **Orçamento estrito de KV-cache:** o roteador seleciona **no máximo 2 skills primárias + 1 toolkit** para o turno, entregues via *Hybrid Skill Stubs* (`hivemind skills get <slug>`) ou cauda do turno sem mutar o prefixo estático do sistema.

#### C. Matriz de Seleção de Skills (Design, Arquitetura, Segurança & Plataforma)

| Domínio / Cenário | Intenção | Skill Nativa Selecionada | Referência / Estratégia Aplicada | Kit / MCP Associado |
|---|---|---|---|---|
| `saas_product`, `finance_erp`, `services`, `devtools` | Criar / evoluir UI | **`ui-craft-product`** | Rigor tipográfico, tabelas tabulares, espaçamento denso, micro-interações e estados async claros (inspirado em `emilkowalski/skills` + Impeccable — MIT) | `obscura` ou `playwright` / Design Mode CDP |
| `marketing_landing`, `sales_ecommerce`, vitrine institucional | Criar página / vitrine | **`ui-art-direction`** | Direção de arte autoral, hierarquia editorial, ritmo visual de conversão anti-template genérico (inspirado em `Leonxlnx/taste-skill`) | `figma-context` + captura visual `obscura` |
| Qualquer domínio web/app | Redesign / auditoria visual | **`ui-redesign-audit`** | Caça a "cara de IA genérica" (gradientes excessivos, cards repetitivos, espaçamento quebrado) e refatoração progressiva em passos (inspirado em `heydamir/unslop` — CC BY 4.0) | Design Mode CDP + diff visual |
| Todos os domínios | Segurança / Auth / API / Edge | **`security-cloudflare`** | Threat modeling OWASP/API, validação de headers, HMAC webhooks, Zero Trust/Workers edge hardening e checagens `pathsec`/`secretx` | Scanner SAST / checklist AppSec no QA |
| Todos os domínios | Arquitetura / Refactor | **`arch-clean`** | Fatiamento modular <= 600L, fronteiras de pacote, remoção de ciclos e contratos tipados | `ProjectBrain` (Graphify) blast radius |
| Todos os domínios | Code Review / PR | **`code-review-rigor`** | Análise de regressão, concorrência, contratos e evidência obrigatória em `qa_approve` | `ProjectBrain` + `githubx` |
| `mobile_android` (`build.gradle.kts`, `AndroidManifest.xml`) | App Android Kotlin | **`android-kotlin-compose`** | Material 3, estado offline-first Room, coroutines/Flow, alvos de toque >= 48dp e regras R8 | **Kit Android/Kotlin** (ADB + Gradle) |
| `game_godot`, `game_unreal`, `game_web` | Desenvolvimento de jogos | **`gamedev-core`** / **`game-studio-roles`** | Game loop, state machines, física, orçamento de FPS/memória, hierarquia de papéis estilo `Claude-Code-Game-Studios` | **Kit GameDev** (`godot-mcp`, `unreal-claude`, Phaser/Three, `ACE-Step-1.5`) |

> **Política de Repositório Público & Licenças (`AGENTS.md`):** o HiveMind empacota **skills autorais próprias** em `internal/skillx/seeds/` com créditos explícitos em [`internal/skillx/seeds/NOTICE.md`](../../internal/skillx/seeds/NOTICE.md) para princípios inspirados em projetos MIT / CC BY 4.0. Repositórios completos de terceiros podem ser importados pelo usuário localmente via importador protegido pelo Gate de Segurança (§2.2), sem inflar nem copiar código fechado para o Git do HiveMind.

#### D. Slash Commands / Playbooks Prontos (`/layout`, `/redesign`, `/sec`, …)
Em vez de serem apenas strings de prompt, os slash commands tornam-se **Playbooks Declarativos** (`internal/skillrouter/playbooks.go`) disponíveis na IDE (composer autocomplete `/`), TUI e CLI:

| Comando | Ação & Skills Ativadas | Kit / Ferramentas Temporárias | Gate Final do QA |
|---|---|---|---|
| `/layout` | Carrega `ui-craft-product` ou `ui-art-direction` conforme `project-profile.yaml` | `figma-context` (se token existir) + `obscura`/Design Mode CDP | `check-design-tokens`, estados Loading/Empty/Error/Data, captura visual |
| `/redesign` | Carrega `ui-redesign-audit` | Screenshot antes/depois + leitura do DOM via CDP/Obscura | Remoção de padrões genéricos sem quebrar contratos ou testes |
| `/arch` | Carrega `arch-clean` + consulta prévia de impacto no `ProjectBrain` | Consulta de chamadores/dependências no grafo Graphify | Testes afetados verdes + garantia de isolamento de módulos |
| `/review` | Carrega `code-review-rigor` + análise de diff + histórico | `ProjectBrain` blast radius + `GitBlameFile` / `GitFileHistory` | Evidência estruturada obrigatória em `qa_approve` |
| `/sec` | Carrega `security-cloudflare` + regras OWASP/Zero-Trust | Auditoria estática de segredos/auth/CORS/headers/CSRF | Zero exposição de segredos, sanitização de entrada, Fail-Closed |
| `/mobile` | Carrega `android-kotlin-compose` | Habilita **Kit Android** (`adb` + `./gradlew`) no turno | `./gradlew testDebugUnitTest lintDebug` verde |
| `/game` | Carrega `gamedev-core` + preset de papéis Game Studio | Habilita **Kit GameDev** (`godot-mcp` ou `unreal-claude` ou runner Web) | Verificação headless de cena/script/FPS e build sem erro |
| `/audio` | Carrega guia de sound design & loop | Conector local **`ace-step/ACE-Step-1.5`** (`acestep.cpp` / API local) | Saída `.wav`/`.ogg` normalizada, BPM/tom conferidos, assets no path certo |
| `/brain` | Consulta / reindexa / inspeciona ou registra ADR na **Mente do Projeto** | MCP `graphify` / store local do projeto | Mostra fontes, nós afetados e proveniência |
| `/scan-skill` | Executa **Skill Security Gate** em uma skill local ou URL/repo GitHub | Scanner Go nativo + `NVIDIA/SkillSpector` (se instalado) | Relatório de score `0–100`, riscos e bloqueio automático em risco crítico |

---

### 2.2 Skill Security Gate (`internal/skillx/scanner.go` + `NVIDIA/SkillSpector`)

Toda importação externa de skill (via URL GitHub, diretório local ou editor na IDE) passa obrigatoriamente por verificação em **duas camadas**:

1. **Camada 1 — Scanner Estático Nativo em Go (sempre ativo, 0 dependências externas, < 50 ms):**
   - Evolui `ResidentReviewSkill` ([`internal/resident/skills.go`](../../internal/resident/skills.go)) para um motor compartilhado em `internal/skillx/scanner.go`.
   - Calcula score `0–100` e veredito (`approve`, `warn`, `block`) checando:
     - **Prompt Injection & Override:** instruções escondidas (*"ignore previous instructions"*, zero-width Unicode smuggling, comentários HTML ocultos com comandos).
     - **Exfiltração & Cofre:** referências a `.env`, `secretx`, `%LOCALAPPDATA%\HiveMind\resident\vault`, `~/.ssh`, cookies do browser, webhooks desconhecidos (`curl -X POST`, `Invoke-WebRequest` enviando env vars).
     - **Supply-Chain / Shell Perigoso:** `curl | sh`, `iex (irm ...)`, download executável ofuscado, base64 pipe para shell, alteração de `~/.bashrc`/Registry/Cron fora do projeto.
     - **Mismatch de Escopo:** scripts `.py`/`.sh`/`.ps1` dentro da pasta da skill sem declaração ou chamando rede sem aprovação.
   - Em veredito `block`, a IDE recusa ativar automaticamente e exibe os trechos exatos encontrados.
2. **Camada 2 — Ponte Opcional para `NVIDIA/SkillSpector`:**
   - Quando `skillspector` estiver instalado via `uv`/`pipx` ou habilitado via MCP no host, a IDE e a CLI oferecem botão **"Auditoria Profunda (SkillSpector)"** rodando YARA + análise semântica + lookup de CVEs em `OSV.dev` + export SARIF.
   - Job opcional no `.github/workflows/ci.yml` audita `internal/skillx/seeds/` e `.agents/skills/` para manter zero risco de supply-chain no repositório público.

---

### 2.3 Mente Coletiva do Projeto (`internal/brain/` + Graphify Oficial)

Objetivo: criar uma **memória persistente do projeto inteiro** compartilhada por **todas as equipes, lanes manual, AutoReview e Agent Hive** que trabalham naquele repositório — evitando que cada nova sessão comece do zero.

#### A. Interface Unificada `ProjectBrain`
```go
type BrainQuery struct {
    Workspace   string
    Prompt      string
    TouchFiles  []string
    Intent      string
    TokenBudget int // teto rígido para não inflar contexto
}

type BrainContext struct {
    Backend         string       // "graphify-mcp" | "native-sqlite"
    StructuralHits  []GraphNode  // símbolos, chamadores, raio de impacto (1-2 saltos)
    CollectiveNotes []BrainFact  // Decision, DomainRule, Gotcha, DesignRule, Feature
    StaleFiltered   int          // nós descartados por hash de arquivo divergente
}
```

#### B. Como o Graphify Oficial entra por Padrão + Gatilho Objetivo de Fallback
1. **Motor Estrutural Padrão (Graphify Oficial — `graphify-labs/graphify`):**
   - Registrado no catálogo `internal/mcpx/defaults.go` como servidor `graphify` (`graphifyy[mcp]` / `python -m graphify.serve`), rodando local e 100% offline via AST/Tree-sitter.
   - Índice estrutural gravado **fora do repositório** em `%LOCALAPPDATA%\HiveMind\brains\<project-hash>\graphify-out\` (nunca suja o projeto do usuário).
   - Incrementalidade acoplada ao `FileWatcher` ([`ide/internal/idecore/watcher.go`](../../ide/internal/idecore/watcher.go)): alterações de código marcam apenas os arquivos tocados como sujos para reindexação em background.
2. **Métricas & Fallback Explícito para Go + SQLite Nativo:**
   - Toda chamada `EnsureIndexed` / `QueryContext` mede duração e RSS/memória do processo filho Graphify.
   - **Critério de fallback automático ou troca recomendada:** se em um projeto normal (`<= 5.000` arquivos) a indexação inicial passar de **15 s**, a consulta `QueryContext` passar de **500 ms (p95)**, ou o processo passar de **400 MB RAM** (ou se Python/`uvx` falhar no Windows do usuário), a Spec já prevê substituir o backend atrás de `ProjectBrain` pelo motor nativo em **Go + SQLite FTS5/AST** mantendo 100% da interface sem tocar no orquestrador nem na UI.
3. **Camada Semântica Coletiva (`brain.sqlite` local por projeto):**
   - O Graphify indexa *código e docs*; já as decisões vivas do dia a dia ficam num banco local por projeto (`%LOCALAPPDATA%\HiveMind\brains\<project-hash>\collective.db`) com nós tipados:
     - `Decision` (ADR: *"escolhemos X por causa de Y"*);
     - `DomainRule` (*"título financeiro só fecha após conciliação Z"*);
     - `Gotcha` (bugs já barrados pelo QA em entregas passadas para nenhum agente repetir);
     - `DesignRule` (tokens/estética preferida daquele produto);
     - `Feature` (liga commits/PRs aos módulos alterados).
   - **Consolidação & Higiene Anti-Memória-Podre:** no fim de cada turno aprovado pelo QA (ou `MarkDone`), o pipeline extrai aprendizados com **proveniência** (`commit`, `sessionID`, `timestamp`, `fileHashes`). Antes de injetar um nó no turno seguinte, se todos os arquivos ancorados nele tiverem mudado de hash drasticamente, o nó é marcado como `needs_revalidation` (nunca injetado cegamente como verdade absoluta).

#### C. Atualização Futura (Pós-Onda 10): Sincronização via Git para Times
- **Fora das Ondas 7–10 (planejado para release seguinte):** comando/botão opcional **"Exportar/Sincronizar Mente do Projeto"** serializando apenas nós curados (`Decision`, `DomainRule`, `DesignRule`) para `.hivemind/brain/collective.jsonl` determinístico, com sanitização `AGENTS.md` / `secretx` impedindo commit acidental de caminhos locais ou segredos pessoais.

---

### 2.4 Agent Hive Conectado (`Obscura`, Canais Públicos Estilo `Agent Reach` & `strictNetworkGuard`)

#### A. Config `StrictNetworkGuard` (`false` por padrão)
- Em `internal/resident/types.go`: adicionar `StrictNetworkGuard bool `json:"strictNetworkGuard,omitempty"`` em `Config` e `Status`.
- Em `internal/resident/resident.go` e `netguard.go`:
  - **`StrictNetworkGuard == false` (padrão):** o Residente pode pesquisar/ler URLs públicas (`http`/`https`) sem ter que pré-cadastrar domínio por domínio na whitelist `AllowedHosts` — mantendo **sempre** a proteção anti-SSRF (bloqueio incondicional de IPs privados RFC1918, `127.0.0.1`, `169.254.169.254` e protocolos não-HTTP/HTTPS), `--obey-robots`, **zero consulta ao cofre `secretx`**, e encapsulamento obrigatório em `WrapUntrustedData`.
  - **`StrictNetworkGuard == true` (modo corporativo estrito):** exige que todo domínio esteja explícito na allowlist `AllowedHosts` (comportamento fail-closed legado).
- Expor toggle claro **"Exigir allowlist estrita de domínios na web"** nas configurações do Residente na IDE (iniciando desligado).

#### B. Provedor Browser Leve (`h4ckf0r0day/obscura`)
- Adicionar entrada opcional `obscura` em `internal/mcpx/defaults.go` (`TransportStdio`, args garantindo `--obey-robots`, exclusão mútua com `playwright` e `chrome-devtools` igual à regra já existente entre os dois).
- Abstração `HeadlessBrowserProvider` usada pelo Agent Hive e `/layout`/`/redesign`: usa **Obscura** (~30 MB RAM, motor V8 real, CDP) para renderizar SPAs, extrair Markdown/links ou gerar screenshots de validação visual; se `obscura` não estiver disponível no host, degrada suavemente para `playwright`/`chrome-devtools` ou `web_fetch`.

#### C. Canais Públicos Read-Only Estilo `Agent Reach` + Diagnóstico
- Integrar leitores públicos sem custo de API para o Agent Hive pesquisar referências:
  1. Leitor web limpo (HTML → Markdown sanitizado via `WrapUntrustedData`);
  2. Transcrição e metadados públicos de vídeos do YouTube (aulas, docs, talks técnicas);
  3. Leitores de feeds RSS / releases / changelogs de dependências.
- Subcomando / endpoint `ChannelDoctor()` (estilo `agent-reach doctor`) mostrando na UI quais canais estão operacionais (`web_markdown`, `youtube`, `rss`, `obscura`, `github`).
- **Fronteira de segurança explícita:** **NÃO** ler nem importar cookies/bancos locais do navegador pessoal do usuário (Chrome/Edge/Firefox) para redes autenticadas (X, LinkedIn, Instagram). Dados autenticados continuam entrando exclusivamente via conectores oficiais escopados (`githubx`, `jirax`, Slack app token no `secretx`).

---

### 2.5 Jev Free-Tier & Provedores OpenAI-Compatible (Conceito `FreeLLMAPI`)

Em vez de acoplar um proxy externo duplicado ao `FreeLLMAPI`, absorver seus ganhos nativamente no **Jev** + **`internal/modelx`**:
- **Novos adaptadores OpenAI-compatible no `modelx`:** **Groq**, **Cerebras**, **NVIDIA NIM** e **Mistral** (cada um com chave no cofre OS `secretx` + detecção automática em `ScanAllProviders`).
- **Preset de Roteamento `free_economy` no `internal/jev`:** prioriza tiers gratuitos / ultra-econômicos para triagem, classificação do `skillrouter` e tarefas leves do Residente, aplicando o failover/cooldown 429 já existente (Onda 2) entre eles quando bater rate-limit.
- **Custom OpenAI-Compatible Endpoint:** suporte explícito na tela de Provedores (base URL + modelo + API key opcional no cofre `secretx`) para apontar para qualquer gateway local/self-hosted (**Ollama, LM Studio, LiteLLM ou instância própria de `freellmapi`**) com telemetria unificada de uso no HiveMind.

---

### 2.6 Kits Especializados — Mobile (Android/Kotlin) & GameDev Studio

Os Kits são ativados automaticamente pelo Motor de Decisão (conforme permissão ativa) ou pelos comandos `/mobile`, `/game` e `/audio`, evitando carregar ferramentas pesadas em projetos web/Go normais.

#### A. Kit Mobile Prioritário — Android + Kotlin / Jetpack Compose (`internal/kits/android/`)
- **Detecção automática:** presença de `build.gradle.kts`/`settings.gradle.kts` + `AndroidManifest.xml`.
- **Ferramentas seguras expostas aos agentes sob demanda:**
  - `android_devices`: `adb devices -l` (verifica aparelho físico ou emulador ativo);
  - `android_build_test`: wrapper padronizado `./gradlew testDebugUnitTest lintDebug` (com parsing estruturado de falhas JUnit/Lint para o QA);
  - `android_logcat_tail`: captura filtrada de crash/stacktrace por pacote/PID (bounded head+tail, zero dump infinito);
  - `android_ui_snapshot`: captura tela via `adb exec-out screencap -p` + árvore de acessibilidade `uiautomator dump` para o QA/Designer conferir telas Compose no emulador.
- **Skill `android-kotlin-compose`:** padrões de arquitetura MVVM/MVI, StateFlow, Room offline-first, Material 3, tamanho mínimo de toque (`48dp`), regras R8/ProGuard e navegação Compose sem travar a main thread.

#### B. Kit GameDev Studio (`internal/kits/gamedev/` + `internal/mcpx/defaults.go`)
Cobrindo as principais ferramentas **gratuitas / open-source** e as referências aprovadas dos prints:
1. **Preset de Equipe & Papéis (`Donchitos/Claude-Code-Game-Studios`):** template de time selecionável no Wizard (`game-studio`) adaptando os papéis dinâmicos para **Game Director (Chefe)**, **Gameplay/Systems Dev**, **Level/UI/Shader Specialist**, **Audio Designer** e **Playtest QA** sem explodir em 49 agentes simultâneos no contexto (apenas os papéis relevantes são spawnados conforme a tarefa).
2. **Godot Engine (`Coding-Solo/godot-mcp`):** entrada nativa desligada por padrão em `internal/mcpx/defaults.go` (`godot-mcp`), habilitada em projetos com `project.godot`, permitindo validar cenas `.tscn`, executar GDScript headless (`--headless --check-only`) e coletar erros de runtime no loop Dev → QA.
3. **Unreal Engine (`Natfii/UnrealClaude`):** entrada nativa desligada por padrão (`unreal-claude`) para projetos com `.uproject`, oferecendo ponte com Blueprints, manipulação de Actors/Levels e consulta de docs C++/UE mantendo isolamento do projeto.
4. **Web & Indie Zero-Install (`gamedev-web`):** skill focada em **Phaser, Three.js, PixiJS e Bevy (WASM)** validada em browser headless (`obscura` ou `playwright`) com checagem de console errors, canvas renderizando e frame-time estável.
5. **Áudio & Trilha Sonora Local (`ace-step/ACE-Step-1.5` via `/audio`):**
   - Conector local opcional (`acestep.cpp` / HTTP API local em `127.0.0.1`, ativado sob demanda), licença **MIT**, capaz de gerar trilhas e loops em consumidor (< 4 GB VRAM em modo quantizado/offload).
   - Interface de geração parametrizada por **BPM, tom musical, duração, tags (`[Verse]`/`[Loop]`/`[Combat]`)**, exportando direto para `assets/audio/` do projeto (com registro de metadado de licença e limite seguro de tamanho em disco).

---

## 3. Plano de Execução em Ondas (Ondas 7 a 10)

A ordem prioriza primeiro o **cérebro de decisão + segurança de skills** (que melhora todas as tarefas seguintes imediatamente), depois a **memória coletiva entre sessões**, seguida da **conexão web do Agent Hive + economia Jev**, e fechando com os **Kits Android/Kotlin & GameDev**.

| Onda | Foco Principal | Pacotes / Áreas | Gate de Saída (Critério de Aceite) |
|---|---|---|---|
| **Onda 7** | Motor de Decisão, Slash Playbooks, Skills Nativas (Design/AppSec) & Skill Security Gate | `internal/skillrouter/`, `internal/skillx/`, `internal/orchestrator/`, `ide/frontend/` | Testes do roteador `Domínio × Intenção × Plataforma`, chip de permissão (`full_access` auto vs confirmação), `/layout`..`/sec` funcionando, scanner Go barrando fixtures maliciosas + ponte `SkillSpector` |
| **Onda 8** | Mente Coletiva do Projeto (`ProjectBrain` + Graphify oficial) | `internal/brain/`, `internal/mcpx/`, `internal/orchestrator/`, `ide/internal/idecore/` | Índice Graphify local fora do Git, benchmark de latência/RAM documentado, extração de `Decision`/`DomainRule`/`Gotcha` pós-QA entre duas lanes distintas do mesmo projeto |
| **Onda 9** | Agent Hive Conectado (`Obscura`, Canais Públicos, `StrictNetworkGuard=false`) & Jev Free-Tier | `internal/resident/`, `internal/mcpx/`, `internal/modelx/`, `internal/jev/`, `ide/` | `StrictNetworkGuard` desligado por padrão mantendo SSRF/vault/robots/`WrapUntrustedData`, `obscura` + `ChannelDoctor` + Groq/Cerebras/NIM/Mistral + custom OpenAI-compatible verdes |
| **Onda 10** | Kits Especializados (Android/Kotlin Compose & GameDev Studio + Godot/Unreal/`ACE-Step-1.5`) | `internal/kits/android/`, `internal/kits/gamedev/`, `internal/mcpx/`, `ide/` | `/mobile` rodando `lintDebug`/`testDebugUnitTest` + UI snapshot via ADB fake/real; `/game` validando projeto Godot/Web + preset `game-studio`; `/audio` integrando mock HTTP `ACE-Step-1.5` |

### Checklist — Onda 7 (Motor de Decisão, Playbooks, Skills Nativas & Security Gate)
- [ ] **W7-01** Criar `.hivemind/project-profile.yaml` loader/detector automático (`saas_product`, `finance_erp`, `services`, `sales_ecommerce`, `marketing_landing`, `mobile_android`, `game_*`, `backend_edge`) em `internal/skillrouter/profile.go`.
- [ ] **W7-02** Implementar classificador `Domínio × Intenção × Plataforma` (`internal/skillrouter/router.go`) com heurística < 1 ms + fallback Jev opcional, respeitando `full_access` (aplica direto com chip editável) vs `governed`/`auto_edit` (pede confirmação rápida antes de rodar).
- [ ] **W7-03** Implementar Playbooks de Slash Commands (`/layout`, `/redesign`, `/arch`, `/review`, `/sec`, `/mobile`, `/game`, `/audio`, `/brain`, `/scan-skill`) injetando apenas 1–2 *Hybrid Skill Stubs* sem quebrar o KV-cache.
- [ ] **W7-04** Autorar skills nativas em `internal/skillx/seeds/` (`ui-craft-product`, `ui-art-direction`, `ui-redesign-audit`, `security-cloudflare`, `arch-clean`, `code-review-rigor`, `android-kotlin-compose`, `gamedev-core`) + [`NOTICE.md`](../../internal/skillx/seeds/NOTICE.md) com atribuições MIT / CC BY 4.0.
- [ ] **W7-05** Implementar Skill Security Gate em `internal/skillx/scanner.go` (score `0–100`, detecção de prompt injection, exfiltração de cofre/env, `curl | sh`) integrado ao importador/salvamento de skills na IDE e CLI.
- [ ] **W7-06** Adicionar adaptador opcional `NVIDIA/SkillSpector` (CLI/MCP + export SARIF) e passo de auditoria de skills no `.github/workflows/ci.yml`.
- [ ] **W7-07** Atualizar UI do composer da IDE (autocomplete `/playbooks`, chip editável da decisão `Domínio · Skill · Kit` seguindo Constitution `D1–D9`) e feedback visual do Scanner no painel de Skills.
- [ ] **W7-08** `[Verificação Onda 7]` Testes unitários cobrindo roteamento diferenciado (`finance_erp` vs `marketing_landing` vs `redesign`), política por `permission_mode`, bloqueio de skill maliciosa no scanner, `check-design-tokens` e build completo verdes.

### Checklist — Onda 8 (Mente Coletiva do Projeto — Graphify & Knowledge Layer)
- [ ] **W8-01** Criar interface `ProjectBrain` em `internal/brain/` (`EnsureIndexed`, `QueryContext`, `RecordLearnings`, `InvalidateStale`, `Stats`) com storage local fora do repo (`%LOCALAPPDATA%\HiveMind\brains\<project-hash>\`).
- [ ] **W8-02** Registrar `graphify` (`graphifyy[mcp]`) em `internal/mcpx/defaults.go` e implementar backend `GraphifyBrain` com telemetria de latência/memória e acoplamento incremental ao `FileWatcher` da IDE.
- [ ] **W8-03** Implementar camada semântica local em `collective.db` (SQLite: `Decision`, `DomainRule`, `Gotcha`, `DesignRule`, `Feature`) com proveniência (`commit`, `sessionID`, `fileHashes`) e invalidação automática por drift de arquivo.
- [ ] **W8-04** Plugar `QueryContext` (respeitando `TokenBudget`) na abertura de turno e extração consolidada pós-QA em `internal/orchestrator/` + expor aba/modal **Mente do Projeto** na IDE + comando `/brain`.
- [ ] **W8-05** Avaliar benchmark do Graphify em projeto real/sintético grande (> 15s index / > 500ms query p95 / > 400MB RAM); implementar backend fallback nativo Go + SQLite FTS5 se necessário sob o mesmo contrato `ProjectBrain`.
- [ ] **W8-06** `[Verificação Onda 8]` Validar duas sessões/equipes independentes no mesmo projeto herdando decisões e gotchas da primeira sem leitura manual de histórico de sessão; validar re-validação após alteração de arquivo e cobertura de testes.

### Checklist — Onda 9 (Agent Hive Conectado, `StrictNetworkGuard` & Jev Free-Tier)
- [ ] **W9-01** Adicionar `StrictNetworkGuard bool` (`false` por padrão) em `internal/resident/types.go`, `broker.go`, `netguard.go`, `ide/internal/idecore/resident.go` e toggle na UI do Residente, mantendo SSRF guard, zero acesso ao `secretx`, `--obey-robots` e `WrapUntrustedData` sempre ativos.
- [ ] **W9-02** Integrar `h4ckf0r0day/obscura` em `internal/mcpx/defaults.go` + abstração `HeadlessBrowserProvider` (com exclusão mútua vs `playwright`/`chrome-devtools`, screenshot leve e fallback gracioso).
- [ ] **W9-03** Implementar canais públicos read-only inspirados no `Agent Reach` (`web_markdown`, `youtube_transcript`, `rss_feed`) + `ChannelDoctor()` na UI do Residente/Orb (vetando explicitamente extração de cookies do navegador pessoal).
- [ ] **W9-04** Adicionar provedores **Groq**, **Cerebras**, **NVIDIA NIM** e **Mistral** em `internal/modelx/` com chaves no cofre `secretx` + preset `free_economy` + failover em 429 no `internal/jev/`.
- [ ] **W9-05** Adicionar suporte a **Custom OpenAI-Compatible Endpoint** na tela de Provedores (Ollama, LM Studio, LiteLLM, `freellmapi`) mantendo telemetria de tokens unificada.
- [ ] **W9-06** `[Verificação Onda 9]` Testar `StrictNetworkGuard=false` (domínio público permitido + IP privado `127.0.0.1`/`169.254.169.254` e acesso ao vault bloqueados), `Obscura` mockado, canais públicos encapsulados com `WrapUntrustedData` e failover Jev em 429.

### Checklist — Onda 10 (Kits Android/Kotlin & GameDev Studio + Áudio Local)
- [ ] **W10-01** Implementar `internal/kits/android/` com wrapper seguro de `adb` (`devices`, `logcat` bounded, `screencap`, `uiautomator dump`) e runner `./gradlew testDebugUnitTest lintDebug` com parser estruturado de falhas.
- [ ] **W10-02** Criar adaptador visual no fluxo `/mobile` permitindo que o Chefe/QA anexe screenshot + hierarquia UI do emulador Android antes de aprovar entregas Compose.
- [ ] **W10-03** Implementar `internal/kits/gamedev/` + preset de time `game-studio` no Wizard (`Game Director`, `Gameplay Dev`, `Technical Artist/Shader`, `Audio Designer`, `Playtest QA`) inspirado em `Donchitos/Claude-Code-Game-Studios`.
- [ ] **W10-04** Registrar `godot-mcp` (`Coding-Solo/godot-mcp`) e `unreal-claude` (`Natfii/UnrealClaude`) em `internal/mcpx/defaults.go` (desligados por padrão) + runner de validação headless para **Godot** e projetos Web Games (**Phaser/Three.js/PixiJS/Bevy-WASM**).
- [ ] **W10-05** Implementar conector opcional para geração local de áudio/trilha **`ace-step/ACE-Step-1.5`** (`/audio`, parametrização BPM/tom/duração/tags, saída normalizada em `assets/audio/` e checagem de disco/licença MIT).
- [ ] **W10-06** Atualizar catálogo de ícones/badges, cards do Motor de Decisão e UI de configuração de Kits na IDE conforme `.specify/memory/constitution.md` (`D1–D9`).
- [ ] **W10-07** `[Verificação Onda 10]` Executar testes completos do Kit Android (Gradle + ADB mocks), Kit GameDev (Godot/Web runner + `game-studio` preset + mock `ACE-Step-1.5`), `go vet`/`go test` raiz e IDE, `check-design-tokens`, `tsc`, `npm test` e `npm run build`.

---

## 4. Fora de Escopo (Ondas 7–10) e Riscos

- **Fora de escopo nas Ondas 7–10:**
  - Extração/uso de cookies pessoais do navegador do usuário (Chrome/Edge/Firefox) para redes sociais privadas (X, LinkedIn, Instagram).
  - Subir proxy separado estilo `freellmapi` como daemon paralelo ao processo principal do HiveMind (substituído pelo Jev + endpoint OpenAI-compatible).
  - Sync automático da Mente do Projeto via Git (`.hivemind/brain/`) — reservado como **Atualização Futura (Pós-Onda 10)**.
  - Kits dedicados de iOS/Swift (Xcode/macOS) e Roblox Studio — extensíveis no futuro pela mesma interface de Kit.
- **Riscos & Mitigações:**
  - *Peso do Graphify em Python/UVX:* mitigado por métricas automáticas e contrato trocável para Go + SQLite sem impacto nos chamadores.
  - *Explosão de tokens por muitas skills carregadas:* mitigado pelo teto estrito de **2 skills + 1 kit por turno** e *Hybrid Skill Stubs*.
  - *Risco de skills comunitárias maliciosas:* mitigado pelo Gate em Go obrigatório antes da ativação + integração opcional `NVIDIA/SkillSpector`.
  - *Licenças de terceiros em repositório público:* mitigado escrevendo skills autorais próprias com atribuição clara em `NOTICE.md` e mantendo ferramentas externas (`obscura`, `godot-mcp`, `unreal-claude`, `ACE-Step-1.5`, `SkillSpector`, `Graphify`) como integrações configuráveis em tempo de execução, nunca binários/código copiado no Git.
