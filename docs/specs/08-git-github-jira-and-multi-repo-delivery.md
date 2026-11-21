# Spec 08 — Endurecimento VCS (`gitx`, `githubx`, `jirax`) e Entrega Multi-Repo

> **Status:** Proposta de Refatoração & Evolução
> **Alvos:** `internal/gitx/`, `internal/githubx/`, `internal/jirax/`, `ide/internal/idecore/diff.go`, `ide/internal/idecore/git_repos.go`

---

## 1. Diagnóstico do Estado Atual

### Lacunas Observadas no Código
1. **`internal/gitx`:**
   - `gitx.Local.Run` (`git.go:44`) apenas define `GIT_TERMINAL_PROMPT=0`, ao contrário de `idecore/gitaction.go:347-363` que também bloqueia `GCM_INTERACTIVE=Never`, `GIT_ASKPASS=""`, `SSH_ASKPASS=""` — comandos Git do orquestrador ainda podem travar esperando prompts interativos de credential helper no Windows.
   - `EnsureWorktree` (`git.go:86-90`) chama `_ = os.RemoveAll(wt)` sempre que `g.IsRepo(ctx, wt)` retorna `false`, podendo apagar worktree válida em caso de falha transitória (`ctx.Canceled`, lock temporário).
   - `SyncUncommittedToDelivery` (`integrate.go:220-276`, `copyBetweenTrees:379-401`) usa `os.WriteFile` não-atômico e descarta erros de cópia/remoção silenciosamente (`_ =`, `err == nil`), diferente de `idecore.SyncWorktreeToWorkspace` (`diff.go:369-449`).
   - `DiscardFileChanges` (`diff.go:759-761`) passa `relClean` cru sem `gitx.LiteralPathspec`, contrastando com `StageWorkspaceFile` (`266`) e `UnstagedFileDiff` (`824`).
   - Embora a **Worktree Composta Multi-Repo** (`nested_repos.go`, commit/PR por sub-repositório no `finish` e sync no `idecore/runner.go`) tenha acabado de resolver a montagem de `ipg-mobile`/`HiveMind-App` dentro da worktree da conversa, o **cross-linking automático entre os 2 PRs abertos** (editar o corpo do PR A citando o PR B e vice-versa em tarefas full-stack) e o watcher automático do painel Git para worktrees compostas ainda não existem.
2. **`internal/githubx` e Acoplamento no `idecore`:**
   - `ParseRemote` (`githubx/github.go:1169-1190`) faz match hardcoded de `"github.com"`, quebrando **GitHub Enterprise Server (GHES)** apesar de `SaveCredentials` (`credentials.go:108`) e `REST.BaseURL` aceitarem URL base customizada.
   - `REST`, `CLI` (`github.go:487, 522, 555, 577, 704`) e `listPullRequestsGraphQL` (`diff.go:2162`) fixam `per_page=100` / `first:30` / `last:50` sem paginação (`Link` header / GraphQL `pageInfo.endCursor`), truncando silenciosamente PRs/arquivos/comentários em repos grandes.
   - `REST.client()` (`github.go:196-201`), `jirax.HTTP.client()` (`jira.go:100-105`) e `diff.go:1503, 2239, 2447` alocam novo `http.Client` por requisição quando `HTTP == nil`, e `diff.go` bypassa `githubx.Client` duplicando GraphQL/REST/`gh` CLI.
3. **`internal/jirax` e `idecore` Jira Discovery:**
   - `idecore.ListProjectIssues` (`diff.go:1313-1317`) chama `EnableProjectJira(workspace, projKey)` ao inferir projeto, **mutando `.hivemind/team.yaml` durante uma chamada de leitura**.
   - `matchJiraProjectFromNames` (`diff.go:1109-1144`) codifica prefixos/termos específicos de cliente/empresa (`vibetex-`, `auditoriavibe`, `mural`) diretamente no código-fonte da IDE.

---

## 2. Comparação: HiveMind Atual vs Engenharia Google DevTools / GitHub CLI

| Dimensão | HiveMind Atual | Padrão Google DevTools / GitHub CLI |
|---|---|---|
| **Execução Git** | Env não-interativo fragmentado entre `gitx` e `idecore` | Helper único non-interactive env + `LiteralPathspec` obrigatório em todo path de usuário |
| **Resolução de Remotes** | Regex hardcoded para `github.com` | Parsing de URL genérico comparando host com `baseURL` configurado (suporte nativo a GHES) |
| **Paginação & Pool** | Queries fixas (`first:30`), novo `http.Client` por chamada | Paginação paginada via `Link`/`endCursor` com teto seguro + `http.Client` pooled compartilhado |
| **Descoberta Jira** | Heurísticas hardcoded de org + side-effect em `ListIssues` | Matching genérico por tokens do repo/remote sem termos proprietários, e sugestão sem escrita implícita em `team.yaml` |

---

## 3. Especificação Técnica da Solução

1. **Hardening `internal/gitx`:**
   - Mover montagem de ambiente não-interativo (`GIT_TERMINAL_PROMPT=0`, `GCM_INTERACTIVE=Never`, `GIT_ASKPASS=`, `SSH_ASKPASS=`) para `gitx.Local.Run` eliminando duplicação em `idecore/gitaction.go`.
   - Em `EnsureWorktree`: só limpar diretório `wt` quando `wt` existir fisicamente sem `.git` válido (e nunca se `ctx.Err() != nil`).
   - Migrar `copyBetweenTrees` em `SyncUncommittedToDelivery` para `atomicx.WriteFile` retornando erros agregados como em `SyncWorktreeToWorkspace`; usar `gitx.LiteralPathspec` em `DiscardFileChanges`.
   - Completar entrega cross-repo: após abrir `>= 2` PRs em uma execução composta (`root` + `nested`), atualizar o comentário/body vinculando os URLs irmãos (`Relacionado a: ...`).
2. **Paginação, GHES e Unificação em `internal/githubx` & `internal/jirax`:**
   - Atualizar `githubx.ParseRemoteForHost(raw, allowedHosts...)` suportando qualquer host configurado em `githubx.LoadCredentials()` / Enterprise (`apiBaseURL`).
   - Adicionar paginação por `Link: rel="next"` em `REST` e cursor GraphQL em `listPullRequestsGraphQL`; compartilhar `http.Client` singleton com transport pooled em `githubx` e `jirax`.
   - Remover código duplicado de GitHub REST/GraphQL/`gh` CLI de `idecore/diff.go`, consumindo métodos de `githubx.Client`.
3. **Desacoplar `jirax` Inference:**
   - Remover strings proprietárias de `matchJiraProjectFromNames` (substituir por matching genérico de palavras-chave entre nome do repo/remote/branch e chaves/nomes de projetos Jira) e tornar `ListProjectIssues` 100% read-only (sugerindo vínculo na UI em vez de gravar `team.yaml` silenciosamente).

---

## 4. Critérios de Aceite
- `ParseRemote` reconhece remotes SSH/HTTPS de GHES (`git@git.empresa.local:org/repo.git`).
- `ListPullRequests` / `ListPullRequestFiles` retorna mais de 100 itens em repositório simulado com paginação `Link`.
- `ListProjectIssues` nunca altera `.hivemind/team.yaml` em disco.
- Nenhum path com metacaracteres (`-`, `:(top)`) falha em `DiscardFileChanges`.
