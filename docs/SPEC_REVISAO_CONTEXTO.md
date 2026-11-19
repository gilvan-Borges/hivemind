# Spec — Ferramentas de Revisão Profunda & Compactação In-Turn (Custo/Contexto)

**Status:** Proposta (pronta para implementação)  
**Data:** 2026-11-19  
**Origem:** Dogfooding real na IDE (`C:\ProjetosVibetex\IPG`, revisão do PR #716, sessão `af7795677497`)

---

## 1. Problema Observado em Produção

Durante a revisão real do **PR #716** no monorepo `IPG`:
- A **qualidade técnica** do Chefe foi excelente: ele não ficou preso ao diff — auditou DTOs reais do backend (`PageResponse`, `ClienteResponse`, `CondominioResponse`), permissões de rota (`SUPER_ADMIN`/`ADMIN`), a regra da catraca de a11y (`a11y-web.txt`), guard de termos e status de CI.
- **Princípio preservado:** quando houver dúvida fora do diff, o Chefe **deve continuar podendo investigar o projeto inteiro** para validar contratos e efeitos colaterais reais.
- **Custo medido nessa revisão (34 chamadas LLM):**
  - Ferramentas chamadas: `read_file` × 36, `list_dir` × 13, `github_pr_diff` × 2, `github_get_pr` × 1, `github_ci_status` × 1.
  - Consumo acumulado: **2.421.142 tokens** (`2.393.649` entrada / `27.493` saída — **98,9% reenvio de entrada**), chegando a **`146.111` tokens por chamada**.

### Causas Raiz
1. **Falta de ferramentas cirúrgicas de investigação** em `internal/tools/tools.go`: o Chefe só tem `list_dir` e `read_file` (arquivo inteiro até 64 KiB), obrigando-o a fazer 13 `list_dir` + 36 `read_file` para localizar símbolos, rotas e arquivos num monorepo.
2. **Ausência de compactação *in-turn***: `sessionx.CompactOldToolOutputs` roda apenas entre turnos (abertura/fecho da sessão). Dentro de um turno longo com 30+ chamadas de leitura, cada nova chamada reenvia todos os arquivos já lidos integralmente.
3. **Achados colaterais em monorepo (IDE):**
   - `DetectStack` (`ide/internal/idecore/stack.go`) só inspeciona a raiz e classificou o `IPG` como `"Stack genérica / não identificada"`.
   - `.worktrees/` (worktrees criadas por outros agentes) não está em `ignoredDirs`, aumentando uso de RAM/CPU no `FileIndex` e `FileWatcher`.

---

## 2. Objetivos e Metas

1. **Manter 100% da profundidade de auditoria** do projeto inteiro fora do diff.
2. **Reduzir em 70%–85% o consumo de tokens e o número de rodadas** em revisões longas (ex.: de ~2,4M tokens / 34 chamadas para ~250k–450k tokens / ~8–12 chamadas).
3. **Preservar a segurança de workspace** (`workspace.Resolve`, anti-symlink-escape, exclusão de segredos/worktrees) e manter retrocompatibilidade total com sessões e testes existentes.

---

## 3. Especificação Técnica

### Parte A — Novas Ferramentas de Investigação (`internal/tools`)

Todas as ferramentas abaixo são **somente leitura**, respeitam `workspace.Resolve` e são expostas para **todos os papéis** (`project_manager`, `architect`, `tech_lead`, `developer`, `qa`) em `tools.ForRole`.

#### A.1 `search_text` (Busca por texto/regex no projeto)
- **Nome:** `search_text`
- **Descrição:** Busca ocorrências de texto ou regex nos arquivos do workspace, retornando `caminho:linha: trecho`. Ideal para localizar símbolos, rotas, DTOs, testes e referências em 1 chamada sem listar pastas manualmente.
- **Argumentos (`searchTextArgs`):**
  ```go
  type searchTextArgs struct {
      Query         string `json:"query" jsonschema:"Texto ou expressão regular a buscar no código."`
      Path          string `json:"path,omitempty" jsonschema:"Subdiretório relativo opcional para restringir a busca (padrão: raiz do workspace)."`
      Glob          string `json:"glob,omitempty" jsonschema:"Filtro de nome de arquivo opcional (ex.: *.ts, *.java, *.go, *Controller*)."`
      Regex         bool   `json:"regex,omitempty" jsonschema:"Quando true, interpreta query como expressão regular (Go RE2)."`
      CaseSensitive bool   `json:"case_sensitive,omitempty" jsonschema:"Quando true, diferencia maiúsculas e minúsculas (padrão: false)."`
      MaxResults    int    `json:"max_results,omitempty" jsonschema:"Limite de ocorrências retornadas (padrão: 50, máximo: 120)."`
  }
  ```
- **Limites & Segurança:**
  - `Path` resolvido via `workspace.Resolve(d.Root, args.Path)` (rejeita traversal e symlinks fora do root).
  - Diretórios ignorados: `.git`, `.hivemind`, `.worktrees`, `node_modules`, `dist`, `build`, `target`, `.gradle`, `.next`, `vendor`, `__pycache__`, `.venv`, `bin`, `obj`.
  - Ignora arquivos binários (NUL nos primeiros 512 bytes) e arquivos > `512 KiB`.
  - Cada linha de resultado truncada a `200` bytes; saída total limitada a `24 KiB` com indicação de truncamento (`total_matches`, `truncated`).

#### A.2 `find_files` (Localização rápida de arquivos por nome/glob)
- **Nome:** `find_files`
- **Descrição:** Encontra arquivos no workspace pelo nome ou padrão glob (ex.: `*Cliente*.java`, `*.spec.ts`, `a11y*`), evitando múltiplas chamadas de `list_dir`.
- **Argumentos (`findFilesArgs`):**
  ```go
  type findFilesArgs struct {
      Pattern    string `json:"pattern" jsonschema:"Trecho do caminho/nome ou glob (ex.: *.spec.ts, *Route*, a11y-web.txt)."`
      Path       string `json:"path,omitempty" jsonschema:"Subdiretório relativo opcional (padrão: raiz)."`
      MaxResults int    `json:"max_results,omitempty" jsonschema:"Limite de caminhos retornados (padrão: 60, máximo: 200)."`
  }
  ```
- **Comportamento:**
  - Se `Pattern` contiver `*`, `?` ou `[`, aplica `filepath.Match` contra o basename e o caminho relativo; caso contrário faz substring case-insensitive no caminho relativo (`/`).
  - Usa a mesma lista de diretórios ignorados de `search_text` e cap de varredura (20.000 arquivos).

#### A.3 `read_file` com Recorte de Linhas (`start_line` / `end_line`)
- **Evolução retrocompatível de `read_file`:**
  ```go
  type readFileArgs struct {
      Path      string `json:"path" jsonschema:"Caminho relativo ao diretório de trabalho do agente."`
      StartLine int    `json:"start_line,omitempty" jsonschema:"Linha inicial (1-based) opcional para ler apenas um trecho do arquivo."`
      EndLine   int    `json:"end_line,omitempty" jsonschema:"Linha final inclusiva (1-based) opcional (máx. 400 linhas por chamada quando especificado)."`
  }
  ```
- **Comportamento:**
  - Se `StartLine == 0 && EndLine == 0`: mantém o comportamento atual (até 64 KiB), acrescentando cabeçalho leve quando o arquivo é grande (`# path (N linhas)`).
  - Se `StartLine > 0` ou `EndLine > 0`: valida intervalo, retorna apenas as linhas `[StartLine..EndLine]` numeradas (`L42: ...`), mais `total_lines` para o agente saber se precisa ler mais.
  - Em `list_dir`: também ignorar `.worktrees` junto de `.git` e `.hivemind`.

#### A.4 `github_pr_files` (Inventário leve de arquivos do PR)
- **Nome:** `github_pr_files`
- **Descrição:** Lista apenas os arquivos alterados de um PR (`path`, `status`, `additions`, `deletions`) sem trazer o patch completo, útil antes de filtrar o que inspecionar em PRs grandes.

---

### Parte B — Controle de Custo & Compactação *In-Turn* (`internal/orchestrator`)

#### B.1 `BeforeModelCallback` de Compactação *In-Turn* (`internal/orchestrator/context_guard.go`)
- **Onde atua:** plugado em `triageAgent` (`runTriageChief`) e nos agentes do pipeline que usem `IncludeContentsDefault`.
- **Como funciona:** antes de cada envio ao modelo (`BeforeModelCallback(ctx, llmRequest)`):
  1. Clona superficialmente o slice `llmRequest.Contents` (e apenas os `*genai.Content` que serão compactados), **sem alterar** o histórico original salvo em disco na sessão.
  2. Identifica todas as partes `FunctionResponse` em `llmRequest.Contents` da mais antiga para a mais recente.
  3. **Janela ativa preservada intacta:** as últimas **`K` respostas de ferramenta** (padrão `K = 6`, configurável via `settings.in_turn_keep_tool_results`) ficam 100% íntegras.
  4. **Ferramentas nunca compactadas:** `github_get_pr`, `github_ci_status`, `delegate_to_squad`, `qa_approve`, `qa_request_changes`, e qualquer resposta contendo erro (`error`).
  5. **Compactação determinística das respostas antigas (> 600 bytes)** para `read_file`, `list_dir`, `search_text`, `find_files`, `github_pr_diff`, `git_diff`, `run_shell`:
     - Mantém os primeiros `240 bytes` (ou resumo de metadados: nome da ferramenta, caminho/query correlacionado via `FunctionCall.ID`, quantidade de linhas/bytes) + nota:
       `[saída anterior de read_file("...") compactada: 210 linhas / 7.4 KiB — já analisada; chame novamente com start_line/end_line se precisar reler]`
  6. **Preserva integralmente** todo texto escrito pelo usuário e todas as anotações/raciocínio em texto produzidos pelo próprio agente entre uma chamada e outra (é ali que o Chefe registra as conclusões já verificadas!).
- **Observabilidade:** opcionalmente registra contador de bytes economizados por chamada para telemetria/testes.

#### B.2 Prompt de Revisão Econômica (`runTriageChief`)
Quando `r.reviewRequested == true` (revisão de PR):
- Orienta o Chefe explicitamente:
  1. Começar por `github_get_pr` + `github_pr_diff` (e `github_ci_status`).
  2. Quando precisar verificar contexto fora do diff no projeto inteiro: usar **`search_text` / `find_files`** antes de abrir arquivos, e preferir **`read_file` com `start_line`/`end_line`** em arquivos grandes.
  3. Agrupar leituras independentes em **chamadas paralelas no mesmo turno**.
  4. Registrar um resumo de 1 linha das conclusões obtidas após cada bloco de leituras (garantindo continuidade após a compactação *in-turn*).

#### B.3 Perfil/Orçamento Opcional de Revisão (`internal/team/team.go`)
Campos opcionais em `settings` do `.hivemind/team.yaml`:
- `in_turn_keep_tool_results: int` (padrão `6`, `0` usa padrão) — quantas respostas recentes de ferramenta permanecem íntegras no prompt durante o mesmo turno.
- `review_model: string` (opcional) — permite escolher um modelo mais rápido/econômico (ex.: `opencode-go:glm-5.3` ou `agy:gemini-3.8-flash-medium`) especificamente quando o turno for revisão de PR, sem alterar o modelo padrão do Chefe para arquitetura/implementação.

---

### Parte C — Melhorias Colaterais de Monorepo (`ide/internal/idecore`)

1. **`DetectStack` em `stack.go`:** quando a raiz não tiver manifestos ou tiver apenas `Docker`, inspecionar subdiretórios de 1º nível (ignorando `.git`, `.hivemind`, `.worktrees`, `node_modules`, `dist`, `build`) para detectar stacks de monorepo (ex.: `IPG` → `Java / Gradle + React / TypeScript`).
2. **`ignoredDirs` em `explorer.go` e `watcher.go`:** incluir `.worktrees` para não indexar nem criar watchers `fsnotify` em worktrees externas de outros agentes.

---

## 4. Plano de Testes e Critérios de Aceite

### Testes Unitários Obrigatórios
1. `internal/tools/search_test.go`:
   - `TestSearchTextLiteralAndRegex`: encontra ocorrências literais, regex, filtro `glob`, `path` restrito, ignora `.git`/`.hivemind`/`.worktrees`/`node_modules` e binários.
   - `TestSearchTextRejectsPathEscape`: recusa `../` e symlinks fora do workspace.
   - `TestFindFilesGlobAndSubstring`: acha arquivos por glob (`*.spec.ts`) e substring.
   - `TestReadFileLineRange`: retorna exatamente `start_line..end_line` com total de linhas; retrocompatível sem argumentos de linha.
2. `internal/orchestrator/context_guard_test.go`:
   - `TestInTurnCompactionTrimsOldReadsKeepsRecent`: com 12 respostas de `read_file` no mesmo turno e `keep=4`, as 8 mais antigas são compactadas com caminho preservado, as 4 mais recentes ficam intactas, textos do usuário/agente e `github_get_pr` ficam intactos, e o histórico original da sessão não é mutado.
   - `TestReviewModelOverrideUsedOnlyOnPRReview`: verifica uso de `settings.review_model` apenas quando `reviewRequested == true`.
3. `ide/internal/idecore/idecore_test.go`:
   - `TestDetectStackMonorepoSubdirs`: detecta stack em subpastas de 1º nível (`web/package.json` + `api/build.gradle`) ignorando `.worktrees/`.

### Critério de Aceite em Uso Real (Re-teste no `IPG`)
- Repetir uma revisão de PR de porte equivalente ao PR #716 no `IPG`:
  - O Chefe continua validando contratos/rotas fora do diff sem perder precisão.
  - Chamadas `list_dir` caem próximo de zero (substituídas por `search_text` / `find_files`).
  - Prompt por chamada não passa de ~40k–50k tokens (teto estável graças à compactação *in-turn*) em vez de crescer para 146k+.
  - Custo total do turno cai **≥ 70%** comparado aos 2,42M tokens da baseline.

---

## 5. Ordem de Implementação Sugerida

1. **Etapa 1 (Tools):** `search_text`, `find_files`, `read_file(start_line, end_line)` + testes em `internal/tools/`.
2. **Etapa 2 (Custo In-Turn + Revisão):** `context_guard.go` (`BeforeModelCallback`), `settings.in_turn_keep_tool_results` / `settings.review_model`, atualização do prompt de revisão em `orchestrator.go` + testes.
3. **Etapa 3 (Monorepo IDE):** `DetectStack` em subpastas de 1º nível + `.worktrees` em `ignoredDirs` + testes em `idecore`.
4. **Etapa 4 (Build & Validação):** `go test -count=1 ./...`, `idecore`, rebuild do instalador `HiveMindSetup-x64.exe`.
