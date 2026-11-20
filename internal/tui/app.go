package tui

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/gilvan-Borges/HiveMind/internal/gitx"
	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// Options opens the full-screen session.
type Options struct {
	Workspace string
	TeamName  string
}

// Run starts the alternate-screen TUI. Diagnostics go to the external workspace logs directory.
func Run(ctx context.Context, opt Options) error {
	logsDir := workspace.LogsDir(opt.Workspace)
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		return err
	}
	_ = gitx.EnsureHiveIgnore(opt.Workspace)
	logFile, err := os.OpenFile(filepath.Join(logsDir, "hivemind.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err == nil {
		log.SetOutput(logFile)
		prevErr := os.Stderr
		os.Stderr = logFile
		defer func() {
			os.Stderr = prevErr
			logFile.Close()
		}()
	}
	m := newModel(ctx, opt)
	// The themed screen is the interface. NO_COLOR would strip it to bold-only
	// text, including in terminals that set the variable for log output.
	_ = os.Unsetenv("NO_COLOR")
	_ = os.Setenv("COLORTERM", "truecolor")
	prog := tea.NewProgram(m, tea.WithColorProfile(colorprofile.TrueColor))
	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("interface: %w", err)
	}
	return nil
}
