package gitx

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const checkpointRefPrefix = "refs/hivemind/checkpoints/"

var checkpointSeq uint64

// Checkpoint captures a lightweight Git snapshot created before a developer turn.
type Checkpoint struct {
	ID        string    `json:"id"`
	Ref       string    `json:"ref"`
	SHA       string    `json:"sha"`
	Label     string    `json:"label,omitempty"`
	HasDiff   bool      `json:"hasDiff,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// CreateCheckpoint captures the current working tree + index of wtDir into an
// internal ref (refs/hivemind/checkpoints/<id>) without mutating HEAD or files.
func CreateCheckpoint(ctx context.Context, g Git, wtDir, runID, label string) (Checkpoint, error) {
	id := sanitizeCheckpointID(runID)
	out, err := g.Run(ctx, wtDir, "stash", "create", strings.TrimSpace(label))
	sha := extractTrailingSHA(out)
	hasDiff := false
	if err == nil && sha != "" {
		hasDiff = true
	} else {
		headOut, hErr := g.Run(ctx, wtDir, "rev-parse", "HEAD")
		headSHA := extractTrailingSHA(headOut)
		if hErr != nil || headSHA == "" {
			return Checkpoint{}, fmt.Errorf("checkpoint sem commit HEAD em %s: %w", wtDir, hErr)
		}
		sha = headSHA
	}
	ref := checkpointRefPrefix + id
	if _, err := g.Run(ctx, wtDir, "update-ref", ref, sha); err != nil {
		return Checkpoint{}, fmt.Errorf("gravar ref do checkpoint %s: %w", id, err)
	}
	return Checkpoint{
		ID:        id,
		Ref:       ref,
		SHA:       sha,
		Label:     strings.TrimSpace(label),
		HasDiff:   hasDiff,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// ListCheckpoints returns all HiveMind checkpoints sorted newest-first.
func ListCheckpoints(ctx context.Context, g Git, repoOrWT string) ([]Checkpoint, error) {
	out, err := g.Run(ctx, repoOrWT, "for-each-ref", "--format=%(refname)%09%(objectname)%09%(committerdate:unix)%09%(subject)", checkpointRefPrefix)
	if err != nil {
		return nil, err
	}
	var cps []Checkpoint
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 2 {
			continue
		}
		ref := strings.TrimSpace(parts[0])
		id := strings.TrimPrefix(ref, checkpointRefPrefix)
		sha := strings.TrimSpace(parts[1])
		var ts time.Time
		if len(parts) >= 3 {
			if sec, convErr := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64); convErr == nil && sec > 0 {
				ts = time.Unix(sec, 0).UTC()
			}
		}
		subj := ""
		if len(parts) == 4 {
			subj = strings.TrimSpace(parts[3])
		}
		cps = append(cps, Checkpoint{
			ID:        id,
			Ref:       ref,
			SHA:       sha,
			Label:     subj,
			CreatedAt: ts,
		})
	}
	sort.Slice(cps, func(i, j int) bool {
		if !cps[i].CreatedAt.Equal(cps[j].CreatedAt) {
			return cps[i].CreatedAt.After(cps[j].CreatedAt)
		}
		return cps[i].ID > cps[j].ID
	})
	return cps, nil
}

// RollbackCheckpoint restores wtDir back to the exact state stored in checkpointID.
func RollbackCheckpoint(ctx context.Context, g Git, wtDir, checkpointID string) error {
	id := strings.TrimPrefix(strings.TrimSpace(checkpointID), checkpointRefPrefix)
	if id == "" || strings.Contains(id, "..") || strings.ContainsAny(id, "/\\ ") {
		return fmt.Errorf("id de checkpoint inválido: %q", checkpointID)
	}
	ref := checkpointRefPrefix + id
	sha, err := g.Run(ctx, wtDir, "rev-parse", "--verify", ref)
	if err != nil {
		return fmt.Errorf("checkpoint %q não encontrado: %w", id, err)
	}
	sha = strings.TrimSpace(sha)

	parentsLine, err := g.Run(ctx, wtDir, "rev-list", "--parents", "-n", "1", sha)
	if err != nil {
		return fmt.Errorf("inspecionar checkpoint %q: %w", id, err)
	}
	fields := strings.Fields(strings.TrimSpace(parentsLine))
	// `git stash create` produces a merge commit with >= 2 parents:
	// parent 1 = base HEAD, parent 2 = staged index state.
	if len(fields) >= 3 {
		baseSHA := fields[1]
		if _, err := g.Run(ctx, wtDir, "reset", "--hard", baseSHA); err != nil {
			return fmt.Errorf("reset para base %s: %w", baseSHA, err)
		}
		if _, err := g.Run(ctx, wtDir, "clean", "-fd"); err != nil {
			return fmt.Errorf("limpar arquivos após reset: %w", err)
		}
		if _, err := g.Run(ctx, wtDir, "stash", "apply", "--index", sha); err != nil {
			if _, err2 := g.Run(ctx, wtDir, "stash", "apply", sha); err2 != nil {
				return fmt.Errorf("aplicar snapshot do checkpoint: %w", err2)
			}
		}
		return nil
	}

	if _, err := g.Run(ctx, wtDir, "reset", "--hard", sha); err != nil {
		return fmt.Errorf("reset para checkpoint %s: %w", sha, err)
	}
	if _, err := g.Run(ctx, wtDir, "clean", "-fd"); err != nil {
		return fmt.Errorf("limpar arquivos não rastreados: %w", err)
	}
	return nil
}

// DeleteCheckpoint deletes the internal checkpoint ref.
func DeleteCheckpoint(ctx context.Context, g Git, repoOrWT, checkpointID string) error {
	id := strings.TrimPrefix(strings.TrimSpace(checkpointID), checkpointRefPrefix)
	if id == "" || strings.Contains(id, "..") || strings.ContainsAny(id, "/\\ ") {
		return fmt.Errorf("id de checkpoint inválido: %q", checkpointID)
	}
	_, err := g.Run(ctx, repoOrWT, "update-ref", "-d", checkpointRefPrefix+id)
	return err
}

func extractTrailingSHA(raw string) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		cand := strings.TrimSpace(lines[i])
		if len(cand) < 7 || len(cand) > 64 {
			continue
		}
		valid := true
		for _, r := range cand {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				valid = false
				break
			}
		}
		if valid {
			return strings.ToLower(cand)
		}
	}
	return ""
}

func sanitizeCheckpointID(raw string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	base := strings.Trim(b.String(), "-")
	if base == "" {
		base = "cp"
	}
	seq := atomic.AddUint64(&checkpointSeq, 1)
	return fmt.Sprintf("%s-%d-%d", base, time.Now().UTC().UnixMilli(), seq)
}
