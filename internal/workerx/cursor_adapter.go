package workerx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
)

const defaultCursorWorkerTimeout = 15 * time.Minute

// CursorWorkerAdapter executes a governed external task using Cursor CLI (`agent`)
// inside an isolated Git worktree while enforcing the HiveMind Worker Contract.
type CursorWorkerAdapter struct {
	LookCursorFn func() (string, error)
	APIKeyFn     func() string
	BaseArgs     []string
	Git          gitx.Git
}

// NewCursorWorkerAdapter returns the default Cursor CLI worker adapter.
func NewCursorWorkerAdapter() *CursorWorkerAdapter {
	return &CursorWorkerAdapter{
		LookCursorFn: modelx.LookCursor,
		Git:          gitx.Local{},
	}
}

// Provider returns "cursor".
func (a *CursorWorkerAdapter) Provider() string { return "cursor" }

// Execute runs one governed Cursor CLI worker task:
//  1. Resolves cursor binary & worktree directory;
//  2. Starts loopback CallbackServer (/heartbeat, blocking /ask, idempotent /done);
//  3. Writes the Worker Contract preamble + handoff to .hivemind/cursor-task.md;
//  4. Launches Cursor CLI with --trust always, and --force ONLY when PermissionMode == full_access;
//  5. Streams NDJSON lines + fallback inline protocol markers until process exit;
//  6. Returns the single idempotent DoneEvent (or synthesizes one from git status).
func (a *CursorWorkerAdapter) Execute(ctx context.Context, req DispatchRequest, cb Callbacks) (DoneEvent, error) {
	if strings.TrimSpace(req.WorktreeDir) == "" {
		return DoneEvent{}, fmt.Errorf("cursor worker requer worktreeDir isolado")
	}
	if strings.TrimSpace(req.DispatchID) == "" {
		req.DispatchID = fmt.Sprintf("disp-%d", time.Now().UTC().UnixNano())
	}
	if strings.TrimSpace(req.TaskID) == "" {
		req.TaskID = req.DispatchID
	}

	lookFn := a.LookCursorFn
	if lookFn == nil {
		lookFn = modelx.LookCursor
	}
	bin, err := lookFn()
	if err != nil {
		return DoneEvent{}, err
	}

	var apiKey string
	if a.APIKeyFn != nil {
		apiKey = strings.TrimSpace(a.APIKeyFn())
	} else {
		apiKey = strings.TrimSpace(modelx.ResolveCursorAPIKey(ctx))
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = defaultCursorWorkerTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	reg := NewDispatchRegistry()
	srv, err := StartCallbackServer(runCtx, req.DispatchID, req.TaskID, reg, cb)
	if err != nil {
		return DoneEvent{}, err
	}
	defer srv.Close()

	preamble := BuildWorkerPreamble(req, srv.URL())
	taskPath, err := modelx.WriteCursorTask(req.WorktreeDir, preamble)
	if err != nil {
		return DoneEvent{}, fmt.Errorf("gravar tarefa cursor worker: %w", err)
	}
	defer func() { _ = os.Remove(taskPath) }()

	modelID := strings.TrimSpace(req.Model)
	if modelID == "" {
		modelID = "auto"
	}
	cursorArgs := modelx.BuildCursorArgs(modelID, req.WorktreeDir, req.PermissionMode, runtime.GOOS)
	args := make([]string, 0, len(a.BaseArgs)+len(cursorArgs))
	args = append(args, a.BaseArgs...)
	args = append(args, cursorArgs...)

	cmd := modelx.CLICommand(runCtx, bin, args)
	cmd.Dir = req.WorktreeDir

	env := modelx.CursorChildEnv(os.Environ(), apiKey)
	env = append(env,
		"HIVEMIND_WORKER_DISPATCH_ID="+req.DispatchID,
		"HIVEMIND_WORKER_TASK_ID="+req.TaskID,
		"HIVEMIND_WORKER_URL="+srv.URL(),
		"HIVEMIND_WORKER_TOKEN="+srv.Token(),
	)
	for k, v := range req.Env {
		if strings.TrimSpace(k) != "" {
			env = append(env, k+"="+v)
		}
	}
	cmd.Env = env

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return DoneEvent{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &cappedBuffer{w: &stderr, limit: 8 * 1024}

	if err := cmd.Start(); err != nil {
		return DoneEvent{}, fmt.Errorf("iniciar cursor worker (%s): %w", filepath.Base(bin), err)
	}

	var (
		mu         sync.Mutex
		lastResult string
		failedMsg  string
		lastUsage  *modelx.CursorUsage
		wg         sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = modelx.ReadCLIStream(stdout, modelx.MaxCLILine, func(line string) bool {
			if marker, ok := ParseProtocolMarker(line); ok {
				switch marker.Kind {
				case "heartbeat":
					if cb.OnHeartbeat != nil {
						hb := *marker.Heartbeat
						if hb.DispatchID == "" {
							hb.DispatchID = req.DispatchID
						}
						cb.OnHeartbeat(hb)
					}
				case "done":
					d := *marker.Done
					if d.DispatchID == "" {
						d.DispatchID = req.DispatchID
					}
					if d.TaskID == "" {
						d.TaskID = req.TaskID
					}
					reg.AcceptDone(d)
				}
				return true
			}
			notice, ok := modelx.ParseCursorStreamLine(line)
			if !ok {
				return true
			}
			if notice.ToolName != "" && cb.OnTool != nil {
				cb.OnTool(notice.ToolName, notice.ToolPath)
			}
			if notice.Usage != nil {
				mu.Lock()
				lastUsage = notice.Usage
				mu.Unlock()
			}
			if notice.Text != "" {
				if notice.Final {
					mu.Lock()
					lastResult = notice.Text
					if notice.Failed {
						failedMsg = notice.Text
					}
					mu.Unlock()
				}
				if cb.OnOutput != nil {
					cb.OnOutput(notice.Text)
				}
			}
			return true
		})
	}()

	wg.Wait()
	waitErr := cmd.Wait()

	mu.Lock()
	resText := strings.TrimSpace(lastResult)
	failText := strings.TrimSpace(failedMsg)
	usage := lastUsage
	mu.Unlock()

	if doneEv, ok := reg.Get(req.DispatchID); ok {
		if waitErr != nil && doneEv.Outcome == "" {
			doneEv.Outcome = "failed"
		}
		if resText != "" && strings.TrimSpace(doneEv.Summary) == "" {
			doneEv.Summary = resText
		}
		doneEv.Usage = usageReportFrom(req.Model, usage)
		return doneEv, nil
	}

	if waitErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if failText != "" {
			detail = failText
		}
		if detail == "" {
			detail = waitErr.Error()
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return DoneEvent{DispatchID: req.DispatchID, TaskID: req.TaskID, Outcome: "aborted", Summary: "tempo limite excedido", Usage: usageReportFrom(req.Model, usage)}, fmt.Errorf("cursor worker excedeu %s", timeout)
		}
		return DoneEvent{DispatchID: req.DispatchID, TaskID: req.TaskID, Outcome: "failed", Summary: detail, Usage: usageReportFrom(req.Model, usage)}, fmt.Errorf("cursor worker falhou: %s", detail)
	}

	// Fallback if CLI completed cleanly without calling /done HTTP endpoint.
	g := a.Git
	if g == nil {
		g = gitx.Local{}
	}
	guardRes, _ := ValidatePostHocDiff(ctx, g, req.WorktreeDir, nil)
	doneEv := DoneEvent{
		DispatchID:    req.DispatchID,
		TaskID:        req.TaskID,
		Outcome:       "success",
		Summary:       resText,
		FilesModified: guardRes.Modified,
		Usage:         usageReportFrom(req.Model, usage),
	}
	reg.AcceptDone(doneEv)
	return doneEv, nil
}

// usageReportFrom converts one parsed CLI usage record into the ledger-facing
// UsageReport. nil usage yields nil so the event stream stays unchanged for
// CLIs that do not report tokens.
func usageReportFrom(model string, u *modelx.CursorUsage) *UsageReport {
	if u == nil {
		return nil
	}
	rep := &UsageReport{
		Model:           model,
		InputTokens:     u.InputTokens,
		OutputTokens:    u.OutputTokens,
		CacheReadTokens: u.CacheReadTokens,
	}
	if u.CostUSD != nil {
		rep.CostMicroUSD = int64(*u.CostUSD * 1_000_000)
	}
	return rep
}

type cappedBuffer struct {
	w     *bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	orig := len(p)
	if b.limit <= 0 {
		return orig, nil
	}
	if orig > b.limit {
		p = p[:b.limit]
	}
	b.limit -= len(p)
	_, _ = b.w.Write(p)
	return orig, nil
}
