package names

import "testing"

func TestADK(t *testing.T) {
	cases := map[string]string{
		"dev-back": "dev_back",
		"User":     "agent_user",
		"123start": "a_123start",
		"  QA  ":   "qa",
		"":         "agent",
		"Área Dev": "area_dev",
	}
	for in, want := range cases {
		if got := ADK(in); got != want {
			t.Errorf("ADK(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIssueKeyAndBranch(t *testing.T) {
	if got := IssueKey(" proj-123 "); got != "PROJ-123" {
		t.Fatalf("key = %q", got)
	}
	if got := IssueKey("not a key"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := Branch("PROJ-123", "dev-1", "ignored"); got != "hivemind/proj-123/dev_1" {
		t.Fatalf("branch = %q", got)
	}
	if got := Branch("", "dev_1", "Adicionar função"); got != "hivemind/adicionar-funcao/dev_1" {
		t.Fatalf("slug branch = %q", got)
	}
	msg := CommitMessage("PROJ-15", "corrigir o parser\nmais texto")
	if msg != "feat(PROJ-15): corrigir o parser" {
		t.Fatalf("commit = %q", msg)
	}
	title := PRTitle("ABC-9", "um titulo")
	if title != "ABC-9: um titulo" {
		t.Fatalf("title = %q", title)
	}
}
