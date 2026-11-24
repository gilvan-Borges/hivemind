//go:build e2e

package browserx

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestChromeSidePanelDemo abre o side panel nativo na mesma janela e grava
// a janela inteira (aba + painel) pelo display.
func TestChromeSidePanelDemo(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome/Chromium não está no PATH")
	}
	if _, err := exec.LookPath("xdotool"); err != nil {
		t.Skip("xdotool é necessário para o gesto que abre o side panel")
	}
	display := ":98"
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
	userData, err := os.MkdirTemp("", "hm-sidepanel-")
	if err != nil {
		t.Fatal(err)
	}
	copyManifestIntoProfile(t, userData)

	mp4 := filepath.Join(t.TempDir(), "side.mp4")
	ff := exec.Command("ffmpeg", "-y", "-f", "x11grab", "-video_size", "1440x900", "-framerate", "6", "-i", display+".0", "-an", mp4)
	ff.Env = append(os.Environ(), "DISPLAY="+display)
	if err := ff.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = ff.Process.Signal(os.Interrupt)
		_ = ff.Wait()
		gif := filepath.Join(moduleRoot(t), "docs", "screenshots", "chrome-sidepanel-demo.gif")
		cmd := exec.Command("ffmpeg", "-y", "-i", mp4, "-vf", "fps=5,scale=1100:-1:flags=lanczos", gif)
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
	shot := func(name string) string {
		t.Helper()
		time.Sleep(500 * time.Millisecond)
		path := filepath.Join(moduleRoot(t), "docs", "screenshots", name)
		grab := exec.Command("ffmpeg", "-y", "-f", "x11grab", "-video_size", "1440x900", "-i", display+".0", "-frames:v", "1", path)
		grab.Env = append(os.Environ(), "DISPLAY="+display)
		if out, err := grab.CombinedOutput(); err != nil {
			t.Fatalf("screenshot %s: %v\n%s", name, err, out)
		}
		t.Log(path)
		return path
	}
	clickAt(display, 1336, 64)
	time.Sleep(300 * time.Millisecond)
	clickAt(display, 1080, 208)
	time.Sleep(600 * time.Millisecond)
	first := shot("chrome-sidepanel-google-01-painel.png")
	if !rightSideDark(t, first) {
		t.Fatalf("o side panel nativo não está na janela: %s", first)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := b.AnnounceUser(ctx, "Pesquise HiveMind IDE github no Google e abra o primeiro resultado"); err != nil {
		t.Fatal(err)
	}
	s := &pubSession{t: t, ctx: ctx, b: b, display: display}
	if _, err := s.call("navigate", map[string]any{"url": "https://www.google.com"}); err != nil {
		t.Fatal(err)
	}
	page := s.must("read_page", map[string]any{})
	if ref := consentRef(page); ref != "" {
		_, _ = s.call("click", map[string]any{"ref": ref})
		time.Sleep(700 * time.Millisecond)
		page = s.must("read_page", map[string]any{})
	}
	box := searchRef(page)
	if box == "" || isCaptcha(page) {
		t.Fatalf("Google sem busca ou com captcha:\n%s", trim(page, 800))
	}
	s.must("type", map[string]any{"ref": box, "text": "HiveMind IDE github"})
	shot("chrome-sidepanel-google-02-digitando.png")
	s.must("key", map[string]any{"key": "Enter"})
	time.Sleep(1500 * time.Millisecond)
	results := s.must("read_page", map[string]any{})
	shot("chrome-sidepanel-google-03-resultados.png")
	link, label := firstResult(results)
	if link == "" {
		t.Fatalf("sem resultado:\n%s", trim(results, 800))
	}
	t.Logf("clique %s %s", link, label)
	s.must("click", map[string]any{"ref": link})
	for i := 0; i < 8; i++ {
		time.Sleep(600 * time.Millisecond)
		landed := s.must("read_page", map[string]any{})
		if !strings.Contains(landed, "google.com/search") {
			break
		}
	}
	shot("chrome-sidepanel-google-04-destino.png")
}

func xdo(display string, args ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	_ = cmd.Run()
}

func openSidePanel(t *testing.T, display string) {
	t.Helper()
	xdo(display, "xdotool", "search", "--onlyvisible", "--name", "Chrome", "windowactivate")
	xdo(display, "xdotool", "key", "--clearmodifiers", "ctrl+shift+y")
	time.Sleep(500 * time.Millisecond)
}

func clickAt(display string, x, y int) {
	xdo(display, "xdotool", "mousemove", fmt.Sprint(x), fmt.Sprint(y), "click", "1")
}

func rightSideDark(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	dark := 0
	for y := b.Min.Y + b.Dy()/3; y < b.Min.Y+b.Dy()*2/3; y += 24 {
		r, g, bl, _ := img.At(b.Max.X-30, y).RGBA()
		if r>>8 < 45 && g>>8 < 45 && bl>>8 < 45 {
			dark++
		}
	}
	return dark >= 6
}
