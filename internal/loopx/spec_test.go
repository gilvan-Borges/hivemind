package loopx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeRejectsEscalationAndMerge(t *testing.T) {
	base := Spec{ID: "fix-auth", Kind: KindGoal, Objective: "deixar o teste verde"}
	spec, err := Normalize(base)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Limits.MaxIterations != 10 || spec.Limits.MaxCostUSD != 5 || spec.Limits.StallAfter != 3 {
		t.Fatalf("tetos padrão: %+v", spec.Limits)
	}
	if spec.Limits.MaxDuration != "2h0m0s" && spec.Limits.MaxDuration != "2h" {
		t.Fatalf("duração: %s", spec.Limits.MaxDuration)
	}
	if !spec.Git.OpenPR() || spec.Git.AutoMerge {
		t.Fatalf("git: %+v", spec.Git)
	}
	base.Git.AutoMerge = true
	if _, err := Normalize(base); err == nil || !strings.Contains(err.Error(), "merge") {
		t.Fatalf("merge: %v", err)
	}
	base.Git.AutoMerge = false
	base.Permissions.Mode = "full"
	if _, err := Normalize(base); err == nil {
		t.Fatal("modo full na spec deveria falhar")
	}
	base.Permissions.Mode = "inherit"
	base.Executor.Backend = "squad"
	if _, err := Normalize(base); err == nil || !strings.Contains(err.Error(), "hive") {
		t.Fatalf("squad: %v", err)
	}
}

func TestSpecRoundTripAndGitignore(t *testing.T) {
	ws := t.TempDir()
	off := false
	spec := Spec{
		ID: "fix-auth", Kind: KindGoal, Objective: "testes verdes",
		Checks: []Check{{Name: "unit", Run: "go test ./..."}},
		Judge:  JudgeSpec{Enabled: &off},
	}
	if _, err := SaveSpec(ws, spec); err != nil {
		t.Fatal(err)
	}
	if err := EnsureIgnored(ws); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(ws, ".gitignore"))
	if err != nil || !strings.Contains(string(raw), ".hivemind/") {
		t.Fatalf("gitignore: %q %v", raw, err)
	}
	loaded, hash, err := LoadSpec(ws, "fix-auth")
	if err != nil || hash == "" || loaded.Checks[0].Run != "go test ./..." {
		t.Fatalf("load: %+v %s %v", loaded, hash, err)
	}
	if loaded.Judge.On() {
		t.Fatal("juiz foi desligado na spec")
	}
	n, err := PauseRunning(ws)
	if err != nil || n != 0 {
		t.Fatalf("nada rodando: %d %v", n, err)
	}
	if _, err := UpdateState(ws, "fix-auth", func(st *State) error {
		st.Status = StatusRunning
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	n, err = PauseRunning(ws)
	if err != nil || n != 1 {
		t.Fatalf("pause: %d %v", n, err)
	}
	st, err := LoadState(ws, "fix-auth")
	if err != nil || st.Status != StatusPaused {
		t.Fatalf("estado: %+v %v", st, err)
	}
}
