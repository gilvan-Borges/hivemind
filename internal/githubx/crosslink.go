package githubx

import (
	"fmt"
	"sort"
	"strings"
)

// CrossLinking of sibling pull requests in multi-repo deliveries (Spec 08 §3.1:
// "após abrir >= 2 PRs em uma execução composta (root + nested), atualizar o
// comentário/body vinculando os URLs irmãos").
//
// This helper is PURE: it computes the new PR bodies and never performs I/O.
// The orchestrator's `finish` (multi-repo delivery step) will plug it in AFTER
// the Wave 2 merge: after opening all PRs of a composed run (root repo +
// nested repos), call CrossLinkOpenPRs with the open PRs and update each body
// through the existing GitHub client.
//
// Plugging point (post-merge sketch, do NOT wire into orchestrator now):
//
//	bodies := githubx.CrossLinkOpenPRs(prs)
//	for number, body := range bodies {
//	    _ = gh.UpdatePullRequestBody(ctx, number, body)
//	}

// CrossLinkPR describes one pull request opened by the same composed delivery.
// Identity is (Repo, Number): a single repo may contribute more than one PR.
type CrossLinkPR struct {
	Number int    // PR number inside its own repository.
	Repo   string // Short repo label of the delivery (e.g. "hivemind", "HiveMind-App").
	URL    string // Absolute PR URL.
	Title  string // PR title (bullet label).
	Body   string // Current PR body; the map returned by CrossLinkOpenPRs carries the updated one.
}

// crossLinkHeader is the markdown section injected into each sibling body.
const crossLinkHeader = "## PRs relacionados"

// CrossLinkOpenPRs computes the updated body for every PR of a multi-repo
// delivery so each one cites its sibling PRs. With fewer than two PRs there is
// nothing to link: it returns nil. The result maps (repo, number) identity via
// PR number — callers with one PR per repo may use the number alone.
//
// The operation is IDEMPOTENT: an existing "## PRs relacionados" section is
// replaced in place (never duplicated) and every other part of the body
// (description, checklist, notes) is preserved.
func CrossLinkOpenPRs(prs []CrossLinkPR) map[int]string {
	if len(prs) < 2 {
		return nil
	}
	out := make(map[int]string, len(prs))
	for _, pr := range prs {
		siblings := siblingPRs(prs, pr)
		if len(siblings) == 0 {
			continue
		}
		out[pr.Number] = BuildCrossLinkBody(pr.Body, siblings)
	}
	return out
}

// siblingPRs returns every OTHER PR of the delivery, sorted by repo label then
// PR number for a deterministic bullet list.
func siblingPRs(prs []CrossLinkPR, self CrossLinkPR) []CrossLinkPR {
	var out []CrossLinkPR
	for _, p := range prs {
		if p.Repo == self.Repo && p.Number == self.Number {
			continue
		}
		if strings.TrimSpace(p.URL) == "" {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		return out[i].Number < out[j].Number
	})
	return out
}

// BuildCrossLinkBody returns body with an up-to-date "## PRs relacionados"
// section listing the siblings. Idempotent: an existing section is replaced in
// place; when none exists the section is appended after a blank line.
func BuildCrossLinkBody(body string, siblings []CrossLinkPR) string {
	var lines []string
	for _, sib := range siblings {
		label := strings.TrimSpace(sib.Title)
		repo := strings.TrimSpace(sib.Repo)
		url := strings.TrimSpace(sib.URL)
		if label == "" && url == "" {
			continue
		}
		switch {
		case repo != "" && label != "":
			lines = append(lines, fmt.Sprintf("- %s — %s (%s)", url, label, repo))
		case label != "":
			lines = append(lines, fmt.Sprintf("- %s — %s", url, label))
		default:
			lines = append(lines, fmt.Sprintf("- %s (%s)", url, repo))
		}
	}
	if len(lines) == 0 {
		return body
	}
	var b strings.Builder
	b.WriteString(crossLinkHeader + "\n")
	b.WriteString("Entrega multi-repo: este PR faz parte da mesma execução composta.\n")
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	section := strings.TrimRight(b.String(), "\n")
	return replaceCrossLinkSection(body, section)
}

// replaceCrossLinkSection swaps the existing cross-link section for the fresh
// one, preserving everything before and after it. When no section exists yet,
// it is appended separated by a blank line.
func replaceCrossLinkSection(body, section string) string {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	start := -1
	end := len(lines)
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if start < 0 && strings.EqualFold(trimmed, crossLinkHeader) {
			start = i
			continue
		}
		if start >= 0 && strings.HasPrefix(trimmed, "## ") {
			end = i
			break
		}
	}
	if start < 0 {
		base := strings.TrimRight(normalized, "\n")
		if strings.TrimSpace(base) == "" {
			return section
		}
		return base + "\n\n" + section
	}
	var out []string
	out = append(out, lines[:start]...)
	out = append(out, strings.Split(section, "\n")...)
	if end < len(lines) {
		out = append(out, lines[end:]...)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}
