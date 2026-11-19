package idecore

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLSPFileURIRoundtrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		p := `C:\hivemind-go\ide\app.go`
		uri := pathToFileURI(p)
		if uri != "file:///C:/hivemind-go/ide/app.go" {
			t.Fatalf("unexpected Windows file URI: %s", uri)
		}
		back := fileURIToPath(uri)
		if !strings.EqualFold(back, p) {
			t.Fatalf("roundtrip mismatch: got %q want %q", back, p)
		}
	} else {
		p := "/tmp/hivemind/app.go"
		uri := pathToFileURI(p)
		if uri != "file:///tmp/hivemind/app.go" {
			t.Fatalf("unexpected Unix file URI: %s", uri)
		}
		if back := fileURIToPath(uri); back != p {
			t.Fatalf("roundtrip mismatch: got %q want %q", back, p)
		}
	}
}

func TestLSPManagerGoplsEndToEnd(t *testing.T) {
	if findGoplsBinary() == "" {
		t.Skip("gopls não instalado neste ambiente")
	}

	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/lspdemo\n\ngo 1.22\n"), 0o644)
	src := "package main\n\n// Greet returns a greeting.\nfunc Greet(name string) string {\n\treturn \"hi \" + name\n}\n\nfunc main() {\n\t_ = Greet(\"hivemind\")\n\t_ = undefinedSymbol\n}\n"
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte(src), 0o644)

	gotDiag := make(chan LSPDiagnosticsEvent, 4)
	mgr := NewLSPManager(func(ev LSPDiagnosticsEvent) {
		if len(ev.Diagnostics) > 0 {
			select {
			case gotDiag <- ev:
			default:
			}
		}
	})
	defer mgr.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := mgr.DidOpen(ctx, root, "main.go", src); err != nil {
		t.Fatalf("DidOpen falhou: %v", err)
	}

	// 1. Diagnostics should report undefinedSymbol on line 10.
	select {
	case ev := <-gotDiag:
		found := false
		for _, d := range ev.Diagnostics {
			if d.Line == 10 && strings.Contains(strings.ToLower(d.Message), "undefined") {
				found = true
			}
		}
		if !found {
			t.Fatalf("diagnóstico esperado na linha 10 não encontrado: %+v", ev.Diagnostics)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timeout aguardando publishDiagnostics do gopls")
	}

	// 2. Go-to-Definition on Greet call (line 9, col 7) should jump to line 4.
	locs, err := mgr.Definition(ctx, root, "main.go", 9, 7)
	if err != nil {
		t.Fatalf("Definition falhou: %v", err)
	}
	if len(locs) == 0 || locs[0].Line != 4 {
		t.Fatalf("esperava definição na linha 4, obtido: %+v", locs)
	}

	// 3. Hover on Greet call (line 9, col 7) should return signature/doc.
	hov, err := mgr.Hover(ctx, root, "main.go", 9, 7)
	if err != nil {
		t.Fatalf("Hover falhou: %v", err)
	}
	if !strings.Contains(hov.Contents, "Greet") {
		t.Fatalf("hover inesperado: %+v", hov)
	}
}
