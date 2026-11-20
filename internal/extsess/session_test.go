package extsess

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSessionsFromNames(t *testing.T) {
	got := sessionsFrom([]processInfo{
		{Name: "agy.exe", PID: 10, CreateMs: 5, KnownTime: true},
		{Name: "notepad.exe", PID: 11, CreateMs: 6, KnownTime: true},
		{Name: "claude.exe", PID: 12},
	}, builtinNames)
	if len(got) != 2 {
		t.Fatalf("esperava 2 sessões, veio %+v", got)
	}
	if got[0].State != StateRunning || got[0].Source != "agy" || len(got[0].Actions) != 0 {
		t.Fatalf("agy: %+v", got[0])
	}
	if got[1].State != StateUnknown || got[1].Source != "claude-code" {
		t.Fatalf("tempo desconhecido: %+v", got[1])
	}
	for _, s := range got {
		if s.State == "permission_required" {
			t.Fatal("estado inventado")
		}
	}
}

func TestListEmptyWithoutMatches(t *testing.T) {
	if len(sessionsFrom(nil, builtinNames)) != 0 {
		t.Fatal("entrada vazia não pode inventar sessão")
	}
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if s.State == "permission_required" || len(s.Actions) != 0 {
			t.Fatalf("sessão inválida: %+v", s)
		}
	}
	if runtime.GOOS != "windows" && len(got) != 0 {
		t.Fatalf("fora do Windows a lista fica vazia, veio %+v", got)
	}
}

func TestDiscoveryDoesNotImportProcKill(t *testing.T) {
	// internal/extsess não importa o cancelamento de internal/procx nem chama taskkill.
	dir := "."
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		if strings.Contains(text, "procx") || strings.Contains(text, "taskkill") {
			t.Fatalf("%s importa cancelamento de processo", e.Name())
		}
	}
}
