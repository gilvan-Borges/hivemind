package browserx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// lookStandaloneBrowser resolves Chrome or Edge for the built-in standalone
// CDP session. Tests may override it.
var lookStandaloneBrowser = findStandaloneBrowser

func findStandaloneBrowser() string {
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

type standaloneCDP struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	port    int
	conn    *websocket.Conn
	nextID  atomic.Int64
	closing bool
}

var defaultStandalone = &standaloneCDP{}

// CloseStandalone shuts down the managed background CDP browser if running.
func CloseStandalone() {
	defaultStandalone.mu.Lock()
	defer defaultStandalone.mu.Unlock()
	defaultStandalone.resetLocked()
}

// CallStandalone runs a chrome_tab action directly against HiveMind's managed
// background Chrome/Edge CDP instance, completely independent of the Chrome
// extension.
func CallStandalone(ctx context.Context, action string, args map[string]any) (string, error) {
	return defaultStandalone.execute(ctx, action, args)
}

func (s *standaloneCDP) execute(ctx context.Context, action string, args map[string]any) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureConnectedLocked(ctx); err != nil {
		return "", err
	}
	res, err := s.dispatchLocked(ctx, action, args)
	if err != nil && isBrokenConnErr(err) {
		// Reconnect once if the background browser exited or dropped the socket.
		s.resetLocked()
		if rErr := s.ensureConnectedLocked(ctx); rErr != nil {
			return "", err
		}
		res, err = s.dispatchLocked(ctx, action, args)
	}
	return res, err
}

func isBrokenConnErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "websocket") ||
		strings.Contains(low, "broken pipe") ||
		strings.Contains(low, "connection reset") ||
		strings.Contains(low, "closed")
}

func (s *standaloneCDP) resetLocked() {
	if s.conn != nil {
		_ = s.conn.Close(websocket.StatusNormalClosure, "")
		s.conn = nil
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	s.cmd = nil
	s.port = 0
}

func pickFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("endereço TCP inválido")
	}
	return addr.Port, nil
}

func standaloneProfileDir() string {
	base, err := os.UserConfigDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "HiveMind", "agent-browser-profile")
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

func (s *standaloneCDP) ensureConnectedLocked(ctx context.Context) error {
	if s.conn != nil {
		return nil
	}
	bin := lookStandaloneBrowser()
	if strings.TrimSpace(bin) == "" {
		return fmt.Errorf("nenhum navegador compatível (Chrome ou Edge) encontrado neste computador")
	}
	port, err := pickFreePort()
	if err != nil {
		return fmt.Errorf("não foi possível reservar porta local para o navegador: %w", err)
	}
	profileDir := standaloneProfileDir()
	cmdArgs := []string{
		"--headless=new",
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--remote-allow-origins=*",
		"--user-data-dir=" + profileDir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		"--disable-gpu",
		"--disable-background-networking",
		"--disable-blink-features=AutomationControlled",
		"--window-size=1366,900",
		"about:blank",
	}
	cmd := exec.Command(bin, cmdArgs...)
	procx.HideWindow(cmd)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("falha ao iniciar navegador em segundo plano: %w", err)
	}
	s.cmd = cmd
	s.port = port

	wsURL, err := waitForPageTarget(ctx, port, 10*time.Second)
	if err != nil {
		s.resetLocked()
		return err
	}
	dctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	conn, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	cancel()
	if err != nil {
		s.resetLocked()
		return fmt.Errorf("falha ao conectar no CDP local do navegador: %w", err)
	}
	conn.SetReadLimit(16 << 20)
	s.conn = conn

	// Enable domains and strip HeadlessChrome from UA so sites render normally.
	_, _ = s.cdpCallLocked(ctx, "Page.enable", nil)
	_, _ = s.cdpCallLocked(ctx, "Runtime.enable", nil)
	_, _ = s.cdpCallLocked(ctx, "DOM.enable", nil)
	_, _ = s.cdpCallLocked(ctx, "Network.enable", nil)
	if uaRes, uErr := s.evalStringLocked(ctx, `navigator.userAgent.replace(/HeadlessChrome\//g, "Chrome/")`); uErr == nil && uaRes != "" {
		_, _ = s.cdpCallLocked(ctx, "Network.setUserAgentOverride", map[string]any{
			"userAgent":      uaRes,
			"acceptLanguage": "pt-BR,pt;q=0.9,en-US;q=0.8,en;q=0.7",
		})
	}
	_, _ = s.cdpCallLocked(ctx, "Emulation.setFocusEmulationEnabled", map[string]any{"enabled": true})
	return nil
}

type cdpTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func waitForPageTarget(ctx context.Context, port int, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	listURL := fmt.Sprintf("http://127.0.0.1:%d/json/list", port)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		resp, err := client.Get(listURL)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			var targets []cdpTarget
			if json.Unmarshal(body, &targets) == nil {
				for _, t := range targets {
					if t.Type == "page" && t.WebSocketDebuggerURL != "" {
						return t.WebSocketDebuggerURL, nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("navegador em segundo plano não expôs aba CDP na porta %d", port)
}

type cdpResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (s *standaloneCDP) cdpCallLocked(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if s.conn == nil {
		return nil, io.EOF
	}
	id := s.nextID.Add(1)
	req := map[string]any{"id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.conn.Write(callCtx, websocket.MessageText, raw); err != nil {
		return nil, err
	}
	for {
		_, data, err := s.conn.Read(callCtx)
		if err != nil {
			return nil, err
		}
		var resp cdpResponse
		if json.Unmarshal(data, &resp) != nil {
			continue
		}
		if resp.ID != id {
			// Ignore async CDP events while waiting for our response.
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("CDP %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (s *standaloneCDP) evalValueLocked(ctx context.Context, expr string) (any, error) {
	raw, err := s.cdpCallLocked(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
		"timeout":       10000,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out.ExceptionDetails != nil {
		return nil, fmt.Errorf("%s", out.ExceptionDetails.Text)
	}
	return out.Result.Value, nil
}

func (s *standaloneCDP) evalStringLocked(ctx context.Context, expr string) (string, error) {
	val, err := s.evalValueLocked(ctx, expr)
	if err != nil {
		return "", err
	}
	if val == nil {
		return "", nil
	}
	if str, ok := val.(string); ok {
		return str, nil
	}
	b, _ := json.Marshal(val)
	return string(b), nil
}

func (s *standaloneCDP) waitReadyLocked(ctx context.Context, maxWait time.Duration) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		st, err := s.evalStringLocked(ctx, `document.readyState`)
		if err == nil && (st == "complete" || st == "interactive") {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(150 * time.Millisecond):
		}
	}
	// Short settle for dynamic SPAs.
	select {
	case <-ctx.Done():
	case <-time.After(450 * time.Millisecond):
	}
}

func (s *standaloneCDP) dispatchLocked(ctx context.Context, action string, args map[string]any) (string, error) {
	strVal := func(k string) string {
		if args == nil {
			return ""
		}
		v, ok := args[k]
		if !ok || v == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(v))
	}

	switch action {
	case "navigate":
		rawURL := strVal("url")
		if rawURL == "" {
			return "", fmt.Errorf("url obrigatória")
		}
		if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
			rawURL = "https://" + rawURL
		}
		u, err := url.Parse(rawURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", fmt.Errorf("url inválida: %s", rawURL)
		}
		if _, err := s.cdpCallLocked(ctx, "Page.navigate", map[string]any{"url": u.String()}); err != nil {
			return "", err
		}
		s.waitReadyLocked(ctx, 8*time.Second)
		return s.readSnapshotLocked(ctx, "", "")

	case "read":
		return s.readSnapshotLocked(ctx, "", "")

	case "text":
		txt, err := s.pageTextLocked(ctx, 6000)
		if err != nil {
			return "", err
		}
		if txt == "" {
			return "vazio", nil
		}
		return txt, nil

	case "find":
		q := strVal("query")
		if q == "" {
			q = strVal("text")
		}
		role := strVal("role")
		return s.readSnapshotLocked(ctx, q, role)

	case "click":
		ref := strVal("ref")
		name := strVal("name")
		if name == "" {
			name = strVal("text")
		}
		x, y, err := s.locateElementLocked(ctx, ref, name)
		if err != nil {
			return "", err
		}
		if err := s.clickPointLocked(ctx, x, y); err != nil {
			return "", err
		}
		s.waitReadyLocked(ctx, 2500*time.Millisecond)
		return s.pageHeaderLocked(ctx), nil

	case "type":
		ref := strVal("ref")
		name := strVal("name")
		text := strVal("text")
		if text == "" {
			return "", fmt.Errorf("texto vazio")
		}
		if ref != "" || name != "" {
			if x, y, err := s.locateElementLocked(ctx, ref, name); err == nil {
				_ = s.clickPointLocked(ctx, x, y)
			}
		}
		if _, err := s.cdpCallLocked(ctx, "Input.insertText", map[string]any{"text": text}); err != nil {
			return "", err
		}
		return s.pageHeaderLocked(ctx), nil

	case "key":
		key := strVal("key")
		if key == "" {
			key = strVal("text")
		}
		if key == "" {
			return "", fmt.Errorf("tecla vazia")
		}
		if err := s.pressKeyLocked(ctx, key); err != nil {
			return "", err
		}
		if key == "Enter" {
			s.waitReadyLocked(ctx, 2500*time.Millisecond)
		}
		return s.pageHeaderLocked(ctx), nil

	case "scroll":
		dir := strings.ToLower(strVal("direction"))
		dx, dy := 0.0, 500.0
		switch dir {
		case "up":
			dy = -500
		case "down", "":
			dy = 500
		case "left":
			dx, dy = -400, 0
		case "right":
			dx, dy = 400, 0
		}
		_, err := s.cdpCallLocked(ctx, "Input.dispatchMouseEvent", map[string]any{
			"type":   "mouseWheel",
			"x":      680,
			"y":      450,
			"deltaX": dx,
			"deltaY": dy,
		})
		if err != nil {
			return "", err
		}
		time.Sleep(120 * time.Millisecond)
		return s.pageHeaderLocked(ctx), nil

	case "wait":
		target := strVal("text")
		if target != "" {
			lowTarget := strings.ToLower(target)
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				txt, _ := s.pageTextLocked(ctx, 6000)
				if strings.Contains(strings.ToLower(txt), lowTarget) {
					return "ok", nil
				}
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(350 * time.Millisecond):
				}
			}
			return "não apareceu", nil
		}
		s.waitReadyLocked(ctx, 2500*time.Millisecond)
		return s.pageHeaderLocked(ctx), nil

	default:
		return "", fmt.Errorf("action deve ser read, text, find, navigate, click, type, key, scroll ou wait")
	}
}

func (s *standaloneCDP) pageHeaderLocked(ctx context.Context) string {
	val, err := s.evalStringLocked(ctx, `(function(){ return (document.title || "") + " · " + (location.hostname || location.href); })()`)
	if err != nil || strings.TrimSpace(val) == "" {
		return "ok (navegador integrado)"
	}
	return strings.TrimSpace(val)
}

func (s *standaloneCDP) pageTextLocked(ctx context.Context, limit int) (string, error) {
	expr := fmt.Sprintf(`(function(){
		const raw = (document.body && document.body.innerText) ? document.body.innerText : "";
		return raw.replace(/\r/g, "").replace(/[ \t]+/g, " ").replace(/\n{3,}/g, "\n\n").trim().slice(0, %d);
	})()`, limit)
	return s.evalStringLocked(ctx, expr)
}

func (s *standaloneCDP) readSnapshotLocked(ctx context.Context, query, roleFilter string) (string, error) {
	qJSON, _ := json.Marshal(strings.ToLower(strings.TrimSpace(query)))
	rJSON, _ := json.Marshal(strings.ToLower(strings.TrimSpace(roleFilter)))
	expr := fmt.Sprintf(`(function(query, roleFilter){
		const refs = Object.create(null);
		window.__hmRefs = refs;
		const lines = [];
		const seen = new Set();
		const roles = new Set(["button","link","textbox","searchbox","combobox","tab","menuitem","checkbox","radio","option","switch"]);
		let idx = 0;

		function inferRole(el) {
			const r = (el.getAttribute("role") || "").toLowerCase();
			if (roles.has(r)) return r;
			const tag = el.tagName.toLowerCase();
			if (tag === "a" && el.hasAttribute("href")) return "link";
			if (tag === "button") return "button";
			if (tag === "textarea") return "textbox";
			if (tag === "select") return "combobox";
			if (tag === "input") {
				const t = (el.getAttribute("type") || "text").toLowerCase();
				if (t === "submit" || t === "button" || t === "reset") return "button";
				if (t === "checkbox") return "checkbox";
				if (t === "radio") return "radio";
				if (t === "search") return "searchbox";
				return "textbox";
			}
			if (el.getAttribute("contenteditable") === "true") return "textbox";
			if (el.hasAttribute("tabindex") || el.hasAttribute("onclick") || el.hasAttribute("jsaction")) return "button";
			try {
				if (getComputedStyle(el).cursor === "pointer") return "button";
			} catch (e) {}
			return "";
		}

		function labelOf(el) {
			const parts = [
				el.getAttribute("aria-label"),
				el.getAttribute("title"),
				el.getAttribute("placeholder"),
				el.getAttribute("alt"),
				typeof el.innerText === "string" ? el.innerText : "",
				el.value && el.type !== "password" ? String(el.value) : ""
			];
			for (const p of parts) {
				const s = (p || "").replace(/\s+/g, " ").trim();
				if (s) return s.slice(0, 96);
			}
			return "";
		}

		function visit(root) {
			let all;
			try { all = root.querySelectorAll("*"); } catch (e) { return; }
			for (const el of all) {
				if (el.shadowRoot) visit(el.shadowRoot);
				if (lines.length >= 120) continue;
				const role = inferRole(el);
				if (!role) continue;
				if (roleFilter && role !== roleFilter) continue;
				const rect = el.getBoundingClientRect();
				if (rect.width < 4 || rect.height < 4) continue;
				if (rect.bottom < -200 || rect.top > window.innerHeight + 600) continue;
				const name = labelOf(el);
				if (!name && role !== "textbox" && role !== "searchbox" && role !== "combobox") continue;
				const href = (el.tagName.toLowerCase() === "a" ? (el.getAttribute("href") || "") : "").slice(0, 120);
				if (query) {
					const hay = (role + " " + name + " " + href).toLowerCase();
					if (!hay.includes(query)) continue;
				}
				const sig = role + "|" + name + "|" + Math.round(rect.left / 10) + "|" + Math.round(rect.top / 10);
				if (seen.has(sig)) continue;
				seen.add(sig);
				idx += 1;
				const ref = "e" + idx;
				refs[ref] = el;
				let line = "[" + ref + "] " + role;
				if (name) line += " " + JSON.stringify(name);
				if (href && (href.startsWith("http://") || href.startsWith("https://") || href.startsWith("/"))) {
					line += " (" + href + ")";
				}
				lines.push(line);
			}
		}
		visit(document);
		const head = ((document.title || "").trim() + " · " + (location.hostname || location.href)).trim();
		return head + (lines.length ? "\n" + lines.join("\n") : "\n(nenhum elemento interativo encontrado)");
	})(%s, %s)`, string(qJSON), string(rJSON))

	snap, err := s.evalStringLocked(ctx, expr)
	if err != nil {
		return "", err
	}
	if query == "" && roleFilter == "" {
		if bodyTxt, tErr := s.pageTextLocked(ctx, 2600); tErr == nil && strings.TrimSpace(bodyTxt) != "" {
			snap = strings.TrimSpace(snap) + "\n\n--- Texto visível da página ---\n" + strings.TrimSpace(bodyTxt)
		}
	}
	return strx.TruncateBytes(snap, 10*1024, "\n…"), nil
}

func (s *standaloneCDP) locateElementLocked(ctx context.Context, ref, name string) (float64, float64, error) {
	refJSON, _ := json.Marshal(strings.TrimSpace(ref))
	nameJSON, _ := json.Marshal(strings.ToLower(strings.TrimSpace(name)))
	expr := fmt.Sprintf(`(function(ref, query){
		let el = null;
		if (ref && window.__hmRefs && window.__hmRefs[ref]) {
			el = window.__hmRefs[ref];
			if (!el.isConnected) el = null;
		}
		if (!el && query) {
			let best = null;
			let bestScore = Infinity;
			function walk(root) {
				let all;
				try { all = root.querySelectorAll("*"); } catch (e) { return; }
				for (const node of all) {
					if (node.shadowRoot) walk(node.shadowRoot);
					const r = node.getBoundingClientRect();
					if (r.width < 4 || r.height < 4) continue;
					const aria = (node.getAttribute("aria-label") || "").trim().toLowerCase();
					const title = (node.getAttribute("title") || "").trim().toLowerCase();
					const ph = (node.getAttribute("placeholder") || "").trim().toLowerCase();
					const txt = (typeof node.innerText === "string" ? node.innerText : "").replace(/\s+/g, " ").trim().toLowerCase();
					if (!(aria.includes(query) || title.includes(query) || ph.includes(query) || (txt && txt.length <= 160 && txt.includes(query)))) continue;
					const tag = node.tagName.toLowerCase();
					const interactive = tag === "button" || tag === "a" || tag === "input" || tag === "textarea" || tag === "select" || node.hasAttribute("role") || node.hasAttribute("tabindex");
					const exact = (aria === query || txt === query || ph === query) ? 0 : 50;
					const score = exact + (interactive ? 0 : 200) + Math.min(80, (r.width * r.height) / 1000) + (txt ? txt.length : 40);
					if (score < bestScore) {
						bestScore = score;
						best = node;
					}
				}
			}
			walk(document);
			el = best;
		}
		if (!el) return null;
		try { el.scrollIntoView({ block: "center", inline: "center", behavior: "instant" }); } catch (e) {}
		try { el.focus(); } catch (e) {}
		const rect = el.getBoundingClientRect();
		return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 };
	})(%s, %s)`, string(refJSON), string(nameJSON))

	val, err := s.evalValueLocked(ctx, expr)
	if err != nil {
		return 0, 0, err
	}
	m, ok := val.(map[string]any)
	if !ok || m == nil {
		target := ref
		if target == "" {
			target = name
		}
		return 0, 0, fmt.Errorf("elemento %q não encontrado visível na página", target)
	}
	x, _ := m["x"].(float64)
	y, _ := m["y"].(float64)
	return x, y, nil
}

func (s *standaloneCDP) clickPointLocked(ctx context.Context, x, y float64) error {
	for _, ev := range []map[string]any{
		{"type": "mouseMoved", "x": x, "y": y},
		{"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1},
		{"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1},
	} {
		if _, err := s.cdpCallLocked(ctx, "Input.dispatchMouseEvent", ev); err != nil {
			return err
		}
	}
	return nil
}

var standaloneKeyCodes = map[string]int{
	"Enter":      13,
	"Tab":        9,
	"Escape":     27,
	"Backspace":  8,
	"Delete":     46,
	"ArrowLeft":  37,
	"ArrowUp":    38,
	"ArrowRight": 39,
	"ArrowDown":  40,
	"PageUp":     33,
	"PageDown":   34,
	"Home":       36,
	"End":        35,
	"Space":      32,
	" ":          32,
}

func (s *standaloneCDP) pressKeyLocked(ctx context.Context, key string) error {
	named := len(key) > 1
	code := key
	if !named {
		code = "Key" + strings.ToUpper(key)
	} else if key == "Space" {
		key = " "
		code = "Space"
	}
	vk, ok := standaloneKeyCodes[key]
	if !ok && len(key) == 1 {
		vk = int(strings.ToUpper(key)[0])
	}
	text := ""
	if !named {
		text = key
	}
	if _, err := s.cdpCallLocked(ctx, "Input.dispatchKeyEvent", map[string]any{
		"type":                  "keyDown",
		"key":                   key,
		"code":                  code,
		"text":                  text,
		"windowsVirtualKeyCode": vk,
	}); err != nil {
		return err
	}
	_, err := s.cdpCallLocked(ctx, "Input.dispatchKeyEvent", map[string]any{
		"type":                  "keyUp",
		"key":                   key,
		"code":                  code,
		"windowsVirtualKeyCode": vk,
	})
	return err
}
