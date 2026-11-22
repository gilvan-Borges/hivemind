package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundIntakeTrimsHugeObjectiveKeepingHeadAndTail(t *testing.T) {
	ws := t.TempDir()
	r := &Runner{Workspace: ws}

	head := strings.Repeat("H", 7<<10) // 7168 bytes no começo
	mid := strings.Repeat("M", 20<<10) // 20 KiB no meio (será cortado)
	tail := strings.Repeat("T", 1<<10) // 1 KiB no fim
	objective := head + "[PONTO_MEDIO]" + mid + "[FIM_DO_OBJETIVO]" + tail

	got, atts := r.boundIntake(objective, nil)
	if len(atts) != 0 {
		t.Fatalf("sem anexos, devolveu %d", len(atts))
	}
	if len(got) >= len(objective) {
		t.Fatalf("objetivo não foi reduzido: %d -> %d", len(objective), len(got))
	}
	if !strings.Contains(got, "H") || !strings.Contains(got, "T") {
		t.Fatal("cabeça e cauda deveriam permanecer inline")
	}
	if !strings.Contains(got, "TRECHO CENTRAL REMOVIDO") {
		t.Fatal("marcador de corte ausente")
	}
	if !strings.Contains(got, "[FIM_DO_OBJETIVO]") {
		t.Fatal("cauda real do objetivo deveria estar inline (últimos 2 KiB)")
	}

	rel := ".hivemind/intake/objective-" + intakeHash8([]byte(objective)) + ".txt"
	if !strings.Contains(got, rel) {
		t.Fatalf("marcador deveria apontar %s: %q", rel, got)
	}
	raw, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("arquivo integral não foi gravado: %v", err)
	}
	if string(raw) != objective {
		t.Fatal("arquivo de intake deveria conter o objetivo integral")
	}
}

func TestBoundIntakeKeepsSmallObjectiveUntouched(t *testing.T) {
	ws := t.TempDir()
	r := &Runner{Workspace: ws}
	small := "Corrija o bug de login no serviço de autenticação."
	got, atts := r.boundIntake(small, nil)
	if got != small {
		t.Fatalf("objetivo pequeno não deveria mudar: %q", got)
	}
	if len(atts) != 0 {
		t.Fatalf("sem anexos, devolveu %d", len(atts))
	}
	if _, err := os.Stat(filepath.Join(ws, ".hivemind", "intake")); !os.IsNotExist(err) {
		t.Fatal("nada deveria ser gravado para objetivo pequeno")
	}
}

func TestBoundIntakeDivertsLargeTextAttachmentKeepsImageInline(t *testing.T) {
	ws := t.TempDir()
	r := &Runner{Workspace: ws}

	bigText := []byte(strings.Repeat("log line\n", 2<<10)) // ~18 KiB
	img := []byte{0x89, 0x50, 0x4E, 0x47}
	atts := []Attachment{
		{Name: "build.log", MIMEType: "text/plain", Data: bigText},
		{Name: "screenshot.png", MIMEType: "image/png", Data: img},
	}

	got, out := r.boundIntake("Analise o log e a imagem.", atts)
	if len(out) != 1 {
		t.Fatalf("apenas a imagem deveria seguir inline, veio %d", len(out))
	}
	if out[0].Name != "screenshot.png" {
		t.Fatalf("anexo inline inesperado: %+v", out[0])
	}
	if !strings.Contains(got, "build.log") || !strings.Contains(got, "text/plain") {
		t.Fatalf("nota do anexo desviado ausente: %q", got)
	}
	if !strings.Contains(got, ".hivemind/intake/build.log.txt") {
		t.Fatalf("nota deveria apontar o caminho do intake: %q", got)
	}
	raw, err := os.ReadFile(filepath.Join(ws, ".hivemind", "intake", "build.log.txt"))
	if err != nil || string(raw) != string(bigText) {
		t.Fatal("conteúdo do anexo desviado deveria estar integral no intake")
	}
}

func TestBoundIntakeFallsBackToInlineWhenWorkspaceMissing(t *testing.T) {
	r := &Runner{}
	huge := strings.Repeat("x", 32<<10)
	got, atts := r.boundIntake(huge, []Attachment{
		{Name: "big", MIMEType: "text/plain", Data: []byte(strings.Repeat("y", 8<<10))},
	})
	if got != huge {
		t.Fatal("sem workspace, o objetivo deve seguir inline integral (nunca perder input)")
	}
	if len(atts) != 1 {
		t.Fatal("sem workspace, o anexo deve seguir inline integral")
	}
}

func TestRuneSafeHeadTail(t *testing.T) {
	s := "abcdef"
	if got := runeSafeHead(s, 3); got != "abc" {
		t.Fatalf("head: %q", got)
	}
	if got := runeSafeTail(s, 3); got != "def" {
		t.Fatalf("tail: %q", got)
	}
	// Multibyte: cortar no meio de um rune recua até o início do rune.
	mb := "ação"
	if got := runeSafeHead(mb, 2); got != "a" {
		t.Fatalf("head multibyte: %q", got)
	}
	if got := runeSafeTail(mb, 2); got != "o" {
		t.Fatalf("tail multibyte: %q", got)
	}
}
