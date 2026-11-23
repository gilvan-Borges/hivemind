package loopx

import "testing"

func TestProtectedPaths(t *testing.T) {
	patterns := []string{"**/*_test.go", ".hivemind/loops/**", "features.json"}
	files := []string{
		"internal/auth/user.go",
		"internal/auth/user_test.go",
		`.hivemind/loops/fix-auth/loop.yaml`,
	}
	got := Violations(files, patterns)
	if len(got) != 2 {
		t.Fatalf("violações: %#v", got)
	}
	if !MatchPath("**/*_test.go", "a_test.go") {
		t.Fatal("teste na raiz")
	}
	if MatchPath("**/*_test.go", "internal/auth/user.go") {
		t.Fatal("código de produção não é teste")
	}
	if !MatchPath(".hivemind/loops/**", ".hivemind/loops/fix-auth/state.json") {
		t.Fatal("estado do loop deveria ser protegido")
	}
}
