package idecore

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

const (
	designModeMCPServer    = "chrome-devtools"
	maxDesignModeHTMLBytes = 4096
)

// DesignModePage represents one open browser page discovered via Chrome DevTools MCP.
type DesignModePage struct {
	Index int    `json:"index"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url"`
}

// DesignModeElement captures inspected DOM structure and computed styles for the composer.
type DesignModeElement struct {
	URL            string             `json:"url,omitempty"`
	Selector       string             `json:"selector"`
	Tag            string             `json:"tag,omitempty"`
	ID             string             `json:"id,omitempty"`
	Classes        []string           `json:"classes,omitempty"`
	TextSnippet    string             `json:"textSnippet,omitempty"`
	OuterHTML      string             `json:"outerHtml,omitempty"`
	ComputedStyles map[string]string  `json:"computedStyles,omitempty"`
	BoundingBox    map[string]float64 `json:"boundingBox,omitempty"`
}

// mcpCaller abstracts mcpx.Host so DesignModeService is deterministic in unit tests.
type mcpCaller interface {
	EnableForTask(ctx context.Context, name string) (string, error)
	Call(ctx context.Context, serverName, toolName string, args map[string]any) (string, error)
	Close() error
}

// DesignModeService bridges the IDE composer with the user's real Chrome session
// via the built-in Chrome DevTools MCP server (no browser embedded per worktree).
type DesignModeService struct {
	mu        sync.Mutex
	hosts     map[string]mcpCaller
	newHostFn func(ctx context.Context, workspace string) mcpCaller
}

// NewDesignModeService constructs a lazy DesignModeService.
func NewDesignModeService() *DesignModeService {
	return &DesignModeService{
		hosts: make(map[string]mcpCaller),
		newHostFn: func(ctx context.Context, workspace string) mcpCaller {
			return mcpx.Start(ctx, workspace)
		},
	}
}

// CloseWorkspace closes and removes any lazy CDP MCP host opened for workspace.
func (s *DesignModeService) CloseWorkspace(workspace string) {
	if s == nil {
		return
	}
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return
	}
	abs, err := filepath.Abs(ws)
	if err != nil {
		abs = ws
	}
	cleanTarget := filepath.Clean(abs)
	s.mu.Lock()
	var closing []mcpCaller
	for key, h := range s.hosts {
		kAbs, kErr := filepath.Abs(strings.TrimSpace(key))
		if kErr != nil {
			kAbs = strings.TrimSpace(key)
		}
		if strings.EqualFold(filepath.Clean(kAbs), cleanTarget) || strings.EqualFold(strings.TrimSpace(key), ws) {
			if h != nil {
				closing = append(closing, h)
			}
			delete(s.hosts, key)
		}
	}
	s.mu.Unlock()
	for _, h := range closing {
		_ = h.Close()
	}
}

// Close shuts down any lazily started MCP hosts.
func (s *DesignModeService) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	hosts := s.hosts
	s.hosts = make(map[string]mcpCaller)
	s.mu.Unlock()
	for _, h := range hosts {
		if h != nil {
			_ = h.Close()
		}
	}
}

func (s *DesignModeService) ensureCDP(ctx context.Context, workspace string) (mcpCaller, error) {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		ws = "."
	}
	s.mu.Lock()
	if s.hosts == nil {
		s.hosts = make(map[string]mcpCaller)
	}
	h, ok := s.hosts[ws]
	if !ok || h == nil {
		fn := s.newHostFn
		if fn == nil {
			fn = func(c context.Context, dir string) mcpCaller { return mcpx.Start(c, dir) }
		}
		h = fn(ctx, ws)
		s.hosts[ws] = h
	}
	s.mu.Unlock()

	if _, err := h.EnableForTask(ctx, designModeMCPServer); err != nil {
		return nil, fmt.Errorf("conectar ao Chrome DevTools MCP: %w", err)
	}
	return h, nil
}

// ListPages queries open browser tabs via Chrome DevTools MCP (`list_pages`).
func (s *DesignModeService) ListPages(ctx context.Context, workspace string) ([]DesignModePage, error) {
	h, err := s.ensureCDP(ctx, workspace)
	if err != nil {
		return nil, err
	}
	raw, err := h.Call(ctx, designModeMCPServer, "list_pages", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("listar abas via CDP: %w", err)
	}
	return parseCDPListPages(raw), nil
}

// PickElement attaches to pageIdx (when >= 0) and inspects either selector or,
// when selector == "", activates an interactive Havrus Gold click-to-pick overlay.
func (s *DesignModeService) PickElement(ctx context.Context, workspace string, pageIdx int, selector string) (DesignModeElement, error) {
	h, err := s.ensureCDP(ctx, workspace)
	if err != nil {
		return DesignModeElement{}, err
	}
	if pageIdx >= 0 {
		if _, selErr := h.Call(ctx, designModeMCPServer, "select_page", map[string]any{"pageIdx": pageIdx}); selErr != nil {
			return DesignModeElement{}, fmt.Errorf("selecionar página %d no CDP: %w", pageIdx, selErr)
		}
	}

	script := buildCDPPickerScript(selector)
	raw, err := h.Call(ctx, designModeMCPServer, "evaluate_script", map[string]any{
		"function": script,
	})
	if err != nil {
		return DesignModeElement{}, fmt.Errorf("inspecionar elemento no Chrome: %w", err)
	}
	el, parseErr := parseCDPElementResult(raw)
	if parseErr != nil {
		return DesignModeElement{}, parseErr
	}
	if el.OuterHTML != "" {
		el.OuterHTML = strx.TruncateBytes(el.OuterHTML, maxDesignModeHTMLBytes, "\n<!-- truncado -->")
	}
	return el, nil
}

// FormatElementForComposer formats inspected DOM & computed styles as concise prompt context.
func FormatElementForComposer(el DesignModeElement) string {
	var b strings.Builder
	b.WriteString("[Contexto Design Mode (CDP)]\n")
	if el.URL != "" {
		fmt.Fprintf(&b, "URL: %s\n", el.URL)
	}
	fmt.Fprintf(&b, "Seletor: %s\n", el.Selector)
	if el.Tag != "" {
		fmt.Fprintf(&b, "Tag: <%s>", el.Tag)
		if el.ID != "" {
			fmt.Fprintf(&b, " #%s", el.ID)
		}
		if len(el.Classes) > 0 {
			fmt.Fprintf(&b, " .%s", strings.Join(el.Classes, "."))
		}
		b.WriteByte('\n')
	}
	if len(el.ComputedStyles) > 0 {
		b.WriteString("Estilos computados:\n")
		for _, k := range []string{"display", "position", "color", "backgroundColor", "fontFamily", "fontSize", "padding", "margin", "border", "borderRadius", "gap"} {
			if v := strings.TrimSpace(el.ComputedStyles[k]); v != "" {
				fmt.Fprintf(&b, "- %s: %s\n", k, v)
			}
		}
	}
	if len(el.BoundingBox) > 0 {
		fmt.Fprintf(&b, "Dimensões: %.0f×%.0f px\n", el.BoundingBox["width"], el.BoundingBox["height"])
	}
	if el.OuterHTML != "" {
		fmt.Fprintf(&b, "HTML:\n```html\n%s\n```\n", el.OuterHTML)
	}
	return strings.TrimSpace(b.String())
}

func parseCDPListPages(raw string) []DesignModePage {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var asArr []DesignModePage
	if strings.HasPrefix(trimmed, "[") && json.Unmarshal([]byte(trimmed), &asArr) == nil {
		return asArr
	}
	var wrapper struct {
		Pages []DesignModePage `json:"pages"`
	}
	if strings.HasPrefix(trimmed, "{") && json.Unmarshal([]byte(trimmed), &wrapper) == nil && len(wrapper.Pages) > 0 {
		return wrapper.Pages
	}
	var pages []DesignModePage
	for _, line := range strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := len(pages)
		var url, title string
		if colon := strings.IndexByte(line, ':'); colon > 0 && colon <= 4 {
			var parsedIdx int
			if _, err := fmt.Sscanf(line[:colon], "%d", &parsedIdx); err == nil {
				idx = parsedIdx
				line = strings.TrimSpace(line[colon+1:])
			}
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") || strings.HasPrefix(field, "about:") {
				url = strings.Trim(field, "()[]\"',")
				break
			}
		}
		title = strings.TrimSpace(strings.ReplaceAll(line, url, ""))
		title = strings.Trim(title, " -·()[]")
		if url != "" || title != "" {
			pages = append(pages, DesignModePage{Index: idx, Title: title, URL: url})
		}
	}
	return pages
}

func parseCDPElementResult(raw string) (DesignModeElement, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return DesignModeElement{}, fmt.Errorf("nenhum elemento selecionado no Chrome")
	}
	var el DesignModeElement
	if err := json.Unmarshal([]byte(trimmed), &el); err == nil && el.Selector != "" {
		return el, nil
	}
	// chrome-devtools-mcp evaluate_script may wrap JSON string or markdown block.
	if start := strings.IndexByte(trimmed, '{'); start >= 0 {
		if end := strings.LastIndexByte(trimmed, '}'); end > start {
			if err := json.Unmarshal([]byte(trimmed[start:end+1]), &el); err == nil && el.Selector != "" {
				return el, nil
			}
		}
	}
	var unquoted string
	if err := json.Unmarshal([]byte(trimmed), &unquoted); err == nil {
		if err2 := json.Unmarshal([]byte(unquoted), &el); err2 == nil && el.Selector != "" {
			return el, nil
		}
	}
	return DesignModeElement{}, fmt.Errorf("resposta CDP não contém seletor de elemento válido: %s", strx.TruncateBytes(trimmed, 160, "…"))
}

func buildCDPPickerScript(selector string) string {
	quotedSel, _ := json.Marshal(strings.TrimSpace(selector))
	return fmt.Sprintf(`async () => {
  const fixedSel = %s;
  const computeSelector = (el) => {
    if (!el || el.nodeType !== 1) return '';
    if (el.id) return '#' + CSS.escape(el.id);
    const parts = [];
    let cur = el;
    while (cur && cur.nodeType === 1 && parts.length < 5) {
      let seg = cur.tagName.toLowerCase();
      if (cur.classList && cur.classList.length) {
        seg += '.' + Array.from(cur.classList).slice(0, 2).map((c) => CSS.escape(c)).join('.');
      }
      parts.unshift(seg);
      cur = cur.parentElement;
    }
    return parts.join(' > ');
  };
  const extract = (el) => {
    if (!el) return null;
    const cs = window.getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    const styles = {};
    for (const k of ['display','position','color','backgroundColor','fontFamily','fontSize','padding','margin','border','borderRadius','gap']) {
      styles[k] = cs[k] || '';
    }
    return {
      url: location.href,
      selector: computeSelector(el),
      tag: el.tagName.toLowerCase(),
      id: el.id || '',
      classes: Array.from(el.classList || []),
      textSnippet: (el.innerText || '').trim().slice(0, 240),
      outerHtml: (el.outerHTML || '').slice(0, 4096),
      computedStyles: styles,
      boundingBox: { x: rect.x, y: rect.y, width: rect.width, height: rect.height }
    };
  };
  if (fixedSel) {
    return extract(document.querySelector(fixedSel));
  }
  return await new Promise((resolve) => {
    let last = null;
    let prevOutline = '';
    const onMove = (e) => {
      if (last) last.style.outline = prevOutline;
      last = e.target;
      if (last && last.style) {
        prevOutline = last.style.outline;
        last.style.outline = '2px solid #C6A15B';
      }
    };
    const cleanup = () => {
      document.removeEventListener('mousemove', onMove, true);
      document.removeEventListener('click', onClick, true);
      if (last && last.style) last.style.outline = prevOutline;
    };
    const onClick = (e) => {
      e.preventDefault();
      e.stopPropagation();
      cleanup();
      resolve(extract(e.target));
    };
    document.addEventListener('mousemove', onMove, true);
    document.addEventListener('click', onClick, true);
  });
}`, string(quotedSel))
}
