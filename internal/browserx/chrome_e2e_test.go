//go:build e2e

package browserx

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/jpeg"
	"image/png"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestChromeExtensionDrivesAPage abre o Chrome com a extensão e prova
// navegação, preenchimento, clique e screenshot pela ponte em 127.0.0.1.
func TestChromeExtensionDrivesAPage(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome/Chromium não está no PATH")
	}
	dist := extensionDist(t)
	if _, err := os.Stat(filepath.Join(dist, "manifest.json")); err != nil {
		t.Skip("rode npm run build em extension/chrome")
	}

	cfg := testConfig(t)
	b := startBroker(t, cfg)
	cfg.Port = b.Port()
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(buildCLI(t)); err != nil {
		t.Fatal(err)
	}
	userData := t.TempDir()
	copyManifestIntoProfile(t, userData)

	ln := serveForm(t)
	pageURL := "http://" + ln.Addr().String() + "/"
	logPath := filepath.Join(t.TempDir(), "chrome.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()

	cmd := exec.Command(chrome,
		"--headless=new", "--no-sandbox", "--disable-gpu", "--disable-dev-shm-usage",
		"--no-first-run", "--disable-sync", "--enable-logging=stderr", "--v=1",
		"--disable-extensions-except="+dist, "--load-extension="+dist,
		"--user-data-dir="+userData, "about:blank",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	deadline := time.Now().Add(20 * time.Second)
	for !b.ExtensionOn() {
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(logPath)
			t.Fatalf("extensão não conectou pelo host nativo\n%s", filterLog(string(data)))
		}
		time.Sleep(100 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	mustCall(t, ctx, b, "navigate", map[string]any{"url": pageURL})
	pageText := mustCall(t, ctx, b, "read_page", map[string]any{})
	email := refWithRole(pageText, "E-mail", "textbox")
	name := refWithRole(pageText, "Nome", "textbox")
	save := refWithRole(pageText, "Salvar", "button")
	if email == "" || name == "" || save == "" {
		t.Fatalf("refs em falta na árvore:\n%s", pageText)
	}
	mustCall(t, ctx, b, "form_input", map[string]any{"ref": email, "value": "ada@example.com"})
	mustCall(t, ctx, b, "form_input", map[string]any{"ref": name, "value": "Ada"})
	mustCall(t, ctx, b, "click", map[string]any{"ref": save})
	after := mustCall(t, ctx, b, "read_page", map[string]any{})
	if !strings.Contains(after, "salvo Ada ada@example.com") {
		t.Fatalf("página depois do clique:\n%s", after)
	}
	shot := mustCall(t, ctx, b, "screenshot", map[string]any{})
	writeJPEGShot(t, shot, "chrome-agente-formulario.png")
	shootSidePanel(t, chrome, dist)
}

func serveForm(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Acme</title>
<body style="background:#111;color:#eee;font-family:sans-serif">
<form><label>E-mail <input id="email" name="email"></label>
<label>Nome <input id="nome" name="nome"></label>
<button type="button" id="ok">Salvar</button><p id="out"></p></form>
<script>document.getElementById("ok").onclick=()=>{document.getElementById("out").textContent="salvo "+document.getElementById("nome").value+" "+document.getElementById("email").value}</script>`))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln
}

func buildCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "hivemind")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/hivemind")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func mustCall(t *testing.T, ctx context.Context, b *Broker, tool string, args map[string]any) string {
	t.Helper()
	msg, err := b.Call(ctx, tool, args)
	if err != nil || !msg.OK {
		t.Fatalf("%s: %+v %v", tool, msg, err)
	}
	if msg.Image != "" {
		return msg.Text + "\ndata:image/jpeg;base64," + msg.Image
	}
	return msg.Text
}

func refWithRole(tree, label, role string) string {
	for _, line := range strings.Split(tree, "\n") {
		low := strings.ToLower(line)
		if strings.Contains(low, strings.ToLower(label)) && strings.Contains(low, role) {
			for _, field := range strings.Fields(line) {
				if strings.HasPrefix(field, "e") && len(field) > 1 && field[1] >= '0' && field[1] <= '9' {
					return field
				}
			}
		}
	}
	return ""
}

func writeJPEGShot(t *testing.T, text, name string) {
	t.Helper()
	const mark = "base64,"
	i := strings.LastIndex(text, mark)
	if i < 0 {
		t.Fatal("screenshot sem imagem")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text[i+len(mark):]))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	savePNG(t, img, name)
}

func savePNG(t *testing.T, img image.Image, name string) {
	t.Helper()
	path := filepath.Join(moduleRoot(t), "docs", "screenshots", name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	t.Log(path)
}

func shootSidePanel(t *testing.T, chrome, dist string) {
	t.Helper()
	path := filepath.Join(moduleRoot(t), "docs", "screenshots", "chrome-agente-sidepanel.png")
	html := filepath.Join(dist, "sidepanel.html")
	cmd := exec.Command(chrome,
		"--headless=new", "--no-sandbox", "--disable-gpu", "--hide-scrollbars",
		"--window-size=420,820", "--virtual-time-budget=2000",
		"--screenshot="+path, "file://"+html,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("screenshot do side panel: %v\n%s", err, out)
	}
}

func findChrome() string {
	if p := strings.TrimSpace(os.Getenv("CHROME_BIN")); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func extensionDist(t *testing.T) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "extension", "chrome", "dist")
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func copyManifestIntoProfile(t *testing.T, userData string) {
	t.Helper()
	// Com --user-data-dir o Chrome lê o host dentro do perfil, não no XDG.
	src := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "google-chrome", "NativeMessagingHosts", NativeHostName+".json")
	dstDir := filepath.Join(userData, "NativeMessagingHosts")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, NativeHostName+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func filterLog(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "native") || strings.Contains(line, "Native") || strings.Contains(line, "hivemind") || strings.Contains(line, "HiveMind") || strings.Contains(line, "jepaepop") {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if b.Len() == 0 {
		return tail(s, 1500)
	}
	return b.String()
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
