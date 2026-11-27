package shellx

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMissingToolHint(t *testing.T) {
	cases := []struct {
		bin  string
		want string
	}{
		{"flutter", "https://docs.flutter.dev/get-started/install"},
		{"fvm.exe", "https://fvm.app/documentation/getting-started/installation"},
		{"mvn", "https://maven.apache.org/download.cgi"},
		{"python3", "https://www.python.org/downloads/"},
		{"docker", "Docker Desktop"},
		{"mycustomtool", "PATH"},
	}
	for _, tc := range cases {
		got := MissingToolHint(tc.bin)
		if !strings.Contains(got, tc.want) {
			t.Fatalf("MissingToolHint(%q) = %q, want substring %q", tc.bin, got, tc.want)
		}
	}
}

func TestFormatMissingExecutableDiagnostic(t *testing.T) {
	got := FormatMissingExecutableDiagnostic("flutter", errors.New("not found"))
	for _, want := range []string{
		"[DIAGNÓSTICO DE AMBIENTE]",
		"https://docs.flutter.dev/get-started/install",
		"NÃO edite manifestos",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("diagnostic missing %q: %s", want, got)
		}
	}
}

func TestProbeWorktreeEnvironmentFlutterAndFvmrc(t *testing.T) {
	dir := t.TempDir()
	pubspec := "name: app_mobile\nenvironment:\n  sdk: ^3.5.0\n  flutter: 3.24.0\ndependencies:\n  flutter:\n    sdk: flutter\n"
	if err := os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte(pubspec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".fvmrc"), []byte(`{"flutter":"3.24.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := ProbeWorktreeEnvironment(dir)
	for _, want := range []string{
		"[Ambiente da Worktree]",
		"Flutter / Dart",
		"pubspec.yaml flutter=3.24.0",
		".fvmrc flutter=3.24.0",
		"Regra de ambiente:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ProbeWorktreeEnvironment missing %q in:\n%s", want, got)
		}
	}
}

func TestBrokenWindowsBashStubDetection(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`C:\Users\dev\AppData\Local\Microsoft\WindowsApps\bash.exe`, true},
		{`C:\Windows\System32\bash.exe`, true},
		{`C:\Program Files\Git\bin\bash.exe`, false},
		{``, false},
	}
	for _, tc := range cases {
		if got := isBrokenWindowsBashStub(tc.path); got != tc.want {
			t.Fatalf("isBrokenWindowsBashStub(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestResolveCommandArgvEmptyAndShellScript(t *testing.T) {
	if got := ResolveCommandArgv("", nil); len(got) != 0 {
		t.Fatalf("expected empty slice, got %v", got)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "check.sh")
	if err := os.WriteFile(script, []byte("#!/usr/bin/env bash\necho ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ResolveCommandArgv(dir, []string{"./check.sh", "--flag"})
	if len(got) == 0 {
		t.Fatal("ResolveCommandArgv returned empty slice")
	}
	if runtime.GOOS == "windows" {
		if gb := findGitBash(); gb != "" {
			if got[0] != gb || len(got) != 3 || got[1] != script || got[2] != "--flag" {
				t.Fatalf("expected Git Bash prepend %q, got %v", gb, got)
			}
		}
	}
}
