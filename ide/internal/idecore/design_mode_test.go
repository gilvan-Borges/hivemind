package idecore

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeMCPCaller struct {
	enabled []string
	calls   []string
	pages   string
	elem    string
}

func (f *fakeMCPCaller) EnableForTask(_ context.Context, name string) (string, error) {
	f.enabled = append(f.enabled, name)
	return "ok", nil
}

func (f *fakeMCPCaller) Call(_ context.Context, serverName, toolName string, args map[string]any) (string, error) {
	f.calls = append(f.calls, serverName+":"+toolName)
	switch toolName {
	case "list_pages":
		return f.pages, nil
	case "select_page":
		return "selected", nil
	case "evaluate_script":
		return f.elem, nil
	default:
		return "", nil
	}
}

func (f *fakeMCPCaller) Close() error { return nil }

func TestDesignModeService_ListPagesAndPickElement(t *testing.T) {
	bigHTML := "<button class='btn primary'>" + strings.Repeat("x", maxDesignModeHTMLBytes+500) + "</button>"
	elJSON, _ := json.Marshal(DesignModeElement{
		URL:       "http://localhost:5173/",
		Selector:  "button.btn.primary",
		Tag:       "button",
		Classes:   []string{"btn", "primary"},
		OuterHTML: bigHTML,
		ComputedStyles: map[string]string{
			"color":           "rgb(9, 9, 9)",
			"backgroundColor": "rgb(243, 242, 240)",
		},
		BoundingBox: map[string]float64{"width": 120, "height": 36},
	})

	fake := &fakeMCPCaller{
		pages: "0: HiveMind Preview http://localhost:5173/\n1: Docs https://example.com",
		elem:  string(elJSON),
	}
	svc := &DesignModeService{
		hosts: make(map[string]mcpCaller),
		newHostFn: func(context.Context, string) mcpCaller {
			return fake
		},
	}
	defer svc.Close()

	pages, err := svc.ListPages(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("ListPages failed: %v", err)
	}
	if len(pages) != 2 || pages[0].URL != "http://localhost:5173/" {
		t.Fatalf("unexpected pages: %#v", pages)
	}

	picked, err := svc.PickElement(context.Background(), t.TempDir(), 0, "button.btn.primary")
	if err != nil {
		t.Fatalf("PickElement failed: %v", err)
	}
	if picked.Selector != "button.btn.primary" {
		t.Fatalf("unexpected selector: %+v", picked)
	}
	if len(picked.OuterHTML) > maxDesignModeHTMLBytes+32 {
		t.Fatalf("expected outerHTML truncated, got %d bytes", len(picked.OuterHTML))
	}

	formatted := FormatElementForComposer(picked)
	if !strings.Contains(formatted, "[Contexto Design Mode (CDP)]") || !strings.Contains(formatted, "button.btn.primary") {
		t.Fatalf("unexpected formatted composer text:\n%s", formatted)
	}
}
