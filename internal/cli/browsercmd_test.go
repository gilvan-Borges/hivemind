package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/browserx"
)

func TestBrowserStatusOmitsToken(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := browserx.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	root := newRoot()
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"browser", "status"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, cfg.Token) {
		t.Fatal("status imprimiu o token")
	}
	if !strings.Contains(out, "127.0.0.1:47891") {
		t.Fatalf("status: %s", out)
	}
}
