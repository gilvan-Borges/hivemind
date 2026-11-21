package jev

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Spec 06 §4: "chavear OutcomeLog.NoteQA por (repo, laneID)" — lanes paralelas
// registram lastWork independentes e o veredito consolidado do QA alcança todas.
func TestNoteQAKeyedByRepoAndLane(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outcomes.jsonl")
	log := OpenOutcomeLog(path)
	fixed := time.Date(2026, 11, 20, 15, 4, 5, 0, time.UTC)
	log.now = func() time.Time { return fixed }

	repo := "github.com/exemplo/repo"
	// Duas lanes paralelas com pares distintos.
	for _, lane := range []struct {
		step   string
		prov   string
		model  string
	}{
		{"dev_dev_1_report", "openrouter", "mid-model"},
		{"dev_dev_2_report", "opencode", "flash"},
	} {
		if err := log.Append(Outcome{
			RepoKey:    repo,
			Lane:       lane.step,
			Class:      ClassCoding,
			Complexity: ComplexityMedium,
			Provider:   lane.prov,
			Model:      lane.model,
			Tier:       TierMid,
			OK:         true,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Veredito consolidado (lane vazia): o QA aprovou a entrega — CADA lane
	// recebe o próprio outcome, sem sobrescrever o par da outra.
	if err := log.NoteQA(repo, "", true); err != nil {
		t.Fatal(err)
	}
	lines := readOutcomeLines(t, path)
	var qaLines int
	for _, o := range lines {
		if o.QA == QAApprove {
			qaLines++
			switch o.Lane {
			case "dev_dev_1_report":
				if o.Provider != "openrouter" || o.Model != "mid-model" {
					t.Fatalf("lane 1 verdict attributed to wrong pair: %+v", o)
				}
			case "dev_dev_2_report":
				if o.Provider != "opencode" || o.Model != "flash" {
					t.Fatalf("lane 2 verdict attributed to wrong pair: %+v", o)
				}
			default:
				t.Fatalf("verdict with unexpected lane: %+v", o)
			}
		}
	}
	if qaLines != 2 {
		t.Fatalf("expected one QA outcome per lane, got %d", qaLines)
	}

	// Veredito dirigido a UMA lane específica não toca a outra.
	if err := log.NoteQA(repo, "dev_dev_1_report", false); err != nil {
		t.Fatal(err)
	}
	lines = readOutcomeLines(t, path)
	var rejects int
	for _, o := range lines {
		if o.QA == QAReject {
			rejects++
			if o.Lane != "dev_dev_1_report" {
				t.Fatalf("targeted reject leaked to another lane: %+v", o)
			}
		}
	}
	if rejects != 1 {
		t.Fatalf("expected exactly one targeted reject, got %d", rejects)
	}
}

func jsonUnmarshalOutcome(line string, o *Outcome) error {
	return json.Unmarshal([]byte(line), o)
}

func readOutcomeLines(t *testing.T, path string) []Outcome {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []Outcome
	for _, line := range splitLines(string(raw)) {
		if line == "" {
			continue
		}
		var o Outcome
		if err := jsonUnmarshalOutcome(line, &o); err != nil {
			continue
		}
		out = append(out, o)
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
