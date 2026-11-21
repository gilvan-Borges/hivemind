package team

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestExamplesTeamYAMLAllowlistInSync pins the single-source contract of the
// shell allowlist (ROADMAP débito "Allowlist duplicada"): the example
// team.yaml must mirror team.DefaultAllowlist exactly, in the same order.
// Editing DefaultAllowlist without updating examples/team.yaml (or vice
// versa) fails here.
func TestExamplesTeamYAMLAllowlistInSync(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "team.yaml"))
	if err != nil {
		t.Fatalf("ler examples/team.yaml: %v", err)
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		t.Fatalf("interpretar examples/team.yaml: %v", err)
	}
	if len(f.Settings.ShellAllowlist) == 0 {
		t.Fatal("examples/team.yaml deveria listar explicitamente a shell_allowlist default")
	}
	if !reflect.DeepEqual(f.Settings.ShellAllowlist, DefaultAllowlist) {
		t.Fatalf(
			"allowlist de examples/team.yaml divergiu de team.DefaultAllowlist (fonte única).\n"+
				"yaml  : %v\nDefault: %v\nAtualize examples/team.yaml ao editar DefaultAllowlist (ou vice-versa).",
			f.Settings.ShellAllowlist, DefaultAllowlist,
		)
	}
}
