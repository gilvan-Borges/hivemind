package remote

import (
	"bytes"
	"net"
	"strings"
	"testing"
)

func TestBuildAndParsePairURI(t *testing.T) {
	urls := []string{"http://100.101.102.103:47890", "http://192.168.0.15:47890"}
	token := "a1b2c3"
	host := "meu-pc"

	uri := BuildPairURI(urls, token, host)
	if !strings.HasPrefix(uri, "hivemind://pair?") {
		t.Fatalf("URI inválida: %s", uri)
	}
	gotURLs, gotToken, gotHost, ok := ParsePairURI(uri)
	if !ok {
		t.Fatalf("ParsePairURI falhou para %s", uri)
	}
	if strings.Join(gotURLs, ",") != strings.Join(urls, ",") {
		t.Fatalf("urls divergem: %+v vs %+v", gotURLs, urls)
	}
	if gotToken != token || gotHost != host {
		t.Fatalf("token/host divergem: %q %q", gotToken, gotHost)
	}
}

func TestParsePairURIRejectsGarbage(t *testing.T) {
	if _, _, _, ok := ParsePairURI("http://example.com"); ok {
		t.Fatal("URI não-hivemind deveria ser rejeitada")
	}
	if _, _, _, ok := ParsePairURI("hivemind://pair?"); ok {
		t.Fatal("URI sem token/urls deveria ser rejeitada")
	}
}

func TestQRPNGAndDataURL(t *testing.T) {
	png, err := QRPNG("hivemind://pair?urls=x&token=y&host=z", 200)
	if err != nil {
		t.Fatalf("QRPNG: %v", err)
	}
	if len(png) < 200 {
		t.Fatalf("PNG pequeno demais: %d bytes", len(png))
	}
	// PNG magic bytes.
	if !bytes.HasPrefix(png, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("não é PNG")
	}

	dataURL, err := QRDataURL("hivemind://pair?urls=x&token=y&host=z", 160)
	if err != nil {
		t.Fatalf("QRDataURL: %v", err)
	}
	if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Fatalf("data URL inválida: %s", dataURL[:40])
	}
}

func TestQRASCII(t *testing.T) {
	ascii, err := QRASCII("hivemind://pair?urls=x&token=y&host=z")
	if err != nil {
		t.Fatalf("QRASCII: %v", err)
	}
	if len(ascii) < 100 {
		t.Fatalf("QR ASCII pequeno demais: %d", len(ascii))
	}
}

func TestDetectAddrsAreValidIPs(t *testing.T) {
	for _, a := range DetectAddrs() {
		ip := net.ParseIP(a)
		if ip == nil {
			t.Fatalf("endereço inválido: %q", a)
		}
		if ip.To4() == nil {
			t.Fatalf("esperava IPv4: %q", a)
		}
	}
}

func TestIsTailscale(t *testing.T) {
	if !isTailscale(net.ParseIP("100.64.0.1")) {
		t.Fatal("100.64.0.1 é tailscale")
	}
	if !isTailscale(net.ParseIP("100.100.1.2")) {
		t.Fatal("100.100.1.2 é tailscale")
	}
	if isTailscale(net.ParseIP("100.200.1.2")) {
		t.Fatal("100.200.1.2 não é tailscale (fora do /10)")
	}
	if isTailscale(net.ParseIP("192.168.0.1")) {
		t.Fatal("192.168.0.1 não é tailscale")
	}
}

func TestJoinURLs(t *testing.T) {
	got := joinURLs([]string{"100.1.2.3", "192.168.0.10"}, 47890)
	want := []string{"http://100.1.2.3:47890", "http://192.168.0.10:47890"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("joinURLs: %+v", got)
	}
}
