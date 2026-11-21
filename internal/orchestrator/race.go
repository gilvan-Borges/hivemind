package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/session"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/jev"
	"github.com/gilvan-Borges/HiveMind/internal/pathsec"
)

// RaceCandidateResult records the outcome, changed files, cost, and score for one Race Mode candidate.
type RaceCandidateResult struct {
	Index        int      `json:"index"`
	Branch       string   `json:"branch"`
	Worktree     string   `json:"worktree"`
	ChangedFiles []string `json:"changedFiles,omitempty"`
	BlockedFiles []string `json:"blockedFiles,omitempty"`
	SyncedFiles  []string `json:"syncedFiles,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	CostMicroUSD int64    `json:"costMicroUsd,omitempty"`
	TotalTokens  int64    `json:"totalTokens,omitempty"`
	Passed       bool     `json:"passed"`
	Score        int64    `json:"score"`
	Err          string   `json:"err,omitempty"`
}

// ParseRaceCount parses "race" (default 2) or "race:N" clamped to [2, 4].
func ParseRaceCount(runMode string) (int, bool) {
	m := strings.ToLower(strings.TrimSpace(runMode))
	if m == "race" {
		return 2, true
	}
	if strings.HasPrefix(m, "race:") {
		raw := strings.TrimSpace(strings.TrimPrefix(m, "race:"))
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 2, true
		}
		if n < 2 {
			n = 2
		}
		if n > 4 {
			n = 4
		}
		return n, true
	}
	return 0, false
}

func (r *Runner) runRace(ctx context.Context, req Request, sess session.Session, objective, key string, count int) (Result, error) {
	var res Result
	if sess != nil {
		res.SessionID = sess.ID()
	}
	if count < 2 {
		count = 2
	}
	if count > 4 {
		count = 4
	}
	if !r.Git.IsRepo(ctx, r.Workspace) {
		return res, fmt.Errorf("modo race requer um repositório Git inicializado em %s", r.Workspace)
	}
	if !r.Git.HasCommit(ctx, r.Workspace) {
		return res, fmt.Errorf("modo race requer ao menos um commit inicial no repositório")
	}

	var emitMu sync.Mutex
	emit := func(ev Event) {
		if r.OnEvent == nil {
			return
		}
		emitMu.Lock()
		defer emitMu.Unlock()
		r.OnEvent(ev)
	}

	emit(Event{
		Kind:   "race_start",
		Author: "race",
		Label:  "Race Mode",
		Text:   fmt.Sprintf("Disparando %d candidatos paralelos em worktrees isoladas…", count),
	})

	stamp := time.Now().UnixNano()
	candidates := make([]RaceCandidateResult, count)
	for i := 0; i < count; i++ {
		agentKey := fmt.Sprintf("race-%d-%d", stamp, i+1)
		branch := fmt.Sprintf("hivemind/race-%d-%d", stamp, i+1)
		wtPath, err := gitx.CreateWorktreeWithProgress(ctx, r.Git, r.Workspace, agentKey, branch, r.Project.Settings.SharedDirectories, nil)
		if err != nil {
			for j := 0; j < i; j++ {
				if candidates[j].Worktree != "" {
					_ = gitx.ArchiveWorktree(context.Background(), r.Git, r.Workspace, candidates[j].Worktree)
					_, _ = r.Git.Run(context.Background(), r.Workspace, "branch", "-D", candidates[j].Branch)
				}
			}
			return res, fmt.Errorf("preparar worktree do candidato %d: %w", i+1, err)
		}
		_, _ = gitx.CreateCheckpoint(ctx, r.Git, wtPath, fmt.Sprintf("race-%d", i+1), "pré-race")
		candidates[i] = RaceCandidateResult{
			Index:    i + 1,
			Branch:   branch,
			Worktree: wtPath,
		}
	}

	defer func() {
		for _, c := range candidates {
			if c.Worktree != "" {
				_ = gitx.ArchiveWorktree(context.Background(), r.Git, r.Workspace, c.Worktree)
			}
			if c.Branch != "" {
				_, _ = r.Git.Run(context.Background(), r.Workspace, "branch", "-D", c.Branch)
			}
		}
	}()

	var wg sync.WaitGroup
	for i := range candidates {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			c := &candidates[idx]
			var summary string
			var costMicro, totTok int64
			var runErr error

			if r.RaceCandidateExec != nil {
				summary, costMicro, totTok, runErr = r.RaceCandidateExec(ctx, c.Index, c.Worktree, objective)
			} else {
				childTeam := r.Team
				childProj := r.Project
				wtOff := false
				childProj.Settings.Worktrees = &wtOff
				childProj.Settings.AutoCommit = false
				childProj.Settings.AutoPR = false
				child := &Runner{
					Workspace:        c.Worktree,
					Project:          childProj,
					Team:             childTeam,
					Git:              r.Git,
					GitHub:           r.GitHub,
					Jira:             r.Jira,
					Models:           r.Models,
					NewModel:         r.NewModel,
					Confirm:          r.Confirm,
					Approve:          r.Approve,
					MCP:              r.MCP,
					NativeSkills:     r.NativeSkills,
					NativeSkillsRoot: r.NativeSkillsRoot,
					Catalog:          r.Catalog,
					Out:              r.Out,
					Err:              r.Err,
					Streaming:        r.Streaming,
					ExternalWorkers:  r.ExternalWorkers,
				}
				cRes, err := child.Run(ctx, Request{
					Objective:   objective,
					JiraKey:     key,
					LaneID:      fmt.Sprintf("%s-race-%d", req.LaneID, c.Index),
					RunMode:     "direct",
					Attachments: req.Attachments,
				})
				summary = cRes.Summary
				costMicro = cRes.CostMicroUSD
				totTok = cRes.TotalTokens
				runErr = err
			}

			c.Summary = strings.TrimSpace(summary)
			c.CostMicroUSD = costMicro
			c.TotalTokens = totTok
			if runErr != nil {
				c.Err = runErr.Error()
				c.Passed = false
			} else {
				c.Passed = true
			}

			changed, blocked, _ := inspectRaceWorktreeChanges(ctx, r.Git, c.Worktree)
			c.ChangedFiles = changed
			c.BlockedFiles = blocked
			c.Score = scoreRaceCandidate(*c)

			emit(Event{
				Kind:         "race_candidate",
				Author:       fmt.Sprintf("race-%d", c.Index),
				Label:        fmt.Sprintf("Candidato %d/%d", c.Index, count),
				Text:         c.Summary,
				Tokens:       int32(c.TotalTokens),
				CostMicroUSD: c.CostMicroUSD,
			})
		}(i)
	}
	wg.Wait()

	var totalTokens int64
	var totalCostMicro int64
	for _, c := range candidates {
		totalTokens += c.TotalTokens
		totalCostMicro += c.CostMicroUSD
	}
	res.TotalTokens = totalTokens
	res.CostMicroUSD = totalCostMicro

	winnerIdx := pickWinningRaceCandidate(candidates)
	if winnerIdx < 0 {
		return res, fmt.Errorf("nenhum candidato do Race Mode obteve sucesso")
	}
	winner := &candidates[winnerIdx]
	synced, syncErr := syncRaceWinnerFiles(ctx, r.Git, winner.Worktree, r.Workspace)
	if syncErr != nil {
		return res, fmt.Errorf("aplicar alterações do vencedor #%d: %w", winner.Index, syncErr)
	}
	winner.SyncedFiles = synced

	var b strings.Builder
	fmt.Fprintf(&b, "Race Mode (%d candidatos) — Vencedor: #%d (%d arquivos sincronizados, custo total %s):\n",
		count, winner.Index, len(synced), jev.FormatMicroUSD(totalCostMicro))
	for _, c := range candidates {
		status := "PASS"
		if !c.Passed {
			status = "FAIL"
		}
		mark := " "
		if c.Index == winner.Index {
			mark = "*"
		}
		fmt.Fprintf(&b, " %s #%d [%s] score=%d arquivos=%d tokens=%d custo=%s\n",
			mark, c.Index, status, c.Score, len(c.ChangedFiles), c.TotalTokens, jev.FormatMicroUSD(c.CostMicroUSD))
	}
	if winner.Summary != "" {
		b.WriteString("\nResumo do vencedor:\n")
		b.WriteString(winner.Summary)
	}
	res.Summary = strings.TrimSpace(b.String())

	emit(Event{
		Kind:         "race_winner",
		Author:       fmt.Sprintf("race-%d", winner.Index),
		Label:        fmt.Sprintf("Vencedor #%d", winner.Index),
		Text:         res.Summary,
		Tokens:       int32(totalTokens),
		CostMicroUSD: totalCostMicro,
	})
	return res, nil
}

func scoreRaceCandidate(c RaceCandidateResult) int64 {
	var score int64
	if c.Passed {
		score += 100
	}
	if len(c.ChangedFiles) > 0 {
		score += 50
	}
	if c.Summary != "" {
		score += 10
	}
	return score
}

func pickWinningRaceCandidate(candidates []RaceCandidateResult) int {
	bestIdx := -1
	for i, c := range candidates {
		if !c.Passed {
			continue
		}
		if bestIdx < 0 {
			bestIdx = i
			continue
		}
		best := candidates[bestIdx]
		if c.Score != best.Score {
			if c.Score > best.Score {
				bestIdx = i
			}
			continue
		}
		if c.CostMicroUSD != best.CostMicroUSD {
			if c.CostMicroUSD < best.CostMicroUSD {
				bestIdx = i
			}
			continue
		}
		if c.TotalTokens != best.TotalTokens {
			if c.TotalTokens < best.TotalTokens {
				bestIdx = i
			}
			continue
		}
		if c.Index < best.Index {
			bestIdx = i
		}
	}
	return bestIdx
}

type porcelainEntry struct {
	xy      string
	relPath string
}

func readPorcelainEntries(ctx context.Context, g gitx.Git, wtDir string) ([]porcelainEntry, error) {
	out, err := g.Run(ctx, wtDir, "status", "--porcelain", "-z", "-uall")
	if err != nil {
		return nil, err
	}
	records := strings.Split(out, "\x00")
	var entries []porcelainEntry
	for i := 0; i < len(records); i++ {
		rec := records[i]
		// Trim possible CRLF warnings preceding NUL record entries.
		if idx := strings.LastIndex(rec, "\n"); idx >= 0 {
			rec = rec[idx+1:]
		}
		if len(rec) < 3 {
			continue
		}
		// gitx.Local.Run calls strings.TrimSpace, which can strip the leading space
		// of the first entry (" M app.txt" -> "M app.txt"). Handle both intact
		// "XY<space>path" and leading-trimmed "Y<space>path".
		var xy, rawPath string
		switch {
		case len(rec) >= 4 && rec[2] == ' ':
			xy = rec[:2]
			rawPath = rec[3:]
		case rec[1] == ' ':
			xy = " " + rec[:1]
			rawPath = rec[2:]
		default:
			continue
		}
		rel := strings.TrimSpace(gitx.UnquotePath(rawPath))
		if rel == "" {
			continue
		}
		if strings.ContainsAny(xy, "RC") && i+1 < len(records) {
			// Skip old source path on rename/copy in -z format.
			i++
		}
		entries = append(entries, porcelainEntry{xy: xy, relPath: filepath.ToSlash(filepath.Clean(rel))})
	}
	return entries, nil
}

func inspectRaceWorktreeChanges(ctx context.Context, g gitx.Git, wtDir string) (safeFiles []string, blocked []string, err error) {
	entries, err := readPorcelainEntries(ctx, g, wtDir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if pathsec.IsSensitivePath(e.relPath) || strings.HasPrefix(e.relPath, "..") {
			blocked = append(blocked, e.relPath)
			continue
		}
		safeFiles = append(safeFiles, e.relPath)
	}
	sort.Strings(safeFiles)
	sort.Strings(blocked)
	return safeFiles, blocked, nil
}

func syncRaceWinnerFiles(ctx context.Context, g gitx.Git, wtDir, dstRoot string) ([]string, error) {
	entries, err := readPorcelainEntries(ctx, g, wtDir)
	if err != nil {
		return nil, err
	}
	var synced []string
	for _, e := range entries {
		cleanRel := filepath.Clean(filepath.FromSlash(e.relPath))
		if filepath.IsAbs(cleanRel) || strings.HasPrefix(cleanRel, "..") || pathsec.IsSensitivePath(e.relPath) {
			continue
		}
		src := filepath.Join(wtDir, cleanRel)
		dst := filepath.Join(dstRoot, cleanRel)
		if strings.ContainsRune(e.xy, 'D') {
			_ = os.Remove(dst)
			synced = append(synced, e.relPath)
			continue
		}
		info, statErr := os.Lstat(src)
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
			continue
		}
		data, readErr := os.ReadFile(src)
		if readErr != nil {
			return nil, readErr
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, data, info.Mode().Perm()); err != nil {
			return nil, err
		}
		synced = append(synced, e.relPath)
	}
	sort.Strings(synced)
	return synced, nil
}
