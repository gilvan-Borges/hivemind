package shellx

import (
	"strings"
	"testing"
)

func TestEvaluateAllowAndDeny(t *testing.T) {
	allow := []string{"git", "go", "echo", "python", "node", "find"}
	ok := Evaluate("go test ./...", allow)
	if !ok.Allowed || ok.Dangerous || ok.UseShell {
		t.Fatalf("go test: %+v", ok)
	}
	if got := ok.Argv; len(got) != 3 || got[0] != "go" || got[2] != "./..." {
		t.Fatalf("argv = %#v", got)
	}

	// Windows paths with backslashes must not lose characters.
	winPath := Evaluate(`go test .\internal\cli\...`, allow)
	if !winPath.Allowed || winPath.Dangerous || len(winPath.Argv) != 3 || winPath.Argv[2] != `.\internal\cli\...` {
		t.Fatalf("windows path argv = %+v", winPath)
	}

	// Empty quoted arguments and flag="value" must remain intact.
	emptyArg := Evaluate(`git commit -m ""`, allow)
	if len(emptyArg.Argv) != 4 || emptyArg.Argv[3] != "" {
		t.Fatalf("empty quoted arg lost: %#v", emptyArg.Argv)
	}
	concatFlag := Evaluate(`go build -ldflags="-s -w"`, allow)
	if len(concatFlag.Argv) != 3 || concatFlag.Argv[2] != "-ldflags=-s -w" {
		t.Fatalf("concatenated quoted flag split: %#v", concatFlag.Argv)
	}

	denied := Evaluate("rm -rf .", allow)
	if denied.Allowed {
		t.Fatal("rm should be denied")
	}
	force := Evaluate("git push --force origin main", allow)
	if !force.Allowed || !force.Dangerous {
		t.Fatalf("force push: %+v", force)
	}
	gitCfg := Evaluate("git -c core.sshCommand=evil status", allow)
	if !gitCfg.Dangerous {
		t.Fatalf("git -c must require confirmation: %+v", gitCfg)
	}
	gitDirEscape := Evaluate("git -C ../outside status", allow)
	if !gitDirEscape.Dangerous {
		t.Fatalf("git -C must require confirmation: %+v", gitDirEscape)
	}
	gitSwitchCreate := Evaluate("git switch -c feature/nova", allow)
	if !gitSwitchCreate.Allowed || gitSwitchCreate.Dangerous {
		t.Fatalf("git switch -c should be allowed safely: %+v", gitSwitchCreate)
	}
	pyInline := Evaluate(`python -c "import os"`, allow)
	if !pyInline.Dangerous {
		t.Fatalf("python -c must require confirmation: %+v", pyInline)
	}
	nodeInline := Evaluate(`node -e "process.exit(0)"`, allow)
	if !nodeInline.Dangerous {
		t.Fatalf("node -e must require confirmation: %+v", nodeInline)
	}
	findExec := Evaluate(`find . -exec rm {} +`, allow)
	if !findExec.Dangerous {
		t.Fatalf("find -exec must require confirmation: %+v", findExec)
	}
	newlineChain := Evaluate("echo oi\nrm -rf /", allow)
	if !newlineChain.UseShell || !newlineChain.Dangerous {
		t.Fatalf("newline command injection must require confirmation: %+v", newlineChain)
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

	// Wildcard allowlist ("full_access") allows any executable.
	full := Evaluate("docker ps", []string{"*"})
	if !full.Allowed {
		t.Fatalf("wildcard allowlist should allow docker: %+v", full)
	}

	// Commit / PR governance detection in shell commands.
	if blocked, what := IsCommitOrPRCommand(`go test ./... && git commit -m "wip"`); !blocked || what != "git commit" {
		t.Fatalf("expected git commit blocked, got %v %q", blocked, what)
	}
	if blocked, what := IsCommitOrPRCommand(`gh pr merge 1 --merge`); !blocked || what != "gh pr merge" {
		t.Fatalf("expected gh pr merge blocked, got %v %q", blocked, what)
	}
	if blocked, _ := IsCommitOrPRCommand(`git status --short`); blocked {
		t.Fatal("git status must remain allowed for all roles")
	}
	if blocked, _ := IsCommitOrPRCommand(`git -c alias.c=commit c -m wip`); !blocked {
		t.Fatal("git -c alias de commit deve ser detectado como commit")
	}
	if blocked, _ := IsCommitOrPRCommand(`git -calias.p=push p origin main`); !blocked {
		t.Fatal("git -calias de push deve ser detectado como push")
	}
	if blocked, _ := IsCommitOrPRCommand(`git update-ref refs/heads/main HEAD`); !blocked {
		t.Fatal("git update-ref deve ser detectado")
	}
	if blocked, _ := IsCommitOrPRCommand(`git -c core.pager=cat log --oneline`); blocked {
		t.Fatal("git -c benigno não é commit/push")
	}

	issue := Evaluate("gh issue comment 708 --repo acme/app --body oi", []string{"gh"})
	if !issue.Allowed || !issue.Dangerous {
		t.Fatalf("gh issue comment deve pedir confirmação: %+v", issue)
	}
	view := Evaluate("gh issue view 708 --repo acme/app", []string{"gh"})
	if !view.Allowed || view.Dangerous {
		t.Fatalf("gh issue view é leitura: %+v", view)
	}
	leak := Evaluate("gh auth token", []string{"gh", "*"})
	if leak.Allowed || strings.Contains(leak.Reason, "token") && strings.Contains(leak.Reason, "ghp_") {
		t.Fatalf("gh auth token não pode executar nem ecoar segredo: %+v", leak)
	}
	if leak.Reason == "" || !strings.Contains(leak.Reason, "credencial") {
		t.Fatalf("motivo da recusa: %+v", leak)
	}
	shown := Evaluate("gh auth status --show-token", []string{"gh"})
	if shown.Allowed {
		t.Fatal("--show-token não pode executar")
	}
	for _, raw := range []string{
		`gh auth to'ken'`,
		`gh auth "token"`,
		`gh --hostname github.com auth token`,
		`echo $(gh auth token)`,
		`gh auth status --show-to'ken'`,
	} {
		dec := Evaluate(raw, []string{"gh", "echo", "*"})
		if dec.Allowed || !strings.Contains(dec.Reason, "credencial") {
			t.Fatalf("%q deveria ser recusado sem executar: %+v", raw, dec)
		}
		if strings.Contains(dec.Reason, "ghp_") || strings.Contains(dec.Reason, raw) {
			t.Fatalf("motivo não pode repetir o comando: %+v", dec)
		}
	}
	status := Evaluate("gh auth status", []string{"gh"})
	if !status.Allowed || status.Dangerous {
		t.Fatalf("gh auth status sem token continua leitura: %+v", status)
	}
}

func TestShellMetacharactersRequireConfirmation(t *testing.T) {
	allow := []string{"go", "git", "echo"}
	plain := Evaluate("go test ./...", allow)
	if plain.UseShell || plain.Dangerous || !plain.Allowed {
		t.Fatalf("comando simples: %+v", plain)
	}
	for _, raw := range []string{
		"go test ./... && echo ok",
		"go test ./... || echo fail",
		"echo a; echo b",
		"echo hi | findstr x",
		"echo $(go env GOPATH)",
		"echo `go env GOPATH`",
		"go test ./... > out.txt",
		"echo hi < in.txt",
	} {
		dec := Evaluate(raw, allow)
		if !dec.UseShell || !dec.Dangerous || !dec.Allowed {
			t.Fatalf("%q deveria ser allowlisted, perigoso e via shell: %+v", raw, dec)
		}
		if !HasShellMetacharacters(raw) {
			t.Fatalf("HasShellMetacharacters(%q) = false", raw)
		}
	}
	if HasShellMetacharacters("go test ./...") {
		t.Fatal("go test não tem metacaractere")
	}
}
