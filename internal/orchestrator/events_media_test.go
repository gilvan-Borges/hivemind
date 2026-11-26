package orchestrator

import "testing"

func TestExtractToolResultImages(t *testing.T) {
	pngA := "data:image/png;base64,iVBORw0KGgo="
	jpgB := "data:image/jpeg;base64,/9j/4AAQSkZJRg=="
	webpC := "data:image/webp;base64,UklGRh4="
	gifD := "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw=="
	pngE := "data:image/png;base64,AAAA"

	resp := map[string]any{
		"note": "texto comum sem imagem",
		"nested": map[string]any{
			"items": []any{
				map[string]any{"data": pngA},
				map[string]any{"data": pngA}, // duplicata ignorada
				"print salvo em " + jpgB + " e também " + webpC,
				[]any{gifD, pngE},
			},
		},
	}

	got := extractToolResultImages(resp)
	if len(got) != 4 {
		t.Fatalf("esperava 4 imagens (limite), obtido %d: %v", len(got), got)
	}
	want := []string{pngA, jpgB, webpC, gifD}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("imagem[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	if empty := extractToolResultImages(map[string]any{"ok": "sem imagem"}); len(empty) != 0 {
		t.Fatalf("não deveria extrair imagens de texto comum: %v", empty)
	}
}
