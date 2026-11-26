package shellx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLocalEchoAndDeny valida o caminho de execução da policy: binário
// permitido roda (via wrapper local — o "echo" não existe no Windows, e o
// wrapper exercita o resolveLocalExecutable do achado #5), comandos
// destrutivos são recusados pela allowlist e git perigoso exige confirmação.
func TestLocalEchoAndDeny(t *testing.T) {
	dir := t.TempDir()

	// O caso "roda" usa um wrapper do próprio diretório, nos dois SOs.
	var name, content, invoked string
	if runtime.GOOS == "windows" {
		name, content, invoked = "echo.bat", "@echo off\r\necho %*\r\n", "echo hivemind"
	} else {
		name, content, invoked = "echo", "#!/bin/sh\necho \"$@\"\n", "./echo hivemind"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	var ran bool
	out, err := (Local{}).Run(context.Background(), dir, invoked, []string{"echo", "echo.bat"}, func(string) bool {
		ran = true
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hivemind") {
		t.Fatalf("out = %q", out)
	}
	if ran {
		t.Fatal("comando permitido não deveria pedir confirmação")
	}

	// Recusas são avaliadas antes da execução: nenhum binário necessário.
	_, err = (Local{}).Run(context.Background(), dir, "rm -rf .", []string{"echo", "echo.bat"}, func(string) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "recusado") {
		t.Fatalf("esperava recusa, got %v", err)
	}
	_, err = (Local{}).Run(context.Background(), dir, "git push --force", []string{"git"}, func(string) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "perigoso") {
		t.Fatalf("esperava recusa de perigoso, got %v", err)
	}
}


func TestSanitizeWindowsPowerShellCommand(t *testing.T) {
	got := sanitizeWindowsPowerShellCommand(`cd app && echo "a && b" 2>&1 && git status`)
	if strings.Contains(got, "2>&1") {
		t.Fatalf("unexpected 2>&1 in sanitized command: %s", got)
	}
	if !strings.Contains(got, `"a && b"`) {
		t.Fatalf("&& inside quotes should be preserved: %s", got)
	}
	if !strings.Contains(got, "if ($? -and ($null -eq $LASTEXITCODE -or $LASTEXITCODE -eq 0))") {
		t.Fatalf("expected conditional && translation: %s", got)
	}
}
