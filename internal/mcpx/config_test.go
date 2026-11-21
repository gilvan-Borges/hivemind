package mcpx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsLeaveHeavyServersOff(t *testing.T) {
	on := map[string]bool{}
	for _, s := range DefaultServers() {
		on[s.Name] = s.Enabled
	}
	if !on["context7"] || !on["code-review-graph"] {
		t.Fatalf("leves deveriam nascer ligados: %#v", on)
	}
	for _, name := range []string{"playwright", "chrome-devtools", "toolbox", "github", "figma-context", "codebase-memory"} {
		if on[name] {
			t.Fatalf("%s deveria nascer desligado", name)
		}
	}
}

func useTempUserConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-servers.json")
	prev := userConfigPathFn
	userConfigPathFn = func() (string, error) { return path, nil }
	t.Cleanup(func() { userConfigPathFn = prev })
	return path
}

func TestSetEnabledPersistsWithoutSecrets(t *testing.T) {
	userPath := useTempUserConfig(t)
	workspace := t.TempDir()

	got, err := SetEnabled(workspace, "playwright", true, false)
	if err != nil {
		t.Fatal(err)
	}
	play := findServer(got.Servers, "playwright")
	chrome := findServer(got.Servers, "chrome-devtools")
	if play == nil || !play.Enabled {
		t.Fatal("playwright deveria ligar")
	}
	if chrome == nil || chrome.Enabled {
		t.Fatal("chrome-devtools deveria desligar ao ligar o playwright")
	}
	raw, err := os.ReadFile(userPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "figd_") || strings.Contains(text, "ghp_") || strings.Contains(text, "Bearer ") {
		t.Fatalf("o arquivo gravado não pode levar segredo expandido:\n%s", text)
	}
	if strings.Contains(text, "npx") {
		t.Fatalf("o override deveria guardar só o interruptor, não reescrever o comando:\n%s", text)
	}
}

func TestProjectOverlayDoesNotChangeUserDefault(t *testing.T) {
	useTempUserConfig(t)
	workspace := t.TempDir()
	if _, err := SetEnabled(workspace, "github", true, true); err != nil {
		t.Fatal(err)
	}
	withProject, err := Load(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !findServer(withProject.Servers, "github").Enabled {
		t.Fatal("overlay do projeto deveria ligar o github")
	}
	other, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if findServer(other.Servers, "github").Enabled {
		t.Fatal("outro projeto não herda o overlay")
	}
	if _, err := os.Stat(filepath.Join(workspace, ".hivemind", "mcp-servers.json")); err != nil {
		t.Fatal(err)
	}
}

func TestToolboxRejectsNonLoopback(t *testing.T) {
	s := Server{Name: "toolbox", Env: map[string]string{"POSTGRES_HOST": "db.producao.internal"}}
	if err := validateLocal(s); err == nil {
		t.Fatal("host de produção deveria ser recusado")
	}
	s.Env["POSTGRES_HOST"] = "127.0.0.1"
	if err := validateLocal(s); err != nil {
		t.Fatal(err)
	}
}

func TestExpandKeepsPlaceholdersOutOfDisk(t *testing.T) {
	t.Setenv("CONTEXT7_API_KEY", "segredo-de-teste")
	got, ok := expand("Bearer ${env:CONTEXT7_API_KEY}", "/tmp/proj")
	if !ok || got != "Bearer segredo-de-teste" {
		t.Fatalf("expand = %q ok=%v", got, ok)
	}
	_, ok = expand("Bearer ${env:CONTEXT7_API_KEY_AUSENTE}", "")
	if ok {
		t.Fatal("variável ausente deveria falhar a expansão")
	}
	optional, missing := expandKeys("Bearer ${env?:CONTEXT7_API_KEY_AUSENTE}", "")
	if len(missing) != 0 || strings.TrimSpace(optional) != "Bearer" {
		t.Fatalf("opcional = %q missing=%v", optional, missing)
	}
	prev := ghAuthToken
	ghAuthToken = func() string { return "" }
	t.Cleanup(func() { ghAuthToken = prev })
	t.Setenv("GITHUB_MCP_TOKEN", "")
	_, missing = expandKeys("Bearer ${env:GITHUB_MCP_TOKEN}", "")
	if len(missing) != 1 || missing[0] != "GITHUB_MCP_TOKEN" {
		t.Fatalf("missing = %v", missing)
	}
	if !strings.Contains(secretMessage(missing), "gh auth login") {
		t.Fatal(secretMessage(missing))
	}
}

func TestFilteredCommandEnvStripsHostSecrets(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	t.Setenv("OPENAI_API_KEY", "sk-proj-secret")
	t.Setenv("GITHUB_TOKEN", "ghp_host_secret")

	env, cwd := filteredCommandEnv(map[string]string{
		"CUSTOM_TOKEN":     "allowed-explicit",
		"HIVEMIND_MCP_CWD": "/tmp/ws",
	})
	if cwd != "/tmp/ws" {
		t.Fatalf("cwd = %q, want /tmp/ws", cwd)
	}
	joined := strings.Join(env, "\n")
	for _, secret := range []string{"sk-ant-secret", "sk-proj-secret", "ghp_host_secret"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("host secret leaked into MCP env: %s", secret)
		}
	}
	if !strings.Contains(joined, "CUSTOM_TOKEN=allowed-explicit") {
		t.Fatalf("explicit server env missing: %s", joined)
	}
}

func TestWorkspaceTrustGateBlocksUntrustedCustomCommand(t *testing.T) {
	useTempUserConfig(t)
	workspace := t.TempDir()
	projDir := filepath.Join(workspace, ".hivemind")
	if err := os.MkdirAll(projDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgJSON := `{"servers":[{"name":"evil","transport":"stdio","command":"evil-bin","enabled":true}]}`
	if err := os.WriteFile(filepath.Join(projDir, "mcp-servers.json"), []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	if IsWorkspaceConfigTrusted(workspace) {
		t.Fatal("new project overlay should not be trusted initially")
	}
	untrusted, err := Load(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if findServer(untrusted.Servers, "evil") != nil {
		t.Fatal("untrusted project custom MCP command must be blocked")
	}

	if err := TrustWorkspaceConfig(workspace); err != nil {
		t.Fatal(err)
	}
	if !IsWorkspaceConfigTrusted(workspace) {
		t.Fatal("expected workspace config to be trusted after TrustWorkspaceConfig")
	}
	trusted, err := Load(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if s := findServer(trusted.Servers, "evil"); s == nil || s.Command != "evil-bin" {
		t.Fatalf("trusted custom command should load after approval, got: %+v", s)
	}

	// Modifying the workspace file invalidates the recorded digest.
	if err := os.WriteFile(filepath.Join(projDir, "mcp-servers.json"), []byte(`{"servers":[{"name":"evil2","transport":"stdio","command":"evil2-bin","enabled":true}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if IsWorkspaceConfigTrusted(workspace) {
		t.Fatal("modified workspace overlay should invalidate previous trust")
	}
	afterModify, err := Load(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if findServer(afterModify.Servers, "evil2") != nil {
		t.Fatal("modified untrusted project MCP command must be blocked")
	}
}
