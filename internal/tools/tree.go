package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/pathsec"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// list_tree renders a budget-aware directory skeleton: the agent gets the
// tree shape of a subtree within a character budget, and every branch the
// budget could not reach is marked explicitly as unexplored instead of being
// cut silently. Navigating a large repo becomes 1-2 cheap calls (root
// skeleton, then refine into one marked subdirectory) instead of a long tail
// of flat list_dir round-trips whose outputs the compaction layer would trim
// later anyway.

type listTreeArgs struct {
	Path     string `json:"path" jsonschema:"Caminho relativo ao diretório de trabalho do agente (vazio = raiz do workspace)."`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"Orçamento de caracteres da saída (default 6000, teto 20000)."`
	MaxDepth int    `json:"max_depth,omitempty" jsonschema:"Profundidade máxima da árvore (default 3, teto 6)."`
}

const (
	defaultTreeBudgetChars = 6000
	maxTreeBudgetChars     = 20000
	defaultTreeMaxDepth    = 3
	maxTreeMaxDepth        = 6
)

// canListTree registers the budget-aware tree tool for read-capable roles.
func (d Deps) canListTree() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "list_tree",
		Description: "Esqueleto de diretórios com orçamento de caracteres: mostra a estrutura de uma subtree e marca cada ramo que o orçamento não alcançou como '(… não explorado)'. Muito mais barato em tokens do que listar tudo; refine chamando list_tree ou list_dir no subcaminho marcado.",
	}, func(_ agent.Context, args listTreeArgs) (textResult, error) {
		out, err := d.listTree(args)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: out}, nil
	})
}

func (d Deps) listTree(args listTreeArgs) (string, error) {
	if pathsec.IsSensitivePath(args.Path) {
		return "", fmt.Errorf("listagem recusada: %s é um caminho sensível", args.Path)
	}
	root, err := workspace.Resolve(d.Root, args.Path)
	if err != nil {
		return "", err
	}
	if pathsec.IsSensitivePath(root) {
		return "", fmt.Errorf("listagem recusada: %s é um caminho sensível", args.Path)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s não é um diretório", args.Path)
	}

	budget := args.MaxChars
	if budget <= 0 {
		budget = defaultTreeBudgetChars
	}
	if budget > maxTreeBudgetChars {
		budget = maxTreeBudgetChars
	}
	maxDepth := args.MaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultTreeMaxDepth
	}
	if maxDepth > maxTreeMaxDepth {
		maxDepth = maxTreeMaxDepth
	}

	displayRoot := strings.TrimSpace(args.Path)
	if displayRoot == "" {
		displayRoot = "./"
	} else {
		displayRoot = strings.TrimSuffix(filepath.ToSlash(displayRoot), "/") + "/"
	}

	tb := &treeBuilder{budget: budget, maxDepth: maxDepth, displayRoot: displayRoot}
	tb.walk(root, "", 0)
	return tb.render(), nil
}

// treeBuilder accumulates skeleton lines within a character budget. The walk
// stops cleanly at the first entry that does not fit and marks the remaining
// siblings of that directory as unexplored, so the model knows exactly where
// to refine next instead of guessing behind a silent cut.
type treeBuilder struct {
	budget     int
	maxDepth   int
	displayRoot string
	used       int
	lines      []string
	truncated  bool
}

// treeSkipDir reports whether a directory never enters the skeleton: VCS and
// workspace machinery dirs are noise for every role.
func treeSkipDir(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".hivemind", ".worktrees", "node_modules", "__pycache__":
		return true
	default:
		return false
	}
}

func (tb *treeBuilder) walk(dir, rel string, depth int) {
	if tb.truncated {
		return
	}
	if depth > tb.maxDepth {
		return
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		tb.lines = append(tb.lines, tb.line(depth, fmt.Sprintf("(falha ao ler: %v)", err)))
		return
	}
	sort.Slice(des, func(i, j int) bool {
		if des[i].IsDir() != des[j].IsDir() {
			return !des[i].IsDir()
		}
		return des[i].Name() < des[j].Name()
	})

	children := make([]os.DirEntry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if strings.EqualFold(name, ".git") || strings.EqualFold(name, ".hivemind") || strings.EqualFold(name, ".worktrees") {
			continue
		}
		if de.IsDir() && treeSkipDir(name) {
			continue
		}
		if pathsec.IsSensitivePath(name) {
			continue
		}
		if rel != "" && pathsec.IsSensitivePath(rel+"/"+name) {
			continue
		}
		children = append(children, de)
	}

	for i, de := range children {
		name := de.Name()
		suffix := ""
		if de.IsDir() {
			suffix = "/"
		}
		cost := depth*2 + len(name) + len(suffix) + 1
		if tb.used+cost > tb.budget {
			remaining := len(children) - i
			tb.lines = append(tb.lines, tb.line(depth, fmt.Sprintf(
				"(… +%d entradas não exploradas — refine com list_tree/list_dir em subcaminhos deste diretório)", remaining)))
			tb.truncated = true
			return
		}
		tb.used += cost
		tb.lines = append(tb.lines, tb.line(depth, name+suffix))
		if de.IsDir() {
			childRel := name
			if rel != "" {
				childRel = rel + "/" + name
			}
			tb.walk(filepath.Join(dir, name), childRel, depth+1)
			if tb.truncated {
				return
			}
		}
	}
}

func (tb *treeBuilder) line(depth int, text string) string {
	return strings.Repeat("  ", depth) + text
}

func (tb *treeBuilder) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# árvore de %s (orçamento %d caracteres", tb.displayRoot, tb.budget)
	if tb.truncated {
		b.WriteString("; parcial — refine nos subcaminhos marcados")
	}
	b.WriteString(")\n")
	b.WriteString(strings.Join(tb.lines, "\n"))
	return b.String()
}
