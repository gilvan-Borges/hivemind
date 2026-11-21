package cli

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("HIVEMIND_DATA_DIR") == "" {
		tmp, err := os.MkdirTemp("", "hivemind-test-cli-*")
		if err == nil {
			_ = os.Setenv("HIVEMIND_DATA_DIR", tmp)
			code := m.Run()
			_ = os.RemoveAll(tmp)
			os.Exit(code)
		}
	}
	os.Exit(m.Run())
}
