package resident

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// MaxSoulBytes caps SOUL.md injection so the resident prompt budget remains predictable.
const MaxSoulBytes = 2048

// SoulFileName is stored inside the resident base directory alongside memory/.
const SoulFileName = "SOUL.md"

// SoulPath returns the filesystem path of the resident's SOUL.md file.
func SoulPath(baseDir string) string {
	dir := strings.TrimSpace(baseDir)
	if dir == "" {
		dir = DefaultBaseDir()
	}
	return filepath.Join(dir, SoulFileName)
}

// LoadSoul reads, bounds (<= MaxSoulBytes), and redacts SOUL.md if present.
// Missing SOUL.md returns empty string without error.
func LoadSoul(baseDir string) (string, error) {
	raw, err := os.ReadFile(SoulPath(baseDir))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	clean := strings.TrimSpace(RedactSecrets(string(raw)))
	if clean == "" {
		return "", nil
	}
	return strx.TruncateBytes(clean, MaxSoulBytes, "\n…"), nil
}

// SaveSoul atomically writes SOUL.md with owner-only permissions (0600),
// bounded to MaxSoulBytes.
func SaveSoul(baseDir, content string) error {
	path := SoulPath(baseDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	trimmed := strx.TruncateBytes(strings.TrimSpace(content), MaxSoulBytes, "")
	if trimmed != "" {
		trimmed += "\n"
	}
	return atomicx.WriteFile(path, []byte(trimmed), 0o600)
}
