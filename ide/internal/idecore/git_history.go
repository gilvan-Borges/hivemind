package idecore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// BlameLine describes Git authorship metadata and source text for one 1-based line.
type BlameLine struct {
	Line      int    `json:"line"`
	Commit    string `json:"commit"`
	Short     string `json:"short"`
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
	Summary   string `json:"summary"`
	Text      string `json:"text"`
}

// FileCommitEntry represents one commit in a file's --follow history.
type FileCommitEntry struct {
	Hash      string `json:"hash"`
	Short     string `json:"short"`
	Author    string `json:"author"`
	Timestamp string `json:"timestamp"`
	Subject   string `json:"subject"`
}

// ConflictBlock captures one <<<<<<< / ======= / >>>>>>> section inside a conflicted file.
type ConflictBlock struct {
	Index       int    `json:"index"`
	StartLine   int    `json:"startLine"`
	EndLine     int    `json:"endLine"`
	OursLabel   string `json:"oursLabel"`
	TheirsLabel string `json:"theirsLabel"`
	Ours        string `json:"ours"`
	Base        string `json:"base,omitempty"`
	Theirs      string `json:"theirs"`
}

// ConflictedFile lists an unmerged file path and its parsed conflict hunks.
type ConflictedFile struct {
	Path   string          `json:"path"`
	Blocks []ConflictBlock `json:"blocks"`
}

func resolveGitRelativePath(workspaceDir, relPath string) (absFile, relSlash string, err error) {
	wsTrim := strings.TrimSpace(workspaceDir)
	absWS, err := filepath.Abs(wsTrim)
	if err != nil {
		return "", "", err
	}
	if realWS, sErr := filepath.EvalSymlinks(absWS); sErr == nil {
		absWS = realWS
	}
	absFile, err = workspace.Resolve(absWS, relPath)
	if err != nil {
		return "", "", err
	}
	if realFile, sErr := filepath.EvalSymlinks(absFile); sErr == nil {
		absFile = realFile
	}
	rel, err := filepath.Rel(absWS, absFile)
	if err != nil {
		return "", "", err
	}
	relSlash = filepath.ToSlash(rel)
	if relSlash == "" || relSlash == "." || strings.HasPrefix(relSlash, "..") {
		return "", "", fmt.Errorf("caminho inválido no workspace: %q", relPath)
	}
	return absFile, relSlash, nil
}

// GitBlameFile runs `git blame --line-porcelain` for relPath inside workspaceDir.
func GitBlameFile(ctx context.Context, workspaceDir, relPath string) ([]BlameLine, error) {
	_, relSlash, err := resolveGitRelativePath(workspaceDir, relPath)
	if err != nil {
		return nil, err
	}
	out, err := runGit(ctx, workspaceDir, "-c", "core.quotePath=false", "blame", "--line-porcelain", "--", relSlash)
	if err != nil {
		return nil, fmt.Errorf("git blame %s: %w", relSlash, err)
	}
	return ParseGitBlamePorcelain(out), nil
}

// ParseGitBlamePorcelain parses `git blame --line-porcelain` stdout into ordered lines.
func ParseGitBlamePorcelain(raw string) []BlameLine {
	type commitMeta struct {
		author    string
		timestamp string
		summary   string
	}
	metaCache := make(map[string]commitMeta)
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	var result []BlameLine
	var curCommit string
	var curLineNo int
	var curMeta commitMeta

	for _, ln := range lines {
		if curCommit == "" {
			if ln == "" {
				continue
			}
			fields := strings.Fields(ln)
			if len(fields) >= 3 && len(fields[0]) >= 7 {
				curCommit = fields[0]
				n, _ := strconv.Atoi(fields[2])
				curLineNo = n
				curMeta = metaCache[curCommit]
			}
			continue
		}
		if strings.HasPrefix(ln, "\t") {
			short := curCommit
			if len(short) > 8 {
				short = short[:8]
			}
			author := curMeta.author
			if author == "" && strings.Trim(curCommit, "0") == "" {
				author = "Não commitado"
			}
			metaCache[curCommit] = curMeta
			result = append(result, BlameLine{
				Line:      curLineNo,
				Commit:    curCommit,
				Short:     short,
				Author:    author,
				Timestamp: curMeta.timestamp,
				Summary:   curMeta.summary,
				Text:      ln[1:],
			})
			curCommit = ""
			continue
		}
		switch {
		case strings.HasPrefix(ln, "author "):
			curMeta.author = strings.TrimPrefix(ln, "author ")
		case strings.HasPrefix(ln, "author-time "):
			secStr := strings.TrimSpace(strings.TrimPrefix(ln, "author-time "))
			if sec, err := strconv.ParseInt(secStr, 10, 64); err == nil && sec > 0 {
				curMeta.timestamp = time.Unix(sec, 0).UTC().Format(time.RFC3339)
			}
		case strings.HasPrefix(ln, "summary "):
			curMeta.summary = strings.TrimPrefix(ln, "summary ")
		}
	}
	return result
}

// GitFileHistory returns commits touching relPath across renames via `git log --follow`.
func GitFileHistory(ctx context.Context, workspaceDir, relPath string, limit int) ([]FileCommitEntry, error) {
	_, relSlash, err := resolveGitRelativePath(workspaceDir, relPath)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}
	out, err := runGit(ctx, workspaceDir,
		"-c", "core.quotePath=false",
		"log", "-n", strconv.Itoa(limit), "--follow",
		"--format=%H%x1f%h%x1f%an%x1f%aI%x1f%s", "--", relSlash,
	)
	if err != nil {
		return nil, fmt.Errorf("git log %s: %w", relSlash, err)
	}
	var entries []FileCommitEntry
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		ln = strings.TrimRight(ln, "\r")
		if ln == "" {
			continue
		}
		parts := strings.SplitN(ln, "\x1f", 5)
		if len(parts) < 5 {
			continue
		}
		entries = append(entries, FileCommitEntry{
			Hash:      parts[0],
			Short:     parts[1],
			Author:    parts[2],
			Timestamp: parts[3],
			Subject:   parts[4],
		})
	}
	return entries, nil
}

// ParseConflictBlocks extracts 1-based line ranges and content for standard and diff3 conflict markers.
func ParseConflictBlocks(content string) []ConflictBlock {
	norm := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	var blocks []ConflictBlock
	idx := 0
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "<<<<<<<") {
			continue
		}
		startLine := i + 1
		oursLabel := strings.TrimSpace(strings.TrimPrefix(lines[i], "<<<<<<<"))
		section := 0 // 0=ours, 1=base(optional |||||||), 2=theirs
		var ours, base, theirs []string
		endIdx := -1
		theirsLabel := ""

		for j := i + 1; j < len(lines); j++ {
			ln := lines[j]
			if strings.HasPrefix(ln, "|||||||") && section == 0 {
				section = 1
				continue
			}
			if ln == "=======" || strings.HasPrefix(ln, "=======") {
				section = 2
				continue
			}
			if strings.HasPrefix(ln, ">>>>>>>") && section == 2 {
				theirsLabel = strings.TrimSpace(strings.TrimPrefix(ln, ">>>>>>>"))
				endIdx = j
				break
			}
			switch section {
			case 0:
				ours = append(ours, ln)
			case 1:
				base = append(base, ln)
			case 2:
				theirs = append(theirs, ln)
			}
		}
		if endIdx < 0 {
			break
		}
		blocks = append(blocks, ConflictBlock{
			Index:       idx,
			StartLine:   startLine,
			EndLine:     endIdx + 1,
			OursLabel:   oursLabel,
			TheirsLabel: theirsLabel,
			Ours:        strings.Join(ours, "\n"),
			Base:        strings.Join(base, "\n"),
			Theirs:      strings.Join(theirs, "\n"),
		})
		idx++
		i = endIdx
	}
	return blocks
}

// ListConflictedFiles returns all unmerged files in workspaceDir along with their conflict hunks.
func ListConflictedFiles(ctx context.Context, workspaceDir string) ([]ConflictedFile, error) {
	out, err := runGit(ctx, workspaceDir, "-c", "core.quotePath=false", "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, fmt.Errorf("listar conflitos git: %w", err)
	}
	var list []ConflictedFile
	for _, rawLine := range strings.Split(out, "\n") {
		rel := strings.TrimSpace(strings.TrimRight(rawLine, "\r"))
		if rel == "" {
			continue
		}
		absFile, err := workspace.Resolve(workspaceDir, rel)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(absFile)
		if err != nil {
			list = append(list, ConflictedFile{Path: filepath.ToSlash(rel)})
			continue
		}
		list = append(list, ConflictedFile{
			Path:   filepath.ToSlash(rel),
			Blocks: ParseConflictBlocks(string(data)),
		})
	}
	return list, nil
}

// ResolveConflictBlock rewrites blockIndex of relPath using choice ("ours", "theirs", or "both")
// and returns the remaining parsed conflicts for that file.
func ResolveConflictBlock(ctx context.Context, workspaceDir, relPath string, blockIndex int, choice string) (ConflictedFile, error) {
	absFile, relSlash, err := resolveGitRelativePath(workspaceDir, relPath)
	if err != nil {
		return ConflictedFile{}, err
	}
	info, err := os.Stat(absFile)
	if err != nil {
		return ConflictedFile{}, err
	}
	rawBytes, err := os.ReadFile(absFile)
	if err != nil {
		return ConflictedFile{}, err
	}
	raw := string(rawBytes)
	eol := "\n"
	if strings.Contains(raw, "\r\n") {
		eol = "\r\n"
	}
	norm := strings.ReplaceAll(raw, "\r\n", "\n")
	blocks := ParseConflictBlocks(norm)
	if blockIndex < 0 || blockIndex >= len(blocks) {
		return ConflictedFile{}, fmt.Errorf("bloco de conflito #%d não encontrado em %s", blockIndex, relSlash)
	}
	blk := blocks[blockIndex]

	var chosen string
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "ours":
		chosen = blk.Ours
	case "theirs":
		chosen = blk.Theirs
	case "both":
		switch {
		case blk.Ours == "":
			chosen = blk.Theirs
		case blk.Theirs == "":
			chosen = blk.Ours
		default:
			chosen = blk.Ours + "\n" + blk.Theirs
		}
	default:
		return ConflictedFile{}, fmt.Errorf("opção de conflito inválida %q (use ours, theirs ou both)", choice)
	}

	lines := strings.Split(norm, "\n")
	before := lines[:blk.StartLine-1]
	after := lines[blk.EndLine:]
	var merged []string
	merged = append(merged, before...)
	if chosen != "" {
		merged = append(merged, strings.Split(chosen, "\n")...)
	}
	merged = append(merged, after...)

	nextNorm := strings.Join(merged, "\n")
	nextContent := nextNorm
	if eol == "\r\n" {
		nextContent = strings.ReplaceAll(nextNorm, "\n", "\r\n")
	}
	if err := atomicWriteFile(absFile, []byte(nextContent), info.Mode().Perm()); err != nil {
		return ConflictedFile{}, fmt.Errorf("gravar resolução de conflito: %w", err)
	}
	return ConflictedFile{
		Path:   relSlash,
		Blocks: ParseConflictBlocks(nextNorm),
	}, nil
}

// MarkConflictResolved verifies that no conflict markers remain in relPath and runs `git add`.
func MarkConflictResolved(ctx context.Context, workspaceDir, relPath string) error {
	absFile, relSlash, err := resolveGitRelativePath(workspaceDir, relPath)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(absFile)
	if err != nil {
		return err
	}
	remaining := ParseConflictBlocks(string(data))
	if len(remaining) > 0 {
		return fmt.Errorf("%s ainda possui %d bloco(s) de conflito sem resolução", relSlash, len(remaining))
	}
	if _, err := runGit(ctx, workspaceDir, "add", "--", relSlash); err != nil {
		return fmt.Errorf("git add %s: %w", relSlash, err)
	}
	return nil
}
