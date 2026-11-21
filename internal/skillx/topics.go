package skillx

import (
	"embed"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/version"
)

//go:embed topics/*.md
var builtinTopicsFS embed.FS

// BuiltinTopic represents one version-pinned compact guide embedded in the binary.
type BuiltinTopic struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Body    string `json:"body"`
}

func parseTopicMarkdown(name, raw string) BuiltinTopic {
	clean := strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n")
	title := name
	summary := ""
	for _, line := range strings.Split(clean, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "##") {
			continue
		}
		if strings.HasPrefix(trim, "# ") && title == name {
			title = strings.TrimSpace(strings.TrimPrefix(trim, "# "))
			continue
		}
		if !strings.HasPrefix(trim, "#") {
			summary = strings.TrimPrefix(trim, "- ")
			break
		}
	}
	return BuiltinTopic{
		Name:    name,
		Title:   title,
		Summary: summary,
		Version: version.Version,
		Commit:  version.Commit,
		Body:    clean + "\n",
	}
}

// ListBuiltinTopics returns all embedded skill topics sorted by name.
func ListBuiltinTopics() []BuiltinTopic {
	entries, err := builtinTopicsFS.ReadDir("topics")
	if err != nil {
		return nil
	}
	var out []BuiltinTopic
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := builtinTopicsFS.ReadFile("topics/" + e.Name())
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(e.Name()), ".md")
		out = append(out, parseTopicMarkdown(name, string(raw)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GetBuiltinTopic looks up an embedded skill topic case-insensitively (optional .md suffix).
func GetBuiltinTopic(name string) (BuiltinTopic, bool) {
	key := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), ".md"))
	if key == "" {
		return BuiltinTopic{}, false
	}
	for _, t := range ListBuiltinTopics() {
		if strings.ToLower(t.Name) == key {
			return t, true
		}
	}
	return BuiltinTopic{}, false
}
