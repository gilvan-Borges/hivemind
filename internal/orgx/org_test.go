package orgx

import (
	"os"
	"path/filepath"
	"strings"
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

func TestMatchPatternsGitOrgBeatsFolder(t *testing.T) {
	ws := filepath.Join(string(filepath.Separator), "data", "vibetex", "acme-app")
	id := Identity{Workspace: ws, GitOrg: "acme", GitHost: "github.com", WorkspaceLeaf: "acme-app", WorkspaceParent: "vibetex"}
	if ok, _ := MatchPatterns(id, []string{"vibetex"}); ok {
		t.Fatal("git org acme must not match pattern vibetex via a parent folder")
	}
	if ok, _ := MatchPatterns(id, []string{"*vibetex*"}); ok {
		t.Fatal("wildcard must not scan the filesystem path when the git org is known")
	}
	if ok, _ := MatchPatterns(id, []string{"acm", "acme-corp", "*acme-corp*"}); ok {
		t.Fatal("substring or a different org must not match")
	}
	if ok, rule := MatchPatterns(id, []string{"acme"}); !ok || rule != "acme" {
		t.Fatalf("expected exact git org, ok=%v rule=%q", ok, rule)
	}
	if ok, _ := MatchPatterns(id, []string{"github.com/acme"}); !ok {
		t.Fatal("expected host/org match")
	}

	evil := Identity{Workspace: filepath.Join(string(filepath.Separator), "data", "projetos", "acme-evil", "app")}
	if ok, _ := MatchPatterns(evil, []string{filepath.Join(string(filepath.Separator), "data", "projetos", "acme")}); ok {
		t.Fatal("path prefix must stop at a boundary, not acme-evil")
	}
	good := Identity{Workspace: filepath.Join(string(filepath.Separator), "data", "projetos", "acme", "app")}
	if ok, _ := MatchPatterns(good, []string{filepath.Join(string(filepath.Separator), "data", "projetos", "acme")}); !ok {
		t.Fatal("expected anchored path prefix when there is no git org")
	}
}

func TestGitConfigRejectsEscapeAndSecrets(t *testing.T) {
	root := t.TempDir()

	t.Run("strips userinfo", func(t *testing.T) {
		ws := filepath.Join(root, "quoted")
		writeGitConfig(t, ws, "[remote \"origin\"]\n\turl = \"https://user:supersecret@github.com/vibetex/ipg.git\"\n")
		id := Detect(ws)
		if id.GitOrg != "vibetex" || id.GitRepo != "ipg" {
			t.Fatalf("identity = %+v", id)
		}
		if strings.Contains(id.GitRemoteURL, "supersecret") || strings.Contains(id.GitRemoteURL, "user:") {
			t.Fatalf("remote URL leaked credentials: %q", id.GitRemoteURL)
		}
	})

	t.Run("does not follow include", func(t *testing.T) {
		included := filepath.Join(root, "included-config")
		if err := os.WriteFile(included, []byte("[remote \"origin\"]\n\turl = git@github.com:evilcorp/secret.git\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ws := filepath.Join(root, "include-repo")
		writeGitConfig(t, ws, "[include]\n\tpath = "+included+"\n[remote \"origin\"]\n\turl = git@github.com:goodorg/app.git\n")
		id := Detect(ws)
		if id.GitOrg != "goodorg" {
			t.Fatalf("include changed org: %+v", id)
		}

		only := filepath.Join(root, "include-only")
		writeGitConfig(t, only, "[include]\n\tpath = "+included+"\n")
		if got := Detect(only).GitOrg; got != "" {
			t.Fatalf("include-only must not adopt included org, got %q", got)
		}
	})

	t.Run("huge config is refused", func(t *testing.T) {
		ws := filepath.Join(root, "huge")
		cfg := strings.Repeat("x", int(maxGitMetaBytes)+2048) + "\n[remote \"origin\"]\n\turl = git@github.com:hugecorp/app.git\n"
		writeGitConfig(t, ws, cfg)
		if got := Detect(ws).GitOrg; got != "" {
			t.Fatalf("oversized config must fail closed, got %q", got)
		}
	})

	t.Run("gitdir outside a git tree", func(t *testing.T) {
		outside := filepath.Join(root, "aws")
		if err := os.MkdirAll(outside, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outside, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outside, "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:stolen/creds.git\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ws := filepath.Join(root, "fake-wt")
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, ".git"), []byte("gitdir: "+outside+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := Detect(ws).GitOrg; got != "" {
			t.Fatalf("gitdir outside .git leaked org %q", got)
		}
	})

	t.Run("commondir cannot escape", func(t *testing.T) {
		victim := filepath.Join(root, "victim", ".git")
		if err := os.MkdirAll(victim, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(victim, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(victim, "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:victimorg/private.git\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		wtGit := filepath.Join(root, "attacker", ".git", "worktrees", "x")
		if err := os.MkdirAll(wtGit, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wtGit, "HEAD"), []byte("ref: refs/heads/x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wtGit, "commondir"), []byte(victim+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ws := filepath.Join(root, "attacker-wt")
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, ".git"), []byte("gitdir: "+wtGit+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := Detect(ws).GitOrg; got != "" {
			t.Fatalf("commondir escape leaked org %q", got)
		}
	})

	t.Run("legitimate worktree", func(t *testing.T) {
		gitDir := filepath.Join(root, "mainrepo", ".git")
		wtGit := filepath.Join(gitDir, "worktrees", "feat")
		if err := os.MkdirAll(wtGit, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:vibetex/ipg.git\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wtGit, "HEAD"), []byte("ref: refs/heads/feat\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wtGit, "commondir"), []byte("../..\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ws := filepath.Join(root, "feat-wt")
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, ".git"), []byte("gitdir: "+wtGit+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		id := Detect(ws)
		if id.GitOrg != "vibetex" || id.GitRepo != "ipg" {
			t.Fatalf("worktree identity = %+v", id)
		}
	})

	t.Run("symlink config is refused", func(t *testing.T) {
		secret := filepath.Join(root, "secret-config")
		if err := os.WriteFile(secret, []byte("[remote \"origin\"]\n\turl = git@github.com:symlinkorg/app.git\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ws := filepath.Join(root, "symlink-repo")
		gitDir := filepath.Join(ws, ".git")
		if err := os.MkdirAll(gitDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(secret, filepath.Join(gitDir, "config")); err != nil {
			t.Skip("symlink not available")
		}
		if got := Detect(ws).GitOrg; got != "" {
			t.Fatalf("symlink config leaked org %q", got)
		}
	})
}

func writeGitConfig(t *testing.T, ws, cfg string) {
	t.Helper()
	gitDir := filepath.Join(ws, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
