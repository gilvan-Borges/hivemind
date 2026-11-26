// package diffscan implements deterministic unified-diff parsing, hunk line-range
// mapping, and static pre-LLM code-review scanning (inspired by alibaba/open-code-review)
// so HiveMind PR reviewers get high-precision, line-accurate findings and spend
// far fewer tokens.
package diffscan

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Severity classifies a deterministic review finding.
type Severity string

const (
	SeverityP0 Severity = "P0" // Security / critical defect
	SeverityP1 Severity = "P1" // Reliability / error-handling / safety risk
	SeverityP2 Severity = "P2" // Maintainability / leftover debug / TODO
)

// LineRange represents an inclusive 1-based line interval in the new file
// version that is eligible for GitHub inline PR review comments.
type LineRange struct {
	Start int
	End   int
}

// String formats LineRange as L10-L25 or L10.
func (r LineRange) String() string {
	if r.Start == r.End {
		return fmt.Sprintf("L%d", r.Start)
	}
	return fmt.Sprintf("L%d-L%d", r.Start, r.End)
}

// AddedLine holds one added/modified line from a unified diff patch along with
// its exact 1-based line number in the new file.
type AddedLine struct {
	NewLine int
	Text    string
}

// FilePatchInput is the minimal file diff descriptor consumed by reviewx.
type FilePatchInput struct {
	Filename string
	Status   string
	Patch    string
}

// Finding is a single deterministic issue detected on an added diff line.
type Finding struct {
	Severity   Severity
	RuleID     string
	Filename   string
	Line       int
	Message    string
	Snippet    string
	Suggestion string
}

// FileSummary summarizes commentable line ranges for a single changed file.
type FileSummary struct {
	Filename   string
	LineRanges []LineRange
}

// Report aggregates deterministic findings and commentable line ranges across a PR.
type Report struct {
	Files    []FileSummary
	Findings []Finding
}

var reHunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// ParsePatchAddedLines parses a unified diff patch and returns:
// 1. All added lines with their exact 1-based new-file line numbers.
// 2. Merged inclusive LineRanges of all new-file lines present in the diff
//    (both context and added lines are commentable in GitHub PR reviews).
func ParsePatchAddedLines(patch string) ([]AddedLine, []LineRange) {
	if strings.TrimSpace(patch) == "" {
		return nil, nil
	}
	var added []AddedLine
	var commentableLines []int

	newLine := 0
	inHunk := false
	for _, raw := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		if m := reHunkHeader.FindStringSubmatch(raw); len(m) >= 2 {
			start, err := strconv.Atoi(m[1])
			if err != nil {
				inHunk = false
				continue
			}
			newLine = start
			inHunk = true
			continue
		}
		if !inHunk || raw == "" {
			continue
		}
		switch raw[0] {
		case '+':
			// Ignore +++ header lines if any leak in
			if strings.HasPrefix(raw, "+++ ") {
				continue
			}
			added = append(added, AddedLine{NewLine: newLine, Text: raw[1:]})
			commentableLines = append(commentableLines, newLine)
			newLine++
		case ' ':
			commentableLines = append(commentableLines, newLine)
			newLine++
		case '-':
			// Deleted lines do not advance newLine.
		case '\\':
			// "\ No newline at end of file"
		}
	}

	return added, mergeLines(commentableLines)
}

func mergeLines(lines []int) []LineRange {
	if len(lines) == 0 {
		return nil
	}
	sort.Ints(lines)
	var ranges []LineRange
	cur := LineRange{Start: lines[0], End: lines[0]}
	for _, ln := range lines[1:] {
		if ln <= cur.End+1 {
			if ln > cur.End {
				cur.End = ln
			}
			continue
		}
		ranges = append(ranges, cur)
		cur = LineRange{Start: ln, End: ln}
	}
	ranges = append(ranges, cur)
	return ranges
}

type rule struct {
	id         string
	severity   Severity
	message    string
	suggestion string
	skipTests  bool
	pattern    *regexp.Regexp
}

var rules = []rule{
	{
		id:       "hardcoded-secret",
		severity: SeverityP0,
		message:  "PossÃ­vel segredo/token hardcoded no diff; mova para variÃ¡vel de ambiente ou cofre.",
		pattern:  regexp.MustCompile(`(?i)(?:sk-[a-z0-9]{16,}|ghp_[a-z0-9]{20,}|AIza[0-9A-Za-z\-_]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----)`),
	},
	{
		id:       "xss-dom-sink",
		severity: SeverityP0,
		message:  "AtribuiÃ§Ã£o direta a DOM/HTML sem sanitizaÃ§Ã£o (`innerHTML`/`dangerouslySetInnerHTML`); risco de XSS.",
		pattern:  regexp.MustCompile(`(?:\.innerHTML\s*=|dangerouslySetInnerHTML)`),
	},
	{
		id:       "dynamic-eval",
		severity: SeverityP0,
		message:  "Uso de `eval(` / `new Function(`; risco de execuÃ§Ã£o arbitrÃ¡ria de cÃ³digo.",
		pattern:  regexp.MustCompile(`\b(?:eval\s*\(|new\s+Function\s*\()`),
	},
	{
		id:        "shell-exec-interp",
		severity:  SeverityP0,
		message:   "ExecuÃ§Ã£o via subshell (`sh -c` / `cmd /c`) â€” verifique se nÃ£o hÃ¡ concatenaÃ§Ã£o de entrada nÃ£o confiÃ¡vel.",
		skipTests: true,
		pattern:   regexp.MustCompile(`(?:exec\.Command(?:Context)?\([^)]*"(?:sh|bash|cmd)"[^)]*"(?:-c|/c)"|os\.system\s*\(|Runtime\.getRuntime\(\)\.exec\s*\()`),
	},
	{
		id:        "sql-concat",
		severity:  SeverityP0,
		message:   "PossÃ­vel concatenaÃ§Ã£o/interpolaÃ§Ã£o de SQL em string; prefira placeholders/prepared statements.",
		skipTests: true,
		pattern:   regexp.MustCompile(`(?i)(?:SELECT|INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+[^"'\n]*(\+\s*[A-Za-z_]|fmt\.Sprintf|\$\{)`),
	},
	{
		id:        "panic-or-unwrap",
		severity:  SeverityP1,
		message:   "Chamada a `panic(` / `.unwrap()` / `!!` em cÃ³digo de produÃ§Ã£o; trate o erro/nulo explicitamente.",
		skipTests: true,
		pattern:   regexp.MustCompile(`(?:\bpanic\s*\(|\.unwrap\s*\(\)|[A-Za-z0-9_\)\]]!!\.)`),
	},
	{
		id:        "empty-catch",
		severity:  SeverityP1,
		message:   "Bloco `catch` vazio silencia falhas; registre o erro ou trate explicitamente.",
		skipTests: true,
		pattern:   regexp.MustCompile(`\bcatch\s*\([^)]*\)\s*\{\s*\}`),
	},
	{
		id:        "debug-print",
		severity:  SeverityP2,
		message:   "Log de debug (`console.log`/`fmt.Println`/`println`) introduzido fora de testes.",
		skipTests: true,
		pattern:   regexp.MustCompile(`(?:\bconsole\.log\s*\(|\bfmt\.Print(?:ln|f)?\s*\(|\bSystem\.out\.println\s*\(|\bprintln\s*\()`),
	},
	{
		id:       "todo-fixme",
		severity: SeverityP2,
		message:  "Marcador `TODO`/`FIXME`/`HACK` adicionado no PR; confirme se deve ser resolvido antes do merge.",
		pattern:  regexp.MustCompile(`\b(?:TODO|FIXME|HACK|XXX)\b`),
	},
}

// AnalyzePRFiles runs deterministic diff parsing and static rule checks across
// a set of PR files.
func AnalyzePRFiles(files []FilePatchInput) Report {
	var rep Report
	for _, f := range files {
		if strings.TrimSpace(f.Patch) == "" {
			continue
		}
		added, ranges := ParsePatchAddedLines(f.Patch)
		if len(ranges) > 0 {
			rep.Files = append(rep.Files, FileSummary{
				Filename:   f.Filename,
				LineRanges: ranges,
			})
		}
		isTest := isTestFilename(f.Filename)
		for _, ln := range added {
			trimmed := strings.TrimSpace(ln.Text)
			if trimmed == "" {
				continue
			}
			for _, rl := range rules {
				if rl.skipTests && isTest {
					continue
				}
				if rl.pattern.MatchString(ln.Text) {
					snip := trimmed
					if len(snip) > 120 {
						snip = snip[:120] + "â€¦"
					}
					rep.Findings = append(rep.Findings, Finding{
						Severity:   rl.severity,
						RuleID:     rl.id,
						Filename:   f.Filename,
						Line:       ln.NewLine,
						Message:    rl.message,
						Snippet:    snip,
						Suggestion: rl.suggestion,
					})
					break // at most 1 finding per line to avoid noise
				}
			}
		}
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		return severityRank(rep.Findings[i].Severity) < severityRank(rep.Findings[j].Severity)
	})
	return rep
}

// FormatDossierSection renders a compact Markdown section ready to be injected
// into the PR preloaded dossier before the raw unified diffs.
func (r Report) FormatDossierSection() string {
	var b strings.Builder
	b.WriteString("\n### PrÃ©-AnÃ¡lise DeterminÃ­stica (OpenCodeReview-style)\n")
	if len(r.Findings) == 0 {
		b.WriteString("- Nenhum padrÃ£o determinÃ­stico suspeito (segredos, sinks XSS/SQL, panic/catch vazio, debug prints) detectado nas linhas adicionadas.\n")
	} else {
		const maxFindings = 25
		shown := len(r.Findings)
		if shown > maxFindings {
			shown = maxFindings
		}
		for i := 0; i < shown; i++ {
			f := r.Findings[i]
			fmt.Fprintf(&b, "- **[%s Â· %s]** `%s:%d` â€” %s (`%s`)\n",
				f.Severity, f.RuleID, f.Filename, f.Line, f.Message, strings.ReplaceAll(f.Snippet, "`", "'"))
		}
		if len(r.Findings) > maxFindings {
			fmt.Fprintf(&b, "- *... e mais %d achado(s) estÃ¡tico(s).*\n", len(r.Findings)-maxFindings)
		}
	}

	if len(r.Files) > 0 {
		b.WriteString("\n### Linhas ElegÃ­veis para ComentÃ¡rio Inline (`github_review_pr` + bloco `suggestion`)\n")
		b.WriteString("- Para sugerir correÃ§Ã£o exata em linha do diff, inclua `comments: [{path, line, body}]` com bloco ```` ```suggestion ```` quando aplicÃ¡vel.\n")
		const maxFiles = 20
		shown := len(r.Files)
		if shown > maxFiles {
			shown = maxFiles
		}
		for i := 0; i < shown; i++ {
			fs := r.Files[i]
			parts := make([]string, 0, len(fs.LineRanges))
			for _, lr := range fs.LineRanges {
				parts = append(parts, lr.String())
			}
			fmt.Fprintf(&b, "- `%s`: %s\n", fs.Filename, strings.Join(parts, ", "))
		}
	}
	return b.String()
}

func severityRank(s Severity) int {
	switch s {
	case SeverityP0:
		return 0
	case SeverityP1:
		return 1
	default:
		return 2
	}
}

func isTestFilename(name string) bool {
	low := strings.ToLower(strings.TrimSpace(name))
	return strings.HasSuffix(low, "_test.go") ||
		strings.Contains(low, ".test.") ||
		strings.Contains(low, ".spec.") ||
		strings.HasSuffix(low, "test.kt") ||
		strings.HasSuffix(low, "tests.kt") ||
		strings.HasSuffix(low, "test.java") ||
		strings.HasSuffix(low, "tests.java") ||
		strings.Contains(low, "/test/") ||
		strings.Contains(low, "/tests/")
}

