package shellx

import (
	"strings"
	"testing"
)

func TestSanitizedEnvRemovesSecretsAndKeepsBuildVars(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	t.Setenv("JIRA_API_TOKEN", "jira_secret")
	t.Setenv("OPENAI_API_KEY", "sk_secret")
	t.Setenv("MY_CUSTOM_SECRET", "shh")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	t.Setenv("JAVA_HOME", "/opt/jdk")
	t.Setenv("GOPATH", "/go")

	env := SanitizedEnv()
	joined := strings.Join(env, "\n")
	for _, leaked := range []string{"GITHUB_TOKEN=", "JIRA_API_TOKEN=", "OPENAI_API_KEY=", "MY_CUSTOM_SECRET=", "AWS_ACCESS_KEY_ID=", "SSH_AUTH_SOCK="} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("credential %q leaked into child env", leaked)
		}
	}
	for _, kept := range []string{"JAVA_HOME=", "GOPATH="} {
		if !strings.Contains(joined, kept) {
			t.Fatalf("build variable %q was removed", kept)
		}
	}
}

func TestCappedBufferStopsAtLimit(t *testing.T) {
	buf := &cappedBuffer{limit: 8}
	if _, err := buf.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if _, err := buf.Write([]byte("67890")); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "12345678") || !strings.Contains(got, "limite de captura") {
		t.Fatalf("capped buffer = %q", got)
	}
}
