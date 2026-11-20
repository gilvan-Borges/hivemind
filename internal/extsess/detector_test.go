package extsess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectorRejectsCommandAndWritable(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("bad-cmd.json", `{"schemaVersion":1,"id":"my-agent","name":"My Agent","processNames":["my-agent.exe"],"readOnly":true,"command":"cmd /c whoami"}`)
	write("bad-write.json", `{"schemaVersion":1,"id":"other-agent","name":"Other","processNames":["other.exe"],"readOnly":false}`)
	write("good.json", `{"schemaVersion":1,"id":"my-agent","name":"My Agent","processNames":["my-agent.exe"],"readOnly":true,"actions":["kill"]}`)
	names, rejected := loadDetectorNames(dir)
	if len(rejected) != 2 {
		t.Fatalf("rejeitados: %+v", rejected)
	}
	for _, r := range rejected {
		if strings.Contains(r.Reason, "whoami") || strings.Contains(r.Reason, "{") {
			t.Fatalf("motivo vazou o corpo: %+v", r)
		}
	}
	if names["my-agent.exe"] != "my-agent" {
		t.Fatalf("detector válido não entrou: %+v", names)
	}
	if _, ok := names["other.exe"]; ok {
		t.Fatal("readOnly false foi registrado")
	}
}

func TestDetectorFileAppearsAndDisappears(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HIVEMIND_DATA_DIR", root)
	dir := detectorsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "my-agent.json")
	body := []byte(`{"schemaVersion":1,"id":"my-agent","name":"My Agent","processNames":["my-agent.exe"],"readOnly":true}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	names := mergedNames()
	if names["my-agent.exe"] != "my-agent" {
		t.Fatalf("arquivo novo não valeu: %+v", names)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	names = mergedNames()
	if _, ok := names["my-agent.exe"]; ok {
		t.Fatal("arquivo removido ainda vale")
	}
}
