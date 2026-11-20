package orgx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseGitRemote(t *testing.T) {
	cases := []struct {
		raw      string
		wantHost string
		wantOrg  string
		wantRepo string
	}{
		{"https://github.com/vibetex/ipg.git", "github.com", "vibetex", "ipg"},
		{"git@github.com:vibetex/portal.git", "github.com", "vibetex", "portal"},
		{"ssh://git@gitlab.com/MinhaOrg/subgrupo/api.git", "gitlab.com", "MinhaOrg", "api"},
		{"https://user@dev.azure.com/VibetexOrg/IPG/_git/backend", "dev.azure.com", "VibetexOrg", "backend"},
		{"git@ssh.dev.azure.com:v3/VibetexOrg/IPG/backend", "ssh.dev.azure.com", "VibetexOrg", "backend"},
	}
	for _, tc := range cases {
		h, o, r := ParseGitRemote(tc.raw)
		if h != tc.wantHost || o != tc.wantOrg || r != tc.wantRepo {
			t.Fatalf("ParseGitRemote(%q) = (%q,%q,%q), want (%q,%q,%q)", tc.raw, h, o, r, tc.wantHost, tc.wantOrg, tc.wantRepo)
		}
	}
}

func TestDetectAndMatchPatterns(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "projeto-vibetex", "ipg")
	gitDir := filepath.Join(ws, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = git@github.com:vibetex/ipg.git\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	id := Detect(ws)
	if id.GitOrg != "vibetex" || id.GitRepo != "ipg" || id.GitHost != "github.com" {
		t.Fatalf("unexpected identity: %+v", id)
	}
	if ok, _ := MatchPatterns(id, []string{"outra-org", "vibetex"}); !ok {
		t.Fatal("expected vibetex org to match")
	}
	if ok, _ := MatchPatterns(id, []string{"outra-org"}); ok {
		t.Fatal("did not expect outra-org to match")
	}
	if ok, _ := MatchPatterns(Identity{Workspace: ws}, []string{"vibetex"}); !ok {
		t.Fatal("expected folder segment token vibetex to match even without remote")
	}
	// Avoid substring false positive like "bet" matching "vibetex".
	if ok, _ := MatchPatterns(Identity{Workspace: ws}, []string{"bet"}); ok {
		t.Fatal("did not expect substring token 'bet' to match 'projeto-vibetex'")
	}
}
