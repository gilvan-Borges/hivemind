package pathsec

import "testing"

func TestIsSensitivePath(t *testing.T) {
	sensitive := []string{
		".env",
		".env.local",
		".env.production",
		"config/.env",
		`secrets\.env.local`,
		"id_rsa",
		".ssh/id_ed25519",
		".ssh/config",
		"certs/server.pem",
		"certs/app.key",
		"store.p12",
		"api-keys.json",
		"credentials.json",
		".aws/credentials",
		".gnupg/pubring.kbx",
		".git/config",
		".hivemind/team.yaml",
		"deploy/secrets.json",
		"app.keystore",
	}
	for _, p := range sensitive {
		if !IsSensitivePath(p) {
			t.Errorf("IsSensitivePath(%q) = false, queria true", p)
		}
	}
	open := []string{
		"",
		"main.go",
		"README.md",
		".env.example",
		".env.sample",
		".env.template",
		".hivemind/MEMORY.md",
		"src/config.ts",
		"notes/secret-plan.md",
	}
	for _, p := range open {
		if IsSensitivePath(p) {
			t.Errorf("IsSensitivePath(%q) = true, queria false", p)
		}
	}
}
