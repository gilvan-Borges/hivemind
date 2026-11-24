package resident

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// lookHeadlessBrowser resolves Chrome or Edge. A test can replace it.
var lookHeadlessBrowser = findHeadlessBrowser

func findHeadlessBrowser() string {
	if p := strings.TrimSpace(os.Getenv("CHROME_BIN")); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	var candidates []string
	if runtime.GOOS == "windows" {
		pf := os.Getenv("PROGRAMFILES")
		pf86 := os.Getenv("PROGRAMFILES(X86)")
		local := os.Getenv("LOCALAPPDATA")
		candidates = append(candidates,
			filepath.Join(pf, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(pf86, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(pf, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(pf86, "Microsoft", "Edge", "Application", "msedge.exe"),
		)
	}
	candidates = append(candidates, "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "msedge")
	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		if filepath.IsAbs(c) {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c
			}
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

func headlessArgs(userData, pageURL string) []string {
	return []string{
		"--headless=new",
		"--disable-gpu",
		"--no-first-run",
		"--disable-sync",
		"--disable-extensions",
		"--user-data-dir=" + userData,
		"--dump-dom",
		pageURL,
	}
}

func dumpHeadlessDOM(ctx context.Context, bin, pageURL string) (string, error) {
	if strings.TrimSpace(bin) == "" {
		return "", fmt.Errorf("navegador headless não encontrado (Chrome ou Edge)")
	}
	userData, err := os.MkdirTemp("", "hivemind-headless-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(userData)

	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, headlessArgs(userData, pageURL)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("browser headless: %w", err)
		}
		return "", fmt.Errorf("browser headless: %w: %s", err, msg)
	}
	const maxBody = 64 * 1024
	body := stdout.String()
	if len(body) > maxBody {
		body = body[:maxBody] + "\n… [resposta truncada]"
	}
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("browser headless não devolveu DOM")
	}
	return body, nil
}

func (b *Broker) handleBrowserHeadless(ctx context.Context, args map[string]any) (string, error) {
	rawURL := strArg(args, "url")
	var pageURL string
	if b.cfg.FullAccess {
		var err error
		pageURL, err = validatePublicURL(rawURL)
		if err != nil {
			return "", err
		}
	} else {
		u, err := ValidateOutboundURL(rawURL, b.cfg.Policy.AllowedHosts)
		if err != nil {
			return "", err
		}
		pageURL = u.String()
	}
	bin := lookHeadlessBrowser()
	body, err := dumpHeadlessDOM(ctx, bin, pageURL)
	if err != nil {
		return "", err
	}
	return "headless " + pageURL + "\n" + body, nil
}
