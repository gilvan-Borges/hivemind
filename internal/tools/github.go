package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jirax"
	"github.com/gilvan-Borges/HiveMind/internal/names"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

func (d Deps) addGitHubRead(out *[]tool.Tool) error {
	if d.GitHub == nil || !d.GitHub.Enabled() {
		return nil
	}
	getPR, err := functiontool.New(functiontool.Config{
		Name:        "github_get_pr",
		Description: "Lê detalhes completos de um Pull Request via conector nativo GitHub: autoria (indica se o PR é NOSSO ou de terceiro), branches (head -> base), revisores pendentes, revisões formais (APPROVED / CHANGES_REQUESTED), comentários inline no código (com arquivo, linha e comment_id) e comentários gerais na conversa.",
	}, func(ctx agent.Context, args struct {
		Number int `json:"number" jsonschema:"Número do pull request."`
	}) (textResult, error) {
		pr, err := d.GitHub.GetPullRequest(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		if len(pr.IssueComments) == 0 && !hasFetchError(pr.CommentFetchErrors, "comentários gerais") {
			if extra, cErr := d.GitHub.ListIssueComments(ctx, args.Number); cErr == nil {
				pr.IssueComments = extra
			} else {
				pr.CommentFetchErrors = append(pr.CommentFetchErrors, fmt.Sprintf("comentários gerais: %v", cErr))
			}
		}
		me, _ := d.GitHub.CurrentUser(ctx)
		return textResult{Text: formatPRDetail(pr, pr.IssueComments, me)}, nil
	})
	if err != nil {
		return err
	}
	comments, err := functiontool.New(functiontool.Config{
		Name:        "github_issue_comments",
		Description: "Lê todos os comentários de uma issue ou pull request no GitHub (comentários gerais na conversa + comentários inline de revisão no código quando for PR).",
	}, func(ctx agent.Context, args struct {
		Number int `json:"number" jsonschema:"Número da issue ou do pull request."`
	}) (textResult, error) {
		general, err := d.GitHub.ListIssueComments(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		inline, _ := d.GitHub.ListPullRequestComments(ctx, args.Number)
		var b strings.Builder
		if len(inline) > 0 {
			b.WriteString("[Comentários Inline no Código]\n")
			for _, c := range inline {
				loc := c.Path
				if c.Line > 0 {
					loc = fmt.Sprintf("%s:%d", c.Path, c.Line)
				}
				ts := ""
				if strings.TrimSpace(c.CreatedAt) != "" {
					ts = " (" + c.CreatedAt + ")"
				}
				reply := ""
				if c.InReplyToID > 0 {
					reply = fmt.Sprintf(" [em resposta a #%d]", c.InReplyToID)
				}
				fmt.Fprintf(&b, "- [comment_id=%d] @%s em %s%s%s: %s\n", c.ID, c.Author, loc, ts, reply, strings.TrimSpace(c.Body))
			}
		}
		if len(general) > 0 {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString("[Comentários Gerais]\n")
			for _, c := range general {
				ts := ""
				if strings.TrimSpace(c.CreatedAt) != "" {
					ts = " (" + c.CreatedAt + ")"
				}
				fmt.Fprintf(&b, "- @%s%s: %s\n", c.Author, ts, strings.TrimSpace(c.Body))
			}
		}
		if b.Len() == 0 {
			return textResult{Text: fmt.Sprintf("Nenhum comentário encontrado em #%d.", args.Number)}, nil
		}
		return textResult{Text: b.String()}, nil
	})
	if err != nil {
		return err
	}
	prFiles, err := functiontool.New(functiontool.Config{
		Name:        "github_pr_files",
		Description: "Lista apenas os caminhos e contagens (+add -del) dos arquivos alterados em um Pull Request sem trazer o patch completo. Ideal para mapear PRs grandes antes de filtrar github_pr_diff por arquivo.",
	}, func(ctx agent.Context, args struct {
		Number int `json:"number" jsonschema:"Número do pull request."`
	}) (textResult, error) {
		files, err := d.GitHub.ListPullRequestFiles(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		if len(files) == 0 {
			return textResult{Text: fmt.Sprintf("Nenhum arquivo alterado encontrado no PR #%d.", args.Number)}, nil
		}
		var b strings.Builder
		totalAdd, totalDel := 0, 0
		for _, f := range files {
			totalAdd += f.Additions
			totalDel += f.Deletions
		}
		fmt.Fprintf(&b, "PR #%d — %d arquivo(s) alterado(s) (+%d -%d):\n", args.Number, len(files), totalAdd, totalDel)
		for _, f := range files {
			fmt.Fprintf(&b, "- %s [%s] (+%d -%d)\n", f.Filename, f.Status, f.Additions, f.Deletions)
		}
		return textResult{Text: strings.TrimRight(b.String(), "\n")}, nil
	})
	if err != nil {
		return err
	}
	prDiff, err := functiontool.New(functiontool.Config{
		Name:        "github_pr_diff",
		Description: "Lista os arquivos modificados e os trechos de diff (patch) de um Pull Request no GitHub. Aceita filtro opcional por caminho de arquivo.",
	}, func(ctx agent.Context, args struct {
		Number int    `json:"number" jsonschema:"Número do pull request."`
		File   string `json:"file,omitempty" jsonschema:"Filtro opcional por nome/trecho do arquivo para ver o diff completo daquele arquivo."`
	}) (textResult, error) {
		files, err := d.GitHub.ListPullRequestFiles(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		filter := strings.ToLower(strings.TrimSpace(args.File))
		var b strings.Builder
		fmt.Fprintf(&b, "Arquivos alterados no PR #%d (%d arquivo(s)):\n", args.Number, len(files))
		const maxTotalChars = 24000
		for _, f := range files {
			if filter != "" && !strings.Contains(strings.ToLower(f.Filename), filter) {
				continue
			}
			fmt.Fprintf(&b, "\n=== %s [%s] (+%d -%d) ===\n", f.Filename, f.Status, f.Additions, f.Deletions)
			if strings.TrimSpace(f.Patch) != "" {
				remaining := maxTotalChars - b.Len()
				if remaining <= 200 {
					b.WriteString("... (diff truncado; informe o parâmetro file para ver arquivos específicos)\n")
					break
				}
				patch := f.Patch
				if filter == "" && len(patch) > 4000 {
					patch = strx.TruncateBytes(patch, 4000, "\n... (patch truncado; passe file=\""+f.Filename+"\" para ver completo)")
				} else if len(patch) > remaining {
					patch = strx.TruncateBytes(patch, remaining, "\n... (truncado)")
				}
				b.WriteString(patch + "\n")
			}
		}
		return textResult{Text: b.String()}, nil
	})
	if err != nil {
		return err
	}
	listPRs, err := functiontool.New(functiontool.Config{
		Name:        "github_list_prs",
		Description: "Lista Pull Requests do repositório no GitHub com autor, branches, revisores pedidos e marcação se o PR é NOSSO ou aguarda nossa revisão.",
	}, func(ctx agent.Context, args struct {
		State string `json:"state,omitempty" jsonschema:"Estado: open (padrão), closed ou all."`
		Limit int    `json:"limit,omitempty" jsonschema:"Quantidade máxima (padrão 20, máx 100)."`
	}) (textResult, error) {
		if args.Limit <= 0 {
			args.Limit = 20
		}
		prs, err := d.GitHub.ListPullRequests(ctx, args.State, args.Limit)
		if err != nil {
			return textResult{}, err
		}
		if len(prs) == 0 {
			return textResult{Text: "Nenhum Pull Request encontrado."}, nil
		}
		me, _ := d.GitHub.CurrentUser(ctx)
		var b strings.Builder
		for _, pr := range prs {
			tag := ""
			if me != "" && strings.EqualFold(pr.Author, me) {
				tag = " [NOSSO]"
			} else if me != "" {
				for _, rr := range pr.RequestedReviewers {
					if strings.EqualFold(rr, me) {
						tag = " [AGUARDA SUA REVISÃO]"
						break
					}
				}
			}
			draft := ""
			if pr.Draft {
				draft = " (DRAFT)"
			}
			fmt.Fprintf(&b, "#%d%s%s — %s | autor=@%s | %s -> %s", pr.Number, tag, draft, pr.Title, pr.Author, pr.HeadRef, pr.BaseRef)
			if len(pr.RequestedReviewers) > 0 {
				fmt.Fprintf(&b, " | revisores=[%s]", strings.Join(pr.RequestedReviewers, ", "))
			}
			b.WriteString("\n")
		}
		return textResult{Text: b.String()}, nil
	})
	if err != nil {
		return err
	}
	getIssue, err := functiontool.New(functiontool.Config{
		Name:        "github_get_issue",
		Description: "Lê detalhes completos e comentários de uma Issue do GitHub.",
	}, func(ctx agent.Context, args struct {
		Number int `json:"number" jsonschema:"Número da issue no GitHub."`
	}) (textResult, error) {
		iss, err := d.GitHub.GetIssue(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Issue #%d: %s\nAutor: @%s | Estado: %s\nURL: %s\n", iss.Number, iss.Title, iss.Author, iss.State, iss.URL)
		if len(iss.Labels) > 0 {
			fmt.Fprintf(&b, "Labels: %s\n", strings.Join(iss.Labels, ", "))
		}
		if strings.TrimSpace(iss.Body) != "" {
			fmt.Fprintf(&b, "\n[Descrição]\n%s\n", iss.Body)
		}
		if len(iss.Comments) > 0 {
			b.WriteString("\n[Comentários]\n")
			for _, c := range iss.Comments {
				fmt.Fprintf(&b, "- @%s: %s\n", c.Author, strings.TrimSpace(c.Body))
			}
		}
		return textResult{Text: b.String()}, nil
	})
	if err != nil {
		return err
	}
	ci, err := functiontool.New(functiontool.Config{
		Name:        "github_ci_status",
		Description: "Lê o status combinado de CI de um commit ou ref.",
	}, func(ctx agent.Context, args struct {
		Ref string `json:"ref" jsonschema:"SHA, branch ou tag."`
	}) (textResult, error) {
		state, err := d.GitHub.CIStatus(ctx, args.Ref)
		return textResult{Text: state}, err
	})
	if err != nil {
		return err
	}
	*out = append(*out, getPR, comments, prFiles, prDiff, listPRs, getIssue, ci)
	return nil
}

func hasFetchError(errs []string, prefix string) bool {
	for _, e := range errs {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

func (d Deps) addGitHubGovernance(out *[]tool.Tool, includeCreateFromWorktree bool) error {
	if err := d.addGitHubRead(out); err != nil {
		return err
	}
	if d.GitHub == nil || !d.GitHub.Enabled() {
		return nil
	}

	if includeCreateFromWorktree {
		create, err := functiontool.New(functiontool.Config{
			Name:        "github_create_pr",
			Description: "Exclusivo do Chefe/Arquiteto: abre um Pull Request da branch da worktree atual para a branch base (e opcionalmente solicita revisores).",
		}, func(ctx agent.Context, args struct {
			Title     string   `json:"title" jsonschema:"Título do pull request. Inclua a chave Jira quando houver."`
			Body      string   `json:"body" jsonschema:"Descrição do pull request."`
			Base      string   `json:"base,omitempty" jsonschema:"Branch de destino (ex: homolog ou main). Vazio usa a branch base do projeto."`
			Reviewers []string `json:"reviewers,omitempty" jsonschema:"Logins dos revisores no GitHub (opcional)."`
		}) (textResult, error) {
			head, err := d.Git.Run(ctx, d.Root, "rev-parse", "--abbrev-ref", "HEAD")
			if err != nil {
				return textResult{}, err
			}
			head = strings.TrimSpace(head)
			base := strings.TrimSpace(args.Base)
			if base == "" {
				base = d.BaseBranch
			}
			if base == "" {
				base = "main"
			}
			var pr githubx.PullRequest
			if existing, _ := d.GitHub.FindOpenPullRequestByHead(ctx, head); existing != nil {
				pr = *existing
			} else {
				pr, err = d.GitHub.CreatePullRequest(ctx, githubx.CreatePR{
					Title: args.Title,
					Body:  args.Body,
					Head:  head,
					Base:  base,
				})
				if err != nil {
					return textResult{}, err
				}
			}
			if len(args.Reviewers) > 0 && pr.Number > 0 {
				_ = d.GitHub.RequestReviewers(ctx, pr.Number, args.Reviewers)
			}
			return textResult{Text: pr.URL}, nil
		})
		if err != nil {
			return err
		}
		*out = append(*out, create)
	}

	var (
		govWriteMu   sync.Mutex
		reviewedPRs  = make(map[int]string)
		commentedPRs = make(map[string]bool)
	)

	commentPR, err := functiontool.New(functiontool.Config{
		Name:        "github_comment_pr",
		Description: "Comenta na conversa geral de um Pull Request. Permitido SOMENTE quando o PR é de sua própria autoria (ex: respondendo mudanças feitas após review) ou quando há pedido de revisão.",
	}, func(ctx agent.Context, args struct {
		Number int    `json:"number" jsonschema:"Número do pull request."`
		Body   string `json:"body" jsonschema:"Resumo das alterações realizadas ou resposta à revisão."`
	}) (textResult, error) {
		dedupKey := fmt.Sprintf("%d::%s", args.Number, strings.TrimSpace(args.Body))
		govWriteMu.Lock()
		alreadyCommented := commentedPRs[dedupKey]
		govWriteMu.Unlock()
		if alreadyCommented {
			return textResult{Text: fmt.Sprintf("BLOQUEIO DE DUPLICIDADE: este comentário já foi publicado no PR #%d nesta execução. Não envie comentários duplicados.", args.Number)}, nil
		}
		pr, err := d.GitHub.GetPullRequest(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		me, _ := d.GitHub.CurrentUser(ctx)
		if err := githubx.CheckPRCommentAllowed(pr, me, d.ReviewRequestedInObj); err != nil {
			return textResult{}, err
		}
		if err := d.GitHub.CommentOnIssue(ctx, args.Number, args.Body); err != nil {
			return textResult{}, err
		}
		govWriteMu.Lock()
		commentedPRs[dedupKey] = true
		govWriteMu.Unlock()
		return textResult{Text: fmt.Sprintf("comentário registrado no PR #%d", args.Number)}, nil
	})
	if err != nil {
		return err
	}

	replyComment, err := functiontool.New(functiontool.Config{
		Name:        "github_reply_review_comment",
		Description: "Responde diretamente dentro de uma thread de comentário inline de revisão no código (usando o comment_id retornado por github_get_pr ou github_issue_comments).",
	}, func(ctx agent.Context, args struct {
		Number    int    `json:"number" jsonschema:"Número do pull request."`
		CommentID int64  `json:"comment_id" jsonschema:"ID numérico do comentário inline ao qual responder."`
		Body      string `json:"body" jsonschema:"Resposta técnica para a thread inline."`
	}) (textResult, error) {
		pr, err := d.GitHub.GetPullRequest(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		me, _ := d.GitHub.CurrentUser(ctx)
		if err := githubx.CheckPRCommentAllowed(pr, me, d.ReviewRequestedInObj); err != nil {
			return textResult{}, err
		}
		if err := d.GitHub.ReplyToReviewComment(ctx, args.Number, args.CommentID, args.Body); err != nil {
			return textResult{}, err
		}
		return textResult{Text: fmt.Sprintf("resposta registrada no comentário inline #%d do PR #%d", args.CommentID, args.Number)}, nil
	})
	if err != nil {
		return err
	}

	reqReview, err := functiontool.New(functiontool.Config{
		Name:        "github_request_review",
		Description: "Solicita ou re-solicita revisão de um ou mais revisores em um PR de própria autoria (ex: após corrigir pontos pedidos pelo revisor).",
	}, func(ctx agent.Context, args struct {
		Number    int      `json:"number" jsonschema:"Número do pull request."`
		Reviewers []string `json:"reviewers" jsonschema:"Logins dos revisores a notificar para nova revisão."`
	}) (textResult, error) {
		pr, err := d.GitHub.GetPullRequest(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		me, _ := d.GitHub.CurrentUser(ctx)
		if me != "" && !strings.EqualFold(pr.Author, me) && !d.Settings.IsFullAccess() {
			return textResult{}, fmt.Errorf("governança PR: só é permitido solicitar revisores em PR de própria autoria (%s); PR #%d pertence a %q", me, pr.Number, pr.Author)
		}
		if err := d.GitHub.RequestReviewers(ctx, args.Number, args.Reviewers); err != nil {
			return textResult{}, err
		}
		return textResult{Text: fmt.Sprintf("revisão solicitada para %s no PR #%d", strings.Join(args.Reviewers, ", "), args.Number)}, nil
	})
	if err != nil {
		return err
	}

	reviewDesc := "Envia revisão oficial (APPROVE, REQUEST_CHANGES ou COMMENT) em um PR DE TERCEIRO. NUNCA use em PR de própria autoria. Permitido com pedido de revisão no GitHub ou quando o usuário pediu no chat para revisar, comentar, responder menção ou aprovar. Chame NO MÁXIMO UMA VEZ por PR. O parecer vai no body; um resumo só no chat não publica nada."
	if d.ReviewCommentOnly {
		reviewDesc = "Envia comentário de revisão (APENAS event=COMMENT) em um PR DE TERCEIRO. Modo automático configurado como somente-comentário: APPROVE e REQUEST_CHANGES são bloqueados. Chame NO MÁXIMO UMA VEZ por PR."
	}
	reviewPR, err := functiontool.New(functiontool.Config{
		Name:        "github_review_pr",
		Description: reviewDesc,
	}, func(ctx agent.Context, args struct {
		Number int    `json:"number" jsonschema:"Número do pull request."`
		Event  string `json:"event" jsonschema:"Decisão: APPROVE, REQUEST_CHANGES ou COMMENT."`
		Body   string `json:"body" jsonschema:"Parecer técnico da revisão."`
	}) (textResult, error) {
		govWriteMu.Lock()
		prevEvent, alreadyReviewed := reviewedPRs[args.Number]
		govWriteMu.Unlock()
		if alreadyReviewed {
			if ctx != nil && ctx.Actions() != nil {
				ctx.Actions().Escalate = true
			}
			return textResult{Text: fmt.Sprintf("BLOQUEIO DE DUPLICIDADE: a revisão oficial (%s) JÁ foi enviada no PR #%d nesta execução. É proibido enviar múltiplas revisões seguidas no mesmo PR. Encerre imediatamente sua resposta.", prevEvent, args.Number)}, nil
		}
		if d.ReviewCommentOnly && !strings.EqualFold(strings.TrimSpace(args.Event), "COMMENT") {
			return textResult{}, fmt.Errorf("governança auto-review: modo configurado para somente comentar (comment_only); use event=\"COMMENT\" em vez de %q", args.Event)
		}
		pr, err := d.GitHub.GetPullRequest(ctx, args.Number)
		if err != nil {
			return textResult{}, err
		}
		me, _ := d.GitHub.CurrentUser(ctx)
		if err := githubx.CheckPRReviewAllowed(pr, me, args.Event, d.ReviewRequestedInObj); err != nil {
			return textResult{}, err
		}
		if err := d.GitHub.SubmitReview(ctx, args.Number, args.Event, args.Body); err != nil {
			return textResult{}, err
		}
		evUpper := strings.ToUpper(strings.TrimSpace(args.Event))
		govWriteMu.Lock()
		reviewedPRs[args.Number] = evUpper
		govWriteMu.Unlock()
		return textResult{Text: fmt.Sprintf("revisão %s enviada com sucesso no PR #%d (1/1 — revisão concluída; NÃO chame github_review_pr novamente, apenas apresente o resumo final ao usuário)", evUpper, args.Number)}, nil
	})
	if err != nil {
		return err
	}

	*out = append(*out, commentPR, replyComment, reqReview, reviewPR)
	if !d.DisableMerge {
		mergePR, err := functiontool.New(functiontool.Config{
			Name:        "github_merge_pr",
			Description: "Exclusivo do Chefe/Arquiteto: mescla um PR APÓS aprovação do revisor — ou um PR do próprio usuário sem revisor externo quando o usuário ordenar explicitamente no chat (passe force_owner_merge=true). Automático quando a branch alvo é homolog; quando a branch alvo é main, solicita permissão explícita ao usuário (salvo em modo full_access). Quando o Jira estiver conectado e o PR referenciar uma task (ou jira_key for informado), move automaticamente a issue para Concluído/Done e registra comentário com o merge.",
		}, func(ctx agent.Context, args struct {
			Number          int    `json:"number" jsonschema:"Número do pull request."`
			Method          string `json:"method,omitempty" jsonschema:"Método: merge (padrão), squash ou rebase."`
			CommitTitle     string `json:"commit_title,omitempty" jsonschema:"Título opcional do commit de merge."`
			ForceOwnerMerge bool   `json:"force_owner_merge,omitempty" jsonschema:"Use true APENAS quando o usuário ordenou explicitamente no chat mesclar um PR DELE mesmo sem revisor externo (ex.: repositório solo)."`
			JiraKey         string `json:"jira_key,omitempty" jsonschema:"Chave opcional da task no Jira vinculada ao PR (ex.: PROJ-123) para mover para Concluído após o merge. Se omitido, detecta automaticamente pelo título/descrição/branch do PR."`
		}) (textResult, error) {
			pr, err := d.GitHub.GetPullRequest(ctx, args.Number)
			if err != nil {
				return textResult{}, err
			}
			if args.ForceOwnerMerge {
				me, _ := d.GitHub.CurrentUser(ctx)
				if err := githubx.CheckOwnerMergeAllowed(pr, me); err != nil {
					return textResult{}, err
				}
			} else {
				if err := githubx.CheckPRMergeAllowed(pr); err != nil {
					return textResult{}, err
				}
			}
			if d.Settings.NeedsMergePermission(pr.BaseRef) {
				approveFn := d.Approve
				if approveFn == nil && d.Confirm != nil {
					approveFn = func(prompt string) bool { return d.Confirm(prompt) }
				}
				prompt := fmt.Sprintf("O PR #%d (%s -> %s) já foi aprovado pelo revisor. Autoriza o Chefe a mesclar na branch protegida %q?", pr.Number, pr.HeadRef, pr.BaseRef, pr.BaseRef)
				if approveFn == nil || !approveFn(prompt) {
					return textResult{}, fmt.Errorf("governança PR: o PR #%d aponta para %q e exige permissão explícita do usuário antes de mesclar (revisor já aprovou, aguardando sua autorização)", pr.Number, pr.BaseRef)
				}
			}
			if err := d.GitHub.MergePullRequest(ctx, args.Number, args.Method, args.CommitTitle); err != nil {
				return textResult{}, err
			}
			// Automatically clean up worktrees whose branch was just merged.
			if d.Git != nil && strings.TrimSpace(pr.HeadRef) != "" {
				gitx.CleanupMergedWorktrees(ctx, d.Git, d.Root, pr.BaseRef)
			}
			msg := fmt.Sprintf("PR #%d mesclado com sucesso em %s", pr.Number, pr.BaseRef)
			if jiraNote := d.syncJiraAfterPRMerge(ctx, pr, args.JiraKey); jiraNote != "" {
				msg += ". " + jiraNote
			}
			return textResult{Text: msg}, nil
		})
		if err != nil {
			return err
		}
		*out = append(*out, mergePR)
	}
	return nil
}

// syncJiraAfterPRMerge transitions linked Jira issue(s) to Done/Concluído right
// after a PR is merged so merged work never leaves its Jira task stuck open.
func (d Deps) syncJiraAfterPRMerge(ctx context.Context, pr githubx.PullRequestDetail, explicitKey string) string {
	if d.Jira == nil || !d.Jira.Enabled() {
		return ""
	}
	type candidate struct {
		key      string
		explicit bool
	}
	seen := make(map[string]bool)
	var candidates []candidate
	addCand := func(raw string, explicit bool) {
		k := names.IssueKey(raw)
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		candidates = append(candidates, candidate{key: k, explicit: explicit})
	}
	addCand(explicitKey, true)
	addCand(d.JiraKey, true)
	for _, k := range jirax.ExtractIssueKeys(d.JiraProject, pr.Title, pr.Body, pr.HeadRef) {
		addCand(k, false)
		if len(candidates) >= 3 {
			break
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	var notes []string
	for _, c := range candidates {
		issue, err := d.Jira.GetIssue(ctx, c.key)
		if err != nil {
			if c.explicit {
				notes = append(notes, fmt.Sprintf("aviso Jira %s: não foi possível consultar (%v)", c.key, err))
			}
			continue
		}
		key := strings.TrimSpace(issue.Key)
		if key == "" {
			key = c.key
		}
		if jirax.IsDoneStatus(issue.Status) {
			notes = append(notes, fmt.Sprintf("Jira %s já estava em %s", key, issue.Status))
			continue
		}
		if err := d.Jira.Transition(ctx, key, "Concluído"); err != nil {
			notes = append(notes, fmt.Sprintf("aviso Jira %s: não foi possível mover para Concluído (%v) — use jira_transition manualmente", key, err))
			continue
		}
		commentBody := fmt.Sprintf("PR #%d mesclado em %s.", pr.Number, pr.BaseRef)
		if strings.TrimSpace(pr.URL) != "" {
			commentBody = fmt.Sprintf("PR #%d mesclado em %s (%s).", pr.Number, pr.BaseRef, strings.TrimSpace(pr.URL))
		}
		_ = d.Jira.Comment(ctx, key, commentBody)
		notes = append(notes, fmt.Sprintf("Jira %s atualizado para Concluído", key))
	}
	return strings.Join(notes, "; ")
}

func formatPRDetail(pr githubx.PullRequestDetail, issueComments []githubx.Comment, currentUser string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PR #%d: %s\nAutor: @%s | Estado: %s | Mesclado: %v\nBranches: %s -> %s\nURL: %s\n",
		pr.Number, pr.Title, pr.Author, pr.State, pr.Merged, pr.HeadRef, pr.BaseRef, pr.URL)
	if pr.ChangedFiles > 0 || pr.Additions > 0 || pr.Deletions > 0 {
		fmt.Fprintf(&b, "Alterações: %d arquivo(s) (+%d -%d)\n", pr.ChangedFiles, pr.Additions, pr.Deletions)
	}
	me := strings.TrimSpace(currentUser)
	if me != "" {
		if strings.EqualFold(strings.TrimSpace(pr.Author), me) {
			fmt.Fprintf(&b, "Papel neste PR: PR NOSSO (autor = você, @%s). VOCÊ NÃO É O REVISOR — leia abaixo os feedbacks dos revisores externos (revisões, comentários inline e comentários gerais) e atenda/corrija o que foi solicitado.\n", me)
		} else {
			fmt.Fprintf(&b, "Papel neste PR: PR DE TERCEIRO (autor: @%s | usuário autenticado: @%s).\n", pr.Author, me)
		}
	}
	if len(pr.RequestedReviewers) > 0 {
		fmt.Fprintf(&b, "Revisores aguardando resposta: %s\n", strings.Join(pr.RequestedReviewers, ", "))
	}
	approved, reason := githubx.HasReviewerApproval(pr)
	if approved {
		b.WriteString("Status de aprovação: APROVADO por revisor\n")
	} else {
		fmt.Fprintf(&b, "Status de aprovação: PENDENTE (%s)\n", reason)
	}
	if len(pr.CommentFetchErrors) > 0 {
		fmt.Fprintf(&b, "AVISO ao ler feedbacks do GitHub: %s\n", strings.Join(pr.CommentFetchErrors, " | "))
	}
	if strings.TrimSpace(pr.Body) != "" {
		fmt.Fprintf(&b, "\n[Descrição]\n%s\n", pr.Body)
	}
	if len(pr.Reviews) > 0 {
		b.WriteString("\n[Revisões Formais]\n")
		for _, rv := range pr.Reviews {
			ts := ""
			if strings.TrimSpace(rv.SubmittedAt) != "" {
				ts = " (" + rv.SubmittedAt + ")"
			}
			roleTag := ""
			if me != "" && strings.EqualFold(rv.Author, pr.Author) {
				roleTag = " [autor]"
			}
			fmt.Fprintf(&b, "- @%s%s [%s]%s: %s\n", rv.Author, roleTag, rv.State, ts, strings.TrimSpace(rv.Body))
		}
	}
	if len(pr.ReviewComments) > 0 {
		b.WriteString("\n[Comentários Inline no Código]\n")
		for _, c := range pr.ReviewComments {
			loc := c.Path
			if c.Line > 0 {
				loc = fmt.Sprintf("%s:%d", c.Path, c.Line)
			}
			ts := ""
			if strings.TrimSpace(c.CreatedAt) != "" {
				ts = " (" + c.CreatedAt + ")"
			}
			reply := ""
			if c.InReplyToID > 0 {
				reply = fmt.Sprintf(" [resposta a #%d]", c.InReplyToID)
			}
			fmt.Fprintf(&b, "- [comment_id=%d] @%s em %s%s%s: %s\n", c.ID, c.Author, loc, ts, reply, strings.TrimSpace(c.Body))
			if strings.TrimSpace(c.DiffHunk) != "" {
				hunk := strx.TruncateBytes(strings.TrimSpace(c.DiffHunk), 400, "...")
				fmt.Fprintf(&b, "  trecho: %s\n", strings.ReplaceAll(hunk, "\n", " | "))
			}
		}
	}
	if len(issueComments) > 0 {
		b.WriteString("\n[Comentários Gerais na Conversa]\n")
		for _, c := range issueComments {
			ts := ""
			if strings.TrimSpace(c.CreatedAt) != "" {
				ts = " (" + c.CreatedAt + ")"
			}
			roleTag := ""
			if strings.EqualFold(c.Author, pr.Author) {
				roleTag = " [autor]"
			}
			fmt.Fprintf(&b, "- @%s%s%s: %s\n", c.Author, roleTag, ts, strings.TrimSpace(c.Body))
		}
	}
	if len(pr.Reviews) == 0 && len(pr.ReviewComments) == 0 && len(issueComments) == 0 && len(pr.CommentFetchErrors) == 0 {
		b.WriteString("\n[Feedbacks]\nNenhuma revisão formal, comentário inline ou comentário geral registrado neste PR até o momento.\n")
	}
	return b.String()
}
