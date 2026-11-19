package idecore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// FileNode represents a file or directory in the workspace explorer tree.
type FileNode struct {
	Name        string     `json:"name"`
	Path        string     `json:"path"`
	RelPath     string     `json:"relPath"`
	IsDir       bool       `json:"isDir"`
	HasChildren bool       `json:"hasChildren,omitempty"`
	Children    []FileNode `json:"children,omitempty"`
}

var ignoredDirs = map[string]bool{
	".git":         true,
	".worktrees":   true,
	"node_modules": true,
	"target":       true,
	"dist":         true,
	"build":        true,
	".idea":        true,
	".vscode":      true,
	"__pycache__":  true,
}

// ResolveSafe resolves relOrAbs inside root, evaluating symlinks and blocking .git
// while allowing IDE configuration files under .hivemind/.
func ResolveSafe(root, relOrAbs string) (string, error) {
	return workspace.ResolveWithOptions(root, relOrAbs, workspace.Options{AllowHivemind: true})
}

// ListDir returns immediate entries inside relDir (or root when relDir is empty)
// without descending into subdirectories, enabling lazy folder expansion.
func ListDir(root, relDir string) ([]FileNode, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absRoot = filepath.Clean(absRoot)
	target := absRoot
	if strings.TrimSpace(relDir) != "" {
		target, err = ResolveSafe(absRoot, relDir)
		if err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	nodes := make([]FileNode, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() && ignoredDirs[name] {
			continue
		}
		full := filepath.Join(target, name)
		rel, _ := filepath.Rel(absRoot, full)
		rel = filepath.ToSlash(rel)
		isDir := entry.IsDir()
		nodes = append(nodes, FileNode{
			Name:        name,
			Path:        full,
			RelPath:     rel,
			IsDir:       isDir,
			HasChildren: isDir && name != ".hivemind",
		})
	}
	return nodes, nil
}

// ListFileTree builds a bounded directory tree for the File Explorer.
func ListFileTree(root string, maxDepth int) ([]FileNode, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if maxDepth <= 0 {
		maxDepth = 5
	}
	return readDirNodes(absRoot, absRoot, 0, maxDepth)
}

func readDirNodes(root, dir string, depth, maxDepth int) ([]FileNode, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	var nodes []FileNode
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() && ignoredDirs[name] {
			continue
		}
		full := filepath.Join(dir, name)
		rel, _ := filepath.Rel(root, full)
		rel = filepath.ToSlash(rel)
		node := FileNode{
			Name:    name,
			Path:    full,
			RelPath: rel,
			IsDir:   entry.IsDir(),
		}
		if entry.IsDir() && depth+1 < maxDepth && name != ".hivemind" {
			children, err := readDirNodes(root, full, depth+1, maxDepth)
			if err == nil {
				node.Children = children
			}
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// ReadWorkspaceFile reads a UTF-8 text file inside root (capped at 1 MiB).
// Files that are not valid UTF-8 are rejected instead of being lossily converted:
// the IDE sends the text through a JSON bridge, which would replace invalid bytes
// with U+FFFD and silently corrupt the file on the next save.
func ReadWorkspaceFile(root, relOrAbs string) (string, error) {
	target, err := ResolveSafe(root, relOrAbs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s é um diretório", relOrAbs)
	}
	if info.Size() > 1<<20 {
		return "", fmt.Errorf("arquivo muito grande (%d bytes; limite de 1 MiB)", info.Size())
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	if err := validateTextFile(data); err != nil {
		return "", fmt.Errorf("%s: %w", relOrAbs, err)
	}
	return string(data), nil
}

// validateTextFile rejects binary/UTF-16 content and invalid UTF-8 so the
// editor never opens a file it would corrupt when saving.
func validateTextFile(data []byte) error {
	if len(data) >= 2 {
		switch {
		case data[0] == 0xFF && data[1] == 0xFE, data[0] == 0xFE && data[1] == 0xFF:
			return errors.New("arquivo em UTF-16 não é suportado; converta para UTF-8 antes de abrir")
		}
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return errors.New("arquivo binário (contém bytes NUL); abra apenas arquivos de texto UTF-8")
	}
	if !utf8.Valid(data) {
		return errors.New("arquivo não está em UTF-8 válido; salvar pelo editor corromperia os acentos (converta para UTF-8)")
	}
	return nil
}

// WriteWorkspaceFile writes content to a file inside root. Writes are atomic
// (temp file + rename) so a crash or a concurrent reader never sees a truncated
// file. The original permission bits are preserved when the file exists.
func WriteWorkspaceFile(root, relOrAbs, content string) error {
	target, err := ResolveSafe(root, relOrAbs)
	if err != nil {
		return err
	}
	if !utf8.ValidString(content) {
		return errors.New("conteúdo inválido: o texto não está em UTF-8")
	}
	var mode os.FileMode
	if info, statErr := os.Stat(target); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return atomicWriteFile(target, []byte(content), mode)
}

// SearchFiles returns up to limit relative file paths matching query (for @mention autocomplete).
func SearchFiles(root, query string, limit int) []string {
	if limit <= 0 {
		limit = 20
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(query))
	var results []string
	_ = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if ignoredDirs[d.Name()] || d.Name() == ".hivemind" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if q == "" || strings.Contains(strings.ToLower(rel), q) {
			results = append(results, rel)
			if len(results) >= limit {
				return filepath.SkipAll
			}
		}
		return nil
	})
	return results
}

const maxIndexedFiles = 20000

// FileIndex maintains a thread-safe in-memory index of workspace file paths
// per project root for sub-millisecond @arquivo and Ctrl+P Quick Open searches
// across multiple simultaneously open project tabs.
type FileIndex struct {
	mu     sync.RWMutex
	byRoot map[string][]string
}

// NewFileIndex constructs an empty FileIndex.
func NewFileIndex() *FileIndex {
	return &FileIndex{
		byRoot: make(map[string][]string),
	}
}

func normalizeRootKey(absRoot string) string {
	clean := filepath.Clean(absRoot)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

// Refresh walks root once and replaces the indexed file list for that workspace.
func (f *FileIndex) Refresh(root string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absRoot = filepath.Clean(absRoot)
	var collected []string
	_ = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if ignoredDirs[d.Name()] || d.Name() == ".hivemind" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}
		collected = append(collected, filepath.ToSlash(rel))
		if len(collected) >= maxIndexedFiles {
			return filepath.SkipAll
		}
		return nil
	})
	key := normalizeRootKey(absRoot)
	f.mu.Lock()
	if f.byRoot == nil {
		f.byRoot = make(map[string][]string)
	}
	f.byRoot[key] = collected
	f.mu.Unlock()
	return nil
}

// Files returns a snapshot of all indexed relative file paths for root,
// populating the index first if empty.
func (f *FileIndex) Files(root string) []string {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	absRoot = filepath.Clean(absRoot)
	key := normalizeRootKey(absRoot)

	f.mu.RLock()
	files, ok := f.byRoot[key]
	needPopulate := !ok || len(files) == 0
	f.mu.RUnlock()
	if needPopulate {
		_ = f.Refresh(absRoot)
		f.mu.RLock()
		files = f.byRoot[key]
		f.mu.RUnlock()
	}
	out := make([]string, len(files))
	copy(out, files)
	return out
}

// Search returns up to limit paths matching query, ranking filename matches
// ahead of directory path matches.
func (f *FileIndex) Search(root, query string, limit int) []string {
	if limit <= 0 {
		limit = 25
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	absRoot = filepath.Clean(absRoot)
	key := normalizeRootKey(absRoot)

	f.mu.RLock()
	files, ok := f.byRoot[key]
	needPopulate := !ok || len(files) == 0
	f.mu.RUnlock()
	if needPopulate {
		_ = f.Refresh(absRoot)
		f.mu.RLock()
		files = f.byRoot[key]
		f.mu.RUnlock()
	}

	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		if len(files) <= limit {
			out := make([]string, len(files))
			copy(out, files)
			return out
		}
		out := make([]string, limit)
		copy(out, files[:limit])
		return out
	}

	var basePrefix, baseSub, pathSub []string
	for _, rel := range files {
		lowerRel := strings.ToLower(rel)
		base := lowerRel
		if idx := strings.LastIndex(lowerRel, "/"); idx >= 0 {
			base = lowerRel[idx+1:]
		}
		switch {
		case strings.HasPrefix(base, q):
			basePrefix = append(basePrefix, rel)
		case strings.Contains(base, q):
			baseSub = append(baseSub, rel)
		case strings.Contains(lowerRel, q):
			pathSub = append(pathSub, rel)
		}
	}

	out := make([]string, 0, limit)
	for _, bucket := range [][]string{basePrefix, baseSub, pathSub} {
		for _, item := range bucket {
			out = append(out, item)
			if len(out) >= limit {
				return out
			}
		}
	}
	return out
}

// CreateWorkspaceFile creates an empty file (and parent dirs) inside root.
func CreateWorkspaceFile(root, relPath string) error {
	target, err := ResolveSafe(root, relPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("arquivo já existe: %s", relPath)
	}
	return os.WriteFile(target, []byte{}, 0o644)
}

// CreateWorkspaceDir creates a directory inside root.
func CreateWorkspaceDir(root, relPath string) error {
	target, err := ResolveSafe(root, relPath)
	if err != nil {
		return err
	}
	return os.MkdirAll(target, 0o755)
}

// RenameWorkspacePath renames or moves oldRel to newRel inside root.
func RenameWorkspacePath(root, oldRel, newRel string) error {
	src, err := ResolveSafe(root, oldRel)
	if err != nil {
		return err
	}
	dst, err := ResolveSafe(root, newRel)
	if err != nil {
		return err
	}
	absRoot, _ := filepath.Abs(root)
	if filepath.Clean(src) == filepath.Clean(absRoot) {
		return fmt.Errorf("não é permitido renomear a raiz do workspace")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// DeleteWorkspacePath deletes a file or directory inside root (never root or .hivemind itself).
func DeleteWorkspacePath(root, relPath string) error {
	target, err := ResolveSafe(root, relPath)
	if err != nil {
		return err
	}
	rootResolved, err := ResolveSafe(root, ".")
	if err != nil {
		return err
	}
	if filepath.Clean(target) == filepath.Clean(rootResolved) {
		return fmt.Errorf("não é permitido excluir a raiz do workspace")
	}
	if strings.EqualFold(filepath.Base(target), ".hivemind") && filepath.Clean(filepath.Dir(target)) == filepath.Clean(rootResolved) {
		return fmt.Errorf("não é permitido excluir o diretório .hivemind inteiro")
	}
	return os.RemoveAll(target)
}

// TextSearchMatch represents one line hit from global project search (Ctrl+Shift+F).
type TextSearchMatch struct {
	RelPath string `json:"relPath"`
	Line    int    `json:"line"`
	Preview string `json:"preview"`
}

// SearchWorkspaceText searches UTF-8 text files inside root for query (case-insensitive).
func SearchWorkspaceText(root, query string, maxResults int) ([]TextSearchMatch, error) {
	return SearchWorkspaceTextIndexed(nil, root, query, maxResults)
}

// SearchWorkspaceTextIndexed performs a parallel worker-pool text search across root,
// reusing idx when provided to skip filesystem directory walks.
func SearchWorkspaceTextIndexed(idx *FileIndex, root, query string, maxResults int) ([]TextSearchMatch, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if maxResults <= 0 {
		maxResults = 100
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absRoot = filepath.Clean(absRoot)

	var relPaths []string
	if idx != nil {
		relPaths = idx.Files(absRoot)
	}
	if len(relPaths) == 0 {
		tempIdx := NewFileIndex()
		_ = tempIdx.Refresh(absRoot)
		relPaths = tempIdx.Files(absRoot)
	}
	if len(relPaths) == 0 {
		return nil, nil
	}

	lowerQ := strings.ToLower(q)
	lowerQBytes := []byte(lowerQ)
	workers := runtime.NumCPU()
	if workers < 2 {
		workers = 2
	}
	if workers > 8 {
		workers = 8
	}
	if workers > len(relPaths) {
		workers = len(relPaths)
	}

	type fileHit struct {
		order int
		hits  []TextSearchMatch
	}

	jobs := make(chan int, len(relPaths))
	for i := range relPaths {
		jobs <- i
	}
	close(jobs)

	var totalHits int64
	resultsCh := make(chan fileHit, len(relPaths))
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if atomic.LoadInt64(&totalHits) >= int64(maxResults) {
					return
				}
				rel := relPaths[i]
				full := filepath.Join(absRoot, filepath.FromSlash(rel))
				info, err := os.Stat(full)
				if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > 512*1024 {
					continue
				}
				data, err := os.ReadFile(full)
				if err != nil {
					continue
				}
				probe := data
				if len(probe) > 512 {
					probe = probe[:512]
				}
				if bytes.IndexByte(probe, 0) >= 0 {
					continue
				}
				if !bytes.Contains(bytes.ToLower(data), lowerQBytes) {
					continue
				}
				lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
				var localHits []TextSearchMatch
				for lineIdx, line := range lines {
					if strings.Contains(strings.ToLower(line), lowerQ) {
						localHits = append(localHits, TextSearchMatch{
							RelPath: rel,
							Line:    lineIdx + 1,
							Preview: strx.TruncateRunes(strings.TrimSpace(line), 140, "…"),
						})
						if atomic.AddInt64(&totalHits, 1) >= int64(maxResults) {
							break
						}
					}
				}
				if len(localHits) > 0 {
					resultsCh <- fileHit{order: i, hits: localHits}
				}
			}
		}()
	}

	wg.Wait()
	close(resultsCh)

	collected := make([]fileHit, 0, len(resultsCh))
	for fh := range resultsCh {
		collected = append(collected, fh)
	}
	sort.Slice(collected, func(i, j int) bool {
		return collected[i].order < collected[j].order
	})

	matches := make([]TextSearchMatch, 0, maxResults)
	for _, fh := range collected {
		for _, h := range fh.hits {
			matches = append(matches, h)
			if len(matches) >= maxResults {
				return matches, nil
			}
		}
	}
	return matches, nil
}
