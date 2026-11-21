package cli

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
	"github.com/gilvan-Borges/HiveMind/internal/modelx"
	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/remote"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
	"github.com/gilvan-Borges/HiveMind/internal/skillx"
	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// serveHost is the CLI-side implementation of remote.Deps for `hivemind serve`
// (Spec 09 §3.3: 100% headless, no hivemind-ide.exe). Squad runs execute in
// process through the orchestrator Runner; every shell/approval gate surfaces
// on the mobile app as an approval_required event resolved via the
// /approvals/resolve route.
//
// Routes that still depend on IDE-only services (git diffs, PR/issue listing
// and auto-review) return an explicit error until the unified bootstrap
// (W3-04, post Wave-2 merge) wires the shared services into the CLI runtime.
const serveApprovalTimeout = 5 * time.Minute

type serveHost struct {
	hub *remote.Hub

	mu        sync.Mutex
	cancels   map[string]context.CancelFunc
	approvals map[string]chan bool
	apprSeq   uint64
}

func newServeHost(hub *remote.Hub) *serveHost {
	return &serveHost{
		hub:       hub,
		cancels:   make(map[string]context.CancelFunc),
		approvals: make(map[string]chan bool),
	}
}

var _ remote.Deps = (*serveHost)(nil)

// requestApproval blocks until the mobile app resolves the gate (or the
// timeout denies, fail closed).
func (s *serveHost) requestApproval(laneID, teamName, gate, prompt string) bool {
	s.mu.Lock()
	s.apprSeq++
	id := "cli-" + strconv.FormatUint(s.apprSeq, 10)
	ch := make(chan bool, 1)
	s.approvals[id] = ch
	s.mu.Unlock()

	s.hub.HandleLaneEvent(remote.LaneEvent{
		LaneID: laneID,
		Team:   teamName,
		Event: orchestrator.Event{
			Kind:       "approval_required",
			ApprovalID: id,
			Gate:       gate,
			Label:      "HiveMind serve",
			Text:       prompt,
		},
	})

	select {
	case ok := <-ch:
		return ok
	case <-time.After(serveApprovalTimeout):
		s.mu.Lock()
		delete(s.approvals, id)
		s.mu.Unlock()
		s.hub.MarkApprovalResolved(id, false)
		return false
	}
}

// ApprovalResolve resolves a pending gate from the mobile app.
func (s *serveHost) ApprovalResolve(id string, approved bool) bool {
	s.mu.Lock()
	ch, ok := s.approvals[id]
	if ok {
		delete(s.approvals, id)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	ch <- approved
	s.hub.MarkApprovalResolved(id, approved)
	return true
}

// RunCancel stops the active run on the lane.
func (s *serveHost) RunCancel(laneID string) bool {
	s.mu.Lock()
	cancel, ok := s.cancels[laneID]
	if ok {
		delete(s.cancels, laneID)
	}
	s.mu.Unlock()
	if ok {
		cancel()
	}
	s.hub.MarkCancel(laneID)
	return ok
}

// RunStart validates the request, registers the lane on the hub and starts the
// squad turn asynchronously (errors surface via SSE as lane error events).
func (s *serveHost) RunStart(
	ctx context.Context,
	laneID, workspace, teamName, objective, jiraKey, sessionID, runMode string,
	images []remote.ImageInput,
) error {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return fmt.Errorf("workspace obrigatório")
	}
	if strings.TrimSpace(laneID) == "" {
		return fmt.Errorf("laneId obrigatório")
	}
	if strings.TrimSpace(objective) == "" && len(images) == 0 {
		return fmt.Errorf("objective (ou images) obrigatório")
	}

	project, err := team.Load(ws)
	if err != nil {
		return fmt.Errorf("time não configurado em %s: %w", ws, err)
	}
	squad, err := project.ActiveTeam(strings.TrimSpace(teamName))
	if err != nil {
		return err
	}

	s.hub.RegisterRun(laneID, ws, squad.Name, objective, sessionID, runMode)
	if text := strings.TrimSpace(objective); text != "" {
		s.hub.HandleLaneEvent(remote.LaneEvent{
			LaneID: laneID,
			Team:   squad.Name,
			Event:  remote.UserMessageEvent(text),
		})
	}

	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[laneID] = cancel
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.cancels, laneID)
			s.mu.Unlock()
			cancel()
		}()

		defer modelx.FlushAIUsage()

		attachments, attErr := decodeServeImages(images)
		if attErr != nil {
			s.hub.HandleRunError(laneID, ws, attErr)
			return
		}

		mcpHost := mcpx.Start(runCtx, ws)
		defer mcpHost.Close()

		nativeRoot := skillx.UserSkillsDir()
		if _, seedErr := skillx.SeedProductSkills(nativeRoot); seedErr != nil {
			s.hub.HandleLaneEvent(remote.LaneEvent{
				LaneID: laneID,
				Team:   squad.Name,
				Event: orchestrator.Event{
					Kind:  "text",
					Stage: "chief",
					Label: "HiveMind serve",
					Text:  "aviso: skills nativas: " + seedErr.Error(),
				},
			})
		}

		emit := func(ev orchestrator.Event) {
			s.hub.HandleLaneEvent(remote.LaneEvent{LaneID: laneID, Team: squad.Name, Event: ev})
		}

		runner := &orchestrator.Runner{
			Workspace:        ws,
			Project:          project,
			Team:             squad,
			Git:              gitx.Local{},
			Confirm:          func(command string) bool { return s.requestApproval(laneID, squad.Name, "shell", command) },
			Approve:          func(prompt string) bool { return s.requestApproval(laneID, squad.Name, "merge", prompt) },
			MCP:              mcpHost,
			NativeSkills:     skillx.LoadNative(nativeRoot),
			NativeSkillsRoot: nativeRoot,
			Out:              io.Discard,
			Err:              io.Discard,
			Streaming:        agent.StreamingModeSSE,
			OnEvent:          emit,
		}
		runner.NewModel = orchestrator.NewJevModelFactory(orchestrator.JevFactoryOptions{
			Workspace: ws,
			Approve:   runner.Approve,
			OnEvent:   runner.OnEvent,
		})

		_, runErr := runner.Run(runCtx, orchestrator.Request{
			Objective:   objective,
			JiraKey:     strings.TrimSpace(jiraKey),
			SessionID:   strings.TrimSpace(sessionID),
			LaneID:      laneID,
			RunMode:     strings.TrimSpace(runMode),
			Attachments: attachments,
		})
		if runErr != nil {
			s.hub.HandleRunError(laneID, ws, runErr)
			return
		}
		emit(orchestrator.Event{Kind: "finish", Stage: "done", Text: "execução concluída"})
	}()
	return nil
}

// ListSessions lists saved sessions and totals from the shared sessionx store.
func (s *serveHost) ListSessions(ctx context.Context, workspace string) (sessionx.WorkspaceSessionsSummary, error) {
	return sessionx.ListWorkspaceSessions(ctx, workspace)
}

// LoadSession loads one saved session transcript from the shared sessionx store.
func (s *serveHost) LoadSession(ctx context.Context, workspace, id string) (sessionx.SessionDetail, error) {
	return sessionx.LoadSessionDetail(ctx, workspace, id)
}

// Diff is not yet available on the CLI serve host: it depends on the IDE diff
// services and will be provided by the unified runtime bootstrap (W3-04).
func (s *serveHost) Diff(ctx context.Context, workspace string) ([]remote.FileDiff, error) {
	return nil, fmt.Errorf("diffs ainda não disponíveis no serve headless; abra a IDE ou aguarde o bootstrap unificado (W3-04)")
}

// ListPRs is not yet available on the CLI serve host (W3-04 will wire it).
func (s *serveHost) ListPRs(ctx context.Context, workspace string) ([]remote.ProjectPullRequest, error) {
	return nil, fmt.Errorf("listagem de PRs ainda não disponível no serve headless; abra a IDE ou aguarde o bootstrap unificado (W3-04)")
}

// ListIssues is not yet available on the CLI serve host (W3-04 will wire it).
func (s *serveHost) ListIssues(ctx context.Context, workspace string) ([]remote.ProjectIssue, error) {
	return nil, fmt.Errorf("listagem de issues ainda não disponível no serve headless; abra a IDE ou aguarde o bootstrap unificado (W3-04)")
}

// AutoReviewStatus reports idle: the auto-review watcher is IDE-side for now.
func (s *serveHost) AutoReviewStatus(workspace string) remote.AutoReviewStatus {
	return remote.AutoReviewStatus{Workspace: workspace, Enabled: false, State: "idle"}
}

// AutoReviewPoll is a no-op on the CLI serve host.
func (s *serveHost) AutoReviewPoll(workspace string) remote.AutoReviewStatus {
	return s.AutoReviewStatus(workspace)
}

// decodeServeImages converts bridge ImageInput payloads into orchestrator
// attachments (same normalization the IDE chat applies).
func decodeServeImages(inputs []remote.ImageInput) ([]orchestrator.Attachment, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([]orchestrator.Attachment, 0, len(inputs))
	for i, in := range inputs {
		raw := strings.TrimSpace(in.DataBase64)
		mime := strings.TrimSpace(in.MIMEType)
		if strings.HasPrefix(raw, "data:") {
			if comma := strings.IndexByte(raw, ','); comma > 0 {
				header := raw[5:comma]
				if semi := strings.IndexByte(header, ';'); semi > 0 && mime == "" {
					mime = header[:semi]
				}
				raw = raw[comma+1:]
			}
		}
		if raw == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, fmt.Errorf("imagem %d inválida: %w", i+1, err)
		}
		if mime == "" {
			mime = "image/png"
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = fmt.Sprintf("imagem_%d.png", i+1)
		}
		out = append(out, orchestrator.Attachment{Name: name, MIMEType: mime, Data: decoded})
	}
	return out, nil
}
