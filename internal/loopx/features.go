package loopx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
)

// Feature is one harness item. The model may only flip Passes, and only the host does.
type Feature struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Priority int      `json:"priority,omitempty"`
	Passes   bool     `json:"passes"`
	Steps    []string `json:"steps,omitempty"`
}

func featuresFile(workspace, id, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("features vazio")
	}
	if strings.Contains(rel, "..") || filepath.IsAbs(rel) || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("caminho de features inválido")
	}
	dir, err := Dir(workspace, id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, filepath.FromSlash(rel)), nil
}

// LoadFeatures reads the harness list. A missing file is an empty list and exists=false.
func LoadFeatures(workspace, id, rel string) ([]Feature, bool, error) {
	path, err := featuresFile(workspace, id, rel)
	if err != nil {
		return nil, false, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	items, err := decodeFeatures(raw)
	if err != nil {
		return nil, true, err
	}
	return items, true, nil
}

// SaveFeatures writes the list. Every new item from the model is stored with passes false
// unless the caller already decided a pass.
func SaveFeatures(workspace, id, rel string, items []Feature) error {
	path, err := featuresFile(workspace, id, rel)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return atomicx.WriteFile(path, raw, 0o644)
}

// ParseFeatureBlock extracts a JSON array from a model reply and forces passes false.
func ParseFeatureBlock(text string) ([]Feature, error) {
	raw := extractJSONArray(text)
	if raw == nil {
		return nil, fmt.Errorf("a resposta não trouxe a lista de features")
	}
	items, err := decodeFeatures(raw)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("lista de features vazia")
	}
	for i := range items {
		items[i].ID = strings.TrimSpace(items[i].ID)
		items[i].Title = strings.TrimSpace(items[i].Title)
		if items[i].ID == "" {
			return nil, fmt.Errorf("feature sem id")
		}
		items[i].Passes = false
	}
	return items, nil
}

// NextFeature returns the pending item with the smallest priority.
func NextFeature(items []Feature) (Feature, bool) {
	best := -1
	for i := range items {
		if items[i].Passes {
			continue
		}
		if best < 0 || lessFeature(items[i], items[best], i, best) {
			best = i
		}
	}
	if best < 0 {
		return Feature{}, false
	}
	return items[best], true
}

func lessFeature(a, b Feature, ai, bi int) bool {
	ap, bp := a.Priority, b.Priority
	if ap == 0 {
		ap = ai + 1
	}
	if bp == 0 {
		bp = bi + 1
	}
	if ap != bp {
		return ap < bp
	}
	return ai < bi
}

// AllPass reports whether every item is done. An empty list is not done.
func AllPass(items []Feature) bool {
	if len(items) == 0 {
		return false
	}
	for _, it := range items {
		if !it.Passes {
			return false
		}
	}
	return true
}

// MarkPass sets passes on one id and leaves the rest untouched.
func MarkPass(items []Feature, id string, pass bool) []Feature {
	out := append([]Feature(nil), items...)
	for i := range out {
		if out[i].ID == id {
			out[i].Passes = pass
		}
	}
	return out
}

// FeaturesTampered reports a features.json edit that is not only the passes field.
func FeaturesTampered(before, after []byte) bool {
	if bytesEqual(before, after) {
		return false
	}
	return !PassesOnlyChange(before, after)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// PassesOnlyChange is true when the two documents are the same list and only passes differs.
func PassesOnlyChange(before, after []byte) bool {
	if len(before) == 0 || len(after) == 0 {
		return false
	}
	b, err1 := decodeFeatures(before)
	a, err2 := decodeFeatures(after)
	if err1 != nil || err2 != nil || len(a) != len(b) {
		return false
	}
	for i := range b {
		if b[i].ID != a[i].ID || b[i].Title != a[i].Title || b[i].Priority != a[i].Priority || !sameStrings(b[i].Steps, a[i].Steps) {
			return false
		}
	}
	return true
}

func decodeFeatures(raw []byte) ([]Feature, error) {
	var items []Feature
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("features.json inválido: %w", err)
	}
	return items, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func extractJSONArray(text string) []byte {
	start := strings.IndexByte(text, '[')
	end := strings.LastIndexByte(text, ']')
	if start < 0 || end <= start {
		return nil
	}
	return []byte(text[start : end+1])
}

// ReadFeatureBytes returns the raw file. Missing is nil, nil.
func ReadFeatureBytes(workspace, id, rel string) ([]byte, error) {
	if strings.TrimSpace(rel) == "" {
		return nil, nil
	}
	path, err := featuresFile(workspace, id, rel)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return raw, nil
}
