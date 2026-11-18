package idecore

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileNode represents a file or directory in the workspace explorer tree.
type FileNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	RelPath  string     `json:"relPath"`
	IsDir    bool       `json:"isDir"`
	Children []FileNode `json:"children,omitempty"`
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

// ResolveSafe resolves relOrAbs inside root, rejecting path traversal outside root.
func ResolveSafe(root, relOrAbs string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absRoot = filepath.Clean(absRoot)
	var target string
	if filepath.IsAbs(relOrAbs) {
		target = filepath.Clean(relOrAbs)
	} else {
		target = filepath.Clean(filepath.Join(absRoot, relOrAbs))
	}
	rel, err := filepath.Rel(absRoot, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("caminho fora do workspace: %s", relOrAbs)
	}
	return target, nil
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
