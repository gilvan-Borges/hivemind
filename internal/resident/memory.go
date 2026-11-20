package resident

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

const (
	// DefaultAccountID is used when the IDE is not linked to a relay account (§6).
	DefaultAccountID = "local"

	// DefaultWorkstream is the fallback personal workstream name.
	DefaultWorkstream = "geral"

	// Spec §6 & §13 memory injection budgets.
	MaxMemoryItems = 8
	MaxMemoryBytes = 4096
	MaxPinnedBytes = 1024
)

// DefaultBaseDir returns %LOCALAPPDATA%\HiveMind\resident on Windows
// (or UserConfigDir/HiveMind/resident on Linux/macOS, §11).
func DefaultBaseDir() string {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
			base = cfg
		} else {
			base = os.TempDir()
		}
	}
	return filepath.Join(base, "HiveMind", "resident")
}

// SanitizeAccountID converts an account identifier (e.g. relay email or accountId)
// into a safe single-directory segment so accounts are strictly isolated and
// path traversal is impossible (§6, §12.7).
func SanitizeAccountID(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" || s == DefaultAccountID {
		return DefaultAccountID
	}
	var b strings.Builder
	needsHash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
			needsHash = true
		}
	}
	slug := strings.Trim(b.String(), "_")
	if len(slug) > 32 {
		slug = slug[:32]
		needsHash = true
	}
	if slug == "" || slug == DefaultAccountID {
		slug = "acct"
		needsHash = true
	}
	if needsHash || strings.Contains(s, "..") || strings.ContainsAny(s, `/\`) {
		sum := sha256.Sum256([]byte(s))
		return slug + "_" + hex.EncodeToString(sum[:6])
	}
	return slug
}

// SanitizeWorkstream normalizes a workstream identifier into a safe filename slug.
func SanitizeWorkstream(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return DefaultWorkstream
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if unicode.IsSpace(r) || r == '/' || r == '\\' || r == '.' {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return DefaultWorkstream
	}
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}

// MemoryStore manages local personal memory isolated per account (§6).
// It never touches .hivemind/MEMORY.md and never sends memory across the relay.
type MemoryStore struct {
	mu          sync.Mutex
	baseDir     string
	accountID   string
	cacheLoaded bool
	cacheSize   int64
	cacheMod    time.Time
	cacheFacts  []MemoryFact
}

// NewMemoryStore opens the personal memory store for accountID under baseDir.
// Switching accounts opens a completely separate directory; previous accounts
// are neither erased nor readable from the new store.
func NewMemoryStore(baseDir, accountID string) *MemoryStore {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = DefaultBaseDir()
	}
	return &MemoryStore{
		baseDir:   baseDir,
		accountID: SanitizeAccountID(accountID),
	}
}

// AccountID returns the sanitized account directory key.
func (m *MemoryStore) AccountID() string {
	return m.accountID
}

// AccountDir returns <baseDir>/<account>.
func (m *MemoryStore) AccountDir() string {
	return filepath.Join(m.baseDir, m.accountID)
}

// FilePath returns <baseDir>/<account>/memory.jsonl.
func (m *MemoryStore) FilePath() string {
	return filepath.Join(m.AccountDir(), "memory.jsonl")
}

// SessionsDir returns <baseDir>/<account>/sessions.
func (m *MemoryStore) SessionsDir() string {
	return filepath.Join(m.AccountDir(), "sessions")
}

// AddFact persists an explicit user-provided personal fact to memory.jsonl (mode 0600).
// Secrets are scrubbed before writing; tool/web/MCP outputs must NEVER call AddFact.
func (m *MemoryStore) AddFact(workstream, text string, pinned bool) (MemoryFact, error) {
	cleanText := strings.TrimSpace(RedactSecrets(text))
	if cleanText == "" {
		return MemoryFact{}, fmt.Errorf("texto do fato de memória não pode ser vazio")
	}
	cleanText = strx.TruncateBytes(cleanText, 1024, "…")

	fact := MemoryFact{
		ID:         newShortID("mem"),
		Workstream: SanitizeWorkstream(workstream),
		Text:       cleanText,
		Pinned:     pinned,
		Source:     "user",
		CreatedAt:  time.Now().UTC(),
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.AccountDir(), 0o700); err != nil {
		return MemoryFact{}, fmt.Errorf("criar diretório de memória da conta: %w", err)
	}
	line, err := json.Marshal(fact)
	if err != nil {
		return MemoryFact{}, err
	}
	line = append(line, '\n')

	f, err := os.OpenFile(m.FilePath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return MemoryFact{}, fmt.Errorf("gravar memória pessoal: %w", err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return MemoryFact{}, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return MemoryFact{}, err
	}
	if err := f.Close(); err != nil {
		return MemoryFact{}, err
	}
	if m.cacheLoaded {
		m.cacheFacts = append(m.cacheFacts, fact)
		if info, err := os.Stat(m.FilePath()); err == nil {
			m.cacheSize = info.Size()
			m.cacheMod = info.ModTime()
		}
	}
	return fact, nil
}

// ListFacts returns all saved personal facts for this account, optionally
// filtered by workstream (empty workstream returns all facts for this account).
func (m *MemoryStore) ListFacts(workstream string) ([]MemoryFact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	all, err := m.loadAllLocked()
	if err != nil {
		return nil, err
	}
	ws := strings.TrimSpace(workstream)
	if ws == "" {
		return all, nil
	}
	normWS := SanitizeWorkstream(ws)
	out := make([]MemoryFact, 0, len(all))
	for _, f := range all {
		if f.Pinned || f.Workstream == normWS || f.Workstream == DefaultWorkstream {
			out = append(out, f)
		}
	}
	return out, nil
}

// DeleteFact removes a single fact by ID for this account and atomically rewrites memory.jsonl.
func (m *MemoryStore) DeleteFact(id string) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	all, err := m.loadAllLocked()
	if err != nil {
		return false, err
	}
	kept := make([]MemoryFact, 0, len(all))
	removed := false
	for _, f := range all {
		if f.ID == id {
			removed = true
			continue
		}
		kept = append(kept, f)
	}
	if !removed {
		return false, nil
	}
	if err := m.rewriteLocked(kept); err != nil {
		return false, err
	}
	return true, nil
}

// RetrieveResult holds the bounded personal memory snippets ready for prompt injection.
type RetrieveResult struct {
	Pinned     []MemoryFact
	Matches    []MemoryFact
	TotalBytes int
}

// Retrieve performs pure-Go term-overlap search over this account's memory.jsonl,
// enforcing §6 & §13 budgets: max 1 KiB pinned facts + max 8 matched snippets / 4 KiB total.
func (m *MemoryStore) Retrieve(workstream, query string) (RetrieveResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	all, err := m.loadAllLocked()
	if err != nil {
		return RetrieveResult{}, err
	}
	if len(all) == 0 {
		return RetrieveResult{}, nil
	}

	normWS := SanitizeWorkstream(workstream)
	qTerms := tokenizeTerms(query)

	var res RetrieveResult
	pinnedBytes := 0
	var candidates []scoredFact

	for _, f := range all {
		if f.Pinned {
			lineLen := len(f.Text) + 16
			if pinnedBytes+lineLen <= MaxPinnedBytes {
				res.Pinned = append(res.Pinned, f)
				pinnedBytes += lineLen
			}
			continue
		}
		// Keep facts from the active workstream or global "geral" workstream.
		if normWS != "" && f.Workstream != "" && f.Workstream != normWS && f.Workstream != DefaultWorkstream {
			continue
		}
		score := scoreOverlap(qTerms, f.Text)
		if len(qTerms) > 0 && score == 0 {
			continue
		}
		if f.Workstream == normWS {
			score += 1 // prefer exact workstream match
		}
		candidates = append(candidates, scoredFact{fact: f, score: score})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].fact.CreatedAt.After(candidates[j].fact.CreatedAt)
	})

	usedBytes := 0
	for _, c := range candidates {
		if len(res.Matches) >= MaxMemoryItems {
			break
		}
		cost := len(c.fact.Text) + len(c.fact.Workstream) + 24
		if usedBytes+cost > MaxMemoryBytes {
			continue
		}
		res.Matches = append(res.Matches, c.fact)
		usedBytes += cost
	}
	res.TotalBytes = pinnedBytes + usedBytes
	return res, nil
}

type scoredFact struct {
	fact  MemoryFact
	score int
}

func (m *MemoryStore) loadAllLocked() ([]MemoryFact, error) {
	info, err := os.Stat(m.FilePath())
	if err != nil {
		if os.IsNotExist(err) {
			m.cacheLoaded = true
			m.cacheSize = 0
			m.cacheMod = time.Time{}
			m.cacheFacts = nil
			return nil, nil
		}
		return nil, err
	}
	if m.cacheLoaded && m.cacheSize == info.Size() && m.cacheMod.Equal(info.ModTime()) {
		out := make([]MemoryFact, len(m.cacheFacts))
		copy(out, m.cacheFacts)
		return out, nil
	}

	f, err := os.Open(m.FilePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []MemoryFact
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 16*1024), 256*1024)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var fact MemoryFact
		if err := json.Unmarshal([]byte(raw), &fact); err != nil {
			continue
		}
		out = append(out, fact)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	m.cacheLoaded = true
	m.cacheSize = info.Size()
	m.cacheMod = info.ModTime()
	m.cacheFacts = make([]MemoryFact, len(out))
	copy(m.cacheFacts, out)
	return out, nil
}

func (m *MemoryStore) rewriteLocked(facts []MemoryFact) error {
	if err := os.MkdirAll(m.AccountDir(), 0o700); err != nil {
		return err
	}
	tmp := m.FilePath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	for _, fact := range facts {
		line, err := json.Marshal(fact)
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, m.FilePath()); err != nil {
		return err
	}
	m.cacheLoaded = true
	m.cacheFacts = make([]MemoryFact, len(facts))
	copy(m.cacheFacts, facts)
	if info, err := os.Stat(m.FilePath()); err == nil {
		m.cacheSize = info.Size()
		m.cacheMod = info.ModTime()
	}
	return nil
}

// LocalSession stores compact short-term conversation history per account & workstream (§6).
type LocalTurnMessage struct {
	Role string    `json:"role"` // "user" | "model"
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// LoadSessionMessages returns the last maxTurns messages for workstream in this account.
func (m *MemoryStore) LoadSessionMessages(workstream string, maxMessages int) ([]LocalTurnMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := filepath.Join(m.SessionsDir(), SanitizeWorkstream(workstream)+".json")
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var msgs []LocalTurnMessage
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, nil
	}
	if maxMessages > 0 && len(msgs) > maxMessages {
		msgs = msgs[len(msgs)-maxMessages:]
	}
	return msgs, nil
}

// AppendSessionTurn persists a user+model turn exchange for workstream (redacted, capped).
func (m *MemoryStore) AppendSessionTurn(workstream, userText, modelText string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(m.SessionsDir(), 0o700); err != nil {
		return err
	}
	p := filepath.Join(m.SessionsDir(), SanitizeWorkstream(workstream)+".json")
	var msgs []LocalTurnMessage
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &msgs)
	}
	now := time.Now().UTC()
	if u := strings.TrimSpace(RedactSecrets(userText)); u != "" {
		msgs = append(msgs, LocalTurnMessage{Role: "user", Text: strx.TruncateBytes(u, 4096, "…"), At: now})
	}
	if r := strings.TrimSpace(RedactSecrets(modelText)); r != "" {
		msgs = append(msgs, LocalTurnMessage{Role: "model", Text: strx.TruncateBytes(r, 8192, "…"), At: now})
	}
	const maxStored = 40
	if len(msgs) > maxStored {
		msgs = msgs[len(msgs)-maxStored:]
	}
	raw, err := json.MarshalIndent(msgs, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// ClearSession removes the short-term conversation file for workstream.
func (m *MemoryStore) ClearSession(workstream string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := filepath.Join(m.SessionsDir(), SanitizeWorkstream(workstream)+".json")
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func tokenizeTerms(s string) []string {
	low := strings.ToLower(s)
	fields := strings.FieldsFunc(low, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-'
	})
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

func scoreOverlap(qTerms []string, text string) int {
	if len(qTerms) == 0 {
		return 1
	}
	low := strings.ToLower(text)
	score := 0
	for _, t := range qTerms {
		if strings.Contains(low, t) {
			score += 2
		}
	}
	return score
}

func newShortID(prefix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}
