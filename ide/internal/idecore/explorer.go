package idecore

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

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
		return "", fmt.Errorf("arquivo muito grande (%d bytes)", info.Size())
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteWorkspaceFile writes content to a file inside root.
func WriteWorkspaceFile(root, relOrAbs, content string) error {
	target, err := ResolveSafe(root, relOrAbs)
	if err != nil {
		return err
	}
	return os.WriteFile(target, []byte(content), 0o644)
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
// for sub-millisecond @arquivo and Ctrl+P Quick Open searches.
type FileIndex struct {
	mu    sync.RWMutex
	root  string
	files []string
}

// NewFileIndex constructs an empty FileIndex.
func NewFileIndex() *FileIndex {
	return &FileIndex{}
}

// Refresh walks root once and replaces the indexed file list.
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
	f.mu.Lock()
	f.root = absRoot
	f.files = collected
	f.mu.Unlock()
	return nil
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

	f.mu.RLock()
	sameRoot := f.root == absRoot
	if !sameRoot && runtime.GOOS == "windows" {
		sameRoot = strings.EqualFold(f.root, absRoot)
	}
	needPopulate := !sameRoot || len(f.files) == 0
	f.mu.RUnlock()
	if needPopulate {
		_ = f.Refresh(absRoot)
	}

	f.mu.RLock()
	files := f.files
	f.mu.RUnlock()

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
	lowerQ := strings.ToLower(q)

	var matches []TextSearchMatch
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
		info, err := d.Info()
		if err != nil || info.Size() == 0 || info.Size() > 512*1024 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		// Skip binary files containing NUL byte in first 512 bytes.
		probe := data
		if len(probe) > 512 {
			probe = probe[:512]
		}
		for _, b := range probe {
			if b == 0 {
				return nil
			}
		}
		content := string(data)
		if !strings.Contains(strings.ToLower(content), lowerQ) {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		for idx, line := range strings.Split(content, "\n") {
			if strings.Contains(strings.ToLower(line), lowerQ) {
				preview := strx.TruncateRunes(strings.TrimSpace(line), 140, "…")
				matches = append(matches, TextSearchMatch{
					RelPath: rel,
					Line:    idx + 1,
					Preview: preview,
				})
				if len(matches) >= maxResults {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	return matches, nil
}
