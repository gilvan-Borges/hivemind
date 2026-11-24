package resident

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// maxConversationMessages caps one transcript. Older lines drop off the
	// head. shortcut: 400 lines, raise if a single thread needs the full log.
	maxConversationMessages = 400
	maxConversationText     = 32 * 1024
)

// ConvMessage is one line of the shared IDE / Agent Hive transcript.
// ID is stable: appending the same ID again does not duplicate the line.
type ConvMessage struct {
	ID     string    `json:"id"`
	Role   string    `json:"role"` // user | model | system
	Author string    `json:"author,omitempty"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
	Kind   string    `json:"kind,omitempty"` // loop, when the line is a loop summary
}

// Conversation is the shared transcript both the IDE and the Agent Hive read.
// It lives under the resident account directory, outside any git checkout,
// so a turn started while the IDE is closed is still there when it opens.
type Conversation struct {
	ID         string        `json:"id"`
	Title      string        `json:"title"`
	Workspace  string        `json:"workspace,omitempty"`
	Workstream string        `json:"workstream,omitempty"`
	Kind       string        `json:"kind"`   // hive | team
	Origin     string        `json:"origin"` // ide | hive | orb | slack | webhook | routine | remote
	SessionID  string        `json:"sessionId,omitempty"`
	LaneID     string        `json:"laneId,omitempty"`
	UpdatedAt  time.Time     `json:"updatedAt"`
	Messages   []ConvMessage `json:"messages,omitempty"`
}

// ConversationSummary is the list row. Messages stay on disk until Get.
type ConversationSummary struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Workspace    string    `json:"workspace,omitempty"`
	Workstream   string    `json:"workstream,omitempty"`
	Kind         string    `json:"kind"`
	Origin       string    `json:"origin"`
	SessionID    string    `json:"sessionId,omitempty"`
	LaneID       string    `json:"laneId,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
	MessageCount int       `json:"messageCount"`
}

type activeBinding struct {
	ByWorkspace map[string]string `json:"byWorkspace"`
}

// ConversationStore is the single transcript log for the Agent Hive and the IDE.
type ConversationStore struct {
	dir string
	mu  sync.Mutex
}

// NewConversationStore opens the account-scoped transcript directory.
func NewConversationStore(accountDir string) *ConversationStore {
	return &ConversationStore{dir: filepath.Join(strings.TrimSpace(accountDir), "conversations")}
}

func (s *ConversationStore) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// BindSession returns the conversation linked to a workspace session, creating
// it when the IDE (or the Hive) first touches that session.
func (s *ConversationStore) BindSession(workspace, sessionID, laneID, kind, origin, title string) (Conversation, error) {
	if s == nil {
		return Conversation{}, fmt.Errorf("conversa indisponível")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Conversation{}, fmt.Errorf("session id vazio")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return Conversation{}, err
	}
	if existing, ok := s.findBySessionLocked(sessionID); ok {
		if laneID != "" {
			existing.LaneID = laneID
		}
		if strings.TrimSpace(workspace) != "" {
			existing.Workspace = workspace
		}
		if title != "" && (existing.Title == "" || existing.Title == "Conversa") {
			existing.Title = cleanTitle(title)
		}
		if err := s.saveLocked(&existing); err != nil {
			return Conversation{}, err
		}
		_ = s.setActiveLocked(existing.Workspace, existing.ID)
		return existing, nil
	}
	kind = normalizeKind(kind)
	origin = normalizeOrigin(origin, kind)
	conv := Conversation{
		ID:        newShortID("conv"),
		Title:     cleanTitle(title),
		Workspace: strings.TrimSpace(workspace),
		Kind:      kind,
		Origin:    origin,
		SessionID: sessionID,
		LaneID:    strings.TrimSpace(laneID),
		UpdatedAt: time.Now().UTC(),
	}
	if err := s.saveLocked(&conv); err != nil {
		return Conversation{}, err
	}
	_ = s.setActiveLocked(conv.Workspace, conv.ID)
	return conv, nil
}

// OpenFreshHive starts a hive thread that is not the IDE conversation already open.
func (s *ConversationStore) OpenFreshHive(workspace, workstream, origin string) (Conversation, error) {
	if s == nil {
		return Conversation{}, fmt.Errorf("conversa indisponível")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return Conversation{}, err
	}
	conv := Conversation{
		ID:         newShortID("conv"),
		Title:      "Conversa",
		Workspace:  strings.TrimSpace(workspace),
		Workstream: SanitizeWorkstream(workstream),
		Kind:       "hive",
		Origin:     normalizeOrigin(origin, "hive"),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.saveLocked(&conv); err != nil {
		return Conversation{}, err
	}
	_ = s.setActiveLocked(conv.Workspace, conv.ID)
	return conv, nil
}

// EnsureActive returns the conversation the Hive should continue for this
// workspace and workstream. An empty session keeps the latest hive thread,
// or opens one and imports the legacy workstream file the first time.
func (s *ConversationStore) EnsureActive(workspace, workstream, origin string, legacy []LocalTurnMessage) (Conversation, error) {
	if s == nil {
		return Conversation{}, fmt.Errorf("conversa indisponível")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return Conversation{}, err
	}
	workspace = strings.TrimSpace(workspace)
	workstream = SanitizeWorkstream(workstream)
	if id := s.activeIDLocked(workspace); id != "" {
		if conv, err := s.loadLocked(id); err == nil {
			return conv, nil
		}
	}
	conv := Conversation{
		ID:         newShortID("conv"),
		Title:      "Conversa",
		Workspace:  workspace,
		Workstream: workstream,
		Kind:       "hive",
		Origin:     normalizeOrigin(origin, "hive"),
		UpdatedAt:  time.Now().UTC(),
	}
	for i, m := range legacy {
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}
		role := m.Role
		if role != "user" && role != "model" {
			role = "model"
		}
		conv.Messages = append(conv.Messages, ConvMessage{
			ID:     fmt.Sprintf("legacy:%s:%d", conv.ID, i),
			Role:   role,
			Author: authorFor(role, ""),
			Text:   clipText(text),
			At:     m.At,
		})
	}
	if len(conv.Messages) > 0 {
		conv.Title = cleanTitle(firstUserText(conv.Messages))
	}
	if err := s.saveLocked(&conv); err != nil {
		return Conversation{}, err
	}
	_ = s.setActiveLocked(workspace, conv.ID)
	return conv, nil
}

// AppendMessage adds one line. A repeated ID is a no-op and inserted is false.
func (s *ConversationStore) AppendMessage(id string, msg ConvMessage) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("conversa indisponível")
	}
	id = strings.TrimSpace(id)
	msg.ID = strings.TrimSpace(msg.ID)
	msg.Text = clipText(strings.TrimSpace(msg.Text))
	if id == "" || msg.ID == "" || msg.Text == "" {
		return false, nil
	}
	if msg.Role != "user" && msg.Role != "model" && msg.Role != "system" {
		msg.Role = "model"
	}
	if msg.Author == "" {
		msg.Author = authorFor(msg.Role, "")
	}
	if msg.At.IsZero() {
		msg.At = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, err := s.loadLocked(id)
	if err != nil {
		return false, err
	}
	for _, existing := range conv.Messages {
		if existing.ID == msg.ID {
			return false, nil
		}
	}
	conv.Messages = append(conv.Messages, msg)
	if len(conv.Messages) > maxConversationMessages {
		conv.Messages = conv.Messages[len(conv.Messages)-maxConversationMessages:]
	}
	if conv.Title == "" || conv.Title == "Conversa" {
		if t := firstUserText(conv.Messages); t != "" {
			conv.Title = cleanTitle(t)
		}
	}
	conv.UpdatedAt = msg.At
	if err := s.saveLocked(&conv); err != nil {
		return false, err
	}
	return true, nil
}

// AttachSession records the sessionx id used by the IDE session list.
func (s *ConversationStore) AttachSession(id, workspace, sessionID, laneID string) error {
	if s == nil {
		return fmt.Errorf("conversa indisponível")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, err := s.loadLocked(id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(workspace) != "" {
		conv.Workspace = workspace
	}
	if strings.TrimSpace(sessionID) != "" {
		conv.SessionID = sessionID
	}
	if strings.TrimSpace(laneID) != "" {
		conv.LaneID = laneID
	}
	conv.UpdatedAt = time.Now().UTC()
	if err := s.saveLocked(&conv); err != nil {
		return err
	}
	return s.setActiveLocked(conv.Workspace, conv.ID)
}

// SetActive makes this conversation the one the Orb and the IDE continue.
func (s *ConversationStore) SetActive(workspace, id string) error {
	if s == nil {
		return fmt.Errorf("conversa indisponível")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.loadLocked(id); err != nil {
		return err
	}
	return s.setActiveLocked(workspace, id)
}

// ActiveID is the conversation the Orb shows for this workspace.
func (s *ConversationStore) ActiveID(workspace string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeIDLocked(workspace)
}

// Get loads one conversation including its messages.
func (s *ConversationStore) Get(id string) (Conversation, error) {
	if s == nil {
		return Conversation{}, fmt.Errorf("conversa indisponível")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(id)
}

// List returns conversations newest first. Empty workspace returns every thread.
func (s *ConversationStore) List(workspace string) ([]ConversationSummary, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	workspace = strings.TrimSpace(workspace)
	out := make([]ConversationSummary, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || name == "active.json" {
			continue
		}
		conv, err := s.loadLocked(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		if workspace != "" && conv.Workspace != "" && conv.Workspace != workspace {
			continue
		}
		out = append(out, summaryOf(conv))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// Recent returns the last n messages for the prompt. n <= 0 means 12.
func (s *ConversationStore) Recent(id string, n int) []ConvMessage {
	if s == nil {
		return nil
	}
	if n <= 0 {
		n = 12
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, err := s.loadLocked(id)
	if err != nil || len(conv.Messages) == 0 {
		return nil
	}
	if len(conv.Messages) > n {
		conv.Messages = conv.Messages[len(conv.Messages)-n:]
	}
	out := make([]ConvMessage, len(conv.Messages))
	copy(out, conv.Messages)
	return out
}

// ClearMessages drops the transcript but keeps the conversation id.
func (s *ConversationStore) ClearMessages(id string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	conv, err := s.loadLocked(id)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	conv.Messages = nil
	conv.UpdatedAt = time.Now().UTC()
	return s.saveLocked(&conv)
}

func (s *ConversationStore) findBySessionLocked(sessionID string) (Conversation, bool) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return Conversation{}, false
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || name == "active.json" {
			continue
		}
		conv, err := s.loadLocked(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		if conv.SessionID == sessionID {
			return conv, true
		}
	}
	return Conversation{}, false
}

func (s *ConversationStore) loadLocked(id string) (Conversation, error) {
	id = strings.TrimSpace(id)
	if !validConvID(id) {
		return Conversation{}, fmt.Errorf("conversa inválida")
	}
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return Conversation{}, err
	}
	var conv Conversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return Conversation{}, err
	}
	if conv.ID == "" {
		conv.ID = id
	}
	return conv, nil
}

func (s *ConversationStore) saveLocked(conv *Conversation) error {
	if conv == nil || !validConvID(conv.ID) {
		return fmt.Errorf("conversa inválida")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(conv, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(conv.ID) + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(conv.ID))
}

func (s *ConversationStore) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *ConversationStore) activePath() string {
	return filepath.Join(s.dir, "active.json")
}

func (s *ConversationStore) activeIDLocked(workspace string) string {
	data, err := os.ReadFile(s.activePath())
	if err != nil {
		return ""
	}
	var binding activeBinding
	if json.Unmarshal(data, &binding) != nil || binding.ByWorkspace == nil {
		return ""
	}
	return binding.ByWorkspace[workspaceKey(workspace)]
}

func (s *ConversationStore) setActiveLocked(workspace, id string) error {
	binding := activeBinding{ByWorkspace: map[string]string{}}
	if data, err := os.ReadFile(s.activePath()); err == nil {
		_ = json.Unmarshal(data, &binding)
	}
	if binding.ByWorkspace == nil {
		binding.ByWorkspace = map[string]string{}
	}
	binding.ByWorkspace[workspaceKey(workspace)] = id
	raw, err := json.MarshalIndent(binding, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.activePath() + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.activePath())
}

func summaryOf(conv Conversation) ConversationSummary {
	return ConversationSummary{
		ID:           conv.ID,
		Title:        conv.Title,
		Workspace:    conv.Workspace,
		Workstream:   conv.Workstream,
		Kind:         conv.Kind,
		Origin:       conv.Origin,
		SessionID:    conv.SessionID,
		LaneID:       conv.LaneID,
		UpdatedAt:    conv.UpdatedAt,
		MessageCount: len(conv.Messages),
	}
}

func workspaceKey(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "_"
	}
	return filepath.Clean(workspace)
}

func validConvID(id string) bool {
	if id == "" || id == "active" || strings.Contains(id, "..") || strings.ContainsAny(id, `/\:*?"<>|`) {
		return false
	}
	return true
}

func normalizeKind(kind string) string {
	if strings.EqualFold(strings.TrimSpace(kind), "team") {
		return "team"
	}
	return "hive"
}

func normalizeOrigin(origin, kind string) string {
	origin = strings.TrimSpace(strings.ToLower(origin))
	switch origin {
	case "ide", "hive", "orb", "slack", "webhook", "routine", "remote", "local", "loop":
		return origin
	}
	if kind == "team" {
		return "ide"
	}
	return "hive"
}

func authorFor(role, author string) string {
	if strings.TrimSpace(author) != "" {
		return author
	}
	if role == "user" {
		return "Você"
	}
	return "Agent Hive"
}

func firstUserText(msgs []ConvMessage) string {
	for _, m := range msgs {
		if m.Role == "user" && strings.TrimSpace(m.Text) != "" {
			return m.Text
		}
	}
	if len(msgs) > 0 {
		return msgs[0].Text
	}
	return ""
}

func cleanTitle(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "Conversa"
	}
	line := raw
	if i := strings.IndexAny(raw, "\r\n"); i >= 0 {
		line = raw[:i]
	}
	line = strings.TrimSpace(line)
	if len([]rune(line)) > 80 {
		line = string([]rune(line)[:80]) + "…"
	}
	if line == "" {
		return "Conversa"
	}
	return line
}

// NewMessageID returns an id for one transcript line.
func NewMessageID(prefix string) string {
	return newShortID(prefix)
}

func clipText(text string) string {
	if len(text) <= maxConversationText {
		return text
	}
	return text[:maxConversationText] + "\n…(trecho truncado)"
}
