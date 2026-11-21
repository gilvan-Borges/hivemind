package idecore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitHelperProcess(t *testing.T) {
	if os.Getenv("HM_GIT_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	if len(args) == 0 {
		os.Exit(2)
	}
	switch args[0] {
	case "sleep":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "progress":
		_, _ = os.Stderr.WriteString("remote: Counting objects: 40%\rremote: Counting objects: 100%\n")
		_, _ = os.Stdout.WriteString("ok\n")
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown %s\n", args[0])
		os.Exit(1)
	}
}

func useGitHelper(t *testing.T) {
	t.Helper()
	prev := gitCommandHook
	gitCommandHook = func(args []string) (string, []string) {
		argv := append([]string{"-test.run=^TestGitHelperProcess$", "--"}, args...)
		return os.Args[0], argv
	}
	t.Cleanup(func() { gitCommandHook = prev })
}

func TestGitNetworkArgsAreExplicit(t *testing.T) {
	sets := [][]string{
		pullArgs("origin", "main"),
		pushArgs("origin", "main"),
		fetchArgs(),
	}
	for _, args := range sets {
		if args[0] == "sh" || args[0] == "cmd" || args[0] == "bash" || args[0] == "powershell" {
			t.Fatalf("argv não pode ser shell: %v", args)
		}
		joined := strings.Join(args, " ")
		if strings.ContainsAny(joined, "|&;<>`$") {
			t.Fatalf("metacaractere de shell: %s", joined)
		}
		if !strings.Contains(joined, "--progress") {
			t.Fatalf("sem --progress: %s", joined)
		}
	}
}

func TestNonInteractiveGitEnvDisablesPrompts(t *testing.T) {
	t.Setenv("GIT_ASKPASS", `C:\ask.exe`)
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	t.Setenv("SSH_ASKPASS", "askpass")
	env := nonInteractiveGitEnv()
	foundPrompt := 0
	foundGCM := 0
	for _, entry := range env {
		name, val, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "GIT_TERMINAL_PROMPT":
			foundPrompt++
			if val != "0" {
				t.Fatalf("prompt ainda interativo: %s", entry)
			}
		case "GCM_INTERACTIVE":
			foundGCM++
			if !strings.EqualFold(val, "Never") {
				t.Fatalf("GCM interativo: %s", entry)
			}
		case "GIT_ASKPASS", "SSH_ASKPASS":
			t.Fatalf("askpass herdado: %s", entry)
		}
	}
	if foundPrompt != 1 || foundGCM != 1 {
		t.Fatalf("prompt=%d gcm=%d", foundPrompt, foundGCM)
	}
}

func TestExplainGitFailureSuggestsAction(t *testing.T) {
	cases := []struct {
		out  string
		want string
	}{
		{"error: cannot pull with rebase: You have unstaged changes.\nPlease commit or stash them.", "Stash"},
		{"fatal: could not read Username for 'https://github.com': terminal prompts disabled", "prompt invisível"},
		{"fatal: Authentication failed for 'https://github.com/x/y.git/'", "gh auth login"},
		{"CONFLICT (content): Merge conflict in a.go", "rebase --abort"},
		{"! [rejected] main -> main (non-fast-forward)", "Pull antes do Push"},
	}
	for _, tc := range cases {
		err := explainGitFailure("pull", tc.out, errors.New("exit status 1"))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q → %v", tc.out, err)
		}
	}
	if err := explainGitFailure("pull", "", context.Canceled); err == nil || !strings.Contains(err.Error(), "cancelada") {
		t.Fatal(err)
	}
	if err := explainGitFailure("fetch", "", context.DeadlineExceeded); err == nil || !strings.Contains(err.Error(), "HIVEMIND_GIT_TIMEOUT") {
		t.Fatal(err)
	}
}

func TestResolveGitTimeout(t *testing.T) {
	if ResolveGitTimeout(0) != 2*time.Minute {
		t.Fatal(ResolveGitTimeout(0))
	}
	if ResolveGitTimeout(90) != 90*time.Second {
		t.Fatal(ResolveGitTimeout(90))
	}
	t.Setenv("HIVEMIND_GIT_TIMEOUT", "3m")
	if ResolveGitTimeout(0) != 3*time.Minute {
		t.Fatal(ResolveGitTimeout(0))
	}
	t.Setenv("HIVEMIND_GIT_TIMEOUT", "45")
	if ResolveGitTimeout(0) != 45*time.Second {
		t.Fatal(ResolveGitTimeout(0))
	}
}

func TestGitStreamSplitsCarriageProgress(t *testing.T) {
	useGitHelper(t)
	var lines []string
	out, err := runGitStream(context.Background(), t.TempDir(), func(line string) {
		lines = append(lines, line)
	}, "progress")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "40%") || !strings.Contains(out, "100%") || !strings.Contains(out, "ok") {
		t.Fatalf("saída %q linhas %v", out, lines)
	}
	if len(lines) < 3 {
		t.Fatalf("progresso colado: %v", lines)
	}
}

func TestGitStreamCancelKillsChild(t *testing.T) {
	useGitHelper(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := runGitStream(ctx, t.TempDir(), nil, "sleep")
		done <- err
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("esperava cancelamento, got %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("o processo filho não morreu após o cancelamento")
	}
}

func TestGitPullPushFetchLocalRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não está no PATH")
	}
	ctx := context.Background()
	bare := t.TempDir()
	gitLocal(t, bare, "init", "--bare", "-b", "main")

	seed := t.TempDir()
	gitLocal(t, seed, "init", "-b", "main")
	prepRepo(t, seed)
	if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitLocal(t, seed, "add", "a.txt")
	gitLocal(t, seed, "commit", "-m", "v1")
	gitLocal(t, seed, "remote", "add", "origin", bare)
	gitLocal(t, seed, "push", "-u", "origin", "main")

	local := t.TempDir()
	gitLocal(t, local, "clone", bare, ".")
	prepRepo(t, local)
	other := t.TempDir()
	gitLocal(t, other, "clone", bare, ".")
	prepRepo(t, other)

	if err := os.WriteFile(filepath.Join(other, "a.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitLocal(t, other, "add", "a.txt")
	gitLocal(t, other, "commit", "-m", "v2")
	gitLocal(t, other, "push")

	res, err := RunGitActionProgress(ctx, local, "pull", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "Atualizado, 1 commit." {
		t.Fatalf("pull: %q", res.Summary)
	}
	res, err = RunGitActionProgress(ctx, local, "pull", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "Já estava atualizado." {
		t.Fatalf("pull ocioso: %q", res.Summary)
	}

	if err := os.WriteFile(filepath.Join(local, "b.txt"), []byte("novo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitLocal(t, local, "add", "b.txt")
	gitLocal(t, local, "commit", "-m", "b")
	res, err = RunGitActionProgress(ctx, local, "push", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "Enviado, 1 commit." {
		t.Fatalf("push: %q", res.Summary)
	}
	res, err = RunGitActionProgress(ctx, local, "push", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary != "Já estava atualizado." {
		t.Fatalf("push ocioso: %q", res.Summary)
	}

	if err := os.WriteFile(filepath.Join(local, "a.txt"), []byte("sujo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "a.txt"), []byte("remoto\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitLocal(t, other, "add", "a.txt")
	gitLocal(t, other, "commit", "-m", "remoto")
	gitLocal(t, other, "pull", "--rebase")
	gitLocal(t, other, "push")
	_, err = RunGitActionProgress(ctx, local, "pull", nil)
	if err == nil || !strings.Contains(err.Error(), "Stash") {
		t.Fatalf("pull sujo: %v", err)
	}

	if err := os.WriteFile(filepath.Join(other, "c.txt"), []byte("c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitLocal(t, other, "add", "c.txt")
	gitLocal(t, other, "commit", "-m", "c")
	gitLocal(t, other, "push")
	res, err = RunGitActionProgress(ctx, local, "fetch", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "commit") {
		t.Fatalf("fetch: %q", res.Summary)
	}
}

func prepRepo(t *testing.T, dir string) {
	t.Helper()
	gitLocal(t, dir, "config", "user.email", "t@example.com")
	gitLocal(t, dir, "config", "user.name", "t")
	gitLocal(t, dir, "config", "commit.gpgsign", "false")
	gitLocal(t, dir, "config", "core.autocrlf", "false")
}

func gitLocal(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v %s", strings.Join(args, " "), err, out)
	}
}
