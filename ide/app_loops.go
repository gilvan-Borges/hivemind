package main

import (
	"context"
	"fmt"

	"github.com/gilvan-Borges/HiveMind/internal/loopx"
	"github.com/gilvan-Borges/HiveMind/internal/resident"
)

// ResidentSetAllowFullUnattended is the only writer of the unattended Full switch.
func (a *App) ResidentSetAllowFullUnattended(on bool) (resident.Status, error) {
	if a.resident == nil {
		return resident.Status{}, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.SetAllowFullUnattended(on)
}

// ResidentListLoops lists loops in the open project.
func (a *App) ResidentListLoops(workspace string) ([]loopx.Summary, error) {
	if a.resident == nil {
		return nil, fmt.Errorf("agente residente indisponível")
	}
	return a.resident.ListLoops(workspace)
}

// ResidentStopLoop stops a loop. It does not merge.
func (a *App) ResidentStopLoop(workspace, id string) error {
	if a.resident == nil {
		return fmt.Errorf("agente residente indisponível")
	}
	return a.resident.StopLoop(workspace, id)
}

// ResidentPauseLoop pauses a loop until resume.
func (a *App) ResidentPauseLoop(workspace, id string) error {
	if a.resident == nil {
		return fmt.Errorf("agente residente indisponível")
	}
	return a.resident.PauseLoop(workspace, id)
}

// ResidentResumeLoop continues a paused loop as an accompanied run.
func (a *App) ResidentResumeLoop(workspace, id string) error {
	if a.resident == nil {
		return fmt.Errorf("agente residente indisponível")
	}
	return a.resident.ResumeLoop(context.Background(), workspace, id)
}
