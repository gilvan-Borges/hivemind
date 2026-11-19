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
}
