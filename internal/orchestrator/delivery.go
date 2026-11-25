package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/v2/session"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/names"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func (r *Runner) detectGitHub(ctx context.Context) githubx.Client {
	url, err := r.Git.RemoteURL(ctx, r.Workspace)
	if err != nil {
		return githubx.Disabled{Reason: "repositório sem remote origin"}
	}
	owner, repo, ok := githubx.ParseRemoteForConfiguredHosts(url)
	if !ok {
		return githubx.Disabled{Reason: "remote origin não é um repositório GitHub"}
	}
	return githubx.Detect(owner, repo)
}

func (r *Runner) maxAllowedDevs() int {
	baseCount := len(r.Team.ByRole(team.RoleDeveloper))
	maxDevs := r.Project.Settings.EffectiveMaxDynamicDevs()
	if baseCount > maxDevs {
		return baseCount
	}
	return maxDevs
}

func (r *Runner) resolveDynamicDevs() []team.Agent {
	baseDevs := r.Team.ByRole(team.RoleDeveloper)
	if len(baseDevs) == 0 {
		return nil
	}
	desired := len(r.devSlices)
	if desired == 0 {
		return baseDevs
	}
	maxDevs := r.maxAllowedDevs()
	if desired > maxDevs {
		desired = maxDevs
	}
	if desired == 1 {
		return []team.Agent{baseDevs[0]}
	}

	out := make([]team.Agent, 0, desired)
	used := make(map[string]bool, desired)
	for i := 0; i < desired; i++ {
		var a team.Agent
		if i < len(baseDevs) {
			a = baseDevs[i]
			if len(baseDevs) == 1 && strings.EqualFold(a.Name, "dev") {
				a.Name = "dev_1"
			}
		} else {
			a = baseDevs[i%len(baseDevs)]
			a.Name = fmt.Sprintf("dev_%d", i+1)
		}
		if used[strings.ToLower(a.Name)] {
			for k := i + 1; k <= desired+10; k++ {
				cand := fmt.Sprintf("dev_%d", k)
				if !used[strings.ToLower(cand)] {
					a.Name = cand
					break
				}
			}
		}
		used[strings.ToLower(a.Name)] = true
		out = append(out, a)
	}
	return out
}

func (r *Runner) conversationSlot() string {
	raw := strings.TrimSpace(strings.ToLower(r.LaneID))
	if raw == "" {
		return "_integracao"
	}
	var b strings.Builder
	lastDash := false
	for _, ch := range raw {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b.WriteRune(ch)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	clean := strings.Trim(b.String(), "-")
	if clean == "" {
		return "_integracao"
	}
	return "lane-" + clean
}

func (r *Runner) prepareDevs(ctx context.Context, key, objective string) ([]devEnv, error) {
	agents := r.resolveDynamicDevs()
	envs := make([]devEnv, 0, len(agents))
	allowWorktrees := r.Project.Settings.Worktrees == nil || *r.Project.Settings.Worktrees
	hasGitRepo := r.Git.IsRepo(ctx, r.Workspace) && r.Git.HasCommit(ctx, r.Workspace)
	repo := allowWorktrees && hasGitRepo
	if !hasGitRepo {
		fmt.Fprintln(r.Err, "aviso: o workspace não tem um commit Git; os desenvolvedores compartilham a pasta e não usam worktrees")
	} else if !allowWorktrees {
		fmt.Fprintln(r.Err, "aviso: worktrees desativadas em settings.worktrees; os desenvolvedores compartilham a pasta do workspace")
	}

	_ = gitx.EnsureHiveIgnore(r.Workspace)

	slot := r.conversationSlot()
	if repo {
		targetBranch := strings.TrimSpace(r.prBranch)
		preserveExisting := targetBranch != ""
		if targetBranch == "" {
			targetBranch = names.DeliveryBranch(key, objective)
		}
		delivWT, effBranch, err := gitx.PrepareDeliveryWorktreeForSlot(ctx, r.Git, r.Workspace, slot, targetBranch, preserveExisting)
		if err != nil {
			return nil, fmt.Errorf("worktree da conversa (%s): %w", targetBranch, err)
		}
		r.integration = delivWT
		r.deliveryRoot = delivWT
		r.deliveryBranch = effBranch
		if nested, nErr := gitx.PrepareNestedWorktrees(ctx, r.Git, r.Workspace, delivWT, effBranch, preserveExisting); nErr != nil {
			fmt.Fprintf(r.Err, "aviso: worktree de sub-repositório: %v\n", nErr)
		} else {
			r.nestedWorktrees = nested
		}
		if hydRep, hErr := gitx.HydrateWorktree(r.Workspace, delivWT, r.Project.Settings.SharedDirectories); hErr != nil {
			fmt.Fprintf(r.Err, "aviso: hidratação de worktree: %v\n", hErr)
		} else {
			for _, w := range hydRep.Shared.Warnings {
				fmt.Fprintf(r.Out, "aviso: %s\n", w)
			}
			for _, b := range hydRep.Includes.Blocked {
				fmt.Fprintf(r.Err, "aviso: .worktreeinclude bloqueou %s\n", b)
			}
		}
	} else {
		r.deliveryRoot = r.Workspace
		for _, nr := range gitx.DiscoverNestedRepos(ctx, r.Git, r.Workspace) {
			r.nestedWorktrees = append(r.nestedWorktrees, gitx.NestedWorktree{
				NestedRepo:  nr,
				WorktreeDir: nr.RepoDir,
				Branch:      nr.BaseBranch,
			})
		}
	}

	disjointOwned := DisjointOwnedPaths(r.devOwnedPaths, len(agents))
	shareConversationWT := (!repo || len(agents) <= 1 || r.devSequential || disjointOwned) && !r.hasExternalWorkers(agents)
	r.sharedDevWorktree = shareConversationWT

	if r.sharedBoard == nil {
		r.sharedBoard = newTeamBoard()
	}

	devNames := make([]string, 0, len(agents))
	for idx, a := range agents {
		adkName := names.ADK(a.Name)
		branch := r.deliveryBranch
		root := r.deliveryRoot
		var owned []string
		if idx < len(r.devOwnedPaths) {
			owned = NormalizeOwnedPaths(r.devOwnedPaths[idx])
		}
		if shareConversationWT && len(owned) > 0 {
			r.sharedBoard.RegisterOwner(adkName, owned)
		} else if len(agents) > 1 && shareConversationWT {
			r.sharedBoard.RegisterOwner(adkName, nil)
		}
		var devNested []gitx.NestedWorktree
		if repo && !shareConversationWT {
			branch = names.Branch(key, a.Name, objective)
			wt, err := gitx.AlignDeveloperWorktree(ctx, r.Git, r.Workspace, adkName, branch, r.deliveryBranch)
			if err != nil {
				return nil, fmt.Errorf("worktree de %s: %w", a.Name, err)
			}
			root = wt
			if _, hErr := gitx.HydrateWorktree(r.Workspace, wt, r.Project.Settings.SharedDirectories); hErr != nil {
				fmt.Fprintf(r.Err, "aviso: hidratação da worktree de %s: %v\n", a.Name, hErr)
			}
			if cp, cpErr := gitx.CreateCheckpoint(ctx, r.Git, wt, key+"-"+adkName, "antes do dev "+a.Name); cpErr == nil && r.OnEvent != nil {
				r.OnEvent(Event{Kind: "checkpoint_created", Stage: "dev", Author: a.Name, Detail: cp.ID})
			}
			if aligned, aErr := gitx.AlignNestedWorktrees(ctx, r.Git, wt, branch, r.nestedWorktrees); aErr == nil {
				devNested = aligned
			}
		} else {
			if idx == 0 && repo && r.deliveryRoot != "" {
				if cp, cpErr := gitx.CreateCheckpoint(ctx, r.Git, r.deliveryRoot, key+"-shared", "antes do turno"); cpErr == nil && r.OnEvent != nil {
					r.OnEvent(Event{Kind: "checkpoint_created", Stage: "dev", Author: a.Name, Detail: cp.ID})
				}
			}
			devNested = r.nestedWorktrees
		}
		envs = append(envs, devEnv{Agent: a, ADK: adkName, Root: root, Branch: branch, OwnedPaths: owned, Nested: devNested})
		devNames = append(devNames, a.Name)
	}
	if r.OnEvent != nil && len(devNames) > 0 {
		r.OnEvent(Event{
			Kind:  "devs_scaled",
			Stage: "dev",
			Devs:  devNames,
		})
	}
	return envs, nil
}

type openedPRRef struct {
	client githubx.Client
	pr     githubx.CrossLinkPR
}

func (r *Runner) finish(ctx context.Context, sess session.Session, result *Result, devs []devEnv, key, objective string) error {
	if r.integration != "" {
		result.WorktreeDir = r.deliveryRoot
		result.DeliveryBranch = r.deliveryBranch
		if r.conversationSlot() == "_integracao" {
			result.IntegrationBranch = gitx.IntegrationBranch
		} else {
			result.IntegrationBranch = r.deliveryBranch
		}
	}
	base := strings.TrimSpace(r.Project.Settings.BaseBranch)
	if base == "" {
		base = "main"
	}

	commitRoot := r.deliveryRoot
	if commitRoot == "" {
		commitRoot = r.Workspace
	}
	commitBranch := strings.TrimSpace(r.deliveryBranch)
	if commitBranch == "" && r.Git.IsRepo(ctx, commitRoot) {
		commitBranch, _ = r.Git.CurrentBranch(ctx, commitRoot)
	}
	if commitBranch != "" {
		result.Branches = append(result.Branches, commitBranch)
	} else {
		for _, dev := range devs {
			if dev.Branch != "" {
				result.Branches = append(result.Branches, dev.Branch)
			}
		}
	}

	for _, nw := range r.nestedWorktrees {
		result.NestedDeliveries = append(result.NestedDeliveries, NestedDelivery{
			RelPath:     nw.RelPath,
			RepoDir:     nw.RepoDir,
			WorktreeDir: nw.WorktreeDir,
			Branch:      nw.Branch,
			BaseBranch:  nw.BaseBranch,
		})
	}

	if !qaApproved(sess) {
		result.PendingQA = true
		msg := "entrega NÃO aprovada pelo QA: nenhum commit, push ou PR foi feito; as alterações ficaram na worktree de entrega"
		fmt.Fprintf(r.Err, "aviso: %s\n", msg)
		if r.OnEvent != nil {
			r.OnEvent(Event{Kind: "qa_pending", Stage: "qa", Text: msg})
		}
		return nil
	}

	var notes []string
	var opened []openedPRRef
	pushedToRemote := false
	rootCommitted := false
	if (r.Project.Settings.AutoCommit || r.Project.Settings.AutoPR || r.prNumber > 0) && r.Git.IsRepo(ctx, commitRoot) {
		committed, err := r.Git.CommitAll(ctx, commitRoot, names.CommitMessage(key, objective))
		if err != nil {
			return fmt.Errorf("commit do Chefe/Arquiteto em %s: %w", commitBranch, err)
		}
		rootCommitted = committed
		gitx.UpdateIntegrationRef(ctx, r.Git, r.Workspace, commitRoot)
	}

	hasOrigin := false
	rootRemoteURL := ""
	if remURL, remErr := r.Git.RemoteURL(ctx, r.Workspace); remErr == nil {
		hasOrigin = true
		rootRemoteURL = strings.TrimSpace(remURL)
	}

	rootShouldDeliver := r.prNumber > 0 || len(r.nestedWorktrees) == 0 || rootCommitted || gitx.HasCommitsAhead(ctx, r.Git, r.Workspace, commitBranch, base)
	if r.prNumber > 0 && r.GitHub != nil && r.GitHub.Enabled() {
		if hasOrigin && commitBranch != "" {
			if err := r.Git.Push(ctx, commitRoot, "origin", commitBranch); err != nil {
				return fmt.Errorf("push de revisão %s: %w", commitBranch, err)
			}
			pushedToRemote = true
		}
		if len(r.prReviewers) > 0 {
			_ = r.GitHub.RequestReviewers(ctx, r.prNumber, r.prReviewers)
		}
	} else if r.Project.Settings.AutoPR && commitBranch != "" && rootShouldDeliver {
		if !hasOrigin || r.GitHub == nil || !r.GitHub.Enabled() {
			fmt.Fprintf(r.Err, "aviso: auto_pr está ativo, mas remote origin / GitHub não está configurado; alterações commitadas na worktree isolada (%s)\n", commitBranch)
		} else {
			if err := r.Git.Push(ctx, commitRoot, "origin", commitBranch); err != nil {
				return fmt.Errorf("push de %s: %w", commitBranch, err)
			}
			pushedToRemote = true
			var pr githubx.PullRequest
			var prBody string
			if existing, _ := r.GitHub.FindOpenPullRequestByHead(ctx, commitBranch); existing != nil {
				pr = *existing
				if det, gErr := r.GitHub.GetPullRequest(ctx, pr.Number); gErr == nil {
					prBody = det.Body
				}
			} else {
				prBody = fmt.Sprintf("Aberta pelo Chefe/Arquiteto HiveMind a partir de worktree isolada.\n\nSessão: %s\n", result.SessionID)
				if key != "" {
					prBody += "Issue: " + key + "\n"
				}
				createdPR, err := r.GitHub.CreatePullRequest(ctx, githubx.CreatePR{
					Title: names.PRTitle(key, objective),
					Head:  commitBranch,
					Base:  base,
					Body:  prBody,
				})
				if err != nil {
					return fmt.Errorf("pull request de %s: %w", commitBranch, err)
				}
				pr = createdPR
			}
			result.PullRequests = append(result.PullRequests, pr)
			notes = append(notes, pr.URL)
			opened = append(opened, openedPRRef{
				client: r.GitHub,
				pr: githubx.CrossLinkPR{
					Number: pr.Number,
					Repo:   filepath.Base(strings.TrimSpace(r.Workspace)),
					URL:    pr.URL,
					Title:  pr.Title,
					Body:   prBody,
				},
			})
			fmt.Fprintf(r.Out, "Pull request: %s\n", pr.URL)
		}
	}

	for i, nw := range r.nestedWorktrees {
		if !r.Git.IsRepo(ctx, nw.WorktreeDir) {
			continue
		}
		subCommitted := false
		if r.Project.Settings.AutoCommit || r.Project.Settings.AutoPR || r.prNumber > 0 {
			c, err := r.Git.CommitAll(ctx, nw.WorktreeDir, names.CommitMessage(key, objective))
			if err != nil {
				return fmt.Errorf("commit em sub-repositório %s (%s): %w", nw.RelPath, nw.Branch, err)
			}
			subCommitted = c
		}
		subBase := strings.TrimSpace(nw.BaseBranch)
		if subBase == "" {
			subBase = "main"
		}
		subDirtyOrAhead := subCommitted || gitx.HasCommitsAhead(ctx, r.Git, nw.RepoDir, nw.Branch, subBase)
		if r.Project.Settings.AutoPR && nw.Branch != "" && subDirtyOrAhead {
			subRem, remErr := r.Git.RemoteURL(ctx, nw.RepoDir)
			subClient := r.githubClientForNestedRepo(rootRemoteURL, strings.TrimSpace(subRem))
			if remErr != nil || subClient == nil || !subClient.Enabled() {
				fmt.Fprintf(r.Err, "aviso: auto_pr ativo em %s, mas remote origin / GitHub indisponível; alterações commitadas em (%s)\n", nw.RelPath, nw.Branch)
			} else {
				if err := r.Git.Push(ctx, nw.WorktreeDir, "origin", nw.Branch); err != nil {
					return fmt.Errorf("push de %s (%s): %w", nw.RelPath, nw.Branch, err)
				}
				pushedToRemote = true
				var pr githubx.PullRequest
				var prBody string
				if existing, _ := subClient.FindOpenPullRequestByHead(ctx, nw.Branch); existing != nil {
					pr = *existing
					if det, gErr := subClient.GetPullRequest(ctx, pr.Number); gErr == nil {
						prBody = det.Body
					}
				} else {
					prBody = fmt.Sprintf("Aberta pelo Chefe/Arquiteto HiveMind para o sub-repositório `%s`.\n\nSessão: %s\n", nw.RelPath, result.SessionID)
					if key != "" {
						prBody += "Issue: " + key + "\n"
					}
					createdPR, err := subClient.CreatePullRequest(ctx, githubx.CreatePR{
						Title: fmt.Sprintf("[%s] %s", nw.RelPath, names.PRTitle(key, objective)),
						Head:  nw.Branch,
						Base:  subBase,
						Body:  prBody,
					})
					if err != nil {
						return fmt.Errorf("pull request de %s (%s): %w", nw.RelPath, nw.Branch, err)
					}
					pr = createdPR
				}
				prCopy := pr
				result.NestedDeliveries[i].PullRequest = &prCopy
				result.PullRequests = append(result.PullRequests, pr)
				notes = append(notes, pr.URL)
				opened = append(opened, openedPRRef{
					client: subClient,
					pr: githubx.CrossLinkPR{
						Number: pr.Number,
						Repo:   nw.RelPath,
						URL:    pr.URL,
						Title:  pr.Title,
						Body:   prBody,
					},
				})
				fmt.Fprintf(r.Out, "Pull request (%s): %s\n", nw.RelPath, pr.URL)
			}
		}
	}

	if len(opened) >= 2 {
		r.crossLinkOpenedPRs(ctx, opened)
	}

	if rootCommitted || pushedToRemote {
		r.releaseScratchDevWorktrees(ctx, devs, commitRoot)
	}
	gitx.CleanupMergedWorktrees(ctx, r.Git, r.Workspace, base)
	for _, nw := range r.nestedWorktrees {
		gitx.CleanupMergedWorktrees(ctx, r.Git, nw.RepoDir, nw.BaseBranch)
	}

	if key != "" && r.Jira != nil && r.Jira.Enabled() {
		body := fmt.Sprintf("HiveMind concluiu a sessão %s.", result.SessionID)
		if len(result.Branches) > 0 {
			body += "\nBranches: " + strings.Join(result.Branches, ", ")
		}
		if len(notes) > 0 {
			body += "\nPull requests: " + strings.Join(notes, ", ")
		}
		if err := r.Jira.Comment(ctx, key, body); err != nil {
			return fmt.Errorf("comentário no Jira: %w", err)
		}
	}
	return nil
}

// releaseScratchDevWorktrees drops per-developer worktrees after the delivery
// worktree already holds the integrated result. The delivery worktree and its
// branch stay, so the PR head is not deleted out from under the checkout.
// Cleanup keeps running if the user already cancelled the turn.
func (r *Runner) releaseScratchDevWorktrees(ctx context.Context, devs []devEnv, commitRoot string) {
	if r == nil || r.Git == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancel()
	keepBranch := map[string]bool{}
	if br := strings.TrimSpace(r.deliveryBranch); br != "" {
		keepBranch[br] = true
	}
	for _, dev := range devs {
		if dev.Root == "" || sameCleanPath(dev.Root, r.Workspace) || sameCleanPath(dev.Root, commitRoot) || sameCleanPath(dev.Root, r.deliveryRoot) {
			continue
		}
		deleteBranch := ""
		if br := strings.TrimSpace(dev.Branch); br != "" && !keepBranch[br] {
			deleteBranch = br
		}
		gitx.RemoveWorktree(cleanupCtx, r.Git, r.Workspace, dev.Root, deleteBranch)
	}
}

func sameCleanPath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func (r *Runner) crossLinkOpenedPRs(ctx context.Context, opened []openedPRRef) {
	if len(opened) < 2 {
		return
	}
	for i, item := range opened {
		siblings := make([]githubx.CrossLinkPR, 0, len(opened)-1)
		for j, other := range opened {
			if i == j {
				continue
			}
			siblings = append(siblings, other.pr)
		}
		updatedBody := githubx.BuildCrossLinkBody(item.pr.Body, siblings)
		if updatedBody == item.pr.Body || item.client == nil {
			continue
		}
		if updater, ok := item.client.(githubx.PullRequestBodyUpdater); ok {
			if err := updater.UpdatePullRequestBody(ctx, item.pr.Number, updatedBody); err != nil && r.Err != nil {
				fmt.Fprintf(r.Err, "aviso: cross-link no PR #%d (%s): %v\n", item.pr.Number, item.pr.Repo, err)
			}
			continue
		}
		_ = item.client.CommentOnIssue(ctx, item.pr.Number, updatedBody)
	}
}

func qaApproved(sess session.Session) bool {
	if sess == nil {
		return false
	}
	return qaApprovedReadonly(sess.State())
}

func qaApprovedReadonly(st session.ReadonlyState) bool {
	if st == nil {
		return false
	}
	if v, err := st.Get("qa_approved"); err == nil && strings.TrimSpace(fmt.Sprint(v)) == "1" {
		return true
	}
	if v, err := st.Get("qa_approval_note"); err == nil && strings.TrimSpace(fmt.Sprint(v)) != "" {
		return true
	}
	return false
}

func isClosingPublishTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "git_commit",
		"git_push",
		"github_open_pr",
		"github_create_pr",
		"github_comment_pr",
		"github_reply_review_comment",
		"github_request_review",
		"github_merge_pr",
		"github_review_pr":
		return true
	default:
		return false
	}
}

func (r *Runner) githubClientForNestedRepo(rootRemoteURL, subRemoteURL string) githubx.Client {
	if r.GitHub == nil || !r.GitHub.Enabled() {
		return nil
	}
	if subRemoteURL != "" && strings.EqualFold(rootRemoteURL, subRemoteURL) {
		return r.GitHub
	}
	if owner, repo, ok := githubx.ParseRemoteForConfiguredHosts(subRemoteURL); ok {
		return githubx.Detect(owner, repo)
	}
	return nil
}
