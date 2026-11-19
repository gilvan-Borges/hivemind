package shellx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRunResolvesLocalWrapper reproduces dogfooding achado #5: an allowlisted
// bare command must execute the wrapper script that lives in the working
// directory instead of failing with "executable file not found".
func TestRunResolvesLocalWrapper(t *testing.T) {
	dir := t.TempDir()
	var name, marker, content string
	if runtime.GOOS == "windows" {
		name = "gradlew.bat"
		marker = "ok-from-bat"
		content = "@echo off\r\necho " + marker + "\r\n"
	} else {
		name = "gradlew"
		marker = "ok-from-sh"
		content = "#!/bin/sh\necho " + marker + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	// chamada exatamente como o agente fez na T1: nome puro (sem .bat no Windows)
	invoked := name
	if runtime.GOOS == "windows" {
		invoked = strings.TrimSuffix(name, ".bat")
	}
	out, err := (Local{}).Run(context.Background(), dir, invoked+" :core:test", []string{"gradlew", "gradlew.bat"}, nil)
	if err != nil {
		t.Fatalf("wrapper local não executou: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, marker) {
		t.Fatalf("saída = %q, queria %q", out, marker)
	}
}

// TestRunResolvesDotSlashWrapper covers the "./gradlew" e ".\gradlew.bat"
// variants the agents also tried before the fix.
func TestRunResolvesDotSlashWrapper(t *testing.T) {
	dir := t.TempDir()
	var name, marker, content string
	if runtime.GOOS == "windows" {
		name = "gradlew.bat"
		marker = "ok-dotslash"
		content = "@echo off\r\necho " + marker + "\r\n"
	} else {
		name = "gradlew"
		marker = "ok-dotslash"
		content = "#!/bin/sh\necho " + marker + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	invoked := "./" + name
	if runtime.GOOS == "windows" {
		invoked = `.\\` + name
	}
	out, err := (Local{}).Run(context.Background(), dir, invoked+" build", []string{"gradlew", "gradlew.bat"}, nil)
	if err != nil {
		t.Fatalf("wrapper com ./ não executou: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, marker) {
		t.Fatalf("saída = %q, queria %q", out, marker)
	}
}

// TestRunStillRefusesUnknownLocalWrapper garante que a policy continua
// mandando: um arquivo local cujo nome NÃO está na allowlist é recusado
// mesmo existindo no diretório de trabalho.
func TestRunStillRefusesUnknownLocalWrapper(t *testing.T) {
	dir := t.TempDir()
	name := "exploit.bat"
	if runtime.GOOS != "windows" {
		name = "exploit"
	}
	content := "#!/bin/sh\necho nunca\n"
	if runtime.GOOS == "windows" {
		content = "@echo off\r\necho nunca\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := (Local{}).Run(context.Background(), dir, name, []string{"git", "go"}, nil)
	if err == nil || !strings.Contains(err.Error(), "recusado") {
		t.Fatalf("arquivo local fora da allowlist deveria ser recusado; err = %v", err)
	}
}
