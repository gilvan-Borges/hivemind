package resident

import (
	"context"
	"fmt"
	"html"
	"regexp"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/browserx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// lookHeadlessBrowser resolves Chrome or Edge. A test can replace it.
var lookHeadlessBrowser = browserx.FindStandaloneBrowser

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
	raw, err := browserx.DumpHeadlessDOM(ctx, bin, pageURL)
	if err != nil {
		return "", err
	}
	body := visiblePageText(raw)
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
	if action == "read" {
		type resPair struct {
			txt string
			err error
		}
		treeCh := make(chan resPair, 1)
		bodyCh := make(chan resPair, 1)
		go func() {
			t, e := browserx.CallTool(ctx, "read_page", callArgs)
			treeCh <- resPair{t, e}
		}()
		go func() {
			t, e := browserx.CallTool(ctx, "get_page_text", map[string]any{"limit": 2400})
			bodyCh <- resPair{t, e}
		}()
		tr := <-treeCh
		br := <-bodyCh
		if tr.err != nil {
			return "", tr.err
		}
		out := strings.TrimSpace(tr.txt)
		if br.err == nil {
			cleanBody := strings.TrimSpace(br.txt)
			if cleanBody != "" && cleanBody != "vazio" {
				out += "\n\n--- Texto visível da página ---\n" + cleanBody
			}
		}
		return strx.TruncateBytes(out, 10*1024, "\n…"), nil
	}
	text, err := browserx.CallTool(ctx, tool, callArgs)
	if err != nil {
		return "", err
	}
	if action == "navigate" {
		// Return interactive snapshot + visible text immediately on navigate so
		// the model does not waste a second full LLM turn calling action="read".
		type resPair struct {
			txt string
			err error
		}
		treeCh := make(chan resPair, 1)
		bodyCh := make(chan resPair, 1)
		go func() {
			t, e := browserx.CallTool(ctx, "read_page", map[string]any{"fresh": true})
			treeCh <- resPair{t, e}
		}()
		go func() {
			t, e := browserx.CallTool(ctx, "get_page_text", map[string]any{"limit": 2400})
			bodyCh <- resPair{t, e}
		}()
		tr := <-treeCh
		br := <-bodyCh
		if tr.err == nil && strings.TrimSpace(tr.txt) != "" {
			text = strings.TrimSpace(tr.txt)
		}
		if br.err == nil {
			cleanBody := strings.TrimSpace(br.txt)
			if cleanBody != "" && cleanBody != "vazio" {
				text = strings.TrimSpace(text) + "\n\n--- Texto visível da página ---\n" + cleanBody
			}
		}
	}
	return strx.TruncateBytes(text, 10*1024, "\n…"), nil
}
