//go:build e2e

package browserx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestChromeV2Demo percorre o side panel na mesma janela e grava a conversa
// compacta: vazio, ações agrupadas, resposta e aprovação.
func TestChromeV2Demo(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome/Chromium não está no PATH")
	}
	if _, err := exec.LookPath("xdotool"); err != nil {
		t.Skip("xdotool é necessário para abrir o side panel")
	}
	if _, err := os.Stat(filepath.Join(extensionDist(t), "manifest.json")); err != nil {
		t.Skip("rode npm run build em extension/chrome")
	}
	display := ":99"
	xvfb := exec.Command("Xvfb", display, "-screen", "0", "1440x900x24", "-ac")
	if err := xvfb.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = xvfb.Process.Kill() })
	time.Sleep(200 * time.Millisecond)

	cfg := testConfig(t)
	b := startBroker(t, cfg)
	cfg.Port = b.Port()
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(buildCLI(t)); err != nil {
		t.Fatal(err)
	}
	userData, err := os.MkdirTemp("", "hm-chrome-v2-")
	if err != nil {
		t.Fatal(err)
	}
	copyManifestIntoProfile(t, userData)
	pinExtension(t, userData)

	mp4 := filepath.Join(t.TempDir(), "v2.mp4")
	ff := exec.Command("ffmpeg", "-y", "-f", "x11grab", "-video_size", "1440x900", "-framerate", "6", "-i", display+".0", "-an", mp4)
	ff.Env = append(os.Environ(), "DISPLAY="+display)
	if err := ff.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = ff.Process.Signal(os.Interrupt)
		_ = ff.Wait()
		gif := filepath.Join(moduleRoot(t), "docs", "screenshots", "chrome-v2-demo.gif")
		cmd := exec.Command("ffmpeg", "-y", "-i", mp4, "-vf", "fps=4,scale=1000:-1:flags=lanczos", gif)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("gif: %v\n%s", err, out)
		}
		t.Log(gif)
	}()

	cmd := exec.Command(chrome,
		"--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu",
		"--no-first-run", "--disable-sync", "--start-maximized",
		"--window-size=1440,900", "--window-position=0,0",
		"--disable-extensions-except="+extensionDist(t), "--load-extension="+extensionDist(t),
		"--user-data-dir="+userData, "about:blank",
	)
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.RemoveAll(userData)
	})

	deadline := time.Now().Add(25 * time.Second)
	for !b.ExtensionOn() {
		if time.Now().After(deadline) {
			t.Fatal("extensão não conectou")
		}
		time.Sleep(100 * time.Millisecond)
	}
	openSidePanel(t, display)
	shot := func(name string) {
		t.Helper()
		time.Sleep(400 * time.Millisecond)
		path := filepath.Join(moduleRoot(t), "docs", "screenshots", name)
		grab := exec.Command("ffmpeg", "-y", "-f", "x11grab", "-video_size", "1440x900", "-i", display+".0", "-frames:v", "1", path)
		grab.Env = append(os.Environ(), "DISPLAY="+display)
		if out, err := grab.CombinedOutput(); err != nil {
			t.Fatalf("screenshot %s: %v\n%s", name, err, out)
		}
		t.Log(path)
	}
	first := filepath.Join(moduleRoot(t), "docs", "screenshots", "chrome-v2-01-vazio.png")
	opened := false
	for _, x := range []int{1336, 1296, 1256, 1216} {
		clickAt(display, x, 62)
		time.Sleep(450 * time.Millisecond)
		grab := exec.Command("ffmpeg", "-y", "-f", "x11grab", "-video_size", "1440x900", "-i", display+".0", "-frames:v", "1", first)
		grab.Env = append(os.Environ(), "DISPLAY="+display)
		if out, err := grab.CombinedOutput(); err != nil {
			t.Fatalf("screenshot vazio: %v\n%s", err, out)
		}
		if rightSideDark(t, first) {
			opened = true
			break
		}
		xdo(display, "xdotool", "key", "--clearmodifiers", "Escape")
	}
	if !opened {
		t.Fatalf("o side panel não está na janela: %s", first)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := b.AnnounceUser(ctx, "Pesquise HiveMind IDE github e abra o primeiro resultado"); err != nil {
		t.Fatal(err)
	}
	s := &pubSession{t: t, ctx: ctx, b: b, display: display}
	if !v2Search(s, "https://www.google.com", "HiveMind IDE github") {
		t.Log("Google não entregou a busca; seguindo para DuckDuckGo")
		if !v2Search(s, "https://duckduckgo.com", "HiveMind IDE github") {
			t.Log("DuckDuckGo não entregou a busca; seguindo para Wikipedia")
			v2Wiki(s)
		}
	}
	shot("chrome-v2-02-acoes.png")
	page := s.must("read_page", map[string]any{"fresh": true})
	if len(page) > 9000 {
		t.Fatalf("read_page compacto grande demais: %d", len(page))
	}
	text := s.must("get_page_text", map[string]any{"limit": 800})
	if len(strings.TrimSpace(text)) < 40 {
		t.Fatalf("get_page_text curto:\n%s", trim(text, 200))
	}
	full := imageLen(t, s, map[string]any{"quality": 40, "scale": 1})
	small := imageLen(t, s, map[string]any{"quality": 28, "scale": 0.5})
	t.Logf("screenshot base64 %d -> %d", full, small)
	if small >= full {
		t.Fatalf("o screenshot menor não encolheu: %d vs %d", small, full)
	}
	reply := "## Pronto\nAbri o primeiro resultado de **HiveMind**.\n- li a página\n- cliquei no link"
	for i := 0; i < len(reply); i += 36 {
		end := i + 36
		if end > len(reply) {
			end = len(reply)
		}
		if err := b.Push(ctx, Message{Type: "chat_delta", Text: reply[i:end]}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(40 * time.Millisecond)
	}
	if err := b.Push(ctx, Message{Type: "chat_reply", Text: reply}); err != nil {
		t.Fatal(err)
	}
	shot("chrome-v2-03-resposta.png")
	if err := b.Push(ctx, Message{Type: "approve", ID: "demo", Text: "O Agent Hive quer abrir example.com no seu Chrome.", Host: "example.com"}); err != nil {
		t.Fatal(err)
	}
	shot("chrome-v2-04-aprovacao.png")
	fmt.Fprintf(os.Stderr, "v2 screenshot bytes %d %d read_page %d\n", full, small, len(page))
}

func imageLen(t *testing.T, s *pubSession, args map[string]any) int {
	t.Helper()
	msg, err := s.b.Call(s.ctx, "screenshot", args)
	if err != nil || !msg.OK || msg.Image == "" {
		t.Fatalf("screenshot: %v ok=%v len=%d", err, msg.OK, len(msg.Image))
	}
	return len(msg.Image)
}

func v2Search(s *pubSession, url, query string) bool {
	if _, err := s.call("navigate", map[string]any{"url": url}); err != nil {
		s.t.Log(err)
		return false
	}
	page := s.must("read_page", map[string]any{"fresh": true})
	if isCaptcha(page) {
		return false
	}
	if ref := consentRef(page); ref != "" {
		_, _ = s.call("click", map[string]any{"ref": ref})
		time.Sleep(500 * time.Millisecond)
		page = s.must("read_page", map[string]any{"fresh": true})
	}
	box := searchRef(page)
	if box == "" {
		found := s.must("find", map[string]any{"query": "search", "role": "searchbox"})
		box = searchRef(found + "\n" + page)
	}
	if box == "" {
		s.t.Logf("sem busca em %s\n%s", url, trim(page, 500))
		return false
	}
	if _, err := s.call("batch", map[string]any{"actions": []any{
		map[string]any{"tool": "type", "ref": box, "text": query},
		map[string]any{"tool": "key", "key": "Enter"},
	}}); err != nil {
		s.t.Log(err)
		return false
	}
	time.Sleep(1200 * time.Millisecond)
	results := s.must("read_page", map[string]any{"fresh": true})
	if isCaptcha(results) {
		return false
	}
	link, _ := firstResult(results)
	if link == "" {
		link = refWithRole(results, "hivemind", "link")
	}
	if link == "" {
		s.t.Logf("sem resultado em %s\n%s", url, trim(results, 500))
		return false
	}
	if _, err := s.call("click", map[string]any{"ref": link}); err != nil {
		s.t.Log(err)
		return false
	}
	time.Sleep(800 * time.Millisecond)
	return true
}

func pinExtension(t *testing.T, userData string) {
	t.Helper()
	dir := filepath.Join(userData, "Default")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"extensions":{"pinned_extensions":["` + ExtensionID + `"]}}`
	if err := os.WriteFile(filepath.Join(dir, "Preferences"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func v2Wiki(s *pubSession) {
	s.t.Helper()
	s.must("navigate", map[string]any{"url": "https://en.wikipedia.org/wiki/Go_(programming_language)"})
	page := s.must("read_page", map[string]any{"fresh": true})
	if len(page) < 20 {
		s.t.Fatalf("wikipedia vazia:\n%s", trim(page, 300))
	}
}
