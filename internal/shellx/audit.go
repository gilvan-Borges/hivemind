package shellx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// shellAuditPathFn is the resident audit.jsonl. Tests replace it.
// The directory matches resident.DefaultBaseDir; shellx cannot import resident
// because resident already imports the squad tools, which import shellx.
var shellAuditPathFn = defaultShellAuditPath

var shellAuditMu sync.Mutex

type shellAuditLine struct {
	Timestamp   string `json:"ts"`
	Phase       string `json:"phase"`
	AccountID   string `json:"accountId"`
	Origin      string `json:"origin"`
	Tool        string `json:"tool"`
	Tier        string `json:"tier"`
	Decision    string `json:"decision"`
	Sandbox     string `json:"sandbox"`
	TargetClass string `json:"targetClass"`
	TargetHash  string `json:"targetHash"`
	Reason      string `json:"reason"`
}

func defaultShellAuditPath() (string, error) {
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
			base = cfg
		} else {
			base = os.TempDir()
		}
	}
	return filepath.Join(base, "HiveMind", "resident", "audit.jsonl"), nil
}

// AuditFullAccessShell appends one redacted decision line before a full_access
// shell line with metacharacters runs. The command text is not stored — only
// a short hash — so a secret inside the line cannot land in the audit file.
// A write failure does not block the command: full_access keeps its behavior.
func AuditFullAccessShell(command string) {
	writeShellAudit(command, "not_required", "full_access: linha com metacaractere de shell executada sem confirmação")
}

// AuditUnsandboxedShell appends one redacted line before a squad command runs
// on the host, outside sandboxx. The command text is not stored.
func AuditUnsandboxedShell(command string) {
	writeShellAudit(command, "unisolated", "SEM sandbox: comando do squad autorizado na sessão do host")
}

func writeShellAudit(command, sandbox, reason string) {
	path, err := shellAuditPathFn()
	if err != nil || strings.TrimSpace(path) == "" {
		return
	}
	sum := sha256.Sum256([]byte(command))
	line := shellAuditLine{
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		Phase:       "decision",
		AccountID:   "local",
		Origin:      "local",
		Tool:        "run_shell",
		Tier:        "exec",
		Decision:    "allow",
		Sandbox:     sandbox,
		TargetClass: "exec",
		TargetHash:  hex.EncodeToString(sum[:8]),
		Reason:      reason,
	}
	raw, err := json.Marshal(line)
	if err != nil {
		return
	}
	raw = append(raw, '\n')

	shellAuditMu.Lock()
	defer shellAuditMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(raw)
	_ = f.Sync()
	_ = f.Close()
}

// SetShellAuditPathForTest points full_access shell audit lines at path.
// The returned function restores the previous destination.
func SetShellAuditPathForTest(path string) func() {
	prev := shellAuditPathFn
	shellAuditPathFn = func() (string, error) { return path, nil }
	return func() { shellAuditPathFn = prev }
}
