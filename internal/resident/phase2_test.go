package resident

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/sandboxx"
	"github.com/gilvan-Borges/HiveMind/internal/secretx"
)

type fakeSandboxRunner struct {
	probe  sandboxx.ProbeResult
	runErr error
	calls  int
	last   sandboxx.Options
	output string
}

func (f *fakeSandboxRunner) Probe() sandboxx.ProbeResult {
	return f.probe
}

func (f *fakeSandboxRunner) Run(_ context.Context, opts sandboxx.Options) (sandboxx.Result, error) {
	f.calls++
	f.last = opts
	if !f.probe.Available {
		return sandboxx.Result{Mechanism: f.probe.Mechanism}, fmt.Errorf("%w: %s", sandboxx.ErrSandboxUnavailable, f.probe.Reason)
	}
	if f.runErr != nil {
		return sandboxx.Result{ExitCode: 1, Stderr: f.runErr.Error(), Mechanism: f.probe.Mechanism}, f.runErr
	}
	return sandboxx.Result{
		ExitCode:  0,
		Stdout:    f.output,
		Duration:  5 * time.Millisecond,
		Mechanism: f.probe.Mechanism,
	}, nil
}

func TestPhase2RefusesPCActionsWithoutSandbox(t *testing.T) {
	ws := t.TempDir()
	audit := NewFileAuditLogger(filepath.Join(t.TempDir(), "audit.jsonl"))
	sb := &fakeSandboxRunner{
		probe: sandboxx.ProbeResult{Available: false, Mechanism: "appcontainer+jobobject", Reason: "sandbox desabilitada no teste"},
	}

	b := NewBroker(BrokerConfig{
		Workspace:  ws,
		Workstream: "geral",
		Origin:     OriginLocal,
		Policy:     Policy{AllowedBinaries: []string{"git"}},
		Sandbox:    sb,
		Audit:      audit,
		Approve: func(context.Context, string, string) bool {
			t.Fatal("aprovador não deve ser chamado quando a sandbox está indisponível")
			return true
		},
	})

	if _, err := b.Execute(context.Background(), "write_file", map[string]any{"path": "hello.txt", "content": "data"}); !errors.Is(err, sandboxx.ErrSandboxUnavailable) {
		t.Fatalf("esperado ErrSandboxUnavailable em write_file, obteve %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "hello.txt")); !os.IsNotExist(err) {
		t.Fatalf("hello.txt não deveria ter sido gravado sem sandbox")
	}

	if _, err := b.Execute(context.Background(), "exec_command", map[string]any{"argv": []string{"git", "--version"}}); !errors.Is(err, sandboxx.ErrSandboxUnavailable) {
		t.Fatalf("esperado ErrSandboxUnavailable em exec_command, obteve %v", err)
	}
	if sb.calls != 0 {
		t.Fatalf("sandbox Run não deveria rodar quando Probe().Available=false (calls=%d)", sb.calls)
	}
}

func TestPhase2WriteAndExecRequireApprovalAndTimeoutDenies(t *testing.T) {
	ws := t.TempDir()
	target := filepath.Join(ws, "notes.md")
	if err := os.WriteFile(target, []byte("linha1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	audit := NewFileAuditLogger(filepath.Join(t.TempDir(), "audit.jsonl"))
	sb := &fakeSandboxRunner{
		probe:  sandboxx.ProbeResult{Available: true, Mechanism: "mock-os-sandbox"},
		output: "git version 2.45.0\n",
	}

	// 1. No approver -> write_file, edit_file, delete_file, exec_command are denied.
	noApprove := NewBroker(BrokerConfig{
		Workspace:  ws,
		Workstream: "geral",
		Origin:     OriginLocal,
		Policy:     Policy{AllowedBinaries: []string{"git"}},
		Sandbox:    sb,
		Audit:      audit,
	})
	if _, err := noApprove.Execute(context.Background(), "write_file", map[string]any{"path": "new.txt", "content": "x"}); err == nil {
		t.Fatal("write_file sem Approve deveria falhar")
	}
	if _, err := noApprove.Execute(context.Background(), "exec_command", map[string]any{"argv": []string{"git", "status"}}); err == nil {
		t.Fatal("exec_command sem Approve deveria falhar")
	}
	if sb.calls != 0 {
		t.Fatal("exec_command sem aprovação não pode chamar sandbox.Run")
	}

	// 2. Approval timeout -> fail closed denial.
	timeoutBroker := NewBroker(BrokerConfig{
		Workspace:       ws,
		Workstream:      "geral",
		Origin:          OriginLocal,
		Policy:          Policy{AllowedBinaries: []string{"git"}},
		Sandbox:         sb,
		Audit:           audit,
		ApprovalTimeout: 15 * time.Millisecond,
		Approve: func(ctx context.Context, _ string, _ string) bool {
			<-ctx.Done()
			return ctx.Err() == nil
		},
	})
	if _, err := timeoutBroker.Execute(context.Background(), "delete_file", map[string]any{"path": "notes.md"}); err == nil {
		t.Fatal("delete_file com timeout na aprovação deveria negar")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("notes.md deveria continuar existindo após timeout: %v", err)
	}

	// 3. Explicit approval -> executes write_file, edit_file, exec_command inside sandbox.
	var seenReq ApprovalRequest
	okBroker := NewBroker(BrokerConfig{
		Workspace: ws,
		Origin:    OriginLocal,
		Policy:    Policy{AllowedBinaries: []string{"git"}},
		Sandbox:   sb,
		Audit:     audit,
		ApproveDetailed: func(_ context.Context, req ApprovalRequest) bool {
			seenReq = req
			return true
		},
	})
	if _, err := okBroker.Execute(context.Background(), "write_file", map[string]any{"path": "created.txt", "content": "conteudo ok"}); err != nil {
		t.Fatalf("write_file aprovado falhou: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(ws, "created.txt"))
	if err != nil || string(data) != "conteudo ok" {
		t.Fatalf("conteúdo inesperado: %q err=%v", string(data), err)
	}
	if seenReq.Tier != TierWrite || seenReq.RiskLabel == "" {
		t.Fatalf("ApprovalRequest sem risco: %+v", seenReq)
	}
	if _, err := okBroker.Execute(context.Background(), "exec_command", map[string]any{"argv": []string{"git", "--version"}}); err != nil {
		t.Fatalf("exec_command aprovado falhou: %v", err)
	}
	if sb.calls != 1 || sb.last.AllowNetwork {
		t.Fatalf("sandbox calls=%d allowNet=%v (deveria ser AllowNetwork=false)", sb.calls, sb.last.AllowNetwork)
	}
}

func TestPhase2RemoteRequiresApprovalOnEveryTool(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "readme.txt"), []byte("oi"), 0o644); err != nil {
		t.Fatal(err)
	}
	audit := NewFileAuditLogger(filepath.Join(t.TempDir(), "audit.jsonl"))

	for _, origin := range []Origin{OriginRemote, OriginRoutine} {
		prompted := false
		b := NewBroker(BrokerConfig{
			Workspace: ws,
			Origin:    origin,
			Audit:     audit,
			Approve: func(_ context.Context, _ string, _ string) bool {
				prompted = true
				return false
			},
		})
		if _, err := b.Execute(context.Background(), "read_file", map[string]any{"path": "readme.txt"}); err == nil {
			t.Fatalf("read_file com origem %s sem aprovação deveria falhar", origin)
		}
		if !prompted {
			t.Fatalf("origem %s deveria ter pedido aprovação humana para read_file", origin)
		}
	}
}

func TestPhase2BlocksShellTokenCommandsAndUnallowlistedBinaries(t *testing.T) {
	ws := t.TempDir()
	audit := NewFileAuditLogger(filepath.Join(t.TempDir(), "audit.jsonl"))
	sb := &fakeSandboxRunner{probe: sandboxx.ProbeResult{Available: true, Mechanism: "mock"}}

	b := NewBroker(BrokerConfig{
		Workspace: ws,
		Origin:    OriginLocal,
		Policy:    Policy{AllowedBinaries: []string{"gh", "git"}},
		Sandbox:   sb,
		Audit:     audit,
		Approve: func(context.Context, string, string) bool {
			t.Fatal("comando proibido nunca deveria pedir aprovação")
			return true
		},
	})

	blockedCases := []map[string]any{
		{"command": "echo hi"},                                 // free command string
		{"argv": []string{"sh", "-c", "ls"}},                   // free shell
		{"argv": []string{"powershell.exe", "-Command", "ls"}}, // Windows shell
		{"argv": []string{"cmd", "/c", "dir"}},                 // cmd.exe
		{"argv": []string{"gh", "auth", "token"}},              // token dump
		{"argv": []string{"printenv"}},                         // env dump
		{"argv": []string{"git", "credential", "fill"}},        // git creds
		{"argv": []string{"curl", "https://example.com"}},      // binary not in allowlist
	}
	for i, args := range blockedCases {
		if _, err := b.Execute(context.Background(), "exec_command", args); err == nil {
			t.Errorf("caso %d deveria ter sido recusado fail-closed: %+v", i, args)
		}
	}

	// Wildcard "*" in SavePolicy must fail closed.
	dir := t.TempDir()
	if err := SavePolicy(dir, Policy{AllowedBinaries: []string{"*"}}); err == nil {
		t.Fatal("SavePolicy deveria recusar wildcard '*'")
	}
	if err := SavePolicy(dir, Policy{AllowedHosts: []string{"*"}}); err == nil {
		t.Fatal("SavePolicy deveria recusar wildcard '*' em rede")
	}
}

func TestPhase2NetworkBlockedOutsideAllowlistAndBlocksSSRF(t *testing.T) {
	// Start local test server to inspect SSRF / allowlist rules cleanly without internet access.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "auth=%s", r.Header.Get("Authorization"))
	}))
	defer srv.Close()

	// Empty allowlist always blocks net_fetch.
	if _, err := ValidateOutboundURL("https://api.github.com/user", nil); err == nil {
		t.Fatal("URL deveria ser bloqueada com allowlist vazia")
	}
	// SSRF / metadata IPv4 and loopback blocked even if added or matched.
	for _, unsafe := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:8080/health",
		"http://10.0.0.1/admin",
		"http://localhost/secret",
		"file:///etc/passwd",
	} {
		if _, err := ValidateOutboundURL(unsafe, []string{"169.254.169.254", "127.0.0.1", "10.0.0.1", "localhost", "*.github.com"}); err == nil {
			t.Fatalf("URL insegura %q deveria ser bloqueada", unsafe)
		}
	}

	// Valid public host on allowlist succeeds via injected test transport & scrubs vault secret!
	vault := secretx.NewMemoryVault()
	const secretToken = "ghp_phase2SecretTokenValue99887766"
	if err := vault.Set("github_ro", secretToken); err != nil {
		t.Fatal(err)
	}

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	audit := NewFileAuditLogger(auditPath)
	var events []Event

	// Rewrite requests cleanly to srv while preserving allowlisted host validation on URL.
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = "http"
		clone.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		return srv.Client().Do(clone)
	})
	b := NewBroker(BrokerConfig{
		Workspace:  t.TempDir(),
		Origin:     OriginLocal,
		Policy:     Policy{AllowedHosts: []string{"api.github.com"}},
		Vault:      vault,
		HTTPClient: &http.Client{Transport: rt},
		Audit:      audit,
		Approve:    func(context.Context, string, string) bool { return true },
		OnToolEvent: func(ev Event) {
			events = append(events, ev)
		},
	})

	// 1. Out-of-allowlist host fails closed.
	if _, err := b.Execute(context.Background(), "net_fetch", map[string]any{"url": "https://evil.example.com/exfil"}); err == nil {
		t.Fatal("host fora da allowlist deveria ser bloqueado")
	}

	// 2. Allowlisted host uses vault secret in Authorization header and scrubs secret from tool output, events, and audit!
	out, err := b.Execute(context.Background(), "net_fetch", map[string]any{
		"url":             "https://api.github.com/zen",
		"credential_name": "github_ro",
	})
	if err != nil {
		t.Fatalf("net_fetch allowlisted falhou: %v", err)
	}
	if strings.Contains(out, secretToken) {
		t.Fatalf("segredo do cofre vazou na saída da ferramenta: %q", out)
	}
	for _, ev := range events {
		rawEv, _ := json.Marshal(ev)
		if strings.Contains(string(rawEv), secretToken) {
			t.Fatalf("segredo vazou no evento SSE: %s", string(rawEv))
		}
	}
	rawAudit, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawAudit), secretToken) {
		t.Fatalf("segredo vazou no arquivo de auditoria")
	}
}

func TestPhase2RealSandboxExecCommandEndToEnd(t *testing.T) {
	sb := sandboxx.NewOSRunner()
	probe := sb.Probe()
	if runtime.GOOS == "windows" && !probe.Available {
		t.Fatalf("AppContainer deveria estar disponível no Windows: %+v", probe)
	}
	if !probe.Available {
		t.Skipf("sandbox nativa indisponível neste host: %s", probe.Reason)
	}

	ws := t.TempDir()
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	audit := NewFileAuditLogger(auditPath)

	// Allow running the compiled Go binary itself? Prefer the current test exe in allowlist
	// to avoid PATH assumptions and measure real AppContainer overhead cleanly.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// bwrap esconde /tmp. O broker só monta o workspace; no teste, acrescenta
	// ro-bind só do diretório do binário. Não monta /tmp inteiro.
	runner := sandboxx.Runner(sb)
	if runtime.GOOS == "linux" {
		dir := filepath.Clean(filepath.Dir(exe))
		switch dir {
		case "/", "/tmp", "/var/tmp", "/private/tmp", ".":
			t.Fatalf("recuso montar %s inteiro só para o binário de teste", dir)
		}
		runner = linuxExeReadRoot{inner: sb, dir: dir}
	}
	b := NewBroker(BrokerConfig{
		Workspace: ws,
		Origin:    OriginLocal,
		Policy:    Policy{AllowedBinaries: []string{exe}},
		Sandbox:   runner,
		Audit:     audit,
		Approve:   func(context.Context, string, string) bool { return true },
	})

	start := time.Now()
	out, err := b.Execute(context.Background(), "exec_command", map[string]any{
		"argv": []string{exe, "-test.run=^TestPhase2SandboxSubprocessNoop$"},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("exec_command real na sandbox falhou: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "DADO_NAO_CONFIAVEL") {
		t.Fatalf("saída da ferramenta sem cerca de proteção: %q", out)
	}
	t.Logf("perf: real broker+audit+OS-sandbox exec elapsed=%s (mechanism=%s)", elapsed, probe.Mechanism)
}

func TestPhase2SandboxSubprocessNoop(t *testing.T) {
	// Helper target for TestPhase2RealSandboxExecCommandEndToEnd.
	fmt.Print("")
}

// linuxExeReadRoot ro-binds only the test executable directory on Linux.
type linuxExeReadRoot struct {
	inner sandboxx.Runner
	dir   string
}

func (m linuxExeReadRoot) Probe() sandboxx.ProbeResult { return m.inner.Probe() }

func (m linuxExeReadRoot) Run(ctx context.Context, opts sandboxx.Options) (sandboxx.Result, error) {
	if m.dir != "" {
		roots := make([]string, 0, len(opts.ReadRoots)+1)
		roots = append(roots, opts.ReadRoots...)
		roots = append(roots, m.dir)
		opts.ReadRoots = roots
	}
	return m.inner.Run(ctx, opts)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
