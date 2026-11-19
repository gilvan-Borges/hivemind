package shellx

import (
	"context"
	"strings"
	"testing"
)

func TestLocalEchoAndDeny(t *testing.T) {
	dir := t.TempDir()
	var ran bool
	out, err := (Local{}).Run(context.Background(), dir, "go env GOVERSION", []string{"go"}, func(string) bool {
		ran = true
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "go") {
		t.Fatalf("out = %q", out)
	}
	if ran {
		t.Fatal("go env should not ask for confirmation")
	}
	_, err = (Local{}).Run(context.Background(), dir, "rm -rf .", []string{"go"}, func(string) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "recusado") {
		t.Fatalf("expected refusal, got %v", err)
	}
	_, err = (Local{}).Run(context.Background(), dir, "git push --force", []string{"git"}, func(string) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "perigoso") {
		t.Fatalf("expected dangerous refusal, got %v", err)
	}
}
