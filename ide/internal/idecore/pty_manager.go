package idecore

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// PTYEvent carries live terminal output or exit notification from an interactive PTY session.
type PTYEvent struct {
	TermID    string `json:"termId"`
	Workspace string `json:"workspace"`
	Data      string `json:"data,omitempty"`
	Exited    bool   `json:"exited,omitempty"`
	ExitCode  int    `json:"exitCode,omitempty"`
}

type managedPTY struct {
	id        string
	workspace string
	sess      *ptySession
	alive     bool
	mu        sync.Mutex
	history   []byte
}

func (m *managedPTY) appendHistory(chunk []byte) {
	const maxHist = 32 * 1024
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history = append(m.history, chunk...)
	if len(m.history) > maxHist {
		m.history = append([]byte(nil), m.history[len(m.history)-maxHist:]...)
	}
}

func (m *managedPTY) snapshot() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return string(m.history)
}

// PTYManager owns persistent interactive PTY sessions keyed by termID (typically workspace path).
type PTYManager struct {
	mu      sync.Mutex
	terms   map[string]*managedPTY
	OnEvent func(PTYEvent)
}

// NewPTYManager creates a PTYManager that emits PTYEvent notifications via onEvent.
func NewPTYManager(onEvent func(PTYEvent)) *PTYManager {
	return &PTYManager{
		terms:   make(map[string]*managedPTY),
		OnEvent: onEvent,
	}
}

func normTermID(termID, workspace string) string {
	id := strings.TrimSpace(termID)
	if id == "" {
		id = strings.TrimSpace(workspace)
	}
	return strings.ToLower(strings.ReplaceAll(id, "\\", "/"))
}

// Start ensures a live interactive PTY exists for termID in workspace, returning
// any buffered scrollback if the session was already running.
func (m *PTYManager) Start(termID, workspace string, cols, rows int) (string, error) {
	return m.StartWithEnv(termID, workspace, cols, rows, nil)
}

// StartWithEnv starts (or resumes) an interactive PTY for termID with optional extra env vars.
func (m *PTYManager) StartWithEnv(termID, workspace string, cols, rows int, extraEnv map[string]string) (string, error) {
	key := normTermID(termID, workspace)
	if key == "" {
		return "", fmt.Errorf("workspace inválido para PTY")
	}

	m.mu.Lock()
	if cur, ok := m.terms[key]; ok && cur.alive {
		snap := cur.snapshot()
		m.mu.Unlock()
		_ = cur.sess.Resize(cols, rows)
		return snap, nil
	}
	m.mu.Unlock()

	sess, err := startNativePTYWithEnv(workspace, cols, rows, extraEnv)
	if err != nil {
		return "", err
	}

	mp := &managedPTY{
		id:        key,
		workspace: workspace,
		sess:      sess,
		alive:     true,
	}

	m.mu.Lock()
	prev, hadPrev := m.terms[key]
	if hadPrev && prev != nil {
		prev.alive = false
	}
	m.terms[key] = mp
	m.mu.Unlock()
	if hadPrev && prev != nil && prev.sess != nil {
		_ = prev.sess.Close()
	}

	go m.pump(key, mp)
	return "", nil
}

func (m *PTYManager) pump(key string, mp *managedPTY) {
	buf := make([]byte, 4096)
	for {
		n, err := mp.sess.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			mp.appendHistory(chunk)
			if m.OnEvent != nil {
				m.OnEvent(PTYEvent{
					TermID:    mp.id,
					Workspace: mp.workspace,
					Data:      string(chunk),
				})
			}
		}
		if err != nil {
			break
		}
	}

	exitCode := mp.sess.Wait()
	_ = mp.sess.Close()

	m.mu.Lock()
	if cur, ok := m.terms[key]; ok && cur == mp {
		mp.alive = false
	}
	m.mu.Unlock()

	if m.OnEvent != nil {
		m.OnEvent(PTYEvent{
			TermID:    mp.id,
			Workspace: mp.workspace,
			Exited:    true,
			ExitCode:  exitCode,
		})
	}
}

// Write sends raw terminal input bytes (keystrokes, Ctrl+C, commands) to a running PTY.
func (m *PTYManager) Write(termID, data string) error {
	key := normTermID(termID, "")
	m.mu.Lock()
	mp, ok := m.terms[key]
	alive := ok && mp != nil && mp.alive
	m.mu.Unlock()
	if !alive {
		return fmt.Errorf("sessão PTY não está ativa")
	}
	_, err := mp.sess.Write([]byte(data))
	return err
}

// Resize updates the dimensions (cols x rows) of a running PTY.
func (m *PTYManager) Resize(termID string, cols, rows int) error {
	key := normTermID(termID, "")
	m.mu.Lock()
	mp, ok := m.terms[key]
	alive := ok && mp != nil && mp.alive
	m.mu.Unlock()
	if !alive {
		return nil
	}
	return mp.sess.Resize(cols, rows)
}

// Restart closes any existing PTY for termID and starts a fresh shell in workspace.
func (m *PTYManager) Restart(termID, workspace string, cols, rows int) error {
	m.Close(termID)
	_, err := m.Start(termID, workspace, cols, rows)
	return err
}

// Close terminates a single PTY session cleanly.
func (m *PTYManager) Close(termID string) {
	key := normTermID(termID, "")
	m.mu.Lock()
	mp, ok := m.terms[key]
	if ok {
		delete(m.terms, key)
		if mp != nil {
			mp.alive = false
		}
	}
	m.mu.Unlock()
	if ok && mp != nil && mp.sess != nil {
		_ = mp.sess.Close()
	}
}

func normWorkspaceKey(workspace string) string {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return ""
	}
	if abs, err := filepath.Abs(ws); err == nil {
		ws = abs
	}
	return strings.TrimRight(strings.ToLower(filepath.ToSlash(filepath.Clean(ws))), "/")
}

// CloseWorkspace terminates all active PTY sessions belonging to workspace.
func (m *PTYManager) CloseWorkspace(workspace string) {
	if m == nil {
		return
	}
	target := normWorkspaceKey(workspace)
	if target == "" {
		return
	}
	m.mu.Lock()
	var list []*managedPTY
	for k, mp := range m.terms {
		if mp == nil {
			delete(m.terms, k)
			continue
		}
		if normWorkspaceKey(mp.workspace) == target || strings.TrimRight(k, "/") == target {
			mp.alive = false
			list = append(list, mp)
			delete(m.terms, k)
		}
	}
	m.mu.Unlock()
	for _, mp := range list {
		if mp.sess != nil {
			_ = mp.sess.Close()
		}
	}
}

// CloseAll terminates all active PTY sessions (called on IDE shutdown).
func (m *PTYManager) CloseAll() {
	m.mu.Lock()
	list := make([]*managedPTY, 0, len(m.terms))
	for _, mp := range m.terms {
		if mp != nil {
			mp.alive = false
		}
		list = append(list, mp)
	}
	m.terms = make(map[string]*managedPTY)
	m.mu.Unlock()
	for _, mp := range list {
		if mp != nil && mp.sess != nil {
			_ = mp.sess.Close()
		}
	}
}
