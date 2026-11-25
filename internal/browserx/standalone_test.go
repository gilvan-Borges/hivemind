package browserx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStandaloneCDPNavigateReadTypeClick(t *testing.T) {
	if lookStandaloneBrowser() == "" {
		t.Skip("Chrome/Edge não encontrado neste ambiente")
	}
	tmp := t.TempDir()
	t.Setenv("APPDATA", tmp)
	t.Setenv("XDG_CONFIG_HOME", tmp)
	defer CloseStandalone()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html>
<head><title>Pagina Teste Hive</title></head>
<body>
  <h1>Busca Local</h1>
  <input id="q" type="text" placeholder="Pesquisar" />
  <button id="go" onclick="document.getElementById('out').textContent = 'Resultado: ' + document.getElementById('q').value">Buscar Agora</button>
  <div id="out">aguardando</div>
</body>
</html>`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nav, err := CallStandalone(ctx, "navigate", map[string]any{"url": srv.URL})
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}
	if !strings.Contains(nav, "Pagina Teste Hive") || !strings.Contains(nav, "Buscar Agora") {
		t.Fatalf("unexpected navigate snapshot:\n%s", nav)
	}

	if _, err := CallStandalone(ctx, "type", map[string]any{"name": "Pesquisar", "text": "acme-search"}); err != nil {
		t.Fatalf("type failed: %v", err)
	}
	if _, err := CallStandalone(ctx, "click", map[string]any{"name": "Buscar Agora"}); err != nil {
		t.Fatalf("click failed: %v", err)
	}
	txt, err := CallStandalone(ctx, "text", nil)
	if err != nil {
		t.Fatalf("text failed: %v", err)
	}
	if !strings.Contains(txt, "Resultado: acme-search") {
		t.Fatalf("expected clicked output 'Resultado: acme-search', got:\n%s", txt)
	}
}
