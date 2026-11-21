// Package pathsec classifies filesystem paths that hold secrets.
// The resident broker and the squad tools share this rule so a .env or a
// private key is not readable through one surface and blocked on the other.
package pathsec

import (
	"path/filepath"
	"strings"
)

var sensitiveFileNames = map[string]bool{
	"api-keys.json":           true,
	"remote-bridge.json":      true,
	"relay.json":              true,
	"jira-connector.json":     true,
	"github-connector.json":   true,
	"credentials.json":        true,
	"policy.json":             true,
	"resident.json":           true,
	"initial-password.txt":    true,
	"terraform.tfstate":       true,
	"terraform.tfstate.backup": true,
	".htpasswd":               true,
	"kubeconfig":              true,
	".env":                    true,
	".npmrc":                  true,
	".pypirc":                 true,
	".netrc":                  true,
	"_netrc":                  true,
	"id_rsa":                  true,
	"id_ed25519":              true,
	"id_ecdsa":                true,
	"id_dsa":                  true,
}

var sensitiveFileExts = map[string]bool{
	".pem":      true,
	".key":      true,
	".p12":      true,
	".pfx":      true,
	".jks":      true,
	".keystore": true,
	".kdbx":     true,
	".crt":      true,
	".der":      true,
	".tfvars":   true,
}

// IsSensitivePath reports whether relOrAbs points to a secret, credential store,
// private key, .env file, .git metadata, or protected HiveMind config.
// Paths matching this rule are upgraded from TierRead to TierCredentials.
func IsSensitivePath(relOrAbs string) bool {
	s := strings.TrimSpace(relOrAbs)
	if s == "" {
		return false
	}
	norm := strings.ToLower(filepath.ToSlash(filepath.Clean(strings.ReplaceAll(s, `\`, "/"))))
	parts := strings.Split(norm, "/")
	for i, part := range parts {
		clean := strings.TrimRight(strings.TrimSpace(part), ". ")
		if clean == "" {
			continue
		}
		if clean == ".git" || strings.HasPrefix(clean, "git~") || clean == ".ssh" || clean == ".aws" || clean == ".gnupg" || clean == ".kube" || clean == ".docker" {
			return true
		}
		if clean == ".hivemind" || strings.HasPrefix(clean, "hivemi~") {
			// Only .hivemind/MEMORY.md is readable as project team memory.
			if i+1 == len(parts)-1 && strings.TrimRight(strings.TrimSpace(parts[i+1]), ". ") == "memory.md" {
				continue
			}
			return true
		}
		if sensitiveFileNames[clean] {
			return true
		}
		if strings.HasPrefix(clean, ".env.") && !strings.HasSuffix(clean, ".example") && !strings.HasSuffix(clean, ".sample") && !strings.HasSuffix(clean, ".template") {
			return true
		}
		if strings.Contains(clean, "secret") && (strings.HasSuffix(clean, ".json") || strings.HasSuffix(clean, ".yaml") || strings.HasSuffix(clean, ".yml") || strings.HasSuffix(clean, ".env") || strings.HasSuffix(clean, ".toml") || strings.HasSuffix(clean, ".ini") || strings.HasSuffix(clean, ".properties")) {
			return true
		}
		if sensitiveFileExts[filepath.Ext(clean)] {
			return true
		}
	}
	return false
}
