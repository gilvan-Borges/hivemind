package main

import (
	"context"
	"fmt"

	"github.com/gilvan-Borges/HiveMind/internal/extsess"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
)

// --- Agente Residente Hive ---

// ExternalSessions lists outside processes by executable name. It does not control them.
func (a *App) ExternalSessions() ([]extsess.ExternalSession, error) {
	extsess.OnReject = func(r extsess.Reject) {
		if a != nil && a.resident != nil {
			a.resident.NoteDetectorRejected(r.ID, r.Reason, r.Fingerprint)
		}
	}
	return extsess.List()
}

// ResidentStatus returns the resident agent's live state (zero secrets).
func (a *App) ResidentStatus() resident.Status {
	if a.resident == nil {
		return resident.Status{
			State:      "ocioso",
			Enabled:    false,
			OrbEnabled: false,
			ReadOnly:   false,
			AccountID:  resident.DefaultAccountID,
			Workstream: resident.DefaultWorkstream,
		}
	}
	return a.resident.Status()
}

// ResidentSavePolicy saves Phase 2 binary & outbound host allowlists locally.
func (a *App) ResidentSavePolicy(allowedBinaries, allowedHosts []string) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SavePolicy(allowedBinaries, allowedHosts)
}

// ResidentSetSecret stores a named secret directly inside the OS credential vault.
func (a *App) ResidentSetSecret(name, value string) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SetSecret(name, value)
}

// ResidentDeleteSecret removes a named credential from the OS credential vault.
func (a *App) ResidentDeleteSecret(name string) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.DeleteSecret(name)
}

// ResidentSaveConfig persists local resident preferences (provider, model, workstream).
// ResidentSetEventTriggers turns opt-in host wakes on or off. Off does nothing.
func (a *App) ResidentSetEventTriggers(on bool) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SetEventTriggers(on)
}

// ResidentSetExperimentalUnisolated turns host-session browser and desktop on or off.
// Off keeps the refusal. On runs without OS isolation.
func (a *App) ResidentSetExperimentalUnisolated(on bool) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SetExperimentalUnisolated(on)
}

// ResidentSetIsolatedSandbox turns Windows Sandbox (.wsb) provisioning on or off.
func (a *App) ResidentSetIsolatedSandbox(on bool) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SetIsolatedSandbox(on)
}

// ResidentSetEnabled activates or disables the Agent Hive resident assistant and its floating Orb.
func (a *App) ResidentSetEnabled(on bool) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	st, err := a.resident.SetEnabled(on)
	if err != nil {
		return st, err
	}
	if !on {
		a.hideOrbWindow()
	} else if a.resident.IsOrbEnabled() {
		if err := a.launchOrbWindow(); err == nil {
			go a.orbSyncResidentSnapshot()
		}
	}
	return st, nil
}

// ResidentSetOrbEnabled shows or hides the independent floating Orb mascot and persists the preference.
func (a *App) ResidentSetOrbEnabled(on bool) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	st, err := a.resident.SetOrbEnabled(on)
	if err != nil {
		return st, err
	}
	if !on {
		a.hideOrbWindow()
	} else if a.resident.IsEnabled() {
		if err := a.launchOrbWindow(); err == nil {
			go a.orbSyncResidentSnapshot()
		}
	}
	return st, nil
}

// ResidentSetJevFailover enables or disables Jev fallback route retries for the resident assistant.
func (a *App) ResidentSetJevFailover(on bool) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SetJevFailover(on)
}

func (a *App) ResidentSaveConfig(provider, modelName, workstream string) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SaveConfig(provider, modelName, workstream)
}

// ResidentStartTurn executes a local chat turn with the Hive resident agent.
func (a *App) ResidentStartTurn(workspace, workstream, message string) (resident.TurnResult, error) {
	return a.ResidentStartTurnIn(workspace, workstream, message, "")
}

// ResidentStartTurnIn continues the shared IDE conversation sessionID.
// An empty sessionID keeps the lane the IDE already bound.
func (a *App) ResidentStartTurnIn(workspace, workstream, message, sessionID string) (resident.TurnResult, error) {
	if a.resident == nil {
		return resident.TurnResult{}, fmt.Errorf("agente residente indisponível")
	}
	if a.mobile != nil && a.mobile.hub != nil {
		a.mobile.hub.RegisterRun(resident.LaneID, workspace, "hive-resident", message, sessionID, resident.RunMode)
	}
	res, err := a.resident.RunTurnIn(context.Background(), workspace, workstream, message, sessionID, resident.OriginLocal)
	if err != nil && a.mobile != nil && a.mobile.hub != nil {
		a.mobile.hub.HandleRunError(resident.LaneID, workspace, err)
	}
	return res, err
}

// ResidentBindSession points the Orb at the IDE session the user is looking at.
func (a *App) ResidentBindSession(workspace, sessionID, laneID string) (resident.Conversation, error) {
	if a.resident == nil {
		return resident.Conversation{}, fmt.Errorf("agente residente indisponível")
	}
	conv, err := a.resident.BindSharedSession(workspace, sessionID, laneID)
	if err == nil {
		go a.orbSyncResidentSnapshot()
	}
	return conv, err
}

// ResidentListConversations lists the threads shared by the IDE and the Agent Hive.
func (a *App) ResidentListConversations(workspace string) ([]resident.ConversationSummary, error) {
	if a.resident == nil {
		return nil, nil
	}
	return a.resident.ListSharedConversations(workspace)
}

// ResidentOpenConversation loads one shared thread into the Hive and the Orb.
func (a *App) ResidentOpenConversation(id string) (resident.Conversation, error) {
	if a.resident == nil {
		return resident.Conversation{}, fmt.Errorf("agente residente indisponível")
	}
	conv, err := a.resident.OpenSharedConversation(id)
	if err == nil {
		go a.orbSyncResidentSnapshot()
	}
	return conv, err
}

// ResidentCancel stops the active resident turn.
func (a *App) ResidentCancel() bool {
	if a.resident == nil {
		return false
	}
	return a.resident.Cancel()
}

// ResidentSessionMessages loads short-term conversation messages for the active account/workstream.
func (a *App) ResidentSessionMessages(workstream string) ([]resident.LocalTurnMessage, error) {
	if a.resident == nil {
		return nil, nil
	}
	return a.resident.SessionMessages(workstream)
}

// ResidentClearSession clears the short-term conversation history for workstream.
func (a *App) ResidentClearSession(workstream string) error {
	if a.resident == nil {
		return nil
	}
	return a.resident.ClearSession(workstream)
}

// ResidentListMemory lists personal memory facts isolated to the active account.
func (a *App) ResidentListMemory(workstream string) ([]resident.MemoryFact, error) {
	if a.resident == nil {
		return nil, nil
	}
	return a.resident.ListMemory(workstream)
}

// ResidentAddMemory saves an explicit user fact to the active account's local memory.
func (a *App) ResidentAddMemory(workstream, text string, pinned bool) (resident.MemoryFact, error) {
	if a.resident == nil {
		return resident.MemoryFact{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.AddMemory(workstream, text, pinned)
}

// ResidentDeleteMemory deletes a personal memory fact by ID for the active account.
func (a *App) ResidentDeleteMemory(id string) (bool, error) {
	if a.resident == nil {
		return false, nil
	}
	return a.resident.DeleteMemory(id)
}

// ResidentListRoutines returns structured routines. There is no command field.
func (a *App) ResidentListRoutines() ([]resident.Routine, error) {
	if a.resident == nil {
		return nil, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.ListRoutines()
}

// ResidentSaveRoutine stores one routine from the local UI and rearms the timer.
func (a *App) ResidentSaveRoutine(expr, timezone, text, workstream string, classes []string) (resident.Routine, error) {
	if a.resident == nil {
		return resident.Routine{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SaveRoutine(resident.Routine{
		Expr:       expr,
		Timezone:   timezone,
		Text:       text,
		Workstream: workstream,
		Classes:    classes,
		Enabled:    true,
	})
}

// ResidentSetRoutineEnabled pauses or resumes one routine.
func (a *App) ResidentSetRoutineEnabled(id string, enabled bool) error {
	if a.resident == nil {
		return fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SetRoutineEnabled(id, enabled)
}

// ResidentDeleteRoutine removes one routine.
func (a *App) ResidentDeleteRoutine(id string) error {
	if a.resident == nil {
		return fmt.Errorf("agente residente indisponível")
	}
	return a.resident.DeleteRoutine(id)
}

// ResidentReviewSkill stores the SHA-256 of a native skill the person reviewed.
func (a *App) ResidentReviewSkill(name string) (string, error) {
	if a.resident == nil {
		return "", fmt.Errorf("agente residente indisponível")
	}
	return a.resident.ReviewSkill(name)
}

// ResidentAuditLog returns recent audit entries (read-only).
func (a *App) ResidentAuditLog(limit int) ([]resident.AuditEntry, error) {
	if a.resident == nil {
		return nil, nil
	}
	return a.resident.AuditLog(limit)
}

// ResidentGetSoul reads the resident's SOUL.md personality/preferences file.
func (a *App) ResidentGetSoul() (string, error) {
	if a.resident == nil {
		return "", nil
	}
	return a.resident.GetSoul()
}

// ResidentSaveSoul updates the resident's bounded SOUL.md preferences file.
func (a *App) ResidentSaveSoul(content string) error {
	if a.resident == nil {
		return fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SaveSoul(content)
}
