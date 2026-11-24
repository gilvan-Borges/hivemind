package browserx

import (
	"context"
	"strings"
	"testing"
)

func TestCatalogShrinksAndHidesDevtools(t *testing.T) {
	legacy := legacyCatalogBytes()
	var now int
	names := map[string]bool{}
	for _, tool := range Catalog() {
		now += len(tool.Name) + len(tool.Description) + len(tool.Schema)
		names[tool.Name] = true
	}
	if now >= legacy*70/100 {
		t.Fatalf("esquema novo %d bytes, legado %d", now, legacy)
	}
	for _, hidden := range []string{"console_read", "network_read", "get_dom", "computed_styles", "javascript_exec", "upload"} {
		if names[hidden] {
			t.Fatalf("%s deveria entrar só com devtools", hidden)
		}
	}
	if !names["devtools"] || !names["read_page"] || !names["batch"] {
		t.Fatalf("catálogo: %v", names)
	}
	t.Logf("esquema %d -> %d bytes (%.0f%%)", legacy, now, float64(now)*100/float64(legacy))
}

func TestBrowserHistoryTokensAndUploadGate(t *testing.T) {
	testConfig(t)
	br := &seqBridge{}
	SetBridgeForTest(t, br)
	if _, err := callFromAgent(context.Background(), "upload", map[string]any{"ref": "e1"}); err == nil || br.calls != 0 {
		t.Fatalf("upload sem aprovador chegou na extensão: %v calls=%d", err, br.calls)
	}
	ctx := WithApprover(context.Background(), func(string) bool { return false })
	if _, err := callFromAgent(ctx, "dialog", map[string]any{"accept": true}); err == nil {
		t.Fatal("aceitar diálogo sem sim")
	}
}

func legacyCatalogBytes() int {
	rows := []struct{ name, desc, schema string }{
		{"navigate", "Abre uma URL http(s) na aba atual do Chrome real do usuário. Site fora da lista pede aprovação.", `{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`},
		{"read_page", "Lê a árvore de acessibilidade da aba e devolve refs e1, e2… para click, type e form_input.", `{"type":"object","properties":{"tabId":{"type":"number"}}}`},
		{"find", "Procura texto ou papel na última árvore lida e devolve as refs que casam.", `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`},
		{"click", "Clica numa ref da árvore ou em coordenadas CSS. Enviar, comprar ou pagar pede aprovação.", `{"type":"object","properties":{"ref":{"type":"string"},"x":{"type":"number"},"y":{"type":"number"},"submit":{"type":"boolean"},"name":{"type":"string"}}}`},
		{"type", "Foca a ref e insere texto com eventos reais de teclado. Não use para enviar o formulário.", `{"type":"object","properties":{"ref":{"type":"string"},"text":{"type":"string"}},"required":["text"]}`},
		{"form_input", "Preenche um campo. Com submit true, pede aprovação antes de enviar.", `{"type":"object","properties":{"ref":{"type":"string"},"value":{"type":"string"},"submit":{"type":"boolean"}},"required":["ref","value"]}`},
		{"scroll", "Rola a página com evento de roda do mouse.", `{"type":"object","properties":{"dx":{"type":"number"},"dy":{"type":"number"},"ref":{"type":"string"}}}`},
		{"key", "Aperta uma tecla (Enter, Tab, Escape, setas, ou um caractere).", `{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}`},
		{"screenshot", "Captura a aba em JPEG para o agente ver a tela.", `{"type":"object"}`},
		{"tabs_list", "Lista as abas abertas.", `{"type":"object"}`},
		{"tabs_create", "Abre uma aba. URL nova pede aprovação.", `{"type":"object","properties":{"url":{"type":"string"}}}`},
		{"tabs_close", "Fecha uma aba pelo tabId.", `{"type":"object","properties":{"tabId":{"type":"number"}},"required":["tabId"]}`},
		{"tabs_switch", "Ativa uma aba pelo tabId.", `{"type":"object","properties":{"tabId":{"type":"number"}},"required":["tabId"]}`},
		{"console_read", "Devolve os últimos console logs da aba, sem segredos.", `{"type":"object"}`},
		{"network_read", "Devolve as últimas requisições (método, URL filtrada, status). Sem cookies nem Authorization.", `{"type":"object"}`},
		{"get_dom", "HTML externo do documento ou da ref, truncado.", `{"type":"object","properties":{"ref":{"type":"string"}}}`},
		{"computed_styles", "Estilos computados principais da ref (cor, fonte, display, caixa).", `{"type":"object","properties":{"ref":{"type":"string"}},"required":["ref"]}`},
		{"javascript_exec", "Executa JavaScript na página. Sempre pede aprovação. O resultado pode conter dado da página.", `{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"]}`},
		{"wait", "Espera milissegundos (teto 10s) ou até o texto aparecer na árvore.", `{"type":"object","properties":{"ms":{"type":"number"},"text":{"type":"string"}}}`},
	}
	n := 0
	for _, row := range rows {
		n += len(row.name) + len(row.desc) + len(row.schema)
	}
	return n
}

func TestLegacyCatalogNotEmpty(t *testing.T) {
	if legacyCatalogBytes() < 1000 || strings.Contains(Catalog()[0].Description, "árvore de acessibilidade da aba e devolve refs") {
		t.Fatal("o catálogo legado ou o novo não está no formato esperado")
	}
}
