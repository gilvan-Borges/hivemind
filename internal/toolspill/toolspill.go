// Package toolspill persists oversized tool outputs to disk and replaces them
// in the model-visible result with a bounded head+tail preview plus a
// recoverable read_file pointer. The full payload never needs to ride the
// context window again: one large build log alone can crowd out an entire
// agent turn, so results above the threshold are spilled before they enter
// the transcript.
//
// The spill directory lives at <workspace>/.hivemind/spillover (gitignored by
// the standard .gitignore). Reading it back is a scoped carve-out of the
// workspace jail: ONLY the spillover subtree becomes readable via read_file;
// team.yaml, sessions and worktrees stay off-limits (see workspace.Options
// and internal/pathsec).
//
// Fail-closed rule: if the spill write or the byte-verified read-back fails,
// the inline result falls back to a bounded truncation WITHOUT a pointer
// instead of referencing an archive that silently lost bytes.
package toolspill

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/tool"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// SpillRelDir is the workspace-relative directory (slash-separated) where
// spilled payloads are stored.
const SpillRelDir = ".hivemind/spillover"

const (
	// DefaultPerResultChars is the per-result threshold above which a tool
	// output is spilled instead of riding inline.
	DefaultPerResultChars = 100_000
	// DefaultPreviewChars bounds the inline head+tail preview.
	DefaultPreviewChars = 1_500
	// DefaultTurnBudgetChars is the aggregate inline budget for one agent
	// run; once exceeded, subsequent results spill at a much lower
	// threshold (largest-payload-first is not possible after the fact, so
	// the threshold escalates adaptively for the rest of the run).
	DefaultTurnBudgetChars = 200_000
	// DefaultEscalatedChars is the per-result threshold after the turn
	// budget is exhausted.
	DefaultEscalatedChars = 8_000
	// MinPreviewTailChars keeps a meaningful tail window: a failing
	// build's error line sits at the END of a large output.
	MinPreviewTailChars = 400
	// DefaultRetention bounds how long spilled files survive cleanup sweeps.
	DefaultRetention = 7 * 24 * time.Hour
)

// pinnedTools never spill. read_file is pinned because spilling its output
// would create an infinite persist→read→persist loop; the governance tools
// must stay verbatim so approvals and PR state cannot hide behind a pointer.
var pinnedTools = map[string]bool{
	"read_file":          true,
	"qa_approve":         true,
	"qa_request_changes": true,
	"delegate_to_squad":  true,
	"github_get_pr":      true,
	"github_ci_status":   true,
	"sdd_docs_ready":     true,
}

// secretPatterns redacts obvious credential shapes before a payload is
// persisted to disk (the inline preview gets the same treatment).
var secretPatterns = regexp.MustCompile(`(?i)\b(sk-[a-z0-9_-]{16,}|ghp_[a-z0-9]{20,}|gho_[a-z0-9]{20,}|github_pat_[a-z0-9_]{20,}|xox[baprs]-[a-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9a-z_-]{30,}|eyJhbGciOiJ[0-9a-zA-Z._-]{20,})`)

// Config controls one spill callback. Zero fields fall back to defaults.
type Config struct {
	// PerResultChars is the per-result spill threshold (default 100_000).
	PerResultChars int
	// PreviewChars bounds the inline preview (default 1_500).
	PreviewChars int
	// TurnBudgetChars is the aggregate inline budget per agent run
	// (default 200_000).
	TurnBudgetChars int
	// EscalatedChars is the per-result threshold once the turn budget is
	// exhausted (default 8_000).
	EscalatedChars int
	// Retention bounds spilled file age (default 7 days; <=0 disables).
	Retention time.Duration
}

// DefaultConfig returns the canonical thresholds.
func DefaultConfig() Config {
	return Config{
		PerResultChars:  DefaultPerResultChars,
		PreviewChars:    DefaultPreviewChars,
		TurnBudgetChars: DefaultTurnBudgetChars,
		EscalatedChars:  DefaultEscalatedChars,
		Retention:       DefaultRetention,
	}
}

func (c Config) perResult() int {
	if c.PerResultChars > 0 {
		return c.PerResultChars
	}
	return DefaultPerResultChars
}

func (c Config) preview() int {
	if c.PreviewChars > 0 {
		return c.PreviewChars
	}
	return DefaultPreviewChars
}

func (c Config) turnBudget() int {
	if c.TurnBudgetChars > 0 {
		return c.TurnBudgetChars
	}
	return DefaultTurnBudgetChars
}

func (c Config) escalated() int {
	if c.EscalatedChars > 0 {
		return c.EscalatedChars
	}
	return DefaultEscalatedChars
}

// Store persists spilled payloads under <root>/.hivemind/spillover and
// byte-verifies every write with a read-back before returning the pointer.
type Store struct {
	root string
}

// NewStore returns a Store rooted at the given workspace root.
func NewStore(root string) *Store {
	return &Store{root: strings.TrimSpace(root)}
}

var (
	cleanupMu      sync.Mutex
	lastCleanup    = map[string]time.Time{}
	cleanupMinStep = time.Hour
)

// Save persists content for toolName and returns the workspace-relative,
// slash-separated path handed back to the model. The write is verified with
// a full read-back; on any mismatch or write error Save fails closed.
func (s *Store) Save(toolName, content string) (string, error) {
	if s == nil || s.root == "" {
		return "", fmt.Errorf("spill sem workspace raiz")
	}
	content = RedactSecrets(content)
	dir := filepath.Join(s.root, filepath.FromSlash(SpillRelDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s-%s.txt", sanitizeToolName(toolName), time.Now().Format("20060102-150405"), shortID())
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", err
	}
	back, err := os.ReadFile(full)
	if err != nil || string(back) != content {
		_ = os.Remove(full)
		return "", fmt.Errorf("verificação de spill falhou para %s", name)
	}
	return SpillRelDir + "/" + name, nil
}

// Cleanup removes spilled files older than maxAge. It is throttled to at most
// one sweep per root per hour so wiring it into agent construction stays
// cheap.
func (s *Store) Cleanup(maxAge time.Duration) int {
	if s == nil || s.root == "" || maxAge <= 0 {
		return 0
	}
	cleanupMu.Lock()
	if t, ok := lastCleanup[s.root]; ok && time.Since(t) < cleanupMinStep {
		cleanupMu.Unlock()
		return 0
	}
	lastCleanup[s.root] = time.Now()
	cleanupMu.Unlock()

	dir := filepath.Join(s.root, filepath.FromSlash(SpillRelDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			removed++
		}
	}
	return removed
}

// Monitor accumulates inline tool-result characters for one agent run and
// escalates the per-result threshold once the aggregate turn budget is
// exhausted (the honest equivalent of spilling largest-to-smallest when past
// results can no longer be rewritten).
type Monitor struct {
	mu          sync.Mutex
	inlineChars int
}

// Threshold returns the effective per-result spill threshold under cfg.
func (m *Monitor) Threshold(cfg Config) int {
	if m == nil {
		return cfg.perResult()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inlineChars >= cfg.turnBudget() {
		return cfg.escalated()
	}
	return cfg.perResult()
}

// Add records chars kept inline after the spill decision.
func (m *Monitor) Add(chars int) {
	if m == nil || chars <= 0 {
		return
	}
	m.mu.Lock()
	m.inlineChars += chars
	m.mu.Unlock()
}

// HeadTailPreview renders a bounded preview: ~75% head + ~25% tail snapped
// to newline boundaries, with the elided count between them. The tail window
// exists because diagnostics of large outputs (build/test logs) sit at the
// end.
func HeadTailPreview(text string, previewChars int) string {
	if previewChars <= 0 {
		previewChars = DefaultPreviewChars
	}
	if len(text) <= previewChars {
		return text
	}
	tail := previewChars / 4
	if tail < MinPreviewTailChars {
		tail = MinPreviewTailChars
	}
	head := previewChars - tail
	if head < 1 {
		head = 1
	}
	headText := strx.TruncateBytes(text[:min(head, len(text))], head, "")
	if idx := strings.LastIndexByte(headText, '\n'); idx > 0 {
		headText = headText[:idx]
	}
	tailStart := len(text) - min(tail, len(text))
	tailText := text[tailStart:]
	if idx := strings.IndexByte(tailText, '\n'); idx >= 0 && idx < len(tailText)-1 {
		tailText = tailText[idx+1:]
	}
	elided := len(text) - len(headText) - len(tailText)
	return headText + "\n[… " + fmt.Sprintf("%d", elided) + " caracteres omitidos no meio …]\n" + tailText
}

// SpillText persists text when it exceeds the threshold and returns the
// inline replacement. The bool reports whether the payload was spilled; when
// the store fails, the replacement is a plain bounded preview with no
// pointer (fail-closed) and spilled is false.
func SpillText(toolName, text string, store *Store, threshold, previewChars int) (replacement string, spilled bool) {
	if threshold <= 0 || len(text) <= threshold {
		return text, false
	}
	preview := RedactSecrets(HeadTailPreview(text, previewChars))
	rel, err := store.Save(toolName, text)
	if err != nil || rel == "" {
		return preview, false
	}
	lines := strings.Count(text, "\n") + 1
	pointer := fmt.Sprintf(
		"\n\n[saída de %s persistida: %d caracteres / %d linhas — arquivo: %s — recupere trechos com read_file (path=%s, start_line/end_line); NÃO releia sem necessidade]",
		toolName, len(text), lines, rel, rel,
	)
	return preview + pointer, true
}

// NewAfterToolCallback returns an ADK AfterToolCallback that spills oversized
// "text" results of the agent's tools before they are recorded in the
// transcript. Error results and pinned/governance tools stay verbatim.
func NewAfterToolCallback(cfg Config, store *Store, mon *Monitor) llmagent.AfterToolCallback {
	if mon == nil {
		mon = &Monitor{}
	}
	return func(_ agent.Context, tl tool.Tool, _ map[string]any, result map[string]any, callErr error) (map[string]any, error) {
		if callErr != nil || result == nil {
			return nil, nil
		}
		name := ""
		if tl != nil {
			name = strings.ToLower(strings.TrimSpace(tl.Name()))
		}
		text, ok := result["text"].(string)
		if !ok || text == "" {
			return nil, nil
		}
		if pinnedTools[name] {
			mon.Add(len(text))
			return nil, nil
		}
		store.Cleanup(cfg.Retention)
		replacement, spilled := SpillText(name, text, store, mon.Threshold(cfg), cfg.preview())
		mon.Add(len(replacement))
		if !spilled && replacement == text {
			return nil, nil
		}
		cloned := make(map[string]any, len(result))
		for k, v := range result {
			cloned[k] = v
		}
		cloned["text"] = replacement
		return cloned, nil
	}
}

// RedactSecrets masks common credential shapes in a payload before it is
// persisted or previewed inline.
func RedactSecrets(s string) string {
	if !strings.Contains(s, "sk-") && !strings.Contains(s, "ghp_") && !strings.Contains(s, "xox") &&
		!strings.Contains(s, "AKIA") && !strings.Contains(s, "AIza") && !strings.Contains(s, "eyJ") &&
		!strings.Contains(s, "github_pat_") {
		return s
	}
	return secretPatterns.ReplaceAllString(s, "[REDACTED]")
}

func sanitizeToolName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "tool"
	}
	return out
}

func shortID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "spill"
	}
	return hex.EncodeToString(buf[:])
}
