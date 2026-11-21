package idecore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/githubx"
)

const graphqlPRPageSize = 50
const graphqlPRMaxPages = 4

// graphqlPRNode mirrors one pullRequests GraphQL node.
type graphqlPRNode struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	URL            string `json:"url"`
	State          string `json:"state"`
	IsDraft        bool   `json:"isDraft"`
	HeadRefName    string `json:"headRefName"`
	BaseRefName    string `json:"baseRefName"`
	UpdatedAt      string `json:"updatedAt"`
	ReviewDecision string `json:"reviewDecision"`
	Author         *struct {
		Login string `json:"login"`
	} `json:"author"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer struct {
				Login string `json:"login"`
			} `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	Reviews struct {
		Nodes []struct {
			State       string `json:"state"`
			Body        string `json:"body"`
			SubmittedAt string `json:"submittedAt"`
			Author      *struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"nodes"`
	} `json:"reviews"`
	Comments struct {
		Nodes []struct {
			Body      string `json:"body"`
			CreatedAt string `json:"createdAt"`
			Author    *struct {
				Login string `json:"login"`
			} `json:"author"`
		} `json:"nodes"`
	} `json:"comments"`
	ReviewThreads struct {
		Nodes []struct {
			Comments struct {
				Nodes []struct {
					Body      string `json:"body"`
					CreatedAt string `json:"createdAt"`
					Author    *struct {
						Login string `json:"login"`
					} `json:"author"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				CommittedDate string `json:"committedDate"`
				PushedDate    string `json:"pushedDate"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

const prsGraphQLQuery = `query($owner:String!,$repo:String!,$after:String){
  repository(owner:$owner,name:$repo){
    pullRequests(states:OPEN,first:` + "%d" + `,after:$after,orderBy:{field:UPDATED_AT,direction:DESC}){
      pageInfo{ hasNextPage endCursor }
      nodes{
        number
        title
        body
        url
        state
        isDraft
        headRefName
        baseRefName
        updatedAt
        reviewDecision
        author{ login }
        reviewRequests(first:20){
          nodes{
            requestedReviewer{
              ... on User { login }
            }
          }
        }
        reviews(last:50){
          nodes{
            state
            body
            submittedAt
            author{ login }
          }
        }
        comments(last:50){
          nodes{
            body
            createdAt
            author{ login }
          }
        }
        reviewThreads(last:30){
          nodes{
            comments(last:20){
              nodes{
                body
                createdAt
                author{ login }
              }
            }
          }
        }
        commits(last:1){
          nodes{
            commit{
              committedDate
              pushedDate
            }
          }
        }
      }
    }
  }
}`

// listPullRequestsGraphQL fetches open pull requests through the GraphQL API,
// following pageInfo.endCursor so repos with more open PRs than a single page
// are no longer silently truncated (Spec 08 pagination).
func listPullRequestsGraphQL(ctx context.Context, baseURL, owner, repo, token, currentUser string) ([]ProjectPullRequest, error) {
	var out []ProjectPullRequest
	me := strings.TrimSpace(currentUser)
	after := ""
	for page := 0; page < graphqlPRMaxPages; page++ {
		nodes, hasNext, endCursor, err := fetchPRsGraphQLPage(ctx, baseURL, owner, repo, token, after)
		if err != nil {
			return nil, err
		}
		out = append(out, mapGraphQLPRNodes(nodes, me)...)
		if !hasNext || endCursor == "" || len(nodes) == 0 {
			break
		}
		after = endCursor
	}
	return out, nil
}

// fetchPRsGraphQLPage performs ONE GraphQL page request using the shared
// githubx HTTP client and returns the raw nodes plus the cursor state.
func fetchPRsGraphQLPage(ctx context.Context, baseURL, owner, repo, token, after string) (nodes []graphqlPRNode, hasNext bool, endCursor string, err error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = githubx.DefaultAPIBaseURL
	}
	endpoint := base + "/graphql"
	if strings.HasSuffix(base, "/api/v3") {
		endpoint = strings.TrimSuffix(base, "/v3") + "/graphql"
	}
	query := fmt.Sprintf(prsGraphQLQuery, graphqlPRPageSize)
	payloadBytes, err := json.Marshal(map[string]any{
		"query": query,
		"variables": map[string]string{
			"owner": owner,
			"repo":  repo,
			"after": after,
		},
	})
	if err != nil {
		return nil, false, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, false, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "HiveMind-IDE")

	resp, err := githubx.SharedHTTPClient().Do(req)
	if err != nil {
		return nil, false, "", err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()
	if readErr != nil {
		return nil, false, "", readErr
	}
	if resp.StatusCode >= 300 {
		return nil, false, "", fmt.Errorf("github graphql %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var env struct {
		Data struct {
			Repository struct {
				PullRequests struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []graphqlPRNode `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, false, "", err
	}
	if len(env.Errors) > 0 && len(env.Data.Repository.PullRequests.Nodes) == 0 {
		return nil, false, "", fmt.Errorf("github graphql: %s", env.Errors[0].Message)
	}
	return env.Data.Repository.PullRequests.Nodes,
		env.Data.Repository.PullRequests.PageInfo.HasNextPage,
		env.Data.Repository.PullRequests.PageInfo.EndCursor,
		nil
}

// mapGraphQLPRNodes converts raw GraphQL nodes into ProjectPullRequest entries.
func mapGraphQLPRNodes(nodes []graphqlPRNode, me string) []ProjectPullRequest {
	var out []ProjectPullRequest
	for _, n := range nodes {
		author := ""
		if n.Author != nil {
			author = strings.TrimSpace(n.Author.Login)
		}
		isMine := me != "" && strings.EqualFold(author, me)
		var reqs []string
		reviewRequested := false
		for _, rr := range n.ReviewRequests.Nodes {
			login := strings.TrimSpace(rr.RequestedReviewer.Login)
			if login == "" {
				continue
			}
			reqs = append(reqs, login)
			if me != "" && strings.EqualFold(login, me) {
				reviewRequested = true
			}
		}

		revs := make([]prActivityReview, 0, len(n.Reviews.Nodes))
		for _, rv := range n.Reviews.Nodes {
			login := ""
			if rv.Author != nil {
				login = rv.Author.Login
			}
			revs = append(revs, prActivityReview{
				Author:      login,
				State:       rv.State,
				Body:        rv.Body,
				SubmittedAt: parseGHTime(rv.SubmittedAt),
			})
		}

		cmts := make([]prActivityComment, 0, len(n.Comments.Nodes)+len(n.ReviewThreads.Nodes))
		for _, cm := range n.Comments.Nodes {
			login := ""
			if cm.Author != nil {
				login = cm.Author.Login
			}
			cmts = append(cmts, prActivityComment{
				Author:    login,
				Body:      cm.Body,
				CreatedAt: parseGHTime(cm.CreatedAt),
			})
		}
		for _, th := range n.ReviewThreads.Nodes {
			for _, tc := range th.Comments.Nodes {
				login := ""
				if tc.Author != nil {
					login = tc.Author.Login
				}
				cmts = append(cmts, prActivityComment{
					Author:    login,
					Body:      tc.Body,
					CreatedAt: parseGHTime(tc.CreatedAt),
				})
			}
		}

		var latestCommitAt time.Time
		for _, cn := range n.Commits.Nodes {
			if ts := parseGHTime(cn.Commit.PushedDate); ts.After(latestCommitAt) {
				latestCommitAt = ts
			} else if ts := parseGHTime(cn.Commit.CommittedDate); ts.After(latestCommitAt) {
				latestCommitAt = ts
			}
		}

		flow := computePRFlowDetails(me, isMine, reviewRequested, len(reqs) > 0, author, n.Body, parseGHTime(n.UpdatedAt), revs, cmts, latestCommitAt)

		out = append(out, ProjectPullRequest{
			Number:              n.Number,
			Title:               n.Title,
			Body:                n.Body,
			URL:                 n.URL,
			State:               n.State,
			IsDraft:             n.IsDraft,
			Author:              author,
			HeadRef:             n.HeadRefName,
			BaseRef:             n.BaseRefName,
			UpdatedAt:           n.UpdatedAt,
			ReviewDecision:      n.ReviewDecision,
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
	return out
}
func listPullRequestsREST(ctx context.Context, baseURL, owner, repo, token, currentUser string) ([]ProjectPullRequest, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = githubx.DefaultAPIBaseURL
	}
	me := strings.TrimSpace(currentUser)
	var prs []ProjectPullRequest
	// Paginated by the Link header (up to 10 pages of 100) via the shared
	// githubx HTTP client.
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=open&per_page=100", url.PathEscape(owner), url.PathEscape(repo))
	for page := 0; page < 10; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
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
			Number    int    `json:"number"`
			Title     string `json:"title"`
			Body      string `json:"body"`
			HTMLURL   string `json:"html_url"`
			State     string `json:"state"`
			Draft     bool   `json:"draft"`
			UpdatedAt string `json:"updated_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
			Head struct {
				Ref string `json:"ref"`
			} `json:"head"`
			Base struct {
				Ref string `json:"ref"`
			} `json:"base"`
			RequestedReviewers []struct {
				Login string `json:"login"`
			} `json:"requested_reviewers"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return nil, err
		}
		for _, item := range raw {
			author := strings.TrimSpace(item.User.Login)
			var reqs []string
			reviewReq := false
			for _, rr := range item.RequestedReviewers {
				login := strings.TrimSpace(rr.Login)
				if login == "" {
					continue
				}
				reqs = append(reqs, login)
				if me != "" && strings.EqualFold(login, me) {
					reviewReq = true
				}
			}
			prs = append(prs, ProjectPullRequest{
				Number:             item.Number,
				Title:              item.Title,
				Body:               item.Body,
				URL:                item.HTMLURL,
				State:              strings.ToUpper(item.State),
				IsDraft:            item.Draft,
				Author:             author,
				HeadRef:            item.Head.Ref,
				BaseRef:            item.Base.Ref,
				UpdatedAt:          item.UpdatedAt,
				RequestedReviewers: reqs,
				IsMine:             me != "" && strings.EqualFold(author, me),
				ReviewRequested:    reviewReq,
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
	return prs, nil
}
