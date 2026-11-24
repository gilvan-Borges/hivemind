package idecore

import (
	"context"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/orchestrator"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
	"github.com/gilvan-Borges/HiveMind/internal/sessionx"
)

// sharedTurn is one Hive turn already linked to the conversation the IDE shows.
type sharedTurn struct {
	Conv    resident.Conversation
	UserID  string
	ReplyID string
	History []resident.LocalTurnMessage
}

func (m *ResidentManager) conversations() *resident.ConversationStore {
	return resident.NewConversationStore(m.memoryForCurrentAccount().AccountDir())
}

// freshHiveSession is the session id the Agent Hive panel sends when the
// turn is a new conversation. It must not join the IDE tab that is focused.
// shortcut: sentinel string, replace with a bool if RunTurnIn grows a flag.
const freshHiveSession = "hive:new"

// prepareSharedTurn opens or continues the transcript and stores the user line
// before the model runs, so a crash still leaves the message on disk.
func (m *ResidentManager) prepareSharedTurn(workspace, workstream, message, sessionID, origin string) (sharedTurn, error) {
	store := m.conversations()
	m.mu.Lock()
	laneID := m.boundLane
	boundKind := m.boundKind
	boundSession := m.boundSession
	fresh := strings.TrimSpace(sessionID) == freshHiveSession
	if fresh {
		sessionID = ""
		// A focused team lane stays put. The new thread only keeps a lane
		// when the caller already cleared the bound session onto that lane.
		if boundSession != "" {
			laneID = ""
		}
	} else if strings.TrimSpace(sessionID) == "" {
		sessionID = boundSession
	}
	if workspace == "" {
		workspace = m.boundWorkspace
	}
	m.mu.Unlock()

	var (
		conv resident.Conversation
		err  error
	)
	if fresh {
		conv, err = store.OpenFreshHive(workspace, workstream, origin)
	} else if strings.TrimSpace(sessionID) != "" {
		kind := "team"
		if boundKind == "hive" && sessionID == boundSession {
			kind = "hive"
		} else if workspace != "" {
			if detail, dErr := sessionx.LoadSessionDetail(context.Background(), workspace, sessionID); dErr == nil && detail.Kind == "hive" {
				kind = "hive"
			}
		}
		conv, err = store.BindSession(workspace, sessionID, laneID, kind, origin, message)
	} else {
		legacy, _ := m.memoryForCurrentAccount().LoadSessionMessages(workstream, 40)
		conv, err = store.EnsureActive(workspace, workstream, origin, legacy)
	}
	if err != nil {
		return sharedTurn{}, err
	}
	hist := store.Recent(conv.ID, 12)
	history := make([]resident.LocalTurnMessage, 0, len(hist))
	for _, msg := range hist {
		history = append(history, resident.LocalTurnMessage{Role: msg.Role, Text: msg.Text, At: msg.At})
	}
	userID := resident.NewMessageID("hu")
	replyID := resident.NewMessageID("hm")
	if _, err := store.AppendMessage(conv.ID, resident.ConvMessage{
		ID: userID, Role: "user", Author: "Você", Text: message,
	}); err != nil {
		return sharedTurn{}, err
	}
	if strings.TrimSpace(workspace) != "" {
		kind := ""
		if conv.Kind == "hive" {
			kind = "hive"
		}
		title := conv.Title
		if title == "" || title == "Conversa" {
			title = message
		}
		if sid, sErr := sessionx.EnsureChatSession(workspace, conv.SessionID, title, kind); sErr == nil && sid != "" {
			conv.SessionID = sid
			if laneID != "" {
				conv.LaneID = laneID
			}
			_ = store.AttachSession(conv.ID, workspace, sid, laneID)
			_ = sessionx.AppendChatLine(workspace, sid, userID, "user", "Você", message)
		}
	}
	m.mu.Lock()
	m.boundWorkspace = conv.Workspace
	m.boundSession = conv.SessionID
	m.boundLane = laneID
	m.boundKind = conv.Kind
	m.mu.Unlock()
	return sharedTurn{Conv: conv, UserID: userID, ReplyID: replyID, History: history}, nil
}

func (m *ResidentManager) finishSharedTurn(shared sharedTurn, finalText string) {
	finalText = strings.TrimSpace(finalText)
	if finalText == "" || shared.Conv.ID == "" {
		return
	}
	store := m.conversations()
	_, _ = store.AppendMessage(shared.Conv.ID, resident.ConvMessage{
		ID: shared.ReplyID, Role: "model", Author: "Agent Hive", Text: finalText,
	})
	if shared.Conv.Workspace != "" && shared.Conv.SessionID != "" {
		_ = sessionx.AppendChatLine(shared.Conv.Workspace, shared.Conv.SessionID, shared.ReplyID, "model", "Agent Hive", finalText)
	}
}

// mirrorIDELine copies one IDE timeline line into the shared transcript.
// The same message ID does not create a second line.
func (m *ResidentManager) mirrorIDELine(workspace, laneID, sessionID, role, author, text, messageID string) {
	if m == nil {
		return
	}
	text = strings.TrimSpace(text)
	messageID = strings.TrimSpace(messageID)
	sessionID = strings.TrimSpace(sessionID)
	if text == "" || messageID == "" || sessionID == "" {
		return
	}
	store := m.conversations()
	conv, err := store.BindSession(workspace, sessionID, laneID, "team", "ide", text)
	if err != nil {
		return
	}
	inserted, err := store.AppendMessage(conv.ID, resident.ConvMessage{
		ID: messageID, Role: role, Author: author, Text: text, At: time.Now().UTC(),
	})
	if err != nil || !inserted {
		return
	}
	m.mu.Lock()
	m.boundWorkspace = conv.Workspace
	m.boundSession = conv.SessionID
	if laneID != "" {
		m.boundLane = laneID
	}
	m.boundKind = conv.Kind
	m.mu.Unlock()
	if m.sessions == nil || m.sessions.Emit == nil {
		return
	}
	label := author
	if label == "" {
		label = "IDE"
	}
	m.sessions.Emit(LaneEvent{
		LaneID: resident.LaneID,
		Team:   "hive-resident",
		Event: orchestrator.Event{
			Kind:           "shared_message",
			Author:         author,
			Label:          label,
			Text:           text,
			Role:           role,
			Origin:         "ide",
			MessageID:      messageID,
			ConversationID: conv.ID,
			SessionID:      sessionID,
		},
	})
}

// BindSharedSession points the Orb and the Hive tab at the IDE session.
func (m *ResidentManager) BindSharedSession(workspace, sessionID, laneID string) (resident.Conversation, error) {
	if strings.TrimSpace(sessionID) == "" {
		m.mu.Lock()
		m.boundWorkspace = strings.TrimSpace(workspace)
		m.boundSession = ""
		m.boundLane = strings.TrimSpace(laneID)
		m.boundKind = ""
		m.mu.Unlock()
		return resident.Conversation{}, nil
	}
	conv, err := m.conversations().BindSession(workspace, sessionID, laneID, "team", "ide", "")
	if err != nil {
		return resident.Conversation{}, err
	}
	m.mu.Lock()
	m.boundWorkspace = conv.Workspace
	m.boundSession = conv.SessionID
	m.boundLane = conv.LaneID
	m.boundKind = conv.Kind
	m.mu.Unlock()
	return conv, nil
}

// ListSharedConversations returns the threads the Hive and the IDE share.
func (m *ResidentManager) ListSharedConversations(workspace string) ([]resident.ConversationSummary, error) {
	return m.conversations().List(workspace)
}

// OpenSharedConversation loads one thread and makes it the active one.
func (m *ResidentManager) OpenSharedConversation(id string) (resident.Conversation, error) {
	store := m.conversations()
	conv, err := store.Get(id)
	if err != nil {
		return resident.Conversation{}, err
	}
	if err := store.SetActive(conv.Workspace, conv.ID); err != nil {
		return resident.Conversation{}, err
	}
	m.mu.Lock()
	m.boundWorkspace = conv.Workspace
	m.boundSession = conv.SessionID
	m.boundLane = conv.LaneID
	m.boundKind = conv.Kind
	m.mu.Unlock()
	return conv, nil
}

func (m *ResidentManager) activeConversation() (resident.Conversation, bool) {
	store := m.conversations()
	m.mu.Lock()
	ws := m.boundWorkspace
	m.mu.Unlock()
	id := store.ActiveID(ws)
	if id == "" {
		id = store.ActiveID("")
	}
	if id == "" {
		return resident.Conversation{}, false
	}
	conv, err := store.Get(id)
	if err != nil {
		return resident.Conversation{}, false
	}
	return conv, true
}
