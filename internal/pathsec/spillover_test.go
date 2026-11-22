package pathsec

import "testing"

func TestIsSpilloverPath(t *testing.T) {
	positive := []string{
		`.hivemind/spillover/run-shell-1.txt`,
		`.hivemind/spillover/subagent-summary-2.txt`,
		`.HIVEMIND/Spillover/mixed-case.txt`,
		`.hivemind/spillover/nested/deeper/file.txt`,
	}
	for _, p := range positive {
		if !IsSpilloverPath(p) {
			t.Fatalf("expected %q to match", p)
		}
	}
	negative := []string{
		``,
		`.hivemind`,
		`.hivemind/`,
		`.hivemind/spillover`,
		`.hivemind/team.yaml`,
		`.hivemind/spilloverx/a.txt`,
		`.hivemind/spillover-secret.txt`,
		`docs/.hivemind/spillover/a.txt`,
		`spillover/a.txt`,
		`C:\ws\.hivemind\spillover\a.txt`,
		`/abs/.hivemind/spillover/a.txt`,
	}
	for _, p := range negative {
		if IsSpilloverPath(p) {
			t.Fatalf("expected %q NOT to match", p)
		}
	}
	// Backslashes are normalized on any platform.
	if !IsSpilloverPath(`.hivemind\spillover\a.txt`) {
		t.Fatal("windows separators must normalize")
	}
}
