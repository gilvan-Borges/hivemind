//go:build e2e

package browserx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestChromePublicSearch dirige o Chrome com janela (Xvfb) pela extensão:
// Google e, se houver captcha, DuckDuckGo e a Wikipedia.
func TestChromePublicSearch(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome/Chromium não está no PATH")
	}
	if _, err := os.Stat(filepath.Join(extensionDist(t), "manifest.json")); err != nil {
		t.Skip("rode npm run build em extension/chrome")
	}
	if _, err := exec.LookPath("Xvfb"); err != nil || !hasFFmpeg() {
		t.Skip("Xvfb e ffmpeg são necessários para o demo com janela")
	}

	display := ":99"
	xvfb := exec.Command("Xvfb", display, "-screen", "0", "1280x800x24", "-ac", "+extension", "RANDR")
	if err := xvfb.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = xvfb.Process.Kill() })
	time.Sleep(300 * time.Millisecond)

	cfg := testConfig(t)
	b := startBroker(t, cfg)
	cfg.Port = b.Port()
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(buildCLI(t)); err != nil {
		t.Fatal(err)
	}
	userData, err := os.MkdirTemp("", "hm-chrome-profile-")
	if err != nil {
		t.Fatal(err)
	}
	copyManifestIntoProfile(t, userData)

	mp4 := filepath.Join(t.TempDir(), "demo.mp4")
	ff := exec.Command("ffmpeg", "-y", "-f", "x11grab", "-video_size", "1280x800", "-framerate", "8", "-i", display+".0", "-an", mp4)
	ff.Env = append(os.Environ(), "DISPLAY="+display)
	if err := ff.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = ff.Process.Signal(os.Interrupt)
		_ = ff.Wait()
		makeGIF(t, mp4)
	}()

	logFile, err := os.Create(filepath.Join(t.TempDir(), "chrome.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd := exec.Command(chrome,
		"--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu",
		"--no-first-run", "--disable-sync",
		"--window-size=1280,800", "--window-position=0,0",
		"--disable-extensions-except="+extensionDist(t), "--load-extension="+extensionDist(t),
		"--user-data-dir="+userData, "about:blank",
	)
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
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

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	s := &pubSession{t: t, ctx: ctx, b: b, display: display}

	if !s.google() {
		t.Log("Google bloqueou ou não entregou a busca; seguindo para DuckDuckGo e Wikipedia")
		s.duck()
		s.wiki()
	}
}

type pubSession struct {
	t       *testing.T
	ctx     context.Context
	b       *Broker
	display string
}

func (s *pubSession) call(tool string, args map[string]any) (string, error) {
	s.t.Helper()
	msg, err := s.b.Call(s.ctx, tool, args)
	if err != nil {
		return "", err
	}
	if !msg.OK {
		return msg.Text, errString(msg.Text)
	}
	return msg.Text, nil
}

func (s *pubSession) must(tool string, args map[string]any) string {
	s.t.Helper()
	text, err := s.call(tool, args)
	if err != nil {
		s.t.Fatalf("%s: %v", tool, err)
	}
	return text
}

func (s *pubSession) shot(name string) {
	s.t.Helper()
	time.Sleep(600 * time.Millisecond)
	path := filepath.Join(moduleRoot(s.t), "docs", "screenshots", name)
	cmd := exec.Command("ffmpeg", "-y", "-f", "x11grab", "-video_size", "1280x800", "-i", s.display+".0", "-frames:v", "1", path)
	cmd.Env = append(os.Environ(), "DISPLAY="+s.display)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.t.Fatalf("screenshot %s: %v\n%s", name, err, out)
	}
	s.t.Log(path)
}

func (s *pubSession) google() bool {
	if _, err := s.call("navigate", map[string]any{"url": "https://www.google.com"}); err != nil {
		s.t.Logf("google navigate: %v", err)
		return false
	}
	s.shot("chrome-google-01-home.png")
	page := s.must("read_page", map[string]any{})
	if ref := consentRef(page); ref != "" {
		_, _ = s.call("click", map[string]any{"ref": ref})
		time.Sleep(800 * time.Millisecond)
		s.shot("chrome-google-02-consentimento.png")
		page = s.must("read_page", map[string]any{})
	}
	if isCaptcha(page) {
		s.shot("chrome-google-captcha.png")
		return false
	}
	box := searchRef(page)
	if box == "" {
		s.t.Logf("caixa de busca do Google não apareceu:\n%s", trim(page, 1500))
		s.shot("chrome-google-captcha.png")
		return false
	}
	if _, err := s.call("type", map[string]any{"ref": box, "text": "HiveMind IDE github"}); err != nil {
		s.t.Logf("digitar: %v", err)
		return false
	}
	s.shot("chrome-google-03-digitando.png")
	_, _ = s.call("key", map[string]any{"key": "Enter"})
	time.Sleep(1500 * time.Millisecond)
	results := s.must("read_page", map[string]any{})
	if isCaptcha(results) {
		s.shot("chrome-google-captcha.png")
		return false
	}
	s.shot("chrome-google-04-resultados.png")
	link, label := firstResult(results)
	if link == "" {
		s.t.Logf("sem link de resultado:\n%s", trim(results, 2500))
		return false
	}
	s.t.Logf("clicando %s %s", link, label)
	if _, err := s.call("click", map[string]any{"ref": link}); err != nil {
		s.t.Logf("clique: %v", err)
		return false
	}
	landed := ""
	for i := 0; i < 8; i++ {
		time.Sleep(700 * time.Millisecond)
		landed = s.must("read_page", map[string]any{})
		if !strings.Contains(landed, "google.com/search") {
			break
		}
	}
	if strings.Contains(landed, "google.com/search") {
		s.t.Fatalf("o clique em %s %s não saiu da busca:\n%s", link, label, trim(results, 2500))
	}
	_, _ = s.call("read_page", map[string]any{})
	s.shot("chrome-google-05-destino.png")
	return true
}

func (s *pubSession) duck() {
	s.must("navigate", map[string]any{"url": "https://duckduckgo.com"})
	s.shot("chrome-google-ddg-01-home.png")
	page := s.must("read_page", map[string]any{})
	if ref := consentRef(page); ref != "" {
		_, _ = s.call("click", map[string]any{"ref": ref})
		time.Sleep(600 * time.Millisecond)
		page = s.must("read_page", map[string]any{})
	}
	box := searchRef(page)
	if box == "" {
		s.t.Fatalf("DuckDuckGo sem caixa de busca:\n%s", trim(page, 1500))
	}
	s.must("type", map[string]any{"ref": box, "text": "HiveMind IDE github"})
	s.shot("chrome-google-ddg-02-digitando.png")
	s.must("key", map[string]any{"key": "Enter"})
	time.Sleep(1500 * time.Millisecond)
	results := s.must("read_page", map[string]any{})
	s.shot("chrome-google-ddg-03-resultados.png")
	link, _ := firstResult(results)
	if link == "" {
		s.t.Fatalf("DuckDuckGo sem resultado:\n%s", trim(results, 1500))
	}
	s.must("click", map[string]any{"ref": link})
	time.Sleep(1500 * time.Millisecond)
	s.shot("chrome-google-ddg-04-destino.png")
}

func (s *pubSession) wiki() {
	s.must("navigate", map[string]any{"url": "https://en.wikipedia.org"})
	page := s.must("read_page", map[string]any{})
	box := searchRef(page)
	if box == "" {
		s.t.Fatalf("Wikipedia sem busca:\n%s", trim(page, 1500))
	}
	s.must("type", map[string]any{"ref": box, "text": "Go programming language"})
	s.shot("chrome-google-wiki-01-busca.png")
	s.must("key", map[string]any{"key": "Enter"})
	time.Sleep(1500 * time.Millisecond)
	results := s.must("read_page", map[string]any{})
	link := refWithRole(results, "Go (programming language)", "link")
	if link == "" {
		link = refWithRole(results, "Go ", "link")
	}
	if link == "" {
		link, _ = firstResult(results)
	}
	if link == "" {
		s.t.Fatalf("Wikipedia sem artigo:\n%s", trim(results, 1500))
	}
	s.must("click", map[string]any{"ref": link})
	time.Sleep(1500 * time.Millisecond)
	s.shot("chrome-google-wiki-02-artigo.png")
}

func searchRef(tree string) string {
	for _, role := range []string{"searchbox", "combobox", "textbox"} {
		for _, label := range []string{"search", "pesquisar", "buscar", "busca"} {
			if ref := refWithRole(tree, label, role); ref != "" {
				return ref
			}
		}
	}
	return ""
}

func consentRef(tree string) string {
	for _, label := range []string{"aceitar tudo", "accept all", "i agree", "concordo", "aceitar", "accept", "reject all", "rejeitar tudo", "recusar"} {
		if ref := refWithRole(tree, label, "button"); ref != "" {
			return ref
		}
	}
	return ""
}

func isCaptcha(tree string) bool {
	low := strings.ToLower(tree)
	for _, needle := range []string{"captcha", "recaptcha", "unusual traffic", "tráfego incomum", "not a robot", "não é um robô", "our systems have detected"} {
		if strings.Contains(low, needle) {
			return true
		}
	}
	return false
}

func firstResult(tree string) (string, string) {
	for _, line := range strings.Split(tree, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(low, "link") {
			continue
		}
		name := strings.ToLower(quotedName(line))
		if len(strings.Fields(name)) < 5 {
			continue
		}
		if strings.Contains(name, "imagem") || strings.Contains(name, "image") || strings.Contains(name, "vídeo") || strings.Contains(name, "video") || strings.Contains(name, "maps") || name == "google" {
			continue
		}
		if !strings.Contains(name, "hivemind") && !strings.Contains(name, "github") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if len(field) > 1 && field[0] == 'e' && field[1] >= '0' && field[1] <= '9' {
				return field, name
			}
		}
	}
	return "", ""
}

func quotedName(line string) string {
	i := strings.Index(line, `"`)
	j := strings.LastIndex(line, `"`)
	if i < 0 || j <= i {
		return ""
	}
	return line[i+1 : j]
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func hasFFmpeg() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

func makeGIF(t *testing.T, mp4 string) {
	t.Helper()
	if _, err := os.Stat(mp4); err != nil {
		t.Logf("sem vídeo: %v", err)
		return
	}
	gif := filepath.Join(moduleRoot(t), "docs", "screenshots", "chrome-google-demo.gif")
	cmd := exec.Command("ffmpeg", "-y", "-i", mp4, "-vf", "fps=6,scale=960:-1:flags=lanczos", gif)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gif: %v\n%s", err, out)
	}
	t.Log(gif)
}
