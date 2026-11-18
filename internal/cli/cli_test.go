package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

func TestHelpAndTeamLifecycle(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	root := newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := buf.String()
	for _, want := range []string{"hivemind", "team", "agent", "run", "session", "jira"} {
		if !strings.Contains(help, want) {
			t.Fatalf("ajuda sem %q:\n%s", want, help)
		}
	}

	buf.Reset()
	root = newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"--workspace", dir, "run", "objetivo"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "team create") {
		t.Fatalf("run sem time: %v", err)
	}

	buf.Reset()
	root = newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"--workspace", dir, "team", "create", "--name", "alfa", "--devs", "2", "--jira=false"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "alfa") {
		t.Fatalf("create: %s", buf.String())
	}

	buf.Reset()
	root = newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--workspace", dir, "agent", "add", "--role", "dev", "--name", "dev_back", "--model", "gpt-4.1", "--provider", "openai"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	root = newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--workspace", dir, "agent", "edit", "--name", "dev_back", "--model", "gpt-4.1-mini"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	root = newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--workspace", dir, "team", "show"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	shown := buf.String()
	if !strings.Contains(shown, "dev_back") || !strings.Contains(shown, "gpt-4.1-mini") || !strings.Contains(shown, "openai") {
		t.Fatalf("show:\n%s", shown)
	}

	buf.Reset()
	root = newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--workspace", dir, "team", "permission", "full_access"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "full_access") {
		t.Fatalf("permission output: %s", buf.String())
	}

	buf.Reset()
	root = newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--workspace", dir, "agent", "remove", "--name", "dev_back"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	root = newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--workspace", dir, "doctor"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Time ativo: alfa") || !strings.Contains(buf.String(), "GOOGLE_API_KEY") || !strings.Contains(buf.String(), "agy:") {
		t.Fatalf("doctor:\n%s", buf.String())
	}

	buf.Reset()
	root = newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--workspace", dir, "session", "list"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Nenhuma sessão") {
		t.Fatalf("sessions: %s", buf.String())
	}
}

func TestDoctorProbe(t *testing.T) {
	dir := t.TempDir()
	binDir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" = "models" ]; then echo gemini-3.8-flash-medium; exit 0; fi
if [ "$AGY_FAKE_MODE" = "early" ]; then echo "opção recusada pelo agy" >&2; exit 2; fi
printf '%s\n' '{"status":"SUCCESS","response":"OK","structured_output":{"text":"OK","tool_calls":[]}}'
`
	if err := os.WriteFile(filepath.Join(binDir, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("HIVEMIND_AGY_PATH", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(dir, "sem-local"))
	t.Setenv("AGY_FAKE_MODE", "probe")

	var buf bytes.Buffer
	root := newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--workspace", dir, "doctor", "--probe"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "agy probe: OK") {
		t.Fatalf("probe:\n%s", buf.String())
	}

	buf.Reset()
	t.Setenv("AGY_FAKE_MODE", "early")
	root = newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--workspace", dir, "doctor", "--probe"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "código 2") || strings.Contains(err.Error(), "não encontrei") {
		t.Fatalf("probe early = %v\n%s", err, buf.String())
	}
}

func TestTeamCreateUsesAgyWithoutAPIKey(t *testing.T) {
	dir := t.TempDir()
	binDir := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "agy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var buf bytes.Buffer
	root := newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"--workspace", dir, "team", "create", "--name", "local"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	loaded, err := team.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	active, err := loaded.ActiveTeam("")
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range active.Agents {
		if agent.Provider != "agy" || agent.Model != "agy" {
			t.Fatalf("agente %+v não ficou no agy", agent)
		}
	}
}
