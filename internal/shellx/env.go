package shellx

import (
	"os"
	"strings"
)

// sensitiveEnvSuffixes are removed from subprocess environments. Agents can run
// builds and tests, but they must not be able to read provider/Jira/GitHub
// credentials out of the environment and send them to a model (or persist them
// in the session transcript).
var sensitiveEnvSuffixes = []string{
	"TOKEN", "SECRET", "PASSWORD", "PASSWD", "API_KEY", "APIKEY",
	"PRIVATE_KEY", "CREDENTIAL", "CREDENTIALS", "ACCESS_KEY", "SECRET_KEY",
}

var sensitiveEnvExact = map[string]bool{
	"SSH_AUTH_SOCK":                 true,
	"SSH_AGENT_PID":                 true,
	"GIT_ASKPASS":                   true,
	"GOOGLE_APPLICATION_CREDENTIALS": true,
	"NETRC":                         true,
}

// SanitizedEnv returns os.Environ without credential-bearing variables. PATH,
// HOME/USERPROFILE, TEMP, JAVA_HOME, GOROOT, GOPATH and other build variables
// are preserved so compilers and package managers keep working.
func SanitizedEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			name = kv[:idx]
		}
		if IsSensitiveEnv(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// IsSensitiveEnv reports whether an environment variable name carries a secret.
func IsSensitiveEnv(name string) bool {
	up := strings.ToUpper(strings.TrimSpace(name))
	if sensitiveEnvExact[up] {
		return true
	}
	if strings.HasPrefix(up, "AWS_") || strings.HasPrefix(up, "AZURE_") || strings.HasPrefix(up, "GCP_") {
		return true
	}
	for _, suffix := range sensitiveEnvSuffixes {
		if strings.HasSuffix(up, suffix) {
			return true
		}
	}
	return false
}
