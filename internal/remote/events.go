package remote

import "github.com/gilvan-Borges/HiveMind/internal/orchestrator"

// This file constructs orchestrator.Event values used to synthesize lane
// events that the IDE frontend already knows how to render:
//
//   - "error"                 → rendered as an error bubble
//   - "text" (author mobile)  → rendered as a chat bubble labeled "Celular"
//   - "approval_resolved"     → handled by boot.ts to mark the gate resolved

// newErrorEvent builds an orchestrator error event for a lane whose turn
// failed to start.
func newErrorEvent(text string) orchestrator.Event {
	return orchestrator.Event{
		Kind:   "error",
		Author: "sistema",
		Label:  "Erro",
		Text:   text,
	}
}

// UserMessageEvent builds the event that echoes a mobile-sent message inside
// the IDE chat (label "Celular"). Exported for the idecore Adapter.
func UserMessageEvent(text string) orchestrator.Event {
	return orchestrator.Event{
		Kind:   "text",
		Author: "mobile",
		Label:  "Celular",
		Text:   text,
	}
}

// userMessageEvent keeps the historical intra-package name.
func userMessageEvent(text string) orchestrator.Event {
	return UserMessageEvent(text)
}

// approvalResolvedEvent marks an approval gate as resolved in every open chat.
func approvalResolvedEvent(approvalID string, approved bool) orchestrator.Event {
	text := "Aprovação recusada."
	if approved {
		text = "Aprovação concedida."
	}
	return orchestrator.Event{
		Kind:       "approval_resolved",
		Author:     "sistema",
		Label:      "Sistema",
		Text:       text,
		ApprovalID: approvalID,
	}
}
