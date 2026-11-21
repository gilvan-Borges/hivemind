package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeReleaseJSON(version string, installerHash, installerName string) string {
	return fmt.Sprintf(
		`{"version":%q,"commit":"abc1234","buildDate":"2026-11-21T10:00:00Z","artifacts":{"installer":{"name":%q,"sha256":%q,"size":%d}}}`,
		version, installerName, installerHash, len("conteudo-instalador"),
	)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newReleaseServer(version, body string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, fakeReleaseJSON(version, sha256Hex(body), "HiveMindSetup-x64.exe"))
	})
	mux.HandleFunc("/HiveMindSetup-x64.exe", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	})
	return httptest.NewServer(mux)
}

func TestCheckDetectsNewerRelease(t *testing.T) {
	srv := newReleaseServer("0.3.0", "conteudo-instalador")
	defer srv.Close()

	upd, err := New(srv.URL).Check(context.Background(), "0.2.0", "installer")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if upd.Release.Version != "0.3.0" {
		t.Fatalf("versão do release = %q", upd.Release.Version)
	}
	if !strings.HasSuffix(upd.URL, "/HiveMindSetup-x64.exe") {
		t.Fatalf("URL do artefato = %q", upd.URL)
	}
	if upd.Artifact.SHA256 != sha256Hex("conteudo-instalador") {
		t.Fatalf("hash do artefato divergiu do manifesto")
	}
}

func TestCheckReportsNoUpdateWhenSameOrOlder(t *testing.T) {
	for _, current := range []string{"0.3.0", "0.4.0", "v0.3.0"} {
		srv := newReleaseServer("0.3.0", "x")
		upd, err := New(srv.URL).Check(context.Background(), current, "installer")
		srv.Close()
		if !errors.Is(err, ErrNoUpdate) {
			t.Fatalf("current %q: esperava ErrNoUpdate, veio (%v, %v)", current, upd, err)
		}
	}
}

func TestIsNewerTolerantVersions(t *testing.T) {
	cases := []struct {
		current, candidate string
		want               bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.2.0", "0.2.1", true},
		{"0.2.0", "0.10.0", true}, // numérico, não lexicográfico
		{"0.2.0", "1.0.0", true},
		{"v1.2.3", "1.3.0", true},
		{"0.2.0", "0.2.0", false},
		{"0.2.0", "0.2.0-beta", false}, // pre-release não conta como mais novo
		{"0.3.0", "0.2.9", false},
		{"", "0.1.0", true},
		{"0.1.0", "", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.current, c.candidate); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, queria %v", c.current, c.candidate, got, c.want)
		}
	}
}

func TestDownloadVerifiesSHA256(t *testing.T) {
	body := "conteudo-instalador"
	srv := newReleaseServer("0.3.0", body)
	defer srv.Close()

	dir := t.TempDir()
	client := New(srv.URL)
	upd, err := client.Check(context.Background(), "0.2.0", "installer")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	path, err := client.DownloadArtifact(context.Background(), upd, dir)
	if err != nil {
		t.Fatalf("DownloadArtifact: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ler download: %v", err)
	}
	if string(raw) != body {
		t.Fatalf("conteúdo baixado divergiu")
	}
	if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
		t.Fatalf("arquivo .part não foi finalizado: %v", err)
	}
}

func TestDownloadRejectsTamperedArtifact(t *testing.T) {
	// Manifesto anuncia o hash do conteúdo original, mas o servidor entrega outra coisa.
	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, fakeReleaseJSON("0.3.0", sha256Hex("original"), "HiveMindSetup-x64.exe"))
	})
	mux.HandleFunc("/HiveMindSetup-x64.exe", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "malicioso")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	client := New(srv.URL)
	upd, err := client.Check(context.Background(), "0.2.0", "installer")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	_, err = client.DownloadArtifact(context.Background(), upd, dir)
	if err == nil || !strings.Contains(err.Error(), "SHA-256 divergente") {
		t.Fatalf("esperava falha de hash, veio %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("download reprovado deixou arquivos para trás: %d", len(entries))
	}
}

func TestCheckFailsClosedOnBadManifest(t *testing.T) {
	cases := map[string]func(w http.ResponseWriter, r *http.Request){
		"404":        func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusNotFound) },
		"sem versão": func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"artifacts":{}}`) },
		"sem artefato": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"version":"0.3.0","artifacts":{"outro":{"name":"a.exe"}}}`)
		},
		"hash inválido": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"version":"0.3.0","artifacts":{"installer":{"name":"a.exe","sha256":"zzz"}}}`)
		},
	}
	for name, handler := range cases {
		mux := http.NewServeMux()
		mux.HandleFunc("/manifest.json", handler)
		srv := httptest.NewServer(mux)
		_, err := New(srv.URL).Check(context.Background(), "0.2.0", "installer")
		srv.Close()
		if err == nil {
			t.Fatalf("manifesto %q deveria falhar", name)
		}
	}
}

func TestParseChecksumsRoundTripsInstallerFormat(t *testing.T) {
	// Mesmo formato que scripts/build-installer.ps1 escreve em dist/checksums.txt.
	contents := []byte("HiveMindSetup-x64.exe payload")
	h1 := sha256Hex(string(contents))
	h2 := sha256Hex("hivemind.exe")
	data := fmt.Sprintf("# HiveMind %s\n%s  HiveMindSetup-x64.exe\n%s  hivemind.exe\n", "0.3.0", h1, h2)

	got, err := ParseChecksums([]byte(data))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if got["HiveMindSetup-x64.exe"] != h1 {
		t.Fatalf("hash do instalador divergiu: %q", got["HiveMindSetup-x64.exe"])
	}
	if got["hivemind.exe"] != h2 {
		t.Fatalf("hash da CLI divergiu")
	}

	if _, err := ParseChecksums([]byte("zzz arquivo.exe")); err == nil {
		t.Fatal("linha com hash inválido deveria falhar")
	}
	if _, err := ParseChecksums([]byte("coluna a mais aqui")); err == nil {
		t.Fatal("linha com colunas extras deveria falhar")
	}
}

func TestVerifyFileSHA256ChecksDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFileSHA256(path, sha256Hex("abc")); err != nil {
		t.Fatalf("arquivo íntegro reprovado: %v", err)
	}
	if err := VerifyFileSHA256(path, sha256Hex("abd")); err == nil {
		t.Fatal("arquivo alterado foi aceito")
	}
	if err := VerifyFileSHA256(path, "curto"); err == nil {
		t.Fatal("digest malformado foi aceito")
	}
}
