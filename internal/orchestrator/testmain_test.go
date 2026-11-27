package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("HIVEMIND_DATA_DIR") == "" {
		tmp, err := os.MkdirTemp("", "hivemind-test-orch-*")
		if err == nil {
			cfgDir := filepath.Join(tmp, "config")
			_ = os.MkdirAll(cfgDir, 0o755)
			_ = os.Setenv("HIVEMIND_DATA_DIR", tmp)
			_ = os.Setenv("APPDATA", cfgDir)
			_ = os.Setenv("XDG_CONFIG_HOME", cfgDir)
			_ = os.Setenv("OPENROUTER_API_KEY", "")
			code := m.Run()
			_ = os.RemoveAll(tmp)
			os.Exit(code)
		}
	}
	os.Exit(m.Run())
}
