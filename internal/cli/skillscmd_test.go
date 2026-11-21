package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/version"
)

func TestSkillsListAndGetCLI(t *testing.T) {
	root := newRoot()
	var listBuf bytes.Buffer
	root.SetOut(&listBuf)
	root.SetArgs([]string{"skills", "list", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("skills list failed: %v", err)
	}
	var listed []skillx.BuiltinTopic
	if err := json.Unmarshal(listBuf.Bytes(), &listed); err != nil {
		t.Fatalf("decode list json: %v\nraw: %s", err, listBuf.String())
	}
	if len(listed) < 6 {
		t.Fatalf("expected >= 6 topics, got %d", len(listed))
	}

	root2 := newRoot()
	var getBuf bytes.Buffer
	root2.SetOut(&getBuf)
	root2.SetArgs([]string{"skills", "get", "race"})
	if err := root2.Execute(); err != nil {
		t.Fatalf("skills get failed: %v", err)
	}
	out := getBuf.String()
	if !strings.Contains(out, version.Version) || !strings.Contains(out, "--race") {
		t.Fatalf("unexpected skills get output: %s", out)
	}
}
