package orchestrator

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// TestDynamicReminderDeduplicatesUnchangedStateKeys verifies the token-saving
// behavior of the reminder callback: a state key whose value did not change
// AND whose full value is still verbatim in the request history is re-sent
// as a one-line pointer instead of a full copy. A changed value must return
// to full injection.
func TestDynamicReminderDeduplicatesUnchangedStateKeys(t *testing.T) {
	r := &Runner{}
	devAgent := team.Agent{Role: team.RoleDeveloper, Name: "dev_1", Provider: "agy", Model: "agy"}
	keys := []string{"chief_plan"}
	plan := "plano estável do chefe com 12 passos e orçamento detalhado"
	state := fakeReminderState{m: map[string]any{"chief_plan": plan}}
	ctxFake := &reminderCtxFake{state: state}
	reminder := r.newDynamicContextReminderCallback("dev_1", devAgent, false, "dev_dev_1_report", keys)

	requestFor := func() *model.LLMRequest {
		return &model.LLMRequest{
			Contents: []*genai.Content{
				{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "Implemente X\n[chief_plan]\n" + plan}}},
				{Role: genai.RoleModel, Parts: []*genai.Part{{Text: "ok"}}},
				{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "continua"}}},
			},
		}
	}

	lastReminderText := func(req *model.LLMRequest) string {
		last := req.Contents[len(req.Contents)-1]
		return last.Parts[len(last.Parts)-1].Text
	}

	// Turn 1: first injection must carry the full plan.
	req1 := requestFor()
	if _, err := reminder(ctxFake, req1); err != nil {
		t.Fatalf("turn 1: %v", err)
	}
	if txt := lastReminderText(req1); !strings.Contains(txt, plan) {
		t.Fatalf("turn 1: full plan missing from reminder: %q", txt)
	}

	// Turn 2: unchanged and still in history → pointer, not a second copy.
	req2 := requestFor()
	if _, err := reminder(ctxFake, req2); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	txt2 := lastReminderText(req2)
	if !strings.Contains(txt2, "inalterado desde o turno anterior") {
		t.Fatalf("turn 2: expected dedup pointer, got %q", txt2)
	}
	if strings.Count(txt2, plan) != 0 {
		t.Fatalf("turn 2: full plan leaked into the pointer turn: %q", txt2)
	}

	// Turn 3: the plan changed → full injection again.
	newPlan := plan + " — revisão pós-feedback do QA"
	state.m["chief_plan"] = newPlan
	req3 := requestFor()
	if _, err := reminder(ctxFake, req3); err != nil {
		t.Fatalf("turn 3: %v", err)
	}
	if txt := lastReminderText(req3); !strings.Contains(txt, newPlan) {
		t.Fatalf("turn 3: changed plan missing from reminder: %q", txt)
	}
}

// TestDynamicReminderReinjectsAfterHistoryLoss verifies the compaction safety
// net: when the old turns carrying the full value are no longer part of the
// request (semantic compaction removed them), an unchanged key must be sent
// in full again instead of a pointer to content the model cannot see.
func TestDynamicReminderReinjectsAfterHistoryLoss(t *testing.T) {
	r := &Runner{}
	devAgent := team.Agent{Role: team.RoleDeveloper, Name: "dev_1", Provider: "agy", Model: "agy"}
	keys := []string{"chief_plan"}
	plan := "plano estável do chefe"
	state := fakeReminderState{m: map[string]any{"chief_plan": plan}}
	ctxFake := &reminderCtxFake{state: state}
	reminder := r.newDynamicContextReminderCallback("dev_1", devAgent, false, "dev_dev_1_report", keys)

	withHistory := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "Implemente X\n[chief_plan]\n" + plan}}},
		},
	}
	if _, err := reminder(ctxFake, withHistory); err != nil {
		t.Fatalf("turn 1: %v", err)
	}

	// History was compacted away: the value is gone from the request.
	withoutHistory := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: genai.RoleUser, Parts: []*genai.Part{{Text: "continua"}}},
		},
	}
	if _, err := reminder(ctxFake, withoutHistory); err != nil {
		t.Fatalf("turn 2: %v", err)
	}
	last := withoutHistory.Contents[len(withoutHistory.Contents)-1]
	txt := last.Parts[len(last.Parts)-1].Text
	if strings.Contains(txt, "inalterado desde o turno anterior") {
		t.Fatalf("turn 2: pointer emitted although the value is not in history anymore: %q", txt)
	}
	if !strings.Contains(txt, plan) {
		t.Fatalf("turn 2: full plan not reinjected after history loss: %q", txt)
	}
}

// TestReminderDigestStableAcrossFormatting verifies the digest only reacts
// to content changes, not to how the value is stringified between turns.
func TestReminderDigestStableAcrossFormatting(t *testing.T) {
	d1 := keyContentDigest("plano do chefe")
	d2 := keyContentDigest(fmt.Sprintf("%s", "plano do chefe"))
	if d1 != d2 {
		t.Fatalf("digest differs for identical content")
	}
	if keyContentDigest("plano A") == keyContentDigest("plano B") {
		t.Fatalf("digest collision on distinct content")
	}
}
