package tools

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

type shellArgs struct {
	Command     string `json:"command" jsonschema:"Comando em formato argv, por exemplo: go test ./..."`
	Dir         string `json:"dir,omitempty" jsonschema:"Diretório relativo ao workspace. Vazio usa a raiz do agente."`
	Unsandboxed bool   `json:"unsandboxed,omitempty" jsonschema:"Se true, pede para rodar na sessão do host, sem sandbox. Só vale em Full Acesso ou com confirmação. O padrão é a sandbox."`
}

// applyShellPolicy installs the shell confirmer for the permission mode.
// auto_edit still runs ordinary allowlisted commands without a prompt, but a
// line with shell metacharacters must be confirmed (or refused when nobody
// can ask). full_access keeps running them and writes an audit line first.
func applyShellPolicy(deps Deps) Deps {
	deps.hostConfirm = deps.Confirm
	deps.shellPrepared = true
	if deps.Settings.IsFullAccess() {
		deps.Allowlist = []string{"*"}
		deps.Confirm = func(command string) bool {
			if shellx.HasShellMetacharacters(command) {
				shellx.AuditFullAccessShell(command)
			}
			return true
		}
		return deps
	}
	if len(deps.Allowlist) == 0 {
		deps.Allowlist = deps.Settings.EffectiveAllowlist()
	}
	if deps.Settings.EffectivePermissionMode() == team.PermissionAutoEdit {
		inner := deps.Confirm
		deps.Confirm = func(command string) bool {
			if !deps.Settings.ShellLineNeedsConfirm(command) {
				return true
			}
			if inner == nil {
				return false
			}
			return inner(command)
		}
		return deps
	}
	if !deps.Settings.NeedsShellConfirm() {
		deps.Confirm = func(string) bool { return true }
	}
	return deps
}

func (d Deps) shellBlocksGitWrite() bool {
	return !d.canCommitAndManagePR() || d.AllowReadOnlyTestShell || d.DisableGitWrite || d.DisableMerge
}

func (d Deps) shellDescription() string {
	desc := "Terminal da sessão: run_shell é a única ferramenta de terminal. Por padrão executa na sandbox do sistema, com argv explícito. unsandboxed=true, ou shell_unsandboxed no time, roda na sessão do host só em Full Acesso ou com confirmação, e avisa que está sem sandbox. Se o comando estiver fora da lista, a ferramenta recusa e diz o motivo. Comandos perigosos (incluindo gh que comenta ou altera issue, PR ou repositório) e linhas com metacaractere só rodam depois da aprovação do usuário. O ambiente do processo não leva tokens nem chaves."
	if d.shellBlocksGitWrite() {
		desc += " git commit, push, merge e gh pr/api continuam bloqueados neste modo."
	}
	return desc
}

// shellAllowlist dá `gh` ao Chefe e ao Arquiteto mesmo quando o team.yaml
// foi gravado antes de `gh` entrar na lista padrão. Dev e QA não ganham isso.
func (d Deps) shellAllowlist() []string {
	list := append([]string(nil), d.Allowlist...)
	if !d.canCommitAndManagePR() {
		return list
	}
	for _, item := range list {
		norm := strings.ToLower(strings.TrimSpace(item))
		norm = strings.TrimSuffix(norm, ".exe")
		if norm == "*" || norm == "gh" {
			return list
		}
	}
	return append(list, "gh")
}

const unsandboxedWarning = "SEM SANDBOX: este comando roda na sessão do host, sem isolamento. O ambiente não leva segredos do cofre."

func (d Deps) runShell(ctx context.Context, command, relDir string, unsandboxed bool) (string, error) {
	if d.shellBlocksGitWrite() {
		if blocked, what := shellx.IsCommitOrPRCommand(command); blocked {
			if !d.canCommitAndManagePR() {
				return "", fmt.Errorf("governança HiveMind: apenas o Chefe ou o Arquiteto podem executar %s (desenvolvedores e QA não comitam nem operam PR)", what)
			}
			return "", fmt.Errorf("governança HiveMind: operação %s bloqueada neste modo (apenas leitura e testes; sem commit, push ou merge)", what)
		}
	}
	dir := d.Root
	if strings.TrimSpace(relDir) != "" {
		resolved, err := workspace.Resolve(d.Root, relDir)
		if err != nil {
			return "", err
		}
		dir = resolved
	}
	if unsandboxed || d.Settings.ShellUnsandboxed {
		return d.runShellUnsandboxed(ctx, dir, command)
	}
	return d.runShellSandboxed(ctx, dir, command)
}

func (d Deps) hostShell() shellx.Runner {
	if d.Shell != nil {
		return d.Shell
	}
	return shellx.Local{}
}

func (d Deps) runShellUnsandboxed(ctx context.Context, dir, command string) (string, error) {
	if !d.Settings.IsFullAccess() {
		ask := d.hostConfirm
		if !d.shellPrepared {
			ask = d.Confirm
		}
		prompt := unsandboxedWarning + "\n" + command
		if ask == nil || !ask(prompt) {
			return "", fmt.Errorf("execução sem sandbox recusada: exige Full Acesso ou confirmação")
		}
	}
	log.Printf("squad run_shell SEM sandbox; o ambiente não leva segredos do cofre")
	shellx.AuditUnsandboxedShell(command)
	out, err := d.hostShell().Run(ctx, dir, command, d.shellAllowlist(), d.Confirm)
	if out == "" {
		out = unsandboxedWarning
	} else {
		out = unsandboxedWarning + "\n" + out
	}
	return out, err
}

func (d Deps) runShellSandboxed(ctx context.Context, dir, command string) (string, error) {
	dec := shellx.Evaluate(command, d.shellAllowlist())
	if !dec.Allowed {
		return "", fmt.Errorf("comando recusado: %s", dec.Reason)
	}
	if dec.UseShell {
		return "", fmt.Errorf("comando com metacaractere não entra na sandbox; peça unsandboxed e confirme a execução sem sandbox")
	}
	if dec.Dangerous {
		if d.Confirm == nil || !d.Confirm(command) {
			return "", fmt.Errorf("comando perigoso recusado: %s", dec.Reason)
		}
	}
	sb := d.Sandbox
	if sb == nil {
		sb = sandboxx.NewOSRunner()
	}
	argv := append([]string(nil), dec.Argv...)
	argv[0] = shellx.ResolveLocalExecutable(dir, argv[0])
	res, err := sb.Run(ctx, sandboxx.Options{
		Argv:           argv,
		WorkDir:        dir,
		WriteRoots:     []string{dir},
		AllowNetwork:   true,
		Timeout:        shellx.DefaultTimeout,
		MaxOutputBytes: 1 << 20,
	})
	text := res.Stdout
	if strings.TrimSpace(res.Stderr) != "" {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += res.Stderr
	}
	var logPath string
	if len(text) > 16<<10 {
		if f, tmpErr := os.CreateTemp("", "hivemind-shell-*.log"); tmpErr == nil {
			logPath = f.Name()
			_, _ = f.WriteString(text)
			_ = f.Close()
		}
	}
	text = strx.TruncateHeadTailBytes(text, 4<<10, 12<<10)
	if logPath != "" {
		text += "\n[saída completa salva em: " + logPath + "]"
	}
	if err != nil {
		if strings.TrimSpace(text) == "" {
			return "", err
		}
		return text, err
	}
	return text, nil
}

func (d Deps) canShell() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "run_shell",
		Description: d.shellDescription(),
	}, func(ctx agent.Context, args shellArgs) (textResult, error) {
		out, err := d.runShell(ctx, args.Command, args.Dir, args.Unsandboxed)
		if err != nil {
			return textResult{Text: out}, err
		}
		return textResult{Text: out}, nil
	})
}
