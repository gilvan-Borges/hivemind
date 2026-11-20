package extsess

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

var idPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

var reservedSources = map[string]bool{
	"agy":         true,
	"claude-code": true,
	"codex":       true,
	"opencode":    true,
}

// Reject is a detector file that was skipped. Reason never includes the file body.
// Fingerprint is a hash of the bytes, used to record the rejection once per content.
type Reject struct {
	ID          string
	Reason      string
	Fingerprint string
}

// OnReject receives a skipped detector. It may be nil.
var OnReject func(Reject)

type detectorFile struct {
	SchemaVersion int      `json:"schemaVersion"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	ProcessNames  []string `json:"processNames"`
	ReadOnly      bool     `json:"readOnly"`
	Command       string   `json:"command,omitempty"`
	Actions       any      `json:"actions,omitempty"`
}

func detectorsDir() string {
	return filepath.Join(workspace.AppDataRoot(), "detectors")
}

func loadDetectorNames(dir string) (map[string]string, []Reject) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	names := map[string]string{}
	var rejected []Reject
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			rejected = append(rejected, Reject{ID: e.Name(), Reason: "arquivo ilegível"})
			continue
		}
		fp := fingerprint(raw)
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(raw, &doc); err != nil {
			rejected = append(rejected, Reject{ID: e.Name(), Reason: "json inválido", Fingerprint: fp})
			continue
		}
		if _, ok := doc["command"]; ok {
			rejected = append(rejected, Reject{ID: e.Name(), Reason: "comando não é permitido", Fingerprint: fp})
			continue
		}
		var parsed detectorFile
		if err := json.Unmarshal(raw, &parsed); err != nil {
			rejected = append(rejected, Reject{ID: e.Name(), Reason: "json inválido", Fingerprint: fp})
			continue
		}
		if parsed.SchemaVersion != 1 || !parsed.ReadOnly || !idPattern.MatchString(parsed.ID) || reservedSources[parsed.ID] || len(parsed.ProcessNames) == 0 {
			id := parsed.ID
			if id == "" {
				id = e.Name()
			}
			rejected = append(rejected, Reject{ID: id, Reason: "detector recusado", Fingerprint: fp})
			continue
		}
		for _, name := range parsed.ProcessNames {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
				continue
			}
			names[name] = parsed.ID
		}
	}
	return names, rejected
}

func fingerprint(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func mergedNames() map[string]string {
	names := make(map[string]string, len(builtinNames))
	for k, v := range builtinNames {
		names[k] = v
	}
	extra, rejected := loadDetectorNames(detectorsDir())
	for k, v := range extra {
		if _, reserved := builtinNames[k]; reserved {
			continue
		}
		names[k] = v
	}
	if OnReject != nil {
		for _, r := range rejected {
			OnReject(r)
		}
	}
	return names
}
