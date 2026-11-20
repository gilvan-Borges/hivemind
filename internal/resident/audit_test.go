package resident

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type recordingAudit struct {
	mu          sync.Mutex
	events      []string
	entries     []AuditEntry
	decisionErr error
	outcomeErr  error
}

func (r *recordingAudit) RecordDecision(entry AuditEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "decision:"+entry.Tool+":"+entry.Decision)
	if r.decisionErr != nil {
		return r.decisionErr
	}
	entry.Phase = "decision"
	r.entries = append(r.entries, entry)
	return nil
}

func (r *recordingAudit) RecordOutcome(entry AuditEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "outcome:"+entry.Tool+":"+entry.Outcome)
	if r.outcomeErr != nil {
		return r.outcomeErr
	}
	entry.Phase = "outcome"
	r.entries = append(r.entries, entry)
	return nil
}

func TestBrokerAuditsDecisionBeforeExecutionAndOutcomeAfter(t *testing.T) {
	rec := &recordingAudit{}
	broker := NewBroker(BrokerConfig{
		Workspace:  t.TempDir(),
		Workstream: "ws-ordem",
		Origin:     OriginLocal,
		AccountID:  "acct_alpha",
		Audit:      rec,
		CustomHandlers: map[string]ToolHandler{
			"list_projects": func(context.Context, map[string]any) (string, error) {
				rec.mu.Lock()
				rec.events = append(rec.events, "exec:list_projects")
				rec.mu.Unlock()
				return "proj-a", nil
			},
		},
	})

	out, err := broker.Execute(context.Background(), "list_projects", nil)
	if err != nil {
		t.Fatalf("Execute falhou: %v", err)
	}
	if !strings.Contains(out, "proj-a") || !strings.Contains(out, "[DADO_NAO_CONFIAVEL") {
		t.Fatalf("saída deveria estar cercada com dado: %q", out)
	}

	wantOrder := []string{
		"decision:list_projects:allow",
		"exec:list_projects",
		"outcome:list_projects:ok",
	}
	if len(rec.events) != len(wantOrder) {
		t.Fatalf("eventos = %v, esperado %v", rec.events, wantOrder)
	}
	for i := range wantOrder {
		if rec.events[i] != wantOrder[i] {
			t.Fatalf("ordem[%d] = %q, esperado %q (sequência completa: %v)", i, rec.events[i], wantOrder[i], rec.events)
		}
	}
	if len(rec.entries) != 2 || rec.entries[0].ApprovalID == "" || rec.entries[0].ApprovalID != rec.entries[1].ApprovalID {
		t.Fatalf("decisão e desfecho devem compartilhar o mesmo ApprovalID: %+v", rec.entries)
	}
}

func TestBrokerAbortsCallWhenAuditWriteFails(t *testing.T) {
	rec := &recordingAudit{decisionErr: errors.New("disco cheio / I/O error")}
	executed := 0
	broker := NewBroker(BrokerConfig{
		Workspace: t.TempDir(),
		Origin:    OriginLocal,
		Audit:     rec,
		CustomHandlers: map[string]ToolHandler{
			"list_projects": func(context.Context, map[string]any) (string, error) {
				executed++
				return "nunca-deve-rodar", nil
			},
		},
	})

	_, err := broker.Execute(context.Background(), "list_projects", nil)
	if err == nil {
		t.Fatal("falha ao gravar auditoria deveria abortar a chamada")
	}
	if executed != 0 {
		t.Fatalf("handler executou %d vez(es) mesmo após falha na gravação do log de auditoria", executed)
	}
}

func TestFileAuditLoggerPersistsRedactsAndRotates(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.jsonl")
	logger := NewFileAuditLogger(auditPath)
	logger.maxBytes = 200 // small threshold to exercise rotation cleanly

	if err := logger.RecordDecision(AuditEntry{
		AccountID:  "acct_1",
		Origin:     OriginLocal,
		Workstream: "geral",
		Tool:       "read_file",
		Tier:       TierRead,
		Decision:   "allow",
		ApprovalID: "res_1",
		Target:     "README.md Bearer sk-or-v1-secret1234567890abcdef",
	}); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if err := logger.RecordOutcome(AuditEntry{
		AccountID:  "acct_1",
		Origin:     OriginLocal,
		Workstream: "geral",
		Tool:       "read_file",
		Tier:       TierRead,
		Decision:   "allow",
		ApprovalID: "res_1",
		Outcome:    "ok",
	}); err != nil {
		t.Fatalf("RecordOutcome: %v", err)
	}

	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("ReadFile audit.jsonl: %v", err)
	}
	if strings.Contains(string(raw), "secret1234567890abcdef") {
		t.Fatalf("segredo vazou no audit.jsonl: %s", string(raw))
	}

	entries, err := logger.ReadRecent(10)
	if err != nil {
		t.Fatalf("ReadRecent: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("esperado ao menos 1 registro em audit.jsonl")
	}

	// Trigger rotation on next write (file size > 200 bytes).
	if err := logger.RecordDecision(AuditEntry{
		AccountID: "acct_1",
		Origin:    OriginLocal,
		Tool:      "list_dir",
		Tier:      TierRead,
		Decision:  "allow",
		Target:    ".",
	}); err != nil {
		t.Fatalf("RecordDecision pós-rotação: %v", err)
	}
	matches, _ := filepath.Glob(auditPath + ".*.bak")
	if len(matches) == 0 {
		t.Fatal("esperado arquivo rotacionado .bak após exceder maxBytes")
	}
}

func TestExecAuditDropsArgv(t *testing.T) {
	// ide/internal não entra aqui: o JSONL durável de internal/resident/audit.go
	// guarda classe e hash, não o argv.
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "audit.jsonl")
	logger := NewFileAuditLogger(auditPath)
	const argv = "git status --porcelain C:\\segredo\\repo"
	if err := logger.RecordDecision(AuditEntry{
		Origin:   OriginLocal,
		Tool:     "exec_command",
		Tier:     TierExec,
		Decision: "allow",
		Target:   argv,
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "git") || strings.Contains(text, "segredo") || strings.Contains(text, `"target":`) {
		t.Fatalf("argv ou caminho no audit.jsonl: %s", text)
	}
	if !strings.Contains(text, `"targetClass":"exec"`) || !strings.Contains(text, `"targetHash":"`) {
		t.Fatalf("faltou classe ou hash: %s", text)
	}
	entries, err := logger.ReadRecent(5)
	if err != nil || len(entries) != 1 || entries[0].Target != "" || entries[0].TargetClass != "exec" || len(entries[0].TargetHash) != 16 {
		t.Fatalf("leitura: %+v err=%v", entries, err)
	}
}
