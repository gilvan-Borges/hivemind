package idecore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/procx"
)

// GitProgress is one live update from a Source Control git operation.
type GitProgress struct {
	Phase string `json:"phase"`
	Label string `json:"label"`
	Line  string `json:"line,omitempty"`
}

// GitActionResult is the user-facing outcome of pull, push, fetch or sync.
type GitActionResult struct {
	Summary string `json:"summary"`
	Action  string `json:"action"`
	Commits int    `json:"commits"`
}

// ResolveGitTimeout picks the limit for a git network operation.
// A positive seconds value wins. Otherwise HIVEMIND_GIT_TIMEOUT (Go duration
// like 3m, or a number of seconds) is used. The default is 2 minutes.
func ResolveGitTimeout(seconds int) time.Duration {
	if seconds >= 15 && seconds <= 3600 {
		return time.Duration(seconds) * time.Second
	}
	if raw := strings.TrimSpace(os.Getenv("HIVEMIND_GIT_TIMEOUT")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d >= 15*time.Second && d <= time.Hour {
			return d
		}
		if n, err := strconv.Atoi(raw); err == nil && n >= 15 && n <= 3600 {
			return time.Duration(n) * time.Second
		}
	}
	return 2 * time.Minute
}

// gitCommandHook replaces the git binary in tests. Production leaves it nil.
var gitCommandHook func(args []string) (bin string, argv []string)

// RunGitActionProgress runs an allowlisted git action with --progress, a
// non-interactive environment, and a line callback. The child is started with
// an explicit argv (never a shell). Cancelling ctx kills the process tree.
func RunGitActionProgress(ctx context.Context, workspaceDir, action string, on func(GitProgress)) (GitActionResult, error) {
	g := gitx.Local{}
	if !g.IsRepo(ctx, workspaceDir) {
		return GitActionResult{}, fmt.Errorf("esta pasta não é um repositório Git")
	}
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "fetch":
		return doFetch(ctx, workspaceDir, on)
	case "pull":
		return doPull(ctx, workspaceDir, on)
	case "push":
		return doPush(ctx, workspaceDir, on)
	case "sync":
		pulled, err := doPull(ctx, workspaceDir, on)
		if err != nil {
			return GitActionResult{}, err
		}
		pushed, err := doPush(ctx, workspaceDir, on)
		if err != nil {
			return GitActionResult{}, fmt.Errorf("%s Em seguida o push falhou: %w", pulled.Summary, err)
		}
		summary := strings.TrimSpace(pulled.Summary + " " + pushed.Summary)
		if pulled.Summary == "Já estava atualizado." && pushed.Summary == "Já estava atualizado." {
			summary = "Já estava atualizado."
		}
		return GitActionResult{Summary: summary, Action: "sync", Commits: pulled.Commits + pushed.Commits}, nil
	case "stash":
		return doStash(ctx, workspaceDir, on, false)
	case "stash-pop":
		return doStash(ctx, workspaceDir, on, true)
	default:
		return GitActionResult{}, fmt.Errorf("ação Git não suportada: %s", action)
	}
}

func doPull(ctx context.Context, dir string, on func(GitProgress)) (GitActionResult, error) {
	remote, branch, full := upstreamRef(ctx, dir)
	label := "Puxando de " + fallbackRemote(remote, branch, full) + "…"
	emitGit(on, "pull", label, "")
	before := gitRev(ctx, dir, "HEAD")
	args := pullArgs(remote, branch)
	out, err := runGitStream(ctx, dir, func(line string) { emitGit(on, "pull", label, line) }, args...)
	if err != nil {
		return GitActionResult{}, explainGitFailure("pull", out, err)
	}
	after := gitRev(ctx, dir, "HEAD")
	n := 0
	if before != "" && after != "" && before != after {
		n = gitCount(ctx, dir, before, after)
	}
	return GitActionResult{Summary: pullSummary(before, after, n, out), Action: "pull", Commits: n}, nil
}

func doPush(ctx context.Context, dir string, on func(GitProgress)) (GitActionResult, error) {
	remote, branch, full := upstreamRef(ctx, dir)
	if branch == "" {
		branch = gitRev(ctx, dir, "--abbrev-ref", "HEAD")
	}
	if branch == "" || branch == "HEAD" {
		return GitActionResult{}, fmt.Errorf("branch atual não identificada")
	}
	if remote == "" {
		remote = "origin"
	}
	target := full
	if target == "" {
		target = remote + "/" + branch
	}
	label := "Enviando para " + target + "…"
	emitGit(on, "push", label, "")
	ahead := -1
	if full != "" {
		ahead = gitCount(ctx, dir, full, "HEAD")
	}
	out, err := runGitStream(ctx, dir, func(line string) { emitGit(on, "push", label, line) }, pushArgs(remote, branch)...)
	if err != nil {
		return GitActionResult{}, explainGitFailure("push", out, err)
	}
	summary, n := pushSummary(ahead, out)
	return GitActionResult{Summary: summary, Action: "push", Commits: n}, nil
}

func doFetch(ctx context.Context, dir string, on func(GitProgress)) (GitActionResult, error) {
	_, _, full := upstreamRef(ctx, dir)
	labelTarget := full
	if labelTarget == "" {
		labelTarget = "origin"
	}
	label := "Buscando de " + labelTarget + "…"
	emitGit(on, "fetch", label, "")
	before := ""
	if full != "" {
		before = gitRev(ctx, dir, full)
	}
	out, err := runGitStream(ctx, dir, func(line string) { emitGit(on, "fetch", label, line) }, fetchArgs()...)
	if err != nil {
		return GitActionResult{}, explainGitFailure("fetch", out, err)
	}
	n := 0
	after := ""
	if full != "" {
		after = gitRev(ctx, dir, full)
		if before != "" && after != "" && before != after {
			n = gitCount(ctx, dir, before, after)
		}
	}
	return GitActionResult{Summary: fetchSummary(full, before, after, n), Action: "fetch", Commits: n}, nil
}

func doStash(ctx context.Context, dir string, on func(GitProgress), pop bool) (GitActionResult, error) {
	label := "Guardando alterações…"
	args := []string{"stash", "push", "-u", "-m", "HiveMind IDE stash"}
	action := "stash"
	if pop {
		label = "Restaurando stash…"
		args = []string{"stash", "pop"}
		action = "stash-pop"
	}
	emitGit(on, action, label, "")
	out, err := runGitStream(ctx, dir, func(line string) { emitGit(on, action, label, line) }, args...)
	if err != nil {
		return GitActionResult{}, explainGitFailure(action, out, err)
	}
	summary := strings.TrimSpace(out)
	if summary == "" {
		if pop {
			summary = "Stash restaurado."
		} else {
			summary = "Alterações guardadas no stash."
		}
	}
	return GitActionResult{Summary: summary, Action: action}, nil
}

func pullArgs(remote, branch string) []string {
	args := []string{"pull", "--progress", "--rebase"}
	if remote != "" && branch != "" {
		args = append(args, remote, branch)
	}
	return args
}

func pushArgs(remote, branch string) []string {
	if remote == "" {
		remote = "origin"
	}
	return []string{"push", "--progress", "-u", remote, branch}
}

func fetchArgs() []string {
	return []string{"fetch", "--progress", "--prune", "--all"}
}

func pullSummary(before, after string, n int, output string) string {
	low := strings.ToLower(output)
	if n > 0 {
		return fmt.Sprintf("Atualizado, %d %s.", n, pluralCommit(n))
	}
	if strings.Contains(low, "already up to date") || strings.Contains(low, "já está atualizado") || (before != "" && before == after) {
		return "Já estava atualizado."
	}
	return "Atualizado."
}

func pushSummary(ahead int, output string) (string, int) {
	low := strings.ToLower(output)
	if strings.Contains(low, "everything up-to-date") || ahead == 0 {
		return "Já estava atualizado.", 0
	}
	if ahead > 0 {
		return fmt.Sprintf("Enviado, %d %s.", ahead, pluralCommit(ahead)), ahead
	}
	return "Enviado.", 0
}

func fetchSummary(full, before, after string, n int) string {
	if full == "" {
		return "Fetch concluído (remotes atualizados)."
	}
	if n > 0 {
		return fmt.Sprintf("Fetch concluído, %d %s novos no remoto.", n, pluralCommit(n))
	}
	if before == "" || before == after {
		return "Já estava atualizado."
	}
	return "Fetch concluído (remotes atualizados)."
}

func pluralCommit(n int) string {
	if n == 1 {
		return "commit"
	}
	return "commits"
}

func explainGitFailure(action, output string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("operação de git cancelada")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("a operação de git excedeu o tempo. Tente de novo ou aumente HIVEMIND_GIT_TIMEOUT")
	}
	low := strings.ToLower(output + " " + err.Error())
	switch {
	case strings.Contains(low, "terminal prompts disabled") ||
		strings.Contains(low, "could not read username") ||
		strings.Contains(low, "could not read password") ||
		strings.Contains(low, "authentication failed") ||
		strings.Contains(low, "authentication required") ||
		strings.Contains(low, "invalid username or token") ||
		strings.Contains(low, "support for password authentication") ||
		(strings.Contains(low, "gcm") && strings.Contains(low, "interact")):
		return fmt.Errorf("o Git precisou de autenticação e o HiveMind não abre um prompt invisível. Entre com gh auth login ou configure uma credencial e tente de novo")
	case strings.Contains(low, "please commit or stash") ||
		strings.Contains(low, "unstaged changes") ||
		strings.Contains(low, "would be overwritten") ||
		strings.Contains(low, "local changes") ||
		strings.Contains(low, "your local changes"):
		return fmt.Errorf("alterações locais conflitam — faça Stash e tente de novo")
	case strings.Contains(low, "conflict") || strings.Contains(low, "could not apply"):
		return fmt.Errorf("conflito ao integrar. Resolva os arquivos marcados ou desfaça com git rebase --abort")
	case strings.Contains(low, "non-fast-forward") || strings.Contains(low, "[rejected]"):
		return fmt.Errorf("o remoto tem commits novos — faça Pull antes do Push")
	case strings.Contains(low, "no tracking information") || strings.Contains(low, "no upstream") || strings.Contains(low, "there is no tracking"):
		return fmt.Errorf("esta branch não rastreia um remoto. Publique com Push")
	default:
		detail := strings.TrimSpace(output)
		if detail == "" {
			detail = err.Error()
		}
		if len(detail) > 500 {
			detail = detail[:500]
		}
		return fmt.Errorf("git %s falhou: %s", action, detail)
	}
}

func fallbackRemote(remote, branch, full string) string {
	if full != "" {
		return full
	}
	if remote != "" && branch != "" {
		return remote + "/" + branch
	}
	return "origin"
}

func upstreamRef(ctx context.Context, dir string) (remote, branch, full string) {
	full = gitRev(ctx, dir, "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if full == "" || strings.Contains(full, " ") {
		return "", "", ""
	}
	remote, branch, _ = strings.Cut(full, "/")
	return remote, branch, full
}

func gitRev(ctx context.Context, dir string, args ...string) string {
	out, err := runGitStream(ctx, dir, nil, append([]string{"rev-parse"}, args...)...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func gitCount(ctx context.Context, dir, from, to string) int {
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return 0
	}
	out, err := runGitStream(ctx, dir, nil, "rev-list", "--count", from+".."+to)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func emitGit(on func(GitProgress), phase, label, line string) {
	if on == nil {
		return
	}
	on(GitProgress{Phase: phase, Label: label, Line: line})
}

func nonInteractiveGitEnv() []string {
	drop := map[string]bool{
		"GIT_TERMINAL_PROMPT": true,
		"GCM_INTERACTIVE":     true,
		"GIT_ASKPASS":         true,
		"SSH_ASKPASS":         true,
	}
	out := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if ok && drop[strings.ToUpper(name)] {
			continue
		}
		out = append(out, entry)
	}
	return append(out, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
}

func gitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	bin := "git"
	argv := append([]string{}, args...)
	if gitCommandHook != nil {
		bin, argv = gitCommandHook(args)
	}
	cmd := exec.CommandContext(ctx, bin, argv...)
	procx.ConfigureContext(cmd)
	if dir != "" {
		cmd.Dir = dir
	}
	env := nonInteractiveGitEnv()
	if gitCommandHook != nil {
		env = append(env, "HM_GIT_HELPER=1")
	}
	cmd.Env = env
	return cmd
}

func runGitStream(ctx context.Context, dir string, onLine func(string), args ...string) (string, error) {
	cmd := gitCommand(ctx, dir, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var buf lineBuf
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		readProgress(stdout, onLine, &buf)
	}()
	go func() {
		defer wg.Done()
		readProgress(stderr, onLine, &buf)
	}()
	wg.Wait()
	err = cmd.Wait()
	out := buf.String()
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, err
	}
	return out, nil
}

type lineBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lineBuf) add(s string) {
	l.mu.Lock()
	l.b.WriteString(s)
	l.b.WriteByte('\n')
	l.mu.Unlock()
}

func (l *lineBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func readProgress(r io.Reader, on func(string), buf *lineBuf) {
	br := bufio.NewReaderSize(r, 16*1024)
	var cur []byte
	flush := func() {
		if len(cur) == 0 {
			return
		}
		line := strings.TrimSpace(string(cur))
		cur = cur[:0]
		if line == "" {
			return
		}
		buf.add(line)
		if on != nil {
			on(line)
		}
	}
	for {
		b, err := br.ReadByte()
		if err != nil {
			flush()
			return
		}
		if b == '\n' || b == '\r' {
			flush()
			continue
		}
		cur = append(cur, b)
		if len(cur) > 8*1024 {
			flush()
		}
	}
}
