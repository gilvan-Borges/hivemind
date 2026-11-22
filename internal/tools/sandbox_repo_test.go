package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeRepoSandbox(t *testing.T, root string, v any) {
	t.Helper()
	dir := filepath.Join(root, ".hivemind")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(dir, "sandbox.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Bump mtime so the stamp cache sees a new version even on coarse
	// filesystems.
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, future, future)
}

func TestRepoSandboxDenyMatch(t *testing.T) {
	root := t.TempDir()
	writeRepoSandbox(t, root, repoSandboxFile{DenyCommands: []string{"npm publish", "RM -RF"}})

	if pat, denied := repoSandboxDenyMatch(root, "npm publish --tag beta"); !denied || pat != "npm publish" {
		t.Fatalf("expected npm publish denied, got (%q,%v)", pat, denied)
	}
	if _, denied := repoSandboxDenyMatch(root, "NPM PUBLISH --dry-run"); !denied {
		t.Fatalf("matching must be case-insensitive")
	}
	if _, denied := repoSandboxDenyMatch(root, "rm -rf /"); !denied {
		t.Fatalf("expected rm -rf denied")
	}
	if _, denied := repoSandboxDenyMatch(root, "npm run build"); denied {
		t.Fatalf("npm run build must not be denied")
	}
	if _, denied := repoSandboxDenyMatch(root, ""); denied {
		t.Fatalf("empty command must not be denied")
	}
}

func TestRepoSandboxDenyCacheInvalidatesOnRewrite(t *testing.T) {
	root := t.TempDir()
	writeRepoSandbox(t, root, repoSandboxFile{DenyCommands: []string{"docker push"}})
	if _, denied := repoSandboxDenyMatch(root, "docker push acme/app"); !denied {
		t.Fatalf("initial deny missing")
	}
	writeRepoSandbox(t, root, repoSandboxFile{DenyCommands: []string{"kubectl delete"}})
	if _, denied := repoSandboxDenyMatch(root, "docker push acme/app"); denied {
		t.Fatalf("stale deny survived a rewrite")
	}
	if _, denied := repoSandboxDenyMatch(root, "kubectl delete ns prod"); !denied {
		t.Fatalf("new deny not enforced")
	}
}

func TestRepoSandboxMalformedFileDegradesToNoDenials(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".hivemind")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sandbox.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Bypass the stamp cache for the malformed case.
	repoSandboxMu.Lock()
	delete(repoSandboxCache, root)
	repoSandboxMu.Unlock()
	if _, denied := repoSandboxDenyMatch(root, "anything at all"); denied {
		t.Fatalf("malformed repo file must degrade to no denials")
	}
}

func TestRunShellBlockedByRepoDenyList(t *testing.T) {
	root := t.TempDir()
	writeRepoSandbox(t, root, repoSandboxFile{DenyCommands: []string{"git push --force"}})

	deps := Deps{Root: root}
	_, err := deps.runShell(context.Background(), "git push --force origin main", "", false)
	if err == nil {
		t.Fatalf("repo-denylisted command ran")
	}
	if !strings.Contains(err.Error(), "sandbox.json") || !strings.Contains(err.Error(), "git push --force") {
		t.Fatalf("error must name the policy origin and the pattern: %v", err)
	}
}
