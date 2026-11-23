package loopx

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
	"github.com/gilvan-Borges/HiveMind/internal/fslockx"
)

const (
	StatusRunning  = "running"
	StatusPaused   = "paused"
	StatusWaiting  = "waiting"
	StatusStopped  = "stopped"
	StatusFinished = "finished"
)

// State is the durable loop record in state.json.
type State struct {
	Status                string    `json:"status"`
	Outcome               string    `json:"outcome,omitempty"`
	Iteration             int       `json:"iteration"`
	Phase                 string    `json:"phase,omitempty"`
	CurrentItem           string    `json:"currentItem,omitempty"`
	StartedAt             time.Time `json:"startedAt,omitempty"`
	UpdatedAt             time.Time `json:"updatedAt,omitempty"`
	SpentMicroUSD         int64     `json:"spentMicroUsd,omitempty"`
	Tokens                int64     `json:"tokens,omitempty"`
	LastVerdict           string    `json:"lastVerdict,omitempty"`
	LastReason            string    `json:"lastReason,omitempty"`
	Fingerprint           string    `json:"fingerprint,omitempty"`
	HaveFingerprint       bool      `json:"haveFingerprint,omitempty"`
	PassingChecks         int       `json:"passingChecks,omitempty"`
	ConsecutiveNoProgress int       `json:"consecutiveNoProgress,omitempty"`
	ConsecutiveImpossible int       `json:"consecutiveImpossible,omitempty"`
	SpecHash              string    `json:"specHash,omitempty"`
	ConversationID        string    `json:"conversationId,omitempty"`
	SessionID             string    `json:"sessionId,omitempty"`
	Branch                string    `json:"branch,omitempty"`
	PRURL                 string    `json:"prUrl,omitempty"`
	StopRequested         bool      `json:"stopRequested,omitempty"`
}

// Summary is the row the IDE and the app list.
type Summary struct {
	ID            string    `json:"id"`
	Name          string    `json:"name,omitempty"`
	Kind          string    `json:"kind,omitempty"`
	Status        string    `json:"status"`
	Outcome       string    `json:"outcome,omitempty"`
	Iteration     int       `json:"iteration"`
	Phase         string    `json:"phase,omitempty"`
	LastVerdict   string    `json:"lastVerdict,omitempty"`
	LastReason    string    `json:"lastReason,omitempty"`
	SpentMicroUSD int64     `json:"spentMicroUsd,omitempty"`
	Tokens        int64     `json:"tokens,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt,omitempty"`
	PRURL         string    `json:"prUrl,omitempty"`
	Workspace     string    `json:"workspace,omitempty"`
}

// IterationRecord is one JSONL line.
type IterationRecord struct {
	Iteration     int       `json:"iteration"`
	At            time.Time `json:"at"`
	Phase         string    `json:"phase,omitempty"`
	Item          string    `json:"item,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	ChecksOK      bool      `json:"checksOk"`
	Verdict       string    `json:"verdict,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	SpentMicroUSD int64     `json:"spentMicroUsd,omitempty"`
	Tokens        int64     `json:"tokens,omitempty"`
	Commit        string    `json:"commit,omitempty"`
}

// LoadState reads state.json. A missing file is an empty paused state.
func LoadState(workspace, id string) (State, error) {
	dir, err := Dir(workspace, id)
	if err != nil {
		return State{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return State{Status: StatusPaused}, nil
		}
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{}, fmt.Errorf("state.json inválido: %w", err)
	}
	if st.Status == "" {
		st.Status = StatusPaused
	}
	return st, nil
}

// UpdateState locks, mutates and writes state.json.
func UpdateState(workspace, id string, fn func(*State) error) (State, error) {
	dir, err := Dir(workspace, id)
	if err != nil {
		return State{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return State{}, err
	}
	path := filepath.Join(dir, "state.json")
	release, err := fslockx.Lock(path, 5*time.Second)
	if err != nil {
		return State{}, err
	}
	defer release()
	st, err := LoadState(workspace, id)
	if err != nil {
		return State{}, err
	}
	if err := fn(&st); err != nil {
		return st, err
	}
	st.UpdatedAt = time.Now().UTC()
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return st, err
	}
	raw = append(raw, '\n')
	if err := atomicx.WriteFile(path, raw, 0o644); err != nil {
		return st, err
	}
	return st, nil
}

// List reads every loop directory in the workspace.
func List(workspace string) ([]Summary, error) {
	base, err := safeWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(base, ".hivemind", "loops")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Summary, 0, len(entries))
	for _, ent := range entries {
		if !ent.IsDir() || !ValidID(ent.Name()) {
			continue
		}
		sum, err := Summarize(workspace, ent.Name())
		if err != nil {
			continue
		}
		out = append(out, sum)
	}
	return out, nil
}

// Summarize joins state.json with the spec name.
func Summarize(workspace, id string) (Summary, error) {
	st, err := LoadState(workspace, id)
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{
		ID:            id,
		Status:        st.Status,
		Outcome:       st.Outcome,
		Iteration:     st.Iteration,
		Phase:         st.Phase,
		LastVerdict:   st.LastVerdict,
		LastReason:    st.LastReason,
		SpentMicroUSD: st.SpentMicroUSD,
		Tokens:        st.Tokens,
		UpdatedAt:     st.UpdatedAt,
		PRURL:         st.PRURL,
		Workspace:     workspace,
	}
	if spec, _, err := LoadSpec(workspace, id); err == nil {
		sum.Name = spec.Name
		sum.Kind = spec.Kind
	}
	return sum, nil
}

// PauseRunning moves every running loop to paused. It does not start work.
func PauseRunning(workspace string) (int, error) {
	list, err := List(workspace)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sum := range list {
		if sum.Status != StatusRunning {
			continue
		}
		next := StatusPaused
		if spec, _, serr := LoadSpec(workspace, sum.ID); serr == nil && (spec.Kind == KindTime || spec.Kind == KindProactive) {
			// Um loop por tempo ou evento volta a esperar o próximo gatilho.
			// Pausa de pessoa continua paused e o cron não acorda.
			next = StatusWaiting
		}
		if _, err := UpdateState(workspace, sum.ID, func(st *State) error {
			if st.Status == StatusRunning {
				st.Status = next
				st.Phase = ""
			}
			return nil
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// AppendIteration adds one JSONL record.
func AppendIteration(workspace, id string, rec IterationRecord) error {
	dir, err := Dir(workspace, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	f, err := os.OpenFile(filepath.Join(dir, "iterations.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(raw); err != nil {
		return err
	}
	return f.Sync()
}

// ReadIterations returns the JSONL log, newest last.
func ReadIterations(workspace, id string, limit int) ([]IterationRecord, error) {
	dir, err := Dir(workspace, id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(dir, "iterations.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []IterationRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec IterationRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// AppendProgress adds the iteration diary the next executor reads.
func AppendProgress(workspace, id string, iteration int, text, verdict string) error {
	dir, err := Dir(workspace, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	line := fmt.Sprintf("\n## Iteração %d\n%s\nVeredito: %s\n", iteration, strings.TrimSpace(text), strings.TrimSpace(verdict))
	f, err := os.OpenFile(filepath.Join(dir, "progress.md"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		return err
	}
	return f.Sync()
}

// ReadProgressTail returns the end of the diary, capped at maxBytes.
func ReadProgressTail(workspace, id string, maxBytes int) string {
	dir, err := Dir(workspace, id)
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(dir, "progress.md"))
	if err != nil || len(raw) == 0 {
		return ""
	}
	if maxBytes > 0 && len(raw) > maxBytes {
		raw = raw[len(raw)-maxBytes:]
	}
	return string(raw)
}
