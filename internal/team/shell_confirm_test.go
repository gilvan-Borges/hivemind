package team

import "testing"

func TestShellLineNeedsConfirm(t *testing.T) {
	governed := DefaultSettings()
	if !governed.NeedsShellConfirm() || !governed.ShellLineNeedsConfirm("go test ./...") {
		t.Fatal("governed confirma comando perigoso e linha simples classificada à parte")
	}
	if !governed.ShellLineNeedsConfirm("go test && echo x") {
		t.Fatal("governed confirma metacaractere")
	}

	auto := DefaultSettings()
	auto.PermissionMode = PermissionAutoEdit
	if auto.NeedsShellConfirm() {
		t.Fatal("auto_edit não pede confirmação para comando allowlisted comum")
	}
	if auto.ShellLineNeedsConfirm("go test ./...") {
		t.Fatal("auto_edit não confirma go test")
	}
	for _, raw := range []string{"go test && echo x", "echo a | echo b", "echo $(id)", "echo `id`", "echo a > out"} {
		if !auto.ShellLineNeedsConfirm(raw) {
			t.Fatalf("auto_edit deveria confirmar %q", raw)
		}
	}

	full := DefaultSettings()
	full.PermissionMode = PermissionFullAccess
	if full.NeedsShellConfirm() || full.ShellLineNeedsConfirm("go test && echo x") {
		t.Fatal("full_access não pede confirmação")
	}
}
