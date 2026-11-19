package idecore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ProblemItem represents a clickable diagnostic or compiler/test error in the Problems panel.
type ProblemItem struct {
	Source   string `json:"source"`   // "gopls", "go", "tsc", "maven", "python", "rust", "build"
	Severity string `json:"severity"` // "error", "warning", "info"
	File     string `json:"file"`     // workspace-relative forward-slash path
	Line     int    `json:"line"`     // 1-based
	Col      int    `json:"col"`      // 1-based
	EndLine  int    `json:"endLine,omitempty"`
	EndCol   int    `json:"endCol,omitempty"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message"`
}

// ProblemsCheckResult holds the result of running a project check/build/test command for Problems.
type ProblemsCheckResult struct {
	Command    string        `json:"command"`
	ExitCode   int           `json:"exitCode"`
	DurationMs int64         `json:"durationMs"`
	Output     string        `json:"output"`
	Problems   []ProblemItem `json:"problems"`
}

var (
	ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

	// TypeScript / Vite / Angular: src/foo.ts(12,5): error TS2339: msg
	tscParenRe = regexp.MustCompile(`^\s*([^\s(][^:(]*\.[A-Za-z0-9_]+)\((\d+),(\d+)\):\s*(error|warning)\s+([A-Za-z0-9_-]+)?\s*:\s*(.+)$`)

	// TypeScript / ESLint / Angular colon style: src/foo.ts:12:5 - error TS2339: msg
	tscColonRe = regexp.MustCompile(`^\s*([^\s:][^:]*\.[A-Za-z0-9_]+):(\d+):(\d+)\s*-\s*(error|warning)\s+([A-Za-z0-9_-]+)?\s*:\s*(.+)$`)

	// Java / Maven: [ERROR] /C:/proj/src/Foo.java:[42,15] cannot find symbol
	mavenErrRe = regexp.MustCompile(`^\[(ERROR|WARNING)\]\s+(.+\.java):\[(\d+),(\d+)\]\s*(.+)$`)

	// Rust / Cargo: error[E0425]: msg  followed by  --> src/main.rs:12:5
	rustHeaderRe = regexp.MustCompile(`^(error|warning)(?:\[([A-Z0-9]+)\])?:\s*(.+)$`)
	rustArrowRe  = regexp.MustCompile(`^\s*-->\s+(.+):(\d+):(\d+)\s*$`)

	// Python traceback: File "/path/app.py", line 42, in func
	pyFileRe = regexp.MustCompile(`^\s*File "([^"]+)", line (\d+)(?:, in .*)?$`)

	// Go compiler / vet / test & generic file:line[:col]: message
	goDiagRe = regexp.MustCompile(`^\s*((?:[A-Za-z]:)?[^\s:][^:]*\.[A-Za-z0-9_]+):(\d+)(?::(\d+))?:\s*(.+)$`)
)

// StripANSI removes terminal ANSI escape sequences from raw output.
func StripANSI(s string) string {
	return ansiEscapeRe.ReplaceAllString(s, "")
}

// ParseBuildProblems parses compiler, linter, and test output across Go, TypeScript/Angular,
// Java/Maven, Python, and Rust into deduplicated clickable ProblemItem entries.
func ParseBuildProblems(output, workspace string) []ProblemItem {
	clean := StripANSI(strings.ReplaceAll(output, "\r\n", "\n"))
	lines := strings.Split(clean, "\n")

	absRoot, _ := filepath.Abs(strings.TrimSpace(workspace))
	seen := make(map[string]bool)
	var out []ProblemItem

	add := func(it ProblemItem) {
		it.File = normalizeProblemPath(it.File, absRoot)
		it.Message = strings.TrimSpace(it.Message)
		if it.File == "" || it.Line <= 0 || it.Message == "" {
			return
		}
		if it.Col <= 0 {
			it.Col = 1
		}
		if it.Severity == "" {
			it.Severity = "error"
		}
		key := fmt.Sprintf("%s:%d:%d:%s", strings.ToLower(it.File), it.Line, it.Col, it.Message)
		if seen[key] || len(out) >= 200 {
			return
		}
		seen[key] = true
		out = append(out, it)
	}

	var pendingRustSev, pendingRustCode, pendingRustMsg string

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if m := rustHeaderRe.FindStringSubmatch(trimmed); m != nil {
			pendingRustSev = strings.ToLower(m[1])
			pendingRustCode = m[2]
			pendingRustMsg = strings.TrimSpace(m[3])
			continue
		}
		if m := rustArrowRe.FindStringSubmatch(line); m != nil && pendingRustMsg != "" {
			ln, _ := strconv.Atoi(m[2])
			col, _ := strconv.Atoi(m[3])
			add(ProblemItem{
				Source:   "rust",
				Severity: pendingRustSev,
				File:     m[1],
				Line:     ln,
				Col:      col,
				Code:     pendingRustCode,
				Message:  pendingRustMsg,
			})
			pendingRustMsg = ""
			continue
		}

		if m := mavenErrRe.FindStringSubmatch(trimmed); m != nil {
			sev := "error"
			if strings.EqualFold(m[1], "WARNING") {
				sev = "warning"
			}
			ln, _ := strconv.Atoi(m[3])
			col, _ := strconv.Atoi(m[4])
			add(ProblemItem{
				Source:   "maven",
				Severity: sev,
				File:     m[2],
				Line:     ln,
				Col:      col,
				Message:  m[5],
			})
			continue
		}

		if m := tscParenRe.FindStringSubmatch(line); m != nil {
			ln, _ := strconv.Atoi(m[2])
			col, _ := strconv.Atoi(m[3])
			add(ProblemItem{
				Source:   "tsc",
				Severity: strings.ToLower(m[4]),
				File:     m[1],
				Line:     ln,
				Col:      col,
				Code:     m[5],
				Message:  m[6],
			})
			continue
		}

		if m := tscColonRe.FindStringSubmatch(line); m != nil {
			ln, _ := strconv.Atoi(m[2])
			col, _ := strconv.Atoi(m[3])
			add(ProblemItem{
				Source:   "tsc",
				Severity: strings.ToLower(m[4]),
				File:     m[1],
				Line:     ln,
				Col:      col,
				Code:     m[5],
				Message:  m[6],
			})
			continue
		}

		if m := pyFileRe.FindStringSubmatch(line); m != nil {
			ln, _ := strconv.Atoi(m[2])
			msg := "Erro em execução Python"
			for j := i + 1; j < len(lines) && j <= i+3; j++ {
				cand := strings.TrimSpace(lines[j])
				if cand == "" || strings.HasPrefix(cand, "File ") {
					continue
				}
				if strings.Contains(cand, "Error:") || strings.Contains(cand, "Exception:") {
					msg = cand
					break
				}
				msg = cand
			}
			add(ProblemItem{
				Source:   "python",
				Severity: "error",
				File:     m[1],
				Line:     ln,
				Col:      1,
				Message:  msg,
			})
			continue
		}

		if m := goDiagRe.FindStringSubmatch(line); m != nil {
			fileCand := strings.TrimSpace(m[1])
			// Ignore URLs like https://host:443/...
			if strings.HasPrefix(strings.ToLower(fileCand), "http://") || strings.HasPrefix(strings.ToLower(fileCand), "https://") {
				continue
			}
			ln, _ := strconv.Atoi(m[2])
			col := 1
			if m[3] != "" {
				col, _ = strconv.Atoi(m[3])
			}
			msg := strings.TrimSpace(m[4])
			sev := "error"
			lowMsg := strings.ToLower(msg)
			if strings.HasPrefix(lowMsg, "warning:") || strings.Contains(lowMsg, "deprecated") {
				sev = "warning"
			}
			src := inferProblemSource(fileCand)
			add(ProblemItem{
				Source:   src,
				Severity: sev,
				File:     fileCand,
				Line:     ln,
				Col:      col,
				Message:  msg,
			})
		}
	}

	return out
}

func inferProblemSource(file string) string {
	ext := strings.ToLower(filepath.Ext(file))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx", ".js", ".jsx":
		return "tsc"
	case ".java", ".kt":
		return "maven"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	default:
		return "build"
	}
}

func normalizeProblemPath(raw, absRoot string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}
	// Maven on Windows sometimes emits "/C:/Users/..."
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, ".\\")
	pClean := filepath.Clean(p)

	if absRoot != "" {
		if filepath.IsAbs(pClean) {
			if rel, err := filepath.Rel(absRoot, pClean); err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(rel)
			}
			return filepath.ToSlash(pClean)
		}
		// Check if path exists relative to workspace; if not, keep normalized relative path.
		if _, err := os.Stat(filepath.Join(absRoot, pClean)); err == nil {
			return filepath.ToSlash(pClean)
		}
		// Common Go/frontend sub-package prefixes (e.g. "ide/..." or "frontend/...")
		for _, sub := range []string{"ide", "ide/frontend", "frontend", "backend", "api", "web"} {
			cand := filepath.Join(sub, pClean)
			if _, err := os.Stat(filepath.Join(absRoot, cand)); err == nil {
				return filepath.ToSlash(cand)
			}
		}
	}
	return filepath.ToSlash(pClean)
}

// DefaultProblemsCheckCommand picks the fastest static check/compile command for workspace.
func DefaultProblemsCheckCommand(workspace string) string {
	if _, err := os.Stat(filepath.Join(workspace, "go.mod")); err == nil {
		return "go vet ./..."
	}
	stack := DetectStack(workspace)
	if strings.TrimSpace(stack.TestHint) != "" {
		return strings.TrimSpace(stack.TestHint)
	}
	return "go vet ./..."
}

// CheckWorkspaceProblems runs a build/check command in workspace and parses all diagnostics.
func CheckWorkspaceProblems(ctx context.Context, workspace, customCmd string) (ProblemsCheckResult, error) {
	cmd := strings.TrimSpace(customCmd)
	if cmd == "" {
		cmd = DefaultProblemsCheckCommand(workspace)
	}
	out, err := RunWorkspaceCommand(ctx, workspace, cmd)
	if err != nil {
		return ProblemsCheckResult{}, err
	}
	return ProblemsCheckResult{
		Command:    out.Command,
		ExitCode:   out.ExitCode,
		DurationMs: out.DurationMs,
		Output:     out.Output,
		Problems:   ParseBuildProblems(out.Output, workspace),
	}, nil
}
