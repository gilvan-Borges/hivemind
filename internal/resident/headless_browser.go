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
	"github.com/gilvan-Borges/HiveMind/internal/procx"
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
	procx.ConfigureContext(cmd)
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
	action := strings.ToLower(strArg(args, "action"))
	if action == "" {
		action = "read"
	}
	_, listening, on, _, statusErr := browserx.Status()
	if statusErr != nil || !listening || !on {
		// Extension is not connected (Chrome closed, sidepanel idle, or extension
		// not installed). Automatically run against HiveMind's built-in background
		// Chrome/Edge CDP session so Agent Hive works 100% independently.
		res, err := browserx.CallStandalone(ctx, action, args)
		if err != nil {
			return "", err
		}
		return strx.TruncateBytes(res, 10*1024, "\n…"), nil
	}
	tool := ""
	callArgs := map[string]any{}
	switch action {
	case "read":
		tool = "read_page"
		callArgs["fresh"] = true
	case "text":
		tool = "get_page_text"
	case "find":
		tool = "find"
		if q := firstNonEmptyStr(strArg(args, "query"), strArg(args, "text")); q != "" {
			callArgs["query"] = q
		}
		if r := strArg(args, "role"); r != "" {
			callArgs["role"] = r
		}
	case "navigate":
		tool = "navigate"
		callArgs["url"] = strArg(args, "url")
	case "click":
		tool = "click"
		if ref := strArg(args, "ref"); ref != "" {
			callArgs["ref"] = ref
		}
		if name := firstNonEmptyStr(strArg(args, "name"), strArg(args, "text")); name != "" {
			callArgs["name"] = name
		}
	case "type":
		tool = "type"
		if ref := strArg(args, "ref"); ref != "" {
			callArgs["ref"] = ref
		}
		if name := strArg(args, "name"); name != "" {
			callArgs["name"] = name
		}
		callArgs["text"] = strArg(args, "text")
	case "key":
		tool = "key"
		callArgs["key"] = firstNonEmptyStr(strArg(args, "key"), strArg(args, "text"))
	case "scroll":
		tool = "scroll"
		if dir := strArg(args, "direction"); dir != "" {
			callArgs["direction"] = dir
		}
	case "wait":
		tool = "wait"
		if t := strArg(args, "text"); t != "" {
			callArgs["text"] = t
		}
	default:
		return "", fmt.Errorf("action deve ser read, text, find, navigate, click, type, key, scroll ou wait")
	}
	ctx = browserx.WithApprover(ctx, func(string) bool { return true })
	text, err := browserx.CallTool(ctx, tool, callArgs)
	if err != nil {
		return "", err
	}
	if action == "read" {
		if bodyTxt, tErr := browserx.CallTool(ctx, "get_page_text", map[string]any{"limit": 2400}); tErr == nil {
			cleanBody := strings.TrimSpace(bodyTxt)
			if cleanBody != "" && cleanBody != "vazio" {
				text = strings.TrimSpace(text) + "\n\n--- Texto visível da página ---\n" + cleanBody
			}
		}
	}
	return strx.TruncateBytes(text, 10*1024, "\n…"), nil
}
