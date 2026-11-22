package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildTreeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mk := func(rel string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("cmd/server/main.go")
	mk("internal/domain/order.go")
	mk("internal/domain/billing.go")
	mk("internal/infra/http/handler.go")
	mk("internal/infra/store/repo.go")
	mk("docs/SPEC.md")
	mk(".env")
	return root
}

func TestListTreeRendersSkeleton(t *testing.T) {
	root := buildTreeFixture(t)
	d := Deps{Root: root}
	out, err := d.listTree(listTreeArgs{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cmd/", "server/", "main.go", "internal/", "domain/", "docs/", "SPEC.md"} {
		if !strings.Contains(out, want) {
			t.Fatalf("esperado %q no esqueleto, saída:\n%s", want, out)
		}
	}
	if strings.Contains(out, ".env") {
		t.Fatalf("arquivo sensível não deve aparecer no esqueleto, saída:\n%s", out)
	}
	if strings.Contains(out, "não exploradas") {
		t.Fatalf("fixture pequena não deveria estourar orçamento, saída:\n%s", out)
	}
}

func TestListTreeBudgetMarksUnexplored(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		dir := filepath.Join(root, "pkg", string(rune('a'+i%26)), string(rune('a'+i/26)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "file.go"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d := Deps{Root: root}
	out, err := d.listTree(listTreeArgs{MaxChars: 120})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "não exploradas") {
		t.Fatalf("orçamento pequeno deve marcar ramos não explorados, saída:\n%s", out)
	}
	if !strings.Contains(out, "parcial") {
		t.Fatalf("cabeçalho deve declarar saída parcial, saída:\n%s", out)
	}
	if len(out) > 120+400 { // header + marker tolerances
		t.Fatalf("saída deve respeitar ~orçamento (veio %d bytes):\n%s", len(out), out)
	}
}

func TestListTreeDepthCap(t *testing.T) {
	root := buildTreeFixture(t)
	d := Deps{Root: root}
	out, err := d.listTree(listTreeArgs{MaxDepth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "order.go") {
		t.Fatalf("profundidade 1 não deve alcançar arquivos de internal/domain, saída:\n%s", out)
	}
	if !strings.Contains(out, "internal/") {
		t.Fatalf("nível 1 deve listar internal/, saída:\n%s", out)
	}
}

func TestListTreeRejectsSensitiveAndNonDir(t *testing.T) {
	root := buildTreeFixture(t)
	d := Deps{Root: root}
	if _, err := d.listTree(listTreeArgs{Path: ".env"}); err == nil {
		t.Fatal("caminho sensível deve ser recusado")
	}
	if _, err := d.listTree(listTreeArgs{Path: "docs/SPEC.md"}); err == nil {
		t.Fatal("arquivo (não diretório) deve ser recusado")
	}
}
