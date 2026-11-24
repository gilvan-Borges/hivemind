package resident

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/browserx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
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
	body := visiblePageText(stdout.String())
	if body == "" {
		return "", fmt.Errorf("browser headless não devolveu texto")
	}
	return body, nil
}

var (
	headlessSkipBlock = regexp.MustCompile(`(?is)<(script|style|noscript)\b[^>]*>.*?</(script|style|noscript)>`)
	headlessTag       = regexp.MustCompile(`(?s)<[^>]+>`)
	headlessSpace     = regexp.MustCompile(`[ \t]+`)
	headlessBreaks    = regexp.MustCompile(`\n{3,}`)
)

func visiblePageText(raw string) string {
	raw = headlessSkipBlock.ReplaceAllString(raw, " ")
	raw = headlessTag.ReplaceAllString(raw, "\n")
	raw = html.UnescapeString(raw)
	raw = headlessSpace.ReplaceAllString(raw, " ")
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return strx.TruncateBytes(headlessBreaks.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"), 6*1024, "\n…")
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

func (b *Broker) handleChromeTab(ctx context.Context, args map[string]any) (string, error) {
	_, listening, on, _, err := browserx.Status()
	if err != nil || !listening || !on {
		return "", fmt.Errorf("a extensão Agent Hive não está conectada no Chrome")
	}
	action := strings.ToLower(strArg(args, "action"))
	if action == "" {
		action = "read"
	}
	tool := ""
	callArgs := map[string]any{}
	switch action {
	case "read":
		tool = "read_page"
	case "navigate":
		tool = "navigate"
		callArgs["url"] = strArg(args, "url")
	case "click":
		tool = "click"
		callArgs["ref"] = strArg(args, "ref")
	case "type":
		tool = "type"
		callArgs["ref"] = strArg(args, "ref")
		callArgs["text"] = strArg(args, "text")
	case "key":
		tool = "key"
		callArgs["key"] = firstNonEmptyStr(strArg(args, "key"), strArg(args, "text"))
	default:
		return "", fmt.Errorf("action deve ser read, navigate, click, type ou key")
	}
	ctx = browserx.WithApprover(ctx, func(string) bool { return true })
	text, err := browserx.CallTool(ctx, tool, callArgs)
	if err != nil {
		return "", err
	}
	return strx.TruncateBytes(text, 8*1024, "\n…"), nil
}
