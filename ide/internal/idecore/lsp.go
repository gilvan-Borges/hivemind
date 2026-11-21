package idecore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/procx"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// LSPStatus summarizes gopls availability and state for a workspace.
type LSPStatus struct {
	Available bool   `json:"available"`
	Running   bool   `json:"running"`
	Server    string `json:"server"`
	Binary    string `json:"binary,omitempty"`
	Root      string `json:"root,omitempty"`
	Error     string `json:"error,omitempty"`
}

// LSPDiagnosticsEvent is emitted whenever the LSP server publishes diagnostics for a file.
type LSPDiagnosticsEvent struct {
	Workspace   string        `json:"workspace"`
	RelPath     string        `json:"relPath"`
	Diagnostics []ProblemItem `json:"diagnostics"`
}

// LSPLocation represents a 1-based Go-to-Definition target.
type LSPLocation struct {
	RelPath string `json:"relPath"`
	AbsPath string `json:"absPath"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	EndLine int    `json:"endLine,omitempty"`
	EndCol  int    `json:"endCol,omitempty"`
}

// LSPHover holds formatted hover documentation and range (1-based).
type LSPHover struct {
	Contents string `json:"contents"`
	Line     int    `json:"line,omitempty"`
	Col      int    `json:"col,omitempty"`
	EndLine  int    `json:"endLine,omitempty"`
	EndCol   int    `json:"endCol,omitempty"`
}

// LSPManager manages lazy gopls LSP client sessions per Go module root.
type LSPManager struct {
	mu            sync.Mutex
	clients       map[string]*lspClient // keyed by normalized module root
	OnDiagnostics func(LSPDiagnosticsEvent)
}

// NewLSPManager creates an LSPManager with a diagnostics callback.
func NewLSPManager(onDiag func(LSPDiagnosticsEvent)) *LSPManager {
	return &LSPManager{
		clients:       make(map[string]*lspClient),
		OnDiagnostics: onDiag,
	}
}

func findGoplsBinary() string {
	if p, err := exec.LookPath("gopls"); err == nil && p != "" {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		exe := "gopls"
		if runtime.GOOS == "windows" {
			exe = "gopls.exe"
		}
		cand := filepath.Join(home, "go", "bin", exe)
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	if gp := strings.TrimSpace(os.Getenv("GOPATH")); gp != "" {
		exe := "gopls"
		if runtime.GOOS == "windows" {
			exe = "gopls.exe"
		}
		cand := filepath.Join(gp, "bin", exe)
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand
		}
	}
	return ""
}

func isGoFile(relPath string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(relPath)), ".go")
}

func findGoModuleRoot(workspaceDir, absFile string) string {
	absWS, err := filepath.Abs(strings.TrimSpace(workspaceDir))
	if err != nil {
		absWS = workspaceDir
	}
	if absFile != "" {
		cur := filepath.Dir(absFile)
		for {
			if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
				return cur
			}
			if strings.EqualFold(filepath.Clean(cur), filepath.Clean(absWS)) {
				break
			}
			parent := filepath.Dir(cur)
			if parent == cur || !strings.HasPrefix(strings.ToLower(parent), strings.ToLower(absWS)) {
				break
			}
			cur = parent
		}
	}
	if _, err := os.Stat(filepath.Join(absWS, "go.mod")); err == nil {
		return absWS
	}
	return absWS
}

// Status reports whether gopls is installed and running for workspace.
func (m *LSPManager) Status(workspaceDir string) LSPStatus {
	bin := findGoplsBinary()
	st := LSPStatus{
		Available: bin != "",
		Server:    "gopls",
		Binary:    bin,
	}
	if bin == "" {
		st.Error = "gopls não encontrado no PATH (instale com: go install golang.org/x/tools/gopls@latest)"
		return st
	}
	absWS, _ := filepath.Abs(strings.TrimSpace(workspaceDir))
	normWS := strings.ToLower(filepath.ToSlash(absWS))
	m.mu.Lock()
	for rootKey, cl := range m.clients {
		if cl != nil && cl.isAlive() && (rootKey == normWS || strings.HasPrefix(rootKey, normWS+"/")) {
			st.Running = true
			st.Root = cl.rootDir
			break
		}
	}
	m.mu.Unlock()
	return st
}

func (m *LSPManager) getOrStartClient(ctx context.Context, workspaceDir, absFile string) (*lspClient, error) {
	bin := findGoplsBinary()
	if bin == "" {
		return nil, fmt.Errorf("gopls não encontrado no PATH")
	}
	modRoot := findGoModuleRoot(workspaceDir, absFile)
	key := strings.ToLower(filepath.ToSlash(modRoot))

	m.mu.Lock()
	if cl, ok := m.clients[key]; ok && cl.isAlive() {
		m.mu.Unlock()
		return cl, nil
	}
	m.mu.Unlock()

	absWS, _ := filepath.Abs(strings.TrimSpace(workspaceDir))
	cl, err := startGoplsClient(ctx, bin, modRoot, absWS, m.OnDiagnostics)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if existing, ok := m.clients[key]; ok && existing.isAlive() {
		m.mu.Unlock()
		cl.Close()
		return existing, nil
	}
	m.clients[key] = cl
	m.mu.Unlock()
	return cl, nil
}

// DidOpen notifies gopls that a Go file was opened in the editor.
func (m *LSPManager) DidOpen(ctx context.Context, workspaceDir, relPath, text string) error {
	if !isGoFile(relPath) {
		return nil
	}
	absPath, err := workspace.Resolve(workspaceDir, relPath)
	if err != nil {
		return err
	}
	cl, err := m.getOrStartClient(ctx, workspaceDir, absPath)
	if err != nil {
		return err
	}
	return cl.didOpen(absPath, text)
}

// DidChange notifies gopls of updated unsaved buffer contents.
func (m *LSPManager) DidChange(ctx context.Context, workspaceDir, relPath, text string) error {
	if !isGoFile(relPath) {
		return nil
	}
	absPath, err := workspace.Resolve(workspaceDir, relPath)
	if err != nil {
		return err
	}
	cl, err := m.getOrStartClient(ctx, workspaceDir, absPath)
	if err != nil {
		return err
	}
	return cl.didChange(absPath, text)
}

// DidSave notifies gopls that a Go file was saved.
func (m *LSPManager) DidSave(ctx context.Context, workspaceDir, relPath, text string) error {
	if !isGoFile(relPath) {
		return nil
	}
	absPath, err := workspace.Resolve(workspaceDir, relPath)
	if err != nil {
		return err
	}
	cl, err := m.getOrStartClient(ctx, workspaceDir, absPath)
	if err != nil {
		return err
	}
	return cl.didSave(absPath, text)
}

// DidClose notifies gopls that a Go file tab was closed.
func (m *LSPManager) DidClose(workspaceDir, relPath string) error {
	if !isGoFile(relPath) {
		return nil
	}
	absPath, err := workspace.Resolve(workspaceDir, relPath)
	if err != nil {
		return nil
	}
	modRoot := findGoModuleRoot(workspaceDir, absPath)
	key := strings.ToLower(filepath.ToSlash(modRoot))
	m.mu.Lock()
	cl := m.clients[key]
	m.mu.Unlock()
	if cl == nil || !cl.isAlive() {
		return nil
	}
	return cl.didClose(absPath)
}

// Definition resolves Go-to-Definition targets for 1-based (line, col).
func (m *LSPManager) Definition(ctx context.Context, workspaceDir, relPath string, line, col int) ([]LSPLocation, error) {
	if !isGoFile(relPath) {
		return nil, nil
	}
	absPath, err := workspace.Resolve(workspaceDir, relPath)
	if err != nil {
		return nil, err
	}
	cl, err := m.getOrStartClient(ctx, workspaceDir, absPath)
	if err != nil {
		return nil, err
	}
	return cl.definition(ctx, absPath, line, col)
}

// Hover returns formatted symbol signature and documentation at 1-based (line, col).
func (m *LSPManager) Hover(ctx context.Context, workspaceDir, relPath string, line, col int) (LSPHover, error) {
	if !isGoFile(relPath) {
		return LSPHover{}, nil
	}
	absPath, err := workspace.Resolve(workspaceDir, relPath)
	if err != nil {
		return LSPHover{}, err
	}
	cl, err := m.getOrStartClient(ctx, workspaceDir, absPath)
	if err != nil {
		return LSPHover{}, err
	}
	return cl.hover(ctx, absPath, line, col)
}

// CloseWorkspace shuts down running gopls servers associated with workspaceDir.
func (m *LSPManager) CloseWorkspace(workspaceDir string) {
	if m == nil {
		return
	}
	wsTrim := strings.TrimSpace(workspaceDir)
	if wsTrim == "" {
		return
	}
	absWS, err := filepath.Abs(wsTrim)
	if err != nil {
		absWS = wsTrim
	}
	cleanWS := filepath.Clean(absWS)
	normWS := strings.ToLower(filepath.ToSlash(cleanWS))

	m.mu.Lock()
	var list []*lspClient
	for key, cl := range m.clients {
		if cl == nil {
			delete(m.clients, key)
			continue
		}
		clWS := cl.workspaceDir
		if clABS, aErr := filepath.Abs(strings.TrimSpace(clWS)); aErr == nil {
			clWS = clABS
		}
		if key == normWS || strings.HasPrefix(key, normWS+"/") || strings.EqualFold(filepath.Clean(clWS), cleanWS) {
			list = append(list, cl)
			delete(m.clients, key)
		}
	}
	m.mu.Unlock()
	for _, cl := range list {
		cl.Close()
	}
}

// CloseAll shuts down all running gopls servers cleanly.
func (m *LSPManager) CloseAll() {
	m.mu.Lock()
	list := make([]*lspClient, 0, len(m.clients))
	for _, cl := range m.clients {
		list = append(list, cl)
	}
	m.clients = make(map[string]*lspClient)
	m.mu.Unlock()
	for _, cl := range list {
		if cl != nil {
			cl.Close()
		}
	}
}

type lspClient struct {
	rootDir      string
	workspaceDir string
	cmd          *exec.Cmd
	cancel       context.CancelFunc
	stdin        io.WriteCloser
	writeMu      sync.Mutex
	mu           sync.Mutex
	alive        bool
	nextID       atomic.Int64
	pending      map[int64]chan jsonRPCMessage
	docVersions  map[string]int
	onDiag       func(LSPDiagnosticsEvent)
}

type jsonRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func startGoplsClient(ctx context.Context, bin, modRoot, workspaceDir string, onDiag func(LSPDiagnosticsEvent)) (*lspClient, error) {
	procCtx, procCancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, bin, "serve")
	cmd.Dir = modRoot
	procx.ConfigureContext(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		procCancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		procCancel()
		return nil, err
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		procCancel()
		return nil, fmt.Errorf("falha ao iniciar gopls: %w", err)
	}

	cl := &lspClient{
		rootDir:      modRoot,
		workspaceDir: workspaceDir,
		cmd:          cmd,
		cancel:       procCancel,
		stdin:        stdin,
		alive:        true,
		pending:      make(map[int64]chan jsonRPCMessage),
		docVersions:  make(map[string]int),
		onDiag:       onDiag,
	}

	go cl.readLoop(stdout)

	initCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	initParams := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   pathToFileURI(modRoot),
		"rootPath":  modRoot,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"synchronization": map[string]any{
					"didSave": true,
				},
				"hover": map[string]any{
					"contentFormat": []string{"markdown", "plaintext"},
				},
				"definition": map[string]any{
					"linkSupport": false,
				},
				"publishDiagnostics": map[string]any{
					"relatedInformation": false,
				},
			},
		},
	}
	if _, err := cl.call(initCtx, "initialize", initParams); err != nil {
		cl.Close()
		return nil, fmt.Errorf("gopls initialize falhou: %w", err)
	}
	_ = cl.notify("initialized", map[string]any{})
	return cl, nil
}

func (c *lspClient) isAlive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.alive
}

func (c *lspClient) writeJSON(msg any) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := io.WriteString(c.stdin, header); err != nil {
		return err
	}
	_, err = c.stdin.Write(body)
	return err
}

func (c *lspClient) notify(method string, params any) error {
	return c.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *lspClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan jsonRPCMessage, 1)
	c.mu.Lock()
	if !c.alive {
		c.mu.Unlock()
		return nil, fmt.Errorf("gopls encerrado")
	}
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.writeJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return nil, err
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("gopls desconectado")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("LSP %s erro %d: %s", method, resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *lspClient) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 64*1024)
	defer func() {
		c.mu.Lock()
		c.alive = false
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if c.cmd != nil {
			_ = c.cmd.Wait()
		}
	}()

	for {
		contentLen := 0
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "content-length:") {
				val := strings.TrimSpace(line[len("content-length:"):])
				contentLen, _ = strconv.Atoi(val)
			}
		}
		if contentLen <= 0 || contentLen > 16*1024*1024 {
			continue
		}
		payload := make([]byte, contentLen)
		if _, err := io.ReadFull(br, payload); err != nil {
			return
		}

		var msg jsonRPCMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			continue
		}

		// Server-to-client request: reply immediately so gopls never stalls.
		if msg.Method != "" && len(msg.ID) > 0 {
			var res any = nil
			if msg.Method == "workspace/configuration" {
				res = []any{}
			}
			_ = c.writeJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"result":  res,
			})
			continue
		}

		// Response to our request.
		if msg.Method == "" && len(msg.ID) > 0 {
			var id int64
			if err := json.Unmarshal(msg.ID, &id); err == nil {
				c.mu.Lock()
				ch, ok := c.pending[id]
				c.mu.Unlock()
				if ok {
					select {
					case ch <- msg:
					default:
					}
				}
			}
			continue
		}

		// Notification.
		if msg.Method == "textDocument/publishDiagnostics" && c.onDiag != nil {
			c.handlePublishDiagnostics(msg.Params)
		}
	}
}

func (c *lspClient) handlePublishDiagnostics(raw json.RawMessage) {
	var params struct {
		URI         string `json:"uri"`
		Diagnostics []struct {
			Range struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			} `json:"range"`
			Severity int    `json:"severity"`
			Source   string `json:"source"`
			Message  string `json:"message"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(raw, &params); err != nil || params.URI == "" {
		return
	}
	absPath := fileURIToPath(params.URI)
	relPath := normalizeProblemPath(absPath, c.workspaceDir)
	items := make([]ProblemItem, 0, len(params.Diagnostics))
	for _, d := range params.Diagnostics {
		sev := "error"
		switch d.Severity {
		case 2:
			sev = "warning"
		case 3, 4:
			sev = "info"
		}
		src := strings.TrimSpace(d.Source)
		if src == "" {
			src = "gopls"
		}
		items = append(items, ProblemItem{
			Source:   src,
			Severity: sev,
			File:     relPath,
			Line:     d.Range.Start.Line + 1,
			Col:      d.Range.Start.Character + 1,
			EndLine:  d.Range.End.Line + 1,
			EndCol:   d.Range.End.Character + 1,
			Message:  strings.TrimSpace(d.Message),
		})
	}
	c.onDiag(LSPDiagnosticsEvent{
		Workspace:   c.workspaceDir,
		RelPath:     relPath,
		Diagnostics: items,
	})
}

func (c *lspClient) didOpen(absPath, text string) error {
	uri := pathToFileURI(absPath)
	c.mu.Lock()
	ver, opened := c.docVersions[uri]
	if opened {
		ver++
		c.docVersions[uri] = ver
		c.mu.Unlock()
		return c.notify("textDocument/didChange", map[string]any{
			"textDocument":   map[string]any{"uri": uri, "version": ver},
			"contentChanges": []map[string]any{{"text": text}},
		})
	}
	c.docVersions[uri] = 1
	c.mu.Unlock()
	return c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": "go",
			"version":    1,
			"text":       text,
		},
	})
}

func (c *lspClient) didChange(absPath, text string) error {
	uri := pathToFileURI(absPath)
	c.mu.Lock()
	ver, opened := c.docVersions[uri]
	if !opened {
		c.docVersions[uri] = 1
		c.mu.Unlock()
		return c.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": "go",
				"version":    1,
				"text":       text,
			},
		})
	}
	ver++
	c.docVersions[uri] = ver
	c.mu.Unlock()
	return c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": ver},
		"contentChanges": []map[string]any{{"text": text}},
	})
}

func (c *lspClient) didSave(absPath, text string) error {
	uri := pathToFileURI(absPath)
	return c.notify("textDocument/didSave", map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"text":         text,
	})
}

func (c *lspClient) didClose(absPath string) error {
	uri := pathToFileURI(absPath)
	c.mu.Lock()
	delete(c.docVersions, uri)
	c.mu.Unlock()
	return c.notify("textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": uri},
	})
}

func (c *lspClient) definition(ctx context.Context, absPath string, line, col int) ([]LSPLocation, error) {
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	raw, err := c.call(ctx, "textDocument/definition", map[string]any{
		"textDocument": map[string]any{"uri": pathToFileURI(absPath)},
		"position":     map[string]any{"line": line - 1, "character": col - 1},
	})
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return nil, err
	}

	type rawLoc struct {
		URI   string `json:"uri"`
		Range struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
			End struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"end"`
		} `json:"range"`
	}

	var list []rawLoc
	if err := json.Unmarshal(raw, &list); err != nil {
		var single rawLoc
		if err2 := json.Unmarshal(raw, &single); err2 == nil && single.URI != "" {
			list = []rawLoc{single}
		}
	}

	out := make([]LSPLocation, 0, len(list))
	for _, loc := range list {
		if loc.URI == "" {
			continue
		}
		targetAbs := fileURIToPath(loc.URI)
		rel := normalizeProblemPath(targetAbs, c.workspaceDir)
		out = append(out, LSPLocation{
			RelPath: rel,
			AbsPath: targetAbs,
			Line:    loc.Range.Start.Line + 1,
			Col:     loc.Range.Start.Character + 1,
			EndLine: loc.Range.End.Line + 1,
			EndCol:  loc.Range.End.Character + 1,
		})
	}
	return out, nil
}

func (c *lspClient) hover(ctx context.Context, absPath string, line, col int) (LSPHover, error) {
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	raw, err := c.call(ctx, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": pathToFileURI(absPath)},
		"position":     map[string]any{"line": line - 1, "character": col - 1},
	})
	if err != nil || len(raw) == 0 || string(raw) == "null" {
		return LSPHover{}, err
	}
	var payload struct {
		Contents json.RawMessage `json:"contents"`
		Range    *struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
			End struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"end"`
		} `json:"range"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return LSPHover{}, nil
	}
	text := extractHoverContents(payload.Contents)
	res := LSPHover{Contents: strings.TrimSpace(text)}
	if payload.Range != nil {
		res.Line = payload.Range.Start.Line + 1
		res.Col = payload.Range.Start.Character + 1
		res.EndLine = payload.Range.End.Line + 1
		res.EndCol = payload.Range.End.Character + 1
	}
	return res, nil
}

func extractHoverContents(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str
	}
	var markup struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &markup); err == nil && markup.Value != "" {
		return markup.Value
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		parts := make([]string, 0, len(arr))
		for _, item := range arr {
			if s := strings.TrimSpace(extractHoverContents(item)); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

func (c *lspClient) Close() {
	c.mu.Lock()
	if !c.alive {
		c.mu.Unlock()
		return
	}
	c.alive = false
	c.mu.Unlock()

	_ = c.notify("exit", nil)
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cancel != nil {
		c.cancel()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

// pathToFileURI converts an OS filesystem path into an RFC 8089 file:// URI compatible with gopls on Windows and Unix.
func pathToFileURI(absPath string) string {
	slashed := filepath.ToSlash(filepath.Clean(absPath))
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	segs := strings.Split(slashed, "/")
	for i, seg := range segs {
		if i == 1 && len(seg) == 2 && seg[1] == ':' {
			// Preserve Windows drive letter "C:" without escaping the colon.
			continue
		}
		segs[i] = url.PathEscape(seg)
	}
	return "file://" + strings.Join(segs, "/")
}

// fileURIToPath converts an LSP file:// URI back into a clean OS path.
func fileURIToPath(rawURI string) string {
	u, err := url.Parse(rawURI)
	if err != nil || !strings.EqualFold(u.Scheme, "file") {
		return rawURI
	}
	p := u.Path
	if unesc, err := url.PathUnescape(p); err == nil {
		p = unesc
	}
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.Clean(filepath.FromSlash(p))
}
