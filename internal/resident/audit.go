package resident

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// MaxAuditFileBytes rotates audit.jsonl at 20 MiB (§11).
	MaxAuditFileBytes = 20 * 1024 * 1024
)

// AuditWriter is the durable audit log sink used by Broker (§12.7).
// RecordDecision MUST persist and fsync to disk before returning nil.
type AuditWriter interface {
	RecordDecision(entry AuditEntry) error
	RecordOutcome(entry AuditEntry) error
}

// FileAuditLogger appends JSONL entries to audit.jsonl with fsync on every write.
type FileAuditLogger struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
}

// NewFileAuditLogger creates a durable JSONL audit logger at path.
func NewFileAuditLogger(path string) *FileAuditLogger {
	return &FileAuditLogger{
		path:     path,
		maxBytes: MaxAuditFileBytes,
	}
}

// Path returns the audit file path.
func (l *FileAuditLogger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// RecordDecision appends a decision record and fsyncs before returning.
func (l *FileAuditLogger) RecordDecision(entry AuditEntry) error {
	entry.Phase = "decision"
	return l.appendSync(entry)
}

// RecordOutcome appends an outcome record and fsyncs before returning.
func (l *FileAuditLogger) RecordOutcome(entry AuditEntry) error {
	entry.Phase = "outcome"
	return l.appendSync(entry)
}

func (l *FileAuditLogger) appendSync(entry AuditEntry) error {
	if l == nil || strings.TrimSpace(l.path) == "" {
		return fmt.Errorf("caminho de auditoria não configurado")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if entry.AccountID == "" {
		entry.AccountID = DefaultAccountID
	}
	entry = durableAudit(entry)

	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return fmt.Errorf("criar diretório de auditoria: %w", err)
	}

	if err := l.rotateIfNeededLocked(); err != nil {
		return fmt.Errorf("rotacionar auditoria: %w", err)
	}

	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("serializar auditoria: %w", err)
	}
	line = append(line, '\n')

	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("abrir auditoria: %w", err)
	}
	if _, err := f.Write(line); err != nil {
		_ = f.Close()
		return fmt.Errorf("gravar auditoria: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync auditoria: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("fechar auditoria: %w", err)
	}
	return nil
}

func durableAudit(entry AuditEntry) AuditEntry {
	raw := entry.Target
	entry.TargetClass = targetClass(entry.Tool, raw)
	if raw != "" {
		sum := sha256.Sum256([]byte(raw))
		entry.TargetHash = hex.EncodeToString(sum[:8])
		entry.Reason = strings.ReplaceAll(entry.Reason, raw, "")
	}
	entry.Target = ""
	entry.Reason = RedactSecrets(strings.TrimSpace(entry.Reason))
	entry.ErrorClass = RedactSecrets(entry.ErrorClass)
	return entry
}

func targetClass(tool, raw string) string {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "exec_command":
		return "exec"
	case "net_fetch":
		return "url"
	case "memory_search", "memory_write", "memory_forget":
		return "memory"
	case "read_file", "list_dir", "write_file", "edit_file", "delete_file", "search_text", "find_files":
		return "file"
	default:
		if strings.Contains(strings.ToLower(raw), "://") {
			return "url"
		}
		return "other"
	}
}

func (l *FileAuditLogger) rotateIfNeededLocked() error {
	limit := l.maxBytes
	if limit <= 0 {
		limit = MaxAuditFileBytes
	}
	info, err := os.Stat(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() < limit {
		return nil
	}
	rotated := fmt.Sprintf("%s.%s.bak", l.path, time.Now().UTC().Format("20060102T150405Z"))
	return os.Rename(l.path, rotated)
}

// ReadRecent reads up to limit newest audit entries (read-only UI view, §11).
func (l *FileAuditLogger) ReadRecent(limit int) ([]AuditEntry, error) {
	if l == nil || strings.TrimSpace(l.path) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var all []AuditEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 16*1024), 256*1024)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var e AuditEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			continue
		}
		// Linha antiga pode ter caminho em target. A UI não recebe esse campo.
		e.Target = ""
		all = append(all, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	// Return newest first for UI readability.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	return all, nil
}
