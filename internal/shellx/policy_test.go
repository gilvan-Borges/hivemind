package shellx

import "testing"

func TestEvaluateAllowAndDeny(t *testing.T) {
	allow := []string{"git", "go", "echo"}
	ok := Evaluate("go test ./...", allow)
	if !ok.Allowed || ok.Dangerous || ok.UseShell {
		t.Fatalf("go test: %+v", ok)
	}
	if got := ok.Argv; len(got) != 3 || got[0] != "go" || got[2] != "./..." {
		t.Fatalf("argv = %#v", got)
	}
	denied := Evaluate("rm -rf .", allow)
	if denied.Allowed {
		t.Fatal("rm should be denied")
	}
	force := Evaluate("git push --force origin main", allow)
	if !force.Allowed || !force.Dangerous {
		t.Fatalf("force push: %+v", force)
	}
	hard := Evaluate("git reset --hard HEAD", allow)
	if !hard.Dangerous {
		t.Fatalf("reset: %+v", hard)
	}
	pipe := Evaluate("echo hi | tee out.txt", allow)
	if !pipe.UseShell || !pipe.Dangerous {
		t.Fatalf("pipe: %+v", pipe)
	}
	quoted := Evaluate(`echo "olá mundo"`, allow)
	if len(quoted.Argv) != 2 || quoted.Argv[1] != "olá mundo" {
		t.Fatalf("quoted = %#v", quoted.Argv)
	}
}
