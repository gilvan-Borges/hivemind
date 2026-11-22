package idecore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/remote"
)

// OpenPullRequest pushes the current branch and opens a GitHub PR via `gh`.
func OpenPullRequest(ctx context.Context, workspace, title, body, baseBranch string) (githubx.PullRequest, error) {
	g := gitx.Local{}
	branch, err := g.CurrentBranch(ctx, workspace)
	if err != nil || branch == "" {
		return githubx.PullRequest{}, fmt.Errorf("não foi possível identificar a branch atual")
	}
	if baseBranch == "" {
		baseBranch = "main"
	}
	if err := g.Push(ctx, workspace, "origin", branch); err != nil {
		return githubx.PullRequest{}, fmt.Errorf("push falhou: %w", err)
	}
	url, err := g.RemoteURL(ctx, workspace)
	if err != nil {
		return githubx.PullRequest{}, err
	}
	owner, repo, ok := githubx.ParseRemoteForConfiguredHosts(url)
	if !ok {
		return githubx.PullRequest{}, fmt.Errorf("remote origin não é GitHub: %s", url)
	}
	client := githubx.Detect(owner, repo)
	if !client.Enabled() {
		return githubx.PullRequest{}, fmt.Errorf("gh CLI não está autenticado ou disponível")
	}
	if strings.TrimSpace(title) == "" {
		title = "HiveMind: " + branch
	}
	if strings.TrimSpace(body) == "" {
		body = "Pull Request aberto pela HiveMind IDE."
	}
	return client.CreatePullRequest(ctx, githubx.CreatePR{
		Title: title,
		Head:  branch,
		Base:  baseBranch,
		Body:  body,
	})
}

// GitHubStatusInfo reports the native GitHub connector / CLI authentication status and repo remote.
type GitHubStatusInfo struct {
	Installed      bool   `json:"installed"`
	LoggedIn       bool   `json:"loggedIn"`
	User           string `json:"user"`
	Remote         string `json:"remote"`
	Detail         string `json:"detail"`
	Mode           string `json:"mode,omitempty"`           // "rest-env", "rest-config", "rest-gh", "cli", "none"
	Source         string `json:"source,omitempty"`         // "env", "config", "gh-token", "gh-cli", ""
	ConnectorLabel string `json:"connectorLabel,omitempty"` // human label
	TokenMasked    string `json:"tokenMasked,omitempty"`
	BaseURL        string `json:"baseUrl,omitempty"`
}

func githubConnectorLabel(mode string) string {
	switch mode {
	case "rest-config":
		return "API Nativa (Token salvo)"
	case "rest-env":
		return "API Nativa (ENV)"
	case "rest-gh":
		return "API Nativa (gh auth)"
	case "cli":
		return "gh CLI"
	default:
		return "Desconectado"
	}
}

// CheckGitHubStatus inspects the native GitHub REST connector (saved PAT, env var, or `gh auth token`),
// falls back to `gh` CLI when needed, and reads the workspace `origin` remote.
func CheckGitHubStatus(ctx context.Context, workspace string) GitHubStatusInfo {
	conn := githubx.InspectConnector()
	info := GitHubStatusInfo{
		Mode:           conn.Mode,
		Source:         conn.Source,
		ConnectorLabel: githubConnectorLabel(conn.Mode),
		TokenMasked:    conn.TokenMasked,
		BaseURL:        conn.BaseURL,
	}

	_, ghLookErr := exec.LookPath("gh")
	ghInstalled := ghLookErr == nil
	info.Installed = conn.Configured || ghInstalled

	token, baseURL, _ := githubx.LoadCredentials()
	if token != "" {
		client := &githubx.REST{BaseURL: baseURL, Token: token, Owner: "_", Repo: "_"}
		if user, err := client.CurrentUser(ctx); err == nil && strings.TrimSpace(user) != "" {
			info.LoggedIn = true
			info.User = strings.TrimSpace(user)
			info.Detail = fmt.Sprintf("Conectado como @%s (%s)", info.User, info.ConnectorLabel)
		} else if err != nil {
			info.Detail = fmt.Sprintf("Token GitHub inválido ou sem acesso (%v)", err)
		}
	}

	if !info.LoggedIn && ghInstalled {
		cmd := exec.CommandContext(ctx, "gh", "api", "user", "--jq", ".login")
		procx.ConfigureContext(cmd)
		var out, errBuf bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err == nil {
			info.LoggedIn = true
			info.User = strings.TrimSpace(out.String())
			info.Mode = "cli"
			info.Source = "gh-cli"
			info.ConnectorLabel = githubConnectorLabel("cli")
			info.Detail = "Conectado como @" + info.User + " (gh CLI)"
		} else if info.Detail == "" {
			info.Detail = "Não autenticado (configure o Conector GitHub ou rode `gh auth login`)"
		}
	}

	if !info.LoggedIn && info.Detail == "" {
		info.Detail = "Configure um Token GitHub no Conector Nativo ou autentique o `gh` CLI"
	}

	if workspace != "" {
		g := gitx.Local{}
		if g.IsRepo(ctx, workspace) {
			if url, err := g.RemoteURL(ctx, workspace); err == nil {
				info.Remote = url
			}
		}
	}
	return info
}

func listIssuesREST(ctx context.Context, baseURL, owner, repo, token, currentUser string) ([]ProjectIssue, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = githubx.DefaultAPIBaseURL
	}
	// Paginated by the Link header (up to githubx page cap) so repos with more
	// open issues than one page are no longer silently truncated.
	var out []ProjectIssue
	path := fmt.Sprintf("/repos/%s/%s/issues?state=open&per_page=100", url.PathEscape(owner), url.PathEscape(repo))
	for page := 0; page < 10; page++ {
		endpoint := base + path
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "HiveMind-IDE")

		resp, err := githubx.SharedHTTPClient().Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		linkHeader := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("github API %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		var raw []struct {
			Number      int             `json:"number"`
			Title       string          `json:"title"`
			Body        string          `json:"body"`
			State       string          `json:"state"`
			HTMLURL     string          `json:"html_url"`
			PullRequest json.RawMessage `json:"pull_request"`
			User        struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
		for _, it := range raw {
			if len(bytes.TrimSpace(it.PullRequest)) > 0 && string(bytes.TrimSpace(it.PullRequest)) != "null" {
				continue // GitHub's /issues endpoint also returns PRs; skip them here.
			}
			author := strings.TrimSpace(it.User.Login)
			out = append(out, ProjectIssue{
				Source: "github",
				Key:    fmt.Sprintf("#%d", it.Number),
				Title:  it.Title,
				Body:   it.Body,
				Status: strings.ToUpper(it.State),
				Author: author,
				URL:    it.HTMLURL,
				IsMine: currentUser != "" && strings.EqualFold(author, currentUser),
			})
		}
		next, ok := githubx.ParseLinkNext(linkHeader)
		if !ok || len(raw) == 0 {
			break
		}
		if u, err := url.Parse(next); err == nil && u.Path != "" {
			path = u.Path
			if u.RawQuery != "" {
				path += "?" + u.RawQuery
			}
		} else {
			break
		}
	}
	return out, nil
}

// ProjectPullRequest represents an open GitHub pull request listed in the IDE sidebar.
type ProjectPullRequest = remote.ProjectPullRequest

type prActivityReview struct {
	Author      string
	State       string
	Body        string
	SubmittedAt time.Time
}

type prActivityComment struct {
	Author          string
	Body            string
	ViewerDidAuthor bool
	CreatedAt       time.Time
}

type prFlowDetails struct {
	MyResponse          string
	NeedsAttention      bool
	AttentionReason     string
	MentionedMe         bool
	LastActivityAt      string
	LastActivityAuthor  string
	LastActivityKind    string
	LastActivitySnippet string
}

func isGitHubHandleChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-'
}

// mentionsUser reports whether body contains an @username mention with valid GitHub handle boundaries.
func mentionsUser(body, username string) bool {
	user := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(username, "@")))
	if user == "" || strings.TrimSpace(body) == "" {
		return false
	}
	lower := strings.ToLower(body)
	needle := "@" + user
	offset := 0
	for {
		idx := strings.Index(lower[offset:], needle)
		if idx < 0 {
			return false
		}
		pos := offset + idx
		end := pos + len(needle)
		beforeOK := pos == 0 || !isGitHubHandleChar(lower[pos-1]) && lower[pos-1] != '_' && lower[pos-1] != '.'
		afterOK := end == len(lower) || !isGitHubHandleChar(lower[end]) && lower[end] != '_'
		if beforeOK && afterOK {
			return true
		}
		offset = pos + 1
	}
}

func compactActivitySnippet(raw string, maxRunes int) string {
	s := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if s == "" || maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

func parseGHTime(raw string) time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return ts.UTC()
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts.UTC()
	}
	return time.Time{}
}

func isBotLogin(login string) bool {
	low := strings.ToLower(strings.TrimSpace(login))
	return strings.HasSuffix(low, "[bot]") || low == "dependabot" || low == "renovate" || low == "github-actions"
}

// computePRFlowState derives the user's latest response and whether the PR needs their attention now.
func computePRFlowState(me string, isMine, reviewRequested, hasPendingReviewers bool, reviews []prActivityReview, comments []prActivityComment, latestCommitAt time.Time) (myResp string, needsAttention bool, reason string) {
	det := computePRFlowDetails(me, isMine, reviewRequested, hasPendingReviewers, "", "", time.Time{}, reviews, comments, latestCommitAt)
	return det.MyResponse, det.NeedsAttention, det.AttentionReason
}

// computePRFlowDetails derives the full flow state plus mention and latest external activity metadata:
//   - Third-party PRs return to "Para revisar" when review is requested (or re-requested),
//     when another human mentions @me in the PR body/comment/review after currentUser's last action,
//     when another human comments/replies after currentUser's last review/comment,
//     or when a new commit is pushed after CHANGES_REQUESTED.
//   - Own PRs appear in active "Meus PRs" ONLY when action is needed from the author:
//     (1) an external reviewer approved and the PR is ready to merge, or
//     (2) another human commented/requested changes after the author's last comment or commit.
//     Otherwise (author pushed/commented last and is waiting for reviewer), it moves to "Aguardando / Já respondidos".
func computePRFlowDetails(
	me string,
	isMine, reviewRequested, hasPendingReviewers bool,
	prAuthor, prBody string,
	prUpdatedAt time.Time,
	reviews []prActivityReview,
	comments []prActivityComment,
	latestCommitAt time.Time,
) prFlowDetails {
	me = strings.TrimSpace(me)
	if me == "" {
		return prFlowDetails{}
	}
	var (
		myLastAt       time.Time
		otherLastAt    time.Time
		otherChangesAt time.Time
		otherAuthor    string
		otherKind      string
		otherSnippet   string
		mentionAt      time.Time
		mentionAuthor  string
		mentionSnippet string
		approveAt      time.Time
		approveAuthor  string
		approveSnippet string
		myResp         string
	)
	recordMy := func(ts time.Time) {
		if ts.After(myLastAt) {
			myLastAt = ts
		}
	}
	recordOther := func(author, kind, body string, ts time.Time) {
		a := strings.TrimSpace(author)
		if a == "" || strings.EqualFold(a, me) || isBotLogin(a) {
			return
		}
		if mentionsUser(body, me) && (mentionAt.IsZero() || ts.After(mentionAt)) {
			mentionAt = ts
			mentionAuthor = a
			mentionSnippet = compactActivitySnippet(body, 120)
		}
		if ts.After(otherLastAt) || (otherLastAt.IsZero() && otherAuthor == "") {
			otherLastAt = ts
			otherAuthor = a
			otherKind = kind
			otherSnippet = compactActivitySnippet(body, 120)
		}
	}

	// Check if another human mentioned @me directly in the PR description.
	if !isMine && prAuthor != "" && !strings.EqualFold(prAuthor, me) && !isBotLogin(prAuthor) && mentionsUser(prBody, me) {
		mentionAt = prUpdatedAt
		mentionAuthor = strings.TrimSpace(prAuthor)
		mentionSnippet = compactActivitySnippet(prBody, 120)
	}

	extLatestState := map[string]string{}
	for _, rv := range reviews {
		author := strings.TrimSpace(rv.Author)
		st := strings.ToUpper(strings.TrimSpace(rv.State))
		if strings.EqualFold(author, me) {
			recordMy(rv.SubmittedAt)
			if !isMine {
				switch st {
				case "APPROVED":
					myResp = "approved"
				case "CHANGES_REQUESTED":
					myResp = "changes_requested"
				case "COMMENTED":
					if myResp == "" {
						myResp = "commented"
					}
				}
			}
		} else {
			kind := "comment"
			if st == "CHANGES_REQUESTED" {
				kind = "review_changes"
			} else if st == "APPROVED" {
				kind = "review_approved"
			}
			recordOther(author, kind, rv.Body, rv.SubmittedAt)
			if author != "" && !isBotLogin(author) {
				low := strings.ToLower(author)
				switch st {
				case "APPROVED", "CHANGES_REQUESTED":
					extLatestState[low] = st
					if st == "CHANGES_REQUESTED" && rv.SubmittedAt.After(otherChangesAt) {
						otherChangesAt = rv.SubmittedAt
					}
					if st == "APPROVED" && (approveAt.IsZero() || rv.SubmittedAt.After(approveAt)) {
						approveAt = rv.SubmittedAt
						approveAuthor = author
						approveSnippet = compactActivitySnippet(rv.Body, 120)
					}
				case "DISMISSED":
					delete(extLatestState, low)
				}
			}
		}
	}

	for _, cm := range comments {
		if cm.ViewerDidAuthor || strings.EqualFold(strings.TrimSpace(cm.Author), me) {
			recordMy(cm.CreatedAt)
			if !isMine && myResp == "" {
				myResp = "commented"
			}
		} else {
			recordOther(cm.Author, "comment", cm.Body, cm.CreatedAt)
		}
	}

	formatTS := func(ts time.Time) string {
		if ts.IsZero() {
			return ""
		}
		return ts.UTC().Format(time.RFC3339)
	}

	if isMine {
		authorLast := myLastAt
		if latestCommitAt.After(authorLast) {
			authorLast = latestCommitAt
		}
		approvedCount := 0
		changesCount := 0
		for _, st := range extLatestState {
			if st == "APPROVED" {
				approvedCount++
			} else if st == "CHANGES_REQUESTED" {
				changesCount++
			}
		}
		// Ready to merge once at least one external reviewer approved with no pending re-review or changes requested.
		if !hasPendingReviewers && approvedCount > 0 && changesCount == 0 {
			return prFlowDetails{
				NeedsAttention:      true,
				AttentionReason:     "Pronto p/ merge",
				LastActivityAt:      formatTS(approveAt),
				LastActivityAuthor:  approveAuthor,
				LastActivityKind:    "review_approved",
				LastActivitySnippet: approveSnippet,
			}
		}
		// Needs author's reply/fix if an external human commented or reviewed AFTER the author's last comment/commit.
		if !otherLastAt.IsZero() && (authorLast.IsZero() || otherLastAt.After(authorLast)) {
			mentioned := mentionAuthor != "" && (authorLast.IsZero() || mentionAt.After(authorLast))
			if !otherChangesAt.IsZero() && (authorLast.IsZero() || otherChangesAt.After(authorLast)) {
				return prFlowDetails{
					NeedsAttention:      true,
					AttentionReason:     "Ajustes",
					MentionedMe:         mentioned,
					LastActivityAt:      formatTS(otherLastAt),
					LastActivityAuthor:  otherAuthor,
					LastActivityKind:    "review_changes",
					LastActivitySnippet: otherSnippet,
				}
			}
			kind := otherKind
			if mentioned {
				kind = "mention"
			}
			return prFlowDetails{
				NeedsAttention:      true,
				AttentionReason:     "Nova resposta",
				MentionedMe:         mentioned,
				LastActivityAt:      formatTS(otherLastAt),
				LastActivityAuthor:  otherAuthor,
				LastActivityKind:    kind,
				LastActivitySnippet: otherSnippet,
			}
		}
		return prFlowDetails{}
	}

	hasFreshMention := mentionAuthor != "" && (myLastAt.IsZero() || mentionAt.After(myLastAt))

	if myResp == "" {
		if hasFreshMention {
			return prFlowDetails{
				NeedsAttention:      true,
				AttentionReason:     "Você foi marcado",
				MentionedMe:         true,
				LastActivityAt:      formatTS(mentionAt),
				LastActivityAuthor:  mentionAuthor,
				LastActivityKind:    "mention",
				LastActivitySnippet: mentionSnippet,
			}
		}
		if reviewRequested {
			return prFlowDetails{
				NeedsAttention:   true,
				AttentionReason:  "Sua revisão",
				LastActivityKind: "review_requested",
			}
		}
		return prFlowDetails{}
	}

	// Already responded before — check if the PR needs attention again!
	if hasFreshMention {
		return prFlowDetails{
			MyResponse:          myResp,
			NeedsAttention:      true,
			AttentionReason:     "Você foi marcado",
			MentionedMe:         true,
			LastActivityAt:      formatTS(mentionAt),
			LastActivityAuthor:  mentionAuthor,
			LastActivityKind:    "mention",
			LastActivitySnippet: mentionSnippet,
		}
	}
	if reviewRequested && (myResp == "approved" || myResp == "changes_requested") {
		return prFlowDetails{
			MyResponse:       myResp,
			NeedsAttention:   true,
			AttentionReason:  "Re-revisão",
			LastActivityKind: "review_requested",
		}
	}
	if !myLastAt.IsZero() && otherLastAt.After(myLastAt) {
		return prFlowDetails{
			MyResponse:          myResp,
			NeedsAttention:      true,
			AttentionReason:     "Nova resposta",
			LastActivityAt:      formatTS(otherLastAt),
			LastActivityAuthor:  otherAuthor,
			LastActivityKind:    otherKind,
			LastActivitySnippet: otherSnippet,
		}
	}
	if myResp == "changes_requested" && !myLastAt.IsZero() && latestCommitAt.After(myLastAt) {
		return prFlowDetails{
			MyResponse:         myResp,
			NeedsAttention:     true,
			AttentionReason:    "Novo commit",
			LastActivityAt:     formatTS(latestCommitAt),
			LastActivityAuthor: strings.TrimSpace(prAuthor),
			LastActivityKind:   "commit",
		}
	}
	return prFlowDetails{MyResponse: myResp}
}

// ParseGHPRList parses the JSON output from `gh pr list --json ...` and enriches personal flow fields for currentUser.
func ParseGHPRList(data []byte, currentUser string) ([]ProjectPullRequest, error) {
	var raw []struct {
		Number         int    `json:"number"`
		Title          string `json:"title"`
		Body           string `json:"body"`
		URL            string `json:"url"`
		State          string `json:"state"`
		IsDraft        bool   `json:"isDraft"`
		UpdatedAt      string `json:"updatedAt"`
		HeadRef        string `json:"headRefName"`
		BaseRef        string `json:"baseRefName"`
		ReviewDecision string `json:"reviewDecision"`
		Author         struct {
			Login string `json:"login"`
		} `json:"author"`
		ReviewRequests []struct {
			Login string `json:"login"`
			Name  string `json:"name"`
			Slug  string `json:"slug"`
		} `json:"reviewRequests"`
		Reviews []struct {
			State       string `json:"state"`
			Body        string `json:"body"`
			SubmittedAt string `json:"submittedAt"`
			Author      struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"reviews"`
		Comments []struct {
			Body            string `json:"body"`
			ViewerDidAuthor bool   `json:"viewerDidAuthor"`
			CreatedAt       string `json:"createdAt"`
			Author          struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	me := strings.TrimSpace(currentUser)
	out := make([]ProjectPullRequest, 0, len(raw))
	for _, item := range raw {
		author := strings.TrimSpace(item.Author.Login)
		isMine := me != "" && strings.EqualFold(author, me)

		var reqs []string
		reviewRequested := false
		for _, rr := range item.ReviewRequests {
			name := strings.TrimSpace(rr.Login)
			if name == "" {
				name = strings.TrimSpace(rr.Slug)
			}
			if name == "" {
				name = strings.TrimSpace(rr.Name)
			}
			if name != "" {
				reqs = append(reqs, name)
				if me != "" && strings.EqualFold(name, me) {
					reviewRequested = true
				}
			}
		}

		revs := make([]prActivityReview, 0, len(item.Reviews))
		for _, rv := range item.Reviews {
			revs = append(revs, prActivityReview{
				Author:      rv.Author.Login,
				State:       rv.State,
				Body:        rv.Body,
				SubmittedAt: parseGHTime(rv.SubmittedAt),
			})
		}
		cmts := make([]prActivityComment, 0, len(item.Comments))
		for _, cm := range item.Comments {
			cmts = append(cmts, prActivityComment{
				Author:          cm.Author.Login,
				Body:            cm.Body,
				ViewerDidAuthor: cm.ViewerDidAuthor,
				CreatedAt:       parseGHTime(cm.CreatedAt),
			})
		}
		flow := computePRFlowDetails(me, isMine, reviewRequested, len(reqs) > 0, author, item.Body, parseGHTime(item.UpdatedAt), revs, cmts, time.Time{})

		out = append(out, ProjectPullRequest{
			Number:              item.Number,
			Title:               item.Title,
			Body:                item.Body,
			URL:                 item.URL,
			State:               item.State,
			IsDraft:             item.IsDraft,
			Author:              author,
			HeadRef:             item.HeadRef,
			BaseRef:             item.BaseRef,
			UpdatedAt:           item.UpdatedAt,
			ReviewDecision:      item.ReviewDecision,
			RequestedReviewers:  reqs,
			IsMine:              isMine,
			ReviewRequested:     reviewRequested,
			MyResponse:          flow.MyResponse,
			NeedsAttention:      flow.NeedsAttention,
			AttentionReason:     flow.AttentionReason,
			MentionedMe:         flow.MentionedMe,
			LastActivityAt:      flow.LastActivityAt,
			LastActivityAuthor:  flow.LastActivityAuthor,
			LastActivityKind:    flow.LastActivityKind,
			LastActivitySnippet: flow.LastActivitySnippet,
		})
	}
	return out, nil
}

// ListProjectPullRequests fetches open pull requests for a GitHub-backed workspace,
// aggregating across all discovered Git repositories (root and nested) when the
// workspace contains multiple repositories (e.g. monorepo/container with sibling apps).
func ListProjectPullRequests(ctx context.Context, workspace string) ([]ProjectPullRequest, error) {
	repos, err := DiscoverGitRepos(ctx, workspace)
	if err != nil || len(repos) <= 1 {
		target := workspace
		var repoName string
		if len(repos) == 1 {
			target = repos[0].Root
			repoName = repos[0].Name
		}
		prs, listErr := listPullRequestsForRepo(ctx, target)
		if len(repos) == 1 {
			for i := range prs {
				prs[i].Repo = repoName
				prs[i].RepoRoot = target
			}
		}
		return prs, listErr
	}

	type repoResult struct {
		prs []ProjectPullRequest
		err error
	}
	results := make([]repoResult, len(repos))
	var wg sync.WaitGroup
	for i, r := range repos {
		wg.Add(1)
		go func(idx int, repoInfo WorkspaceGitRepo) {
			defer wg.Done()
			prs, listErr := listPullRequestsForRepo(ctx, repoInfo.Root)
			for j := range prs {
				prs[j].Repo = repoInfo.Name
				prs[j].RepoRoot = repoInfo.Root
			}
			results[idx] = repoResult{prs: prs, err: listErr}
		}(i, r)
	}
	wg.Wait()

	var merged []ProjectPullRequest
	seenURL := make(map[string]bool)
	var firstErr error
	anyOK := false
	for _, res := range results {
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}
		anyOK = true
		for _, pr := range res.prs {
			if pr.URL != "" && seenURL[pr.URL] {
				continue
			}
			if pr.URL != "" {
				seenURL[pr.URL] = true
			}
			merged = append(merged, pr)
		}
	}
	if !anyOK && firstErr != nil {
		return nil, firstErr
	}
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].UpdatedAt > merged[j].UpdatedAt
	})
	return merged, nil
}

func listPullRequestsForRepo(ctx context.Context, workspace string) ([]ProjectPullRequest, error) {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspace) {
		return nil, nil
	}
	remoteURL, err := g.RemoteURL(ctx, workspace)
	if err != nil {
		return nil, nil
	}
	owner, repo, ok := githubx.ParseRemoteForConfiguredHosts(remoteURL)
	if !ok {
		return nil, nil
	}
	client := githubx.Detect(owner, repo)
	currentUser, _ := client.CurrentUser(ctx)

	token, baseURL, _ := githubx.LoadCredentials()
	if token != "" {
		if prs, gqlErr := listPullRequestsGraphQL(ctx, baseURL, owner, repo, token, currentUser); gqlErr == nil {
			return prs, nil
		}
	}

	if ghBin, err := exec.LookPath("gh"); err == nil {
		cmd := exec.CommandContext(ctx, ghBin,
			"pr", "list",
			"--repo", owner+"/"+repo,
			"--state", "open",
			"--limit", "30",
			"--json", "number,title,body,url,state,isDraft,author,headRefName,baseRefName,updatedAt,reviewRequests,reviews,comments,reviewDecision",
		)
		procx.ConfigureContext(cmd)
		var out, errBuf bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err == nil {
			if prs, parseErr := ParseGHPRList(out.Bytes(), currentUser); parseErr == nil {
				return prs, nil
			}
		} else if token == "" {
			return nil, fmt.Errorf("gh pr list: %s", strings.TrimSpace(errBuf.String()))
		}
	}

	if token == "" {
		return nil, fmt.Errorf("configure o Conector GitHub Nativo na IDE, defina GITHUB_TOKEN ou autentique o `gh` CLI")
	}
	return listPullRequestsREST(ctx, baseURL, owner, repo, token, currentUser)
}

// CloseOwnGitHubItem closes an open PR ("pr") or GitHub issue ("issue") ONLY if authored by the authenticated user.
// Uses the native GitHub REST connector when available, falling back to `gh` CLI.
func CloseOwnGitHubItem(ctx context.Context, workspace, kind string, number int) error {
	if number <= 0 {
		return fmt.Errorf("número inválido: %d", number)
	}
	normKind := strings.ToLower(strings.TrimSpace(kind))
	if normKind != "pr" && normKind != "issue" {
		return fmt.Errorf("tipo inválido: %s", kind)
	}

	g := gitx.Local{}
	if !g.IsRepo(ctx, workspace) {
		return fmt.Errorf("esta pasta não é um repositório Git")
	}
	remoteURL, err := g.RemoteURL(ctx, workspace)
	if err != nil {
		return err
	}
	owner, repo, ok := githubx.ParseRemoteForConfiguredHosts(remoteURL)
	if !ok {
		return fmt.Errorf("remote origin não é GitHub")
	}
	client := githubx.Detect(owner, repo)
	if !client.Enabled() {
		return fmt.Errorf("GitHub não está configurado. Configure o Conector GitHub Nativo ou autentique o `gh`")
	}
	currentUser, err := client.CurrentUser(ctx)
	if err != nil || strings.TrimSpace(currentUser) == "" {
		return fmt.Errorf("não foi possível identificar o usuário GitHub autenticado")
	}

	// Verify ownership directly on GitHub before closing.
	var author string
	if normKind == "pr" {
		pr, err := client.GetPullRequest(ctx, number)
		if err != nil {
			return fmt.Errorf("falha ao verificar autoria de pr #%d: %w", number, err)
		}
		author = pr.Author
	} else {
		iss, err := client.GetIssue(ctx, number)
		if err != nil {
			return fmt.Errorf("falha ao verificar autoria de issue #%d: %w", number, err)
		}
		author = iss.Author
	}
	if !strings.EqualFold(strings.TrimSpace(author), strings.TrimSpace(currentUser)) {
		return fmt.Errorf("somente itens abertos por @%s podem ser fechados aqui (autor: @%s)", currentUser, author)
	}

	if normKind == "pr" {
		if err := client.ClosePullRequest(ctx, number); err != nil {
			return fmt.Errorf("falha ao fechar pr #%d: %w", number, err)
		}
		return nil
	}
	if err := client.CloseIssue(ctx, number); err != nil {
		return fmt.Errorf("falha ao fechar issue #%d: %w", number, err)
	}
	return nil
}

// PREventItem representa um evento cronológico recente (revisão, comentário geral ou inline) de um PR.
type PREventItem struct {
	Kind      string `json:"kind"` // "approved" | "changes_requested" | "review_comment" | "comment" | "review"
	Author    string `json:"author"`
	Body      string `json:"body"`
	Path      string `json:"path,omitempty"`
	Line      int    `json:"line,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// PullRequestActivityDetail reúne metadados e os últimos eventos/comentários de um PR para inspeção rápida.
type PullRequestActivityDetail struct {
	Number             int           `json:"number"`
	Body               string        `json:"body,omitempty"`
	ChangedFiles       int           `json:"changedFiles,omitempty"`
	Additions          int           `json:"additions,omitempty"`
	Deletions          int           `json:"deletions,omitempty"`
	RequestedReviewers []string      `json:"requestedReviewers,omitempty"`
	Events             []PREventItem `json:"events"`
	TotalEvents        int           `json:"totalEvents"`
}

// BuildPullRequestActivityDetail agrega revisões e comentários em ordem cronológica decrescente.
func BuildPullRequestActivityDetail(detail githubx.PullRequestDetail, maxEvents int) PullRequestActivityDetail {
	events := make([]PREventItem, 0, len(detail.Reviews)+len(detail.ReviewComments)+len(detail.IssueComments))
	for _, r := range detail.Reviews {
		state := strings.ToUpper(strings.TrimSpace(r.State))
		body := strings.TrimSpace(r.Body)
		if state == "COMMENTED" && body == "" {
			// Comentários inline já chegam em ReviewComments; evita entradas vazias duplicadas.
			continue
		}
		kind := "review"
		switch state {
		case "APPROVED":
			kind = "approved"
		case "CHANGES_REQUESTED":
			kind = "changes_requested"
		case "COMMENTED":
			kind = "comment"
		}
		events = append(events, PREventItem{
			Kind:      kind,
			Author:    strings.TrimSpace(r.Author),
			Body:      body,
			CreatedAt: strings.TrimSpace(r.SubmittedAt),
		})
	}
	for _, c := range detail.ReviewComments {
		events = append(events, PREventItem{
			Kind:      "review_comment",
			Author:    strings.TrimSpace(c.Author),
			Body:      strings.TrimSpace(c.Body),
			Path:      strings.TrimSpace(c.Path),
			Line:      c.Line,
			CreatedAt: strings.TrimSpace(c.CreatedAt),
		})
	}
	for _, c := range detail.IssueComments {
		events = append(events, PREventItem{
			Kind:      "comment",
			Author:    strings.TrimSpace(c.Author),
			Body:      strings.TrimSpace(c.Body),
			CreatedAt: strings.TrimSpace(c.CreatedAt),
		})
	}

	sort.SliceStable(events, func(i, j int) bool {
		ti := parseGHTime(events[i].CreatedAt)
		tj := parseGHTime(events[j].CreatedAt)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return events[i].CreatedAt > events[j].CreatedAt
	})

	total := len(events)
	if maxEvents > 0 && len(events) > maxEvents {
		events = events[:maxEvents]
	}
	return PullRequestActivityDetail{
		Number:             detail.Number,
		Body:               strings.TrimSpace(detail.Body),
		ChangedFiles:       detail.ChangedFiles,
		Additions:          detail.Additions,
		Deletions:          detail.Deletions,
		RequestedReviewers: detail.RequestedReviewers,
		Events:             events,
		TotalEvents:        total,
	}
}

// GetProjectPullRequestDetail carrega revisões, comentários e estatísticas recentes de um PR sob demanda.
func GetProjectPullRequestDetail(ctx context.Context, workspace string, number int) (PullRequestActivityDetail, error) {
	if number <= 0 {
		return PullRequestActivityDetail{}, fmt.Errorf("número de PR inválido: %d", number)
	}
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspace) {
		return PullRequestActivityDetail{}, fmt.Errorf("esta pasta não é um repositório Git")
	}
	remoteURL, err := g.RemoteURL(ctx, workspace)
	if err != nil {
		return PullRequestActivityDetail{}, err
	}
	owner, repo, ok := githubx.ParseRemoteForConfiguredHosts(remoteURL)
	if !ok {
		return PullRequestActivityDetail{}, fmt.Errorf("remote origin não é GitHub")
	}
	client := githubx.Detect(owner, repo)
	if !client.Enabled() {
		return PullRequestActivityDetail{}, fmt.Errorf("GitHub não está configurado. Configure o Conector GitHub Nativo ou autentique o `gh`")
	}
	detail, err := client.GetPullRequest(ctx, number)
	if err != nil {
		return PullRequestActivityDetail{}, err
	}
	return BuildPullRequestActivityDetail(detail, 12), nil
}
