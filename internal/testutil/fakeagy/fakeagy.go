// Package fakeagy builds a small cross-platform fake of the `agy` executable for
// tests. The original fixtures were /bin/sh scripts, which cannot run on Windows
// and made the Windows test job impossible. The fake is a Go program compiled on
// demand into the test's temp directory; it records argv, cwd, stdin and the
// --json-schema body next to itself and emulates the handful of responses the
// modelx/cli tests need.
package fakeagy

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

const source = `package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)
func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func main() {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	if v := os.Getenv("AGY_FAKE_DIR"); v != "" {
		dir = v
	}
	_ = os.MkdirAll(dir, 0o755)
	args := os.Args[1:]
	_ = os.WriteFile(filepath.Join(dir, "args"), []byte(strings.Join(args, "\n")+"\n"), 0o644)
	if wd, err := os.Getwd(); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "pwd"), []byte(wd+"\n"), 0o644)
	}
	mode := envOr("AGY_FAKE_MODE", "text")
	if len(args) > 0 && args[0] == "models" {
		if mode == "auth" {
			fmt.Fprintln(os.Stderr, "authentication required")
			os.Exit(1)
		}
		fmt.Println("gemini-3.8-flash-medium")
		return
	}
	if mode == "early" {
		fmt.Fprintln(os.Stderr, "opção recusada pelo agy")
		os.Exit(2)
	}
	if mode == "quota-once" || mode == "quota-always" {
		hit := filepath.Join(dir, "quota-hit")
		if mode == "quota-always" || !fileExists(hit) {
			_ = os.WriteFile(hit, []byte("1"), 0o644)
			fmt.Fprintln(os.Stderr, "AGY_ERROR: {\"short_error\":\"RESOURCE_EXHAUSTED (code 429): Resource has been exhausted (e.g. check quota).\",\"status\":\"RESOURCE_EXHAUSTED\",\"error_code\":429,\"code_kind\":\"http\",\"retryable\":true,\"error_id\":\"t1\"}")
			fmt.Println("{\"status\":\"ERROR\",\"response\":\"\",\"error\":\"Your AI credits balance is too low to continue.\"}")
			os.Exit(3)
		}
		fmt.Println("{\"status\":\"SUCCESS\",\"response\":\"depois da quota\",\"structured_output\":{\"text\":\"depois da quota\",\"tool_calls\":[]}}")
		return
	}
	stdin, _ := io.ReadAll(os.Stdin)
	_ = os.WriteFile(filepath.Join(dir, "stdin"), stdin, 0o644)
	for i, a := range args {
		if a == "--json-schema" && i+1 < len(args) {
			if body, err := os.ReadFile(args[i+1]); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "schema-body"), body, 0o644)
			}
		}
	}
	switch mode {
	case "probe":
		fmt.Println("{\"status\":\"SUCCESS\",\"response\":\"OK\",\"structured_output\":{\"text\":\"OK\",\"tool_calls\":[]}}")
	case "tool":
		fmt.Println("{\"status\":\"SUCCESS\",\"response\":\"{\\\"text\\\":\\\"\\\",\\\"tool_calls\\\":[{\\\"id\\\":\\\"c1\\\",\\\"name\\\":\\\"write_file\\\",\\\"arguments\\\":{\\\"path\\\":\\\"a.txt\\\",\\\"content\\\":\\\"oi\\\"}}]}\",\"structured_output\":{\"text\":\"\",\"tool_calls\":[{\"id\":\"c1\",\"name\":\"write_file\",\"arguments\":{\"path\":\"a.txt\",\"content\":\"oi\"}}]},\"usage\":{\"input_tokens\":3,\"output_tokens\":2,\"total_tokens\":5}}")
	case "auth":
		fmt.Fprintln(os.Stderr, "authentication required")
		fmt.Println("{\"status\":\"ERROR\",\"response\":\"\",\"error\":\"authentication required\"}")
		os.Exit(1)
	case "stream":
		fmt.Println("{\"event\":\"step_update\",\"step_update\":{\"state\":\"ACTIVE\",\"step_type\":\"agent_response\",\"text_delta\":\"Olá\"}}")
		fmt.Println("{\"event\":\"step_update\",\"step_update\":{\"state\":\"DONE\",\"step_type\":\"tool\",\"tool_name\":\"write_file\",\"tool_info\":{\"name\":\"write_file\",\"parameters\":{\"path\":\"util/soma.go\"},\"output\":\"+func Soma(a, b int) int { return a + b }\\n\"}}}")
		fmt.Println("{\"event\":\"result\",\"result\":{\"status\":\"SUCCESS\",\"response\":\"Olá\",\"structured_output\":{\"text\":\"Olá\",\"tool_calls\":[]},\"usage\":{\"total_tokens\":12}}}")
	default:
		fmt.Println("{\"status\":\"SUCCESS\",\"response\":\"plano pronto\",\"structured_output\":{\"text\":\"plano pronto\",\"tool_calls\":[]}}")
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
`

// Write compiles the fake into dir and returns its path. On Windows the binary
// is named agy.exe so exec.LookPath("agy") and PATH-based discovery work.
func Write(t testing.TB, dir string) string {
	t.Helper()
	src := filepath.Join(dir, "fakeagy_main.go")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "agy"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", out, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake agy: %v\n%s", err, output)
	}
	return out
}
