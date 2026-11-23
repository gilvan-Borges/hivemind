package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
	"github.com/gilvan-Borges/HiveMind/internal/shellx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

type gateShell struct {
	ran   string
	allow []string
}

func (g *gateShell) Run(_ context.Context, _, command string, allowlist []string, confirm shellx.Confirmer) (string, error) {
	dec := shellx.Evaluate(command, allowlist)
	g.allow = allowlist
	if !dec.Allowed {
		return "", errString("comando recusado: " + dec.Reason)
	}
	if dec.Dangerous && (confirm == nil || !confirm(command)) {
		return "", errString("comando perigoso recusado: " + dec.Reason)
	}
	g.ran = command
	return "ok", nil
}

type errString string

func (e errString) Error() string { return string(e) }

func TestAutoReviewWithoutTestsHasNoShell(t *testing.T) {
	list, err := ForRole(team.RoleProjectManager, Deps{
		Root:            t.TempDir(),
		Settings:        team.DefaultSettings(),
		DisableGitWrite: true,
		DisableMerge:    true,
		NoShell:         true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range list {
		if tl.Name() == "run_shell" {
			t.Fatal("revisão automática sem testes não leva run_shell")
		}
	}
}

func TestChiefHasTerminalEvenOnTriageCheckout(t *testing.T) {
	for _, role := range []string{team.RoleProjectManager, team.RoleArchitect} {
		list, err := ForRole(role, Deps{
			Root:            t.TempDir(),
			Settings:        team.DefaultSettings(),
			DisableGitWrite: true,
			Allowlist:       []string{"go", "git"},
		})
		if err != nil {
			t.Fatal(err)
		}
		var shell toolNamer
		found := false
		for _, tl := range list {
			if tl.Name() == "run_shell" {
				found = true
				shell = tl
			}
		}
		if !found {
			t.Fatalf("%s na triagem precisa de run_shell", role)
		}
		if !strings.Contains(shell.Description(), "terminal") {
			t.Fatalf("descrição deve dizer que run_shell é o terminal: %s", shell.Description())
		}
	}

	lead, err := ForRole(team.RoleTechLead, Deps{Root: t.TempDir(), Settings: team.DefaultSettings()})
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range lead {
		if tl.Name() == "run_shell" {
			t.Fatal("tech lead continua sem terminal; o Chefe é quem executa o comando pedido")
		}
	}
}

type toolNamer interface {
	Name() string
	Description() string
}

func TestChiefShellRunsGHOnlyAfterApproval(t *testing.T) {
	sh := &gateShell{}
	sb := &recordSandbox{}
	deps := Deps{
		Root:            t.TempDir(),
		Role:            team.RoleArchitect,
		DisableGitWrite: true,
		Allowlist:       []string{"go", "git"},
		Shell:           sh,
		Sandbox:         sb,
		Confirm:         func(string) bool { return false },
	}
	if _, err := deps.runShell(context.Background(), `git commit -m "nao"`, "", false); err == nil || !strings.Contains(err.Error(), "bloqueada") {
		t.Fatalf("commit na triagem: %v", err)
	}
	if sh.ran != "" || sb.runs != 0 {
		t.Fatal("commit não pode chegar no processo")
	}
	if _, err := deps.runShell(context.Background(), "gh auth token", "", false); err == nil || !strings.Contains(err.Error(), "credencial") {
		t.Fatalf("gh auth token: %v", err)
	}
	if sh.ran != "" || sb.runs != 0 {
		t.Fatal("token não pode ser executado")
	}
	comment := "gh issue comment 708 --repo owner/repo --body oi"
	if _, err := deps.runShell(context.Background(), comment, "", false); err == nil || !strings.Contains(err.Error(), "perigoso") {
		t.Fatalf("sem aprovação: %v", err)
	}
	if sh.ran != "" || sb.runs != 0 {
		t.Fatal("comentário não roda sem aprovação")
	}
	deps.Confirm = func(string) bool { return true }
	out, err := deps.runShell(context.Background(), comment, "", false)
	if err != nil || out != "ok" || sb.runs != 1 || sh.ran != "" {
		t.Fatalf("com aprovação: out=%q err=%v sandbox=%d ran=%q", out, err, sb.runs, sh.ran)
	}
	if len(sb.last.Argv) == 0 || !strings.EqualFold(sb.last.Argv[0], "gh") {
		t.Fatalf("sandbox não recebeu gh: %#v", sb.last.Argv)
	}
	if len(sb.last.ExtraEnv) != 0 {
		t.Fatalf("sandbox não pode receber ambiente extra: %#v", sb.last.ExtraEnv)
	}
	if _, err := deps.runShell(context.Background(), "gh pr review 7 --approve --body ok", "", true); err == nil || !strings.Contains(err.Error(), "não passam pelo gh") {
		t.Fatalf("gh pr review bloqueado: %v", err)
	}
	if sb.runs != 1 {
		t.Fatal("gh pr review não pode executar")
	}
	if !containsFold(deps.shellAllowlist(), "gh") {
		t.Fatalf("chefe antigo sem gh na lista ainda recebe gh: %v", deps.shellAllowlist())
	}

	dev := deps
	dev.Role = team.RoleDeveloper
	dev.Confirm = func(string) bool { return true }
	if _, err := dev.runShell(context.Background(), "gh issue view 708", "", false); err == nil {
		t.Fatal("dev não herda gh fora da allowlist")
	}
}

func containsFold(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), want) {
			return true
		}
	}
	return false
}

func TestMCPListDoesNotCallBrokenServerOn(t *testing.T) {
	text := formatMCPList([]mcpx.ServerView{
		{Name: "context7", Enabled: true, Status: "needs-secret", Description: "docs", Detail: "faltam variáveis: CONTEXT7_API_KEY"},
		{Name: "code-review-graph", Enabled: true, Status: "error", Description: "grafo", Detail: "connection closed"},
		{Name: "github", Enabled: false, Status: "off", Description: "github"},
	})
	if strings.Contains(text, "[ligado]") {
		t.Fatalf("servidor quebrado não pode aparecer ligado:\n%s", text)
	}
	if !strings.Contains(text, "context7 [indisponível]") || !strings.Contains(text, "code-review-graph [indisponível]") {
		t.Fatalf("lista:\n%s", text)
	}
	if !strings.Contains(text, "github [desligado]") {
		t.Fatalf("github desligado:\n%s", text)
	}
}
