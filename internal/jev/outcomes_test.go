package jev

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestOutcomeLogAppendRotateAndRedact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outcomes.jsonl")
	log := OpenOutcomeLog(path)
	log.maxBytes = 1 << 20
	fixed := time.Date(2026, 11, 20, 15, 4, 5, 0, time.UTC)
	log.now = func() time.Time { return fixed }

	err := log.Append(Outcome{
		RepoKey:           "github.com/exemplo/repo",
		Role:              string(team.RoleDeveloper),
		Class:             ClassCoding,
		Complexity:        ComplexityHigh,
		Provider:          "openrouter",
		Model:             "deepseek/deepseek-v4.1-flash",
		Tier:              TierMid,
		OK:                false,
		QA:                QAReject,
		Reason:            "qa_request_changes",
		FailCode:          "error",
		Tokens:            1200,
		LatencyMs:         340,
		EstimatedMicroUSD: 1500,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = log.Append(Outcome{
		RepoKey:  `C:\Users\secret\prompt.txt`,
		Provider: "openrouter",
		Model:    "sk-testsecret12345",
		Class:    ClassCoding,
		Reason:   "PROMPT_SIGILO sk-testsecret12345 crsr_abcdef12345",
		FailCode: "bearer sk-testsecret12345",
		OK:       false,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = log.Append(Outcome{
		RepoKey:  "github.com/exemplo/repo",
		Provider: "openrouter",
		Model:    "deepseek/deepseek-v4.1-flash",
		Class:    ClassCoding,
		Reason:   "qa_approve",
		QA:       QAApprove,
		OK:       true,
	})
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, leaked := range []string{
		"PROMPT_SIGILO", "sk-testsecret12345", "crsr_abcdef12345", `Users\secret`, "prompt.txt", `"prompt"`,
	} {
		if strings.Contains(text, leaked) {
			t.Fatalf("outcomes.jsonl leaked %q:\n%s", leaked, text)
		}
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 2 {
		t.Fatalf("append-only log should keep 2 safe lines, got %d:\n%s", len(lines), text)
	}
	var got Outcome
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Class != ClassCoding || got.Provider != "openrouter" || got.Model != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("missing task identity: %+v", got)
	}
	if got.QA != QAReject || got.Reason != "qa_request_changes" || got.OK {
		t.Fatalf("QA reject not stored: %+v", got)
	}
	if got.LatencyMs != 340 || got.EstimatedMicroUSD != 1500 || got.Tokens != 1200 {
		t.Fatalf("cost/latency/tokens not stored: %+v", got)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("outcomes.jsonl mode = %o, want 0600", fi.Mode().Perm())
		}
	}

	rotDir := t.TempDir()
	rotPath := filepath.Join(rotDir, "outcomes.jsonl")
	rot := OpenOutcomeLog(rotPath)
	rot.maxBytes = 1
	rot.now = func() time.Time { return fixed }
	for i, model := range []string{"m1", "m2", "m3"} {
		if err := rot.Append(Outcome{
			RepoKey:  "github.com/exemplo/repo",
			Provider: "openrouter",
			Model:    model,
			Class:    ClassCoding,
			OK:       i%2 == 0,
		}); err != nil {
			t.Fatal(err)
		}
	}
	prev := rotPath + ".1"
	prevBody, err := os.ReadFile(prev)
	if err != nil {
		t.Fatal(err)
	}
	curBody, err := os.ReadFile(rotPath)
	if err != nil {
		t.Fatal(err)
	}
	combined := string(prevBody) + string(curBody)
	if strings.Contains(combined, `"model":"m1"`) {
		t.Fatalf("rotation kept more than one previous file:\nprev=%s\ncur=%s", prevBody, curBody)
	}
	if !strings.Contains(string(prevBody), `"model":"m2"`) || !strings.Contains(string(curBody), `"model":"m3"`) {
		t.Fatalf("expected m2 in the previous file and m3 in the current one:\nprev=%s\ncur=%s", prevBody, curBody)
	}
	matches, _ := filepath.Glob(filepath.Join(rotDir, "outcomes.jsonl*"))
	if len(matches) != 2 {
		t.Fatalf("want exactly outcomes.jsonl and outcomes.jsonl.1, got %v", matches)
	}

	reloaded := OpenOutcomeLog(rotPath)
	reloaded.now = func() time.Time { return fixed }
	snap := reloaded.Snapshot("github.com/exemplo/repo")
	if snap == nil {
		t.Fatal("reloaded log lost in-window samples")
	}
	if q := snap.Quality("openrouter", "m3", 0.5); q != 0.5 {
		t.Fatalf("two reloaded samples must not blend the prior, got %v", q)
	}
}

func TestLearnedPriorAfterEightSamples(t *testing.T) {
	const (
		repo  = "github.com/exemplo/repo"
		prior = 0.90
	)
	fixed := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	cat := learningCatalog(0.40)
	pol := DefaultPolicy()
	health := HealthSnapshot{Credentials: map[string]bool{"openrouter": true}, AgyRemainingPct: -1}
	task := Task{
		Role:        team.RoleDeveloper,
		Class:       ClassCoding,
		Complexity:  ComplexityLow,
		InputTokens: 1000,
		NeedsTools:  true,
		RepoKey:     repo,
	}

	t.Run("seven keep the prior and the eighth blends", func(t *testing.T) {
		log := newFixedLog(t, fixed)
		if got := BlendQuality(prior, 7, 0); got != prior {
			t.Fatalf("7 samples changed the prior: got %v want %v", got, prior)
		}
		appendSamples(t, log, repo, ClassCoding, "model-a", false, fixed, 7)
		appendSamples(t, log, repo, ClassCoding, "model-b", true, fixed, 7)
		task.Learn = log.Snapshot(repo)
		if choice := mustScore(t, task, cat, pol, health); choice.Model != "model-a" {
			t.Fatalf("7 samples must keep the higher prior, got %s", choice.Model)
		}
		if q := task.Learn.Quality("openrouter", "model-a", prior); q != prior {
			t.Fatalf("quality after 7 failures = %v, want prior %v", q, prior)
		}

		appendSamples(t, log, repo, ClassCoding, "model-a", false, fixed, 1)
		appendSamples(t, log, repo, ClassCoding, "model-b", true, fixed, 1)
		task.Learn = log.Snapshot(repo)
		wantA := BlendQuality(prior, 8, 0)
		wantB := BlendQuality(0.40, 8, 8)
		if math.Abs(wantA-0.45) > 1e-9 || math.Abs(wantB-0.70) > 1e-9 {
			t.Fatalf("fixed blend = %v and %v, want 0.45 and 0.70", wantA, wantB)
		}
		if q := task.Learn.Quality("openrouter", "model-a", prior); math.Abs(q-wantA) > 1e-9 {
			t.Fatalf("quality after 8 failures = %v, want %v", q, wantA)
		}
		if q := task.Learn.Quality("openrouter", "model-b", 0.40); math.Abs(q-wantB) > 1e-9 {
			t.Fatalf("quality after 8 successes = %v, want %v", q, wantB)
		}
		if choice := mustScore(t, task, cat, pol, health); choice.Model != "model-b" {
			t.Fatalf("8th sample must switch the route to the observed winner, got %s", choice.Model)
		}

		cs := &CatalogStore{catalog: cat, nowFn: time.Now}
		router := &Router{
			Catalog:  cs,
			Policy:   pol,
			Health:   func() HealthSnapshot { return health },
			Outcomes: log,
		}
		routed, ref := router.Route(context.Background(), RouteInput{Task: Task{
			Role: task.Role, Class: task.Class, Complexity: task.Complexity,
			InputTokens: task.InputTokens, NeedsTools: task.NeedsTools, RepoKey: repo,
		}})
		if ref.Code != "" || routed.Model != "model-b" {
			t.Fatalf("Route should apply the learned prior, got %+v refusal %+v", routed, ref)
		}
	})

	t.Run("samples older than 30 days stay out", func(t *testing.T) {
		log := newFixedLog(t, fixed)
		old := fixed.Add(-31 * 24 * time.Hour)
		appendSamples(t, log, repo, ClassCoding, "model-a", false, old, 8)
		task.Learn = log.Snapshot(repo)
		if task.Learn != nil {
			if q := task.Learn.Quality("openrouter", "model-a", prior); q != prior {
				t.Fatalf("expired samples changed quality to %v", q)
			}
		}
		wide := learningCatalog(0.80)
		if choice := mustScore(t, task, wide, pol, health); choice.Model != "model-a" {
			t.Fatalf("expired failures must not hand the route to model-b, got %s", choice.Model)
		}
	})

	t.Run("other repo does not count", func(t *testing.T) {
		log := newFixedLog(t, fixed)
		appendSamples(t, log, "github.com/outro/repo", ClassCoding, "model-a", false, fixed, 8)
		task.Learn = log.Snapshot(repo)
		wide := learningCatalog(0.80)
		if choice := mustScore(t, task, wide, pol, health); choice.Model != "model-a" {
			t.Fatalf("failures in another repo changed the route to %s", choice.Model)
		}
	})
}

func TestDeescalateAfterTwoQAApprovals(t *testing.T) {
	const repo = "github.com/exemplo/repo"
	fixed := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	cat := tierCatalog()
	pol := DefaultPolicy()
	health := HealthSnapshot{
		Credentials:     map[string]bool{"openrouter": true, "opencode": true},
		AgyRemainingPct: -1,
	}
	task := Task{
		Role:        team.RoleDeveloper,
		Class:       ClassCoding,
		Complexity:  ComplexityHigh,
		InputTokens: 1000,
		NeedsTools:  true,
		RepoKey:     repo,
	}

	log := newFixedLog(t, fixed)
	if choice := mustScore(t, task, cat, pol, health); choice.Model != "mid-model" || choice.Tier != TierMid {
		t.Fatalf("high coding task starts on mid, got %+v", choice)
	}
	if err := log.Append(Outcome{
		RepoKey: repo, Class: ClassCoding, Complexity: ComplexityHigh,
		Provider: "openrouter", Model: "mid-model", Tier: TierMid, OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := log.NoteQA(repo, "", true); err != nil {
		t.Fatal(err)
	}
	task.Learn = log.Snapshot(repo)
	if choice := mustScore(t, task, cat, pol, health); choice.Model != "mid-model" {
		t.Fatalf("one QA approval must stay on mid, got %s", choice.Model)
	}

	if err := log.NoteQA(repo, "", true); err != nil {
		t.Fatal(err)
	}
	task.Learn = log.Snapshot(repo)
	choice := mustScore(t, task, cat, pol, health)
	if choice.Model != "flash" || choice.Tier != TierCheap || !hasReason(choice, "deescalated") {
		t.Fatalf("two QA approvals must birth the next task on cheap, got %+v", choice)
	}

	if err := log.NoteQA(repo, "", false); err != nil {
		t.Fatal(err)
	}
	task.Learn = log.Snapshot(repo)
	if choice := mustScore(t, task, cat, pol, health); choice.Model != "mid-model" {
		t.Fatalf("a QA rejection clears de-escalation, got %s", choice.Model)
	}

	cheap := newFixedLog(t, fixed)
	if err := cheap.Append(Outcome{
		RepoKey: repo, Class: ClassCoding, Provider: "opencode", Model: "flash", Tier: TierCheap, OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := cheap.NoteQA(repo, "", true); err != nil || cheap.NoteQA(repo, "", true) != nil {
		t.Fatal("note qa")
	}
	task.Learn = cheap.Snapshot(repo)
	if choice := mustScore(t, task, cat, pol, health); choice.Model != "mid-model" {
		t.Fatalf("approvals on the cheap model must not de-escalate, got %s", choice.Model)
	}

	planLog := newFixedLog(t, fixed)
	plan := task
	plan.Class = ClassPlanning
	plan.Learn = nil
	if err := planLog.Append(Outcome{
		At: fixed, RepoKey: repo, Class: ClassPlanning, Complexity: ComplexityHigh,
		Provider: "openrouter", Model: "mid-model", Tier: TierMid, OK: true, PlanningOK: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := planLog.Append(Outcome{
		At: fixed, RepoKey: repo, Class: ClassPlanning, Complexity: ComplexityHigh,
		Provider: "openrouter", Model: "mid-model", Tier: TierMid, OK: true, PlanningOK: true,
	}); err != nil {
		t.Fatal(err)
	}
	plan.Learn = planLog.Snapshot(repo)
	if choice := mustScore(t, plan, cat, pol, health); choice.Tier != TierCheap || choice.Model != "flash" {
		t.Fatalf("two clean planning outcomes must drop one tier, got %+v", choice)
	}
}

func newFixedLog(t *testing.T, now time.Time) *OutcomeLog {
	t.Helper()
	log := OpenOutcomeLog(filepath.Join(t.TempDir(), "outcomes.jsonl"))
	log.now = func() time.Time { return now }
	return log
}

func appendSamples(t *testing.T, log *OutcomeLog, repo string, class Class, model string, ok bool, at time.Time, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := log.Append(Outcome{
			At: at, RepoKey: repo, Class: class, Provider: "openrouter", Model: model, Tier: TierCheap, OK: ok,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func mustScore(t *testing.T, task Task, cat Catalog, pol Policy, health HealthSnapshot) Choice {
	t.Helper()
	choice, ref := Score(task, cat, pol, health, AgentPin{}, RepoPolicy{})
	if ref.Code != "" {
		t.Fatalf("unexpected refusal: %+v", ref)
	}
	return choice
}

func hasReason(c Choice, want string) bool {
	for _, r := range c.Reasons {
		if r == want {
			return true
		}
	}
	return false
}

func learningCatalog(bQuality float64) Catalog {
	base := func(model string, quality float64) ModelEntry {
		return ModelEntry{
			Provider:           "openrouter",
			Model:              model,
			ContextLength:      128_000,
			PromptMicroUSD:     0.10,
			CompletionMicroUSD: 0.20,
			Capabilities:       Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:       500,
			Quality:            quality,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierCheap,
			Source:             "static",
		}
	}
	return Catalog{Version: 1, Entries: []ModelEntry{
		base("model-a", 0.90),
		base("model-b", bQuality),
	}}
}

func tierCatalog() Catalog {
	return Catalog{Version: 1, Entries: []ModelEntry{
		{
			Provider:           "opencode",
			Model:              "flash",
			ContextLength:      128_000,
			PromptMicroUSD:     0.05,
			CompletionMicroUSD: 0.10,
			Capabilities:       Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:       400,
			Quality:            0.99,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierCheap,
			Source:             "static",
		},
		{
			Provider:           "openrouter",
			Model:              "mid-model",
			ContextLength:      128_000,
			PromptMicroUSD:     0.40,
			CompletionMicroUSD: 0.80,
			Capabilities:       Capabilities{Tools: true, Stream: true},
			LatencyP50Ms:       800,
			Quality:            0.20,
			DataClass:          DataClassHTTPAPI,
			Tier:               TierMid,
			Source:             "static",
		},
	}}
}
