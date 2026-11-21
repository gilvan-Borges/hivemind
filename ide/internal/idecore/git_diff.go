package idecore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/remote"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// DiffLineKind classifies a line inside a unified diff.
type DiffLineKind = remote.DiffLineKind

const (
	DiffHeader  = remote.DiffHeader
	DiffHunk    = remote.DiffHunk
	DiffAdded   = remote.DiffAdded
	DiffRemoved = remote.DiffRemoved
	DiffContext = remote.DiffContext
)

// DiffLine is one colored line in the diff viewer.
type DiffLine = remote.DiffLine

// FileDiff holds the parsed patch and line stats for one modified file.
type FileDiff = remote.FileDiff

// ParseUnifiedDiff splits git unified diff output into per-file FileDiff entries.
func ParseUnifiedDiff(raw string) []FileDiff {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var diffs []FileDiff
	var current *FileDiff

	flush := func() {
		if current != nil {
			diffs = append(diffs, *current)
			current = nil
		}
	}

	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			path := parseDiffGitPath(line)
			current = &FileDiff{Path: path}
			current.Lines = append(current.Lines, DiffLine{Kind: DiffHeader, Text: line})
			continue
		}
		if current == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "index "),
			strings.HasPrefix(line, "--- "),
			strings.HasPrefix(line, "+++ "),
			strings.HasPrefix(line, "new file mode "),
			strings.HasPrefix(line, "deleted file mode "):
			current.Lines = append(current.Lines, DiffLine{Kind: DiffHeader, Text: line})
		case strings.HasPrefix(line, "@@"):
			current.Lines = append(current.Lines, DiffLine{Kind: DiffHunk, Text: line})
		case strings.HasPrefix(line, "+"):
			current.Added++
			current.Lines = append(current.Lines, DiffLine{Kind: DiffAdded, Text: line})
		case strings.HasPrefix(line, "-"):
			current.Removed++
			current.Lines = append(current.Lines, DiffLine{Kind: DiffRemoved, Text: line})
		default:
			current.Lines = append(current.Lines, DiffLine{Kind: DiffContext, Text: line})
		}
	}
	flush()
	return diffs
}

func parseDiffGitPath(header string) string {
	// Format: diff --git a/path b/path
	rest := strings.TrimPrefix(header, "diff --git ")
	if strings.HasPrefix(rest, `"`) {
		if _, second, ok := splitGitPathPair(rest); ok {
			return strings.TrimPrefix(gitx.UnquotePath(second), "b/")
		}
	}
	if idx := strings.LastIndex(rest, " b/"); idx >= 0 {
		return gitx.UnquotePath(strings.TrimSpace(rest[idx+3:]))
	}
	if fields := strings.Fields(rest); len(fields) >= 2 {
		return strings.TrimPrefix(gitx.UnquotePath(fields[len(fields)-1]), "b/")
	}
	return gitx.UnquotePath(header)
}

// splitGitPathPair splits the two quoted paths of a "diff --git" header.
func splitGitPathPair(s string) (first, second string, ok bool) {
	i := 0
	read := func() (string, bool) {
		for i < len(s) && s[i] == ' ' {
			i++
		}
		if i >= len(s) || s[i] != '"' {
			return "", false
		}
		start := i
		i++
		for i < len(s) {
			if s[i] == '\\' && i+1 < len(s) {
				i += 2
				continue
			}
			if s[i] == '"' {
				i++
				return s[start:i], true
			}
			i++
		}
		return "", false
	}
	a, okA := read()
	b, okB := read()
	if !okA || !okB {
		return "", "", false
	}
	return a, b, true
}

// WorkspaceDiff returns parsed unified diffs (staged, unstaged, and untracked new files) for workspace.
func WorkspaceDiff(ctx context.Context, workspaceDir string) ([]FileDiff, error) {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) {
		return nil, nil
	}
	raw, err := runGit(ctx, workspaceDir, "diff", "HEAD")
	if err != nil {
		// Repo without HEAD commit yet: fallback to plain diff
		raw, err = runGit(ctx, workspaceDir, "diff")
		if err != nil {
			return nil, err
		}
	}
	diffs := ParseUnifiedDiff(raw)
	seen := make(map[string]bool, len(diffs))
	for _, d := range diffs {
		seen[d.Path] = true
	}

	if untrackedRaw, uErr := runGit(ctx, workspaceDir, "ls-files", "--others", "--exclude-standard"); uErr == nil {
		for _, line := range strings.Split(strings.ReplaceAll(untrackedRaw, "\r\n", "\n"), "\n") {
			rel := gitx.UnquotePath(strings.TrimSpace(line))
			if rel == "" || seen[rel] || rel == ".hivemind" || strings.HasPrefix(rel, ".hivemind/") {
				continue
			}
			full, resErr := workspace.Resolve(workspaceDir, filepath.FromSlash(rel))
			if resErr != nil {
				continue
			}
			info, statErr := os.Stat(full)
			if statErr != nil || info.IsDir() || info.Size() > 512*1024 {
				continue
			}
			data, rdErr := os.ReadFile(full)
			if rdErr != nil {
				continue
			}
			fd := buildUntrackedFileDiff(rel, data)
			fd.Status = "U"
			diffs = append(diffs, fd)
			seen[rel] = true
		}
	}

	// Enrich tracked diffs with status letter (M/A/D/R) and staged flag from porcelain status.
	if stRaw, sErr := runGit(ctx, workspaceDir, "status", "--porcelain=v1", "--untracked-files=no"); sErr == nil {
		statusByPath, stagedByPath := parsePorcelainStatus(stRaw)
		for i := range diffs {
			if diffs[i].Status == "" {
				if st, ok := statusByPath[diffs[i].Path]; ok {
					diffs[i].Status = st
				} else {
					diffs[i].Status = "M"
				}
			}
			if stagedByPath[diffs[i].Path] {
				diffs[i].Staged = true
			}
		}
	}
	return diffs, nil
}

func parsePorcelainStatus(raw string) (map[string]string, map[string]bool) {
	statusMap := make(map[string]string)
	stagedMap := make(map[string]bool)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		x := line[0]
		y := line[1]
		pathPart := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(pathPart, " -> "); idx >= 0 {
			pathPart = strings.TrimSpace(pathPart[idx+4:])
		}
		pathPart = gitx.UnquotePath(pathPart)
		pathPart = filepath.ToSlash(pathPart)
		if pathPart == "" {
			continue
		}
		code := "M"
		switch {
		case x == '?' && y == '?':
			code = "U"
		case x == 'A' || y == 'A':
			code = "A"
		case x == 'D' || y == 'D':
			code = "D"
		case x == 'R' || y == 'R':
			code = "R"
		}
		statusMap[pathPart] = code
		if x != ' ' && x != '?' {
			stagedMap[pathPart] = true
		}
	}
	return statusMap, stagedMap
}

// StageWorkspaceFile stages or unstages a single relative path in workspaceDir.
func StageWorkspaceFile(ctx context.Context, workspaceDir, relPath string, stage bool) error {
	relClean := filepath.ToSlash(strings.TrimSpace(relPath))
	if relClean == "" || relClean == ".hivemind" || strings.HasPrefix(relClean, ".hivemind/") {
		return fmt.Errorf("caminho de arquivo inválido: %s", relPath)
	}
	if _, err := workspace.Resolve(workspaceDir, filepath.FromSlash(relClean)); err != nil {
		return err
	}
	if stage {
		_, err := runGit(ctx, workspaceDir, "add", "--", gitx.LiteralPathspec(relClean))
		return err
	}
	g := gitx.Local{}
	if g.HasCommit(ctx, workspaceDir) {
		_, err := runGit(ctx, workspaceDir, "reset", "HEAD", "--", gitx.LiteralPathspec(relClean))
		return err
	}
	_, err := runGit(ctx, workspaceDir, "rm", "--cached", "--", gitx.LiteralPathspec(relClean))
	return err
}

func buildUntrackedFileDiff(relSlash string, data []byte) FileDiff {
	fd := FileDiff{
		Path: relSlash,
		Lines: []DiffLine{
			{Kind: DiffHeader, Text: fmt.Sprintf("diff --git a/%s b/%s", relSlash, relSlash)},
			{Kind: DiffHeader, Text: "new file mode 100644"},
			{Kind: DiffHeader, Text: "--- /dev/null"},
			{Kind: DiffHeader, Text: "+++ b/" + relSlash},
		},
	}
	probe := data
	if len(probe) > 512 {
		probe = probe[:512]
	}
	for _, b := range probe {
		if b == 0 {
			fd.Lines = append(fd.Lines, DiffLine{Kind: DiffContext, Text: "(arquivo binário novo)"})
			return fd
		}
	}
	norm := strings.ReplaceAll(string(data), "\r\n", "\n")
	norm = strings.TrimSuffix(norm, "\n")
	if norm == "" {
		return fd
	}
	split := strings.Split(norm, "\n")
	fd.Added = len(split)
	fd.Lines = append(fd.Lines, DiffLine{
		Kind: DiffHunk,
		Text: fmt.Sprintf("@@ -0,0 +1,%d @@", len(split)),
	})
	for _, ln := range split {
		fd.Lines = append(fd.Lines, DiffLine{Kind: DiffAdded, Text: "+" + ln})
	}
	return fd
}

// RevertDiffHunk reverses a single unified diff hunk on relPath using `git apply --reverse`.
// If the hunk starts at -0,0 (entire untracked file), it falls back to DiscardFileChanges.
func RevertDiffHunk(ctx context.Context, workspaceDir, relPath, hunkHeader string, hunkLines []string) error {
	relClean, header, patch, err := buildSingleHunkPatch(workspaceDir, relPath, hunkHeader, hunkLines)
	if err != nil {
		return err
	}
	if strings.Contains(header, "@@ -0,0 ") {
		return DiscardFileChanges(ctx, workspaceDir, relClean)
	}
	return runGitApplyPatch(ctx, workspaceDir, patch, "--reverse", "--recount", "--whitespace=nowarn")
}

// StageDiffHunk stages (accepts) a single unified diff hunk into the Git index
// using `git apply --cached`, allowing hunk-by-hunk acceptance of agent edits.
// For untracked files (@@ -0,0 ...), stages the whole new file.
func StageDiffHunk(ctx context.Context, workspaceDir, relPath, hunkHeader string, hunkLines []string) error {
	relClean, header, patch, err := buildSingleHunkPatch(workspaceDir, relPath, hunkHeader, hunkLines)
	if err != nil {
		return err
	}
	if strings.Contains(header, "@@ -0,0 ") {
		return StageWorkspaceFile(ctx, workspaceDir, relClean, true)
	}
	return runGitApplyPatch(ctx, workspaceDir, patch, "--cached", "--recount", "--whitespace=nowarn")
}

// ApplyDiffHunk applies a single unified diff hunk onto relPath in the working tree
// using `git apply`.
func ApplyDiffHunk(ctx context.Context, workspaceDir, relPath, hunkHeader string, hunkLines []string) error {
	_, _, patch, err := buildSingleHunkPatch(workspaceDir, relPath, hunkHeader, hunkLines)
	if err != nil {
		return err
	}
	return runGitApplyPatch(ctx, workspaceDir, patch, "--recount", "--whitespace=nowarn")
}

// UnstagedFileDiff returns only the unstaged hunks (working tree vs Git index) for relPath,
// or an untracked new-file diff if relPath is not tracked yet. Used by the editor's inline
// hunk-by-hunk review bar so accepted (staged) hunks disappear immediately from pending review.
func UnstagedFileDiff(ctx context.Context, workspaceDir, relPath string) (*FileDiff, error) {
	relClean := filepath.ToSlash(strings.TrimSpace(relPath))
	if relClean == "" {
		return nil, fmt.Errorf("caminho de arquivo vazio")
	}
	full, err := workspace.Resolve(workspaceDir, filepath.FromSlash(relClean))
	if err != nil {
		return nil, err
	}
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) {
		return nil, nil
	}
	raw, err := runGit(ctx, workspaceDir, "diff", "--no-color", "--", gitx.LiteralPathspec(relClean))
	if err == nil && strings.TrimSpace(raw) != "" {
		parsed := ParseUnifiedDiff(raw)
		if len(parsed) > 0 {
			parsed[0].Status = "M"
			return &parsed[0], nil
		}
	}
	// Check if file is untracked (new file not yet staged).
	untracked, uErr := runGit(ctx, workspaceDir, "ls-files", "--others", "--exclude-standard", "--", gitx.LiteralPathspec(relClean))
	if uErr == nil && strings.TrimSpace(untracked) != "" {
		data, rErr := os.ReadFile(full)
		if rErr == nil {
			fd := buildUntrackedFileDiff(relClean, data)
			fd.Status = "U"
			return &fd, nil
		}
	}
	return nil, nil
}

func buildSingleHunkPatch(workspaceDir, relPath, hunkHeader string, hunkLines []string) (relClean, header, patch string, err error) {
	relClean = filepath.ToSlash(strings.TrimSpace(relPath))
	if relClean == "" {
		return "", "", "", fmt.Errorf("caminho de arquivo vazio")
	}
	if _, err := workspace.Resolve(workspaceDir, filepath.FromSlash(relClean)); err != nil {
		return "", "", "", err
	}
	header = strings.TrimSpace(hunkHeader)
	if !strings.HasPrefix(header, "@@") {
		return "", "", "", fmt.Errorf("cabeçalho de hunk inválido: %s", header)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("diff --git a/%s b/%s\n", relClean, relClean))
	sb.WriteString(fmt.Sprintf("--- a/%s\n", relClean))
	sb.WriteString(fmt.Sprintf("+++ b/%s\n", relClean))
	sb.WriteString(header + "\n")
	for len(hunkLines) > 0 && hunkLines[len(hunkLines)-1] == "" {
		hunkLines = hunkLines[:len(hunkLines)-1]
	}
	for _, ln := range hunkLines {
		if ln == "" {
			sb.WriteString(" \n")
			continue
		}
		first := ln[0]
		if first == '+' || first == '-' || first == ' ' || first == '\\' {
			sb.WriteString(ln + "\n")
		} else {
			sb.WriteString(" " + ln + "\n")
		}
	}
	return relClean, header, sb.String(), nil
}

func runGitApplyPatch(ctx context.Context, workspaceDir, patch string, flags ...string) error {
	tmpFile, err := os.CreateTemp("", "hivemind-hunk-*.patch")
	if err != nil {
		return err
	}
	patchPath := tmpFile.Name()
	defer os.Remove(patchPath)
	if _, err := tmpFile.WriteString(patch); err != nil {
		_ = tmpFile.Close()
		return err
	}
	_ = tmpFile.Close()

	args := append([]string{"apply"}, flags...)
	args = append(args, patchPath)
	_, err = runGit(ctx, workspaceDir, args...)
	return err
}
