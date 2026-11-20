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

func TestSanitizedEnvDropsCredentialURLs(t *testing.T) {
	const password = "s3cret-pass"
	t.Setenv("DATABASE_URL", "postgres://app:"+password+"@127.0.0.1:5432/app")
	t.Setenv("REDIS_URL", "redis://:"+password+"@127.0.0.1:6379/0")
	t.Setenv("MONGO_URI", "mongodb+srv://app:"+password+"@cluster.example.net/db")
	t.Setenv("MYSQL_DSN", "app:"+password+"@tcp(127.0.0.1:3306)/app")
	t.Setenv("ANY_HOST", "https://app:"+password+"@example.com/hook")
	t.Setenv("PUBLIC_DATABASE", "postgres://127.0.0.1:5432/app")
	t.Setenv("APP_URL", "https://example.com")
	t.Setenv("GIT_URL", "https://git@github.com/acme/app.git")
	t.Setenv("JAVA_HOME", `C:\Program Files\Java\jdk`)

	env := SanitizedEnv()
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, password) {
		t.Fatal("valor com credencial vazou para o ambiente do processo")
	}
	for _, leaked := range []string{"DATABASE_URL=", "REDIS_URL=", "MONGO_URI=", "MYSQL_DSN=", "ANY_HOST=", "GIT_URL="} {
		if strings.Contains(joined, leaked) {
			t.Fatalf("variável com userinfo %q vazou", leaked)
		}
	}
	for _, kept := range []string{"PUBLIC_DATABASE=postgres://127.0.0.1:5432/app", "APP_URL=https://example.com", `JAVA_HOME=`} {
		if !strings.Contains(joined, kept) {
			t.Fatalf("variável sem credencial foi removida: %q", kept)
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
