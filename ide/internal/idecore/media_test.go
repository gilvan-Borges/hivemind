package idecore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLocalMediaDataURI(t *testing.T) {
	ws := t.TempDir()
	imgPath := filepath.Join(ws, "shot.png")
	pngStub := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if err := os.WriteFile(imgPath, pngStub, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadLocalMediaDataURI(ws, "shot.png")
	if err != nil {
		t.Fatalf("ReadLocalMediaDataURI relative falhou: %v", err)
	}
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("esperava data:image/png;base64,..., obtido: %q", got)
	}

	fileURI := "file:///" + filepath.ToSlash(imgPath)
	gotURI, err := ReadLocalMediaDataURI(ws, fileURI)
	if err != nil || !strings.HasPrefix(gotURI, "data:image/png;base64,") {
		t.Fatalf("ReadLocalMediaDataURI file:// falhou: err=%v got=%q", err, gotURI)
	}

	// Rejeita extensão não-imagem
	txtPath := filepath.Join(ws, "notes.txt")
	_ = os.WriteFile(txtPath, []byte("oi"), 0o644)
	if _, err := ReadLocalMediaDataURI(ws, "notes.txt"); err == nil {
		t.Fatal("deveria recusar extensão .txt")
	}

	// Rejeita caminho fora das pastas permitidas
	if _, err := ReadLocalMediaDataURI(ws, filepath.Join("Z:\\fora\\permitido", "shot.png")); err == nil {
		t.Fatal("deveria recusar caminho fora dos diretórios permitidos")
	}
}
