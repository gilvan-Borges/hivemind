package shellx

import (
	"net/url"
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
	"SSH_AUTH_SOCK":                  true,
	"SSH_AGENT_PID":                  true,
	"GIT_ASKPASS":                    true,
	"GOOGLE_APPLICATION_CREDENTIALS": true,
	"NETRC":                          true,
}

// SanitizedEnv returns os.Environ without credential-bearing variables. PATH,
// HOME/USERPROFILE, TEMP, JAVA_HOME, GOROOT, GOPATH and other build variables
// are preserved so compilers and package managers keep working.
func SanitizedEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name := kv
		value := ""
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			name = kv[:idx]
			value = kv[idx+1:]
		}
		if IsSensitiveEnv(name) || envValueHasCredential(name, value) {
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

// envValueHasCredential reports whether value is a URL/DSN that carries userinfo.
// Any value shaped as scheme://user:password@host is removed. Names ending in
// _URL, _URI or _DSN are also removed when they carry userinfo (user@host),
// so DATABASE_URL, REDIS_URL and MONGO_URI do not reach the child process.
func envValueHasCredential(name, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if urlHasPassword(value) {
		return true
	}
	if dsnEnvName(name) && urlHasUserinfo(value) {
		return true
	}
	return false
}

func dsnEnvName(name string) bool {
	up := strings.ToUpper(strings.TrimSpace(name))
	return strings.HasSuffix(up, "_URL") || strings.HasSuffix(up, "_URI") || strings.HasSuffix(up, "_DSN")
}

func urlHasPassword(value string) bool {
	u, err := url.Parse(value)
	if err != nil || u == nil || u.User == nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	_, ok := u.User.Password()
	return ok
}

func urlHasUserinfo(value string) bool {
	if u, err := url.Parse(value); err == nil && u != nil && u.User != nil && u.Scheme != "" && u.Host != "" {
		if u.User.Username() != "" {
			return true
		}
		if _, ok := u.User.Password(); ok {
			return true
		}
	}
	at := strings.LastIndex(value, "@")
	if at <= 0 || at == len(value)-1 {
		return false
	}
	info := value[:at]
	if strings.Contains(info, "://") || strings.ContainsAny(info, " \t") {
		return false
	}
	colon := strings.IndexByte(info, ':')
	return colon > 0 && colon < len(info)-1
}
