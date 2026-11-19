package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/gilvan-Borges/HiveMind/ide/internal/remote"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	smokeCheck := false
	serveBridge := false
	initialDir := ""
	for _, arg := range os.Args[1:] {
		if arg == "--smoke-check" {
			smokeCheck = true
			continue
		}
		if arg == "--serve-bridge" {
			serveBridge = true
			continue
		}
		if initialDir == "" {
			if abs, err := filepath.Abs(arg); err == nil {
				if info, err := os.Stat(abs); err == nil && info.IsDir() {
					initialDir = abs
				}
			}
		}
	}
	if initialDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			initialDir = cwd
		}
	}

	// Headless Remote Bridge (Marco 3: `hivemind serve` spawns this mode) —
	// keeps the mobile bridge running without the IDE window.
	if serveBridge {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := remote.RunHeadless(ctx, initialDir); err != nil {
			fmt.Fprintf(os.Stderr, "hivemind serve: %v\n", err)
			os.Exit(1)
		}
		return
	}

	app := NewApp(initialDir, smokeCheck)
	if smokeCheck {
		state := app.GetInitialState()
		fmt.Printf("HiveMind IDE smoke-check OK: workspace=%s stack=%s\n", state.InitialWorkspace, state.InitialProject.Stack.Summary)
		return
	}

	err := wails.Run(&options.App{
		Title:            "HiveMind IDE",
		Width:            1400,
		Height:           860,
		MinWidth:         980,
		MinHeight:        620,
		Frameless:        true,
		BackgroundColour: &options.RGBA{R: 4, G: 5, B: 7, A: 255},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []any{
			app,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "erro ao iniciar HiveMind IDE: %v\n", err)
		os.Exit(1)
	}
}
