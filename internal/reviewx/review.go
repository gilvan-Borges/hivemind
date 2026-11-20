package reviewx

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

const noteBodyMax = 2 << 10

// LineNote is a local review comment. It is not a merge approval.
type LineNote struct {
	ID         string    `json:"id"`
	RepoKey    string    `json:"repoKey"`
	Path       string    `json:"path"`
	Side       string    `json:"side"`
	Line       int       `json:"line"`
	HunkHeader string    `json:"hunkHeader,omitempty"`
	Body       string    `json:"body"`
	LaneID     string    `json:"laneId,omitempty"`
	SessionID  string    `json:"sessionId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	SentAt     time.Time `json:"sentAt,omitempty"`
}

// Add stores one note outside the worktree. The path must stay inside the workspace jail.
func Add(workspaceRoot, repoKey string, note LineNote) (LineNote, error) {
	if _, err := workspace.Resolve(workspaceRoot, note.Path); err != nil {
		return LineNote{}, err
	}
	side := strings.ToLower(strings.TrimSpace(note.Side))
	if side != "old" && side != "new" {
		return LineNote{}, fmt.Errorf("lado inválido")
	}
	if note.Line < 1 {
		return LineNote{}, fmt.Errorf("linha inválida")
	}
	body := resident.RedactSecrets(strings.TrimSpace(note.Body))
	if body == "" {
		return LineNote{}, fmt.Errorf("comentário vazio")
	}
	if len(body) > noteBodyMax {
		body = body[:noteBodyMax]
	}
	note.Side = side
	note.Body = body
	note.Path = filepath.ToSlash(note.Path)
	note.RepoKey = safeKey(repoKey)
	if note.ID == "" {
		note.ID = newID()
	}
	if note.CreatedAt.IsZero() {
		note.CreatedAt = time.Now().UTC()
	}
	if err := appendNote(note); err != nil {
		return LineNote{}, err
	}
	return note, nil
}

// Send builds one handoff package for the chosen role. It does not include a transcript.
func Send(workspaceRoot, repoKey, noteID, toRole string) (orchestrator.HandoffPackage, error) {
	key := safeKey(repoKey)
	notes, err := readNotes(key)
	if err != nil {
		return orchestrator.HandoffPackage{}, err
	}
	var found *LineNote
	for i := range notes {
		if notes[i].ID == noteID {
			found = &notes[i]
			break
		}
	}
	if found == nil {
		return orchestrator.HandoffPackage{}, fmt.Errorf("nota não encontrada")
	}
	if _, err := workspace.Resolve(workspaceRoot, found.Path); err != nil {
		return orchestrator.HandoffPackage{}, err
	}
	found.SentAt = time.Now().UTC()
	if err := rewriteNotes(key, notes); err != nil {
		return orchestrator.HandoffPackage{}, err
	}
	fact := resident.WrapUntrustedData("review", found.Path, fmt.Sprintf("%s:%d %s %s", found.Side, found.Line, found.HunkHeader, found.Body))
	return orchestrator.HandoffPackage{
		Schema:       1,
		RunID:        "review-" + found.ID,
		FromRole:     "review",
		ToRole:       strings.TrimSpace(toRole),
		Objective:    "comentário de revisão",
		Facts:        []string{fact},
		Files:        []string{found.Path},
		DeliveryHash: found.ID,
	}, nil
}

// SuggestLane returns the lane that edited path in this turn. Empty means no suggestion.
func SuggestLane(edited map[string]string, path string) string {
	return edited[filepath.ToSlash(strings.TrimSpace(path))]
}

func notesPath(repoKey string) string {
	return filepath.Join(workspace.AppDataRoot(), "reviews", safeKey(repoKey)+".jsonl")
}

func appendNote(note LineNote) error {
	path := notesPath(note.RepoKey)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(note)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func readNotes(repoKey string) ([]LineNote, error) {
	f, err := os.Open(notesPath(repoKey))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []LineNote
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var n LineNote
		if err := json.Unmarshal(sc.Bytes(), &n); err != nil {
			continue
		}
		out = append(out, n)
	}
	return out, sc.Err()
}

func rewriteNotes(repoKey string, notes []LineNote) error {
	path := notesPath(repoKey)
	var b strings.Builder
	for _, n := range notes {
		raw, err := json.Marshal(n)
		if err != nil {
			return err
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func safeKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		key = "local"
	}
	var b strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "local"
	}
	return b.String()
}

func newID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
