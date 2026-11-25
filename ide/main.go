package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/gilvan-Borges/HiveMind/ide/internal/idecore"
	"github.com/gilvan-Borges/HiveMind/internal/remote"
	"github.com/gilvan-Borges/HiveMind/internal/version"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	smokeCheck := false
	serveBridge := false
	showVersion := false
	orbMode := false
	orbAddr := ""
	orbToken := ""
	initialDir := ""
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch arg {
		case "--version", "-version":
			showVersion = true
		case "--smoke-check":
			smokeCheck = true
		case "--serve-bridge":
			serveBridge = true
		case "--orb":
			orbMode = true
		case "--orb-addr":
			if i+1 < len(os.Args) {
				i++
				orbAddr = os.Args[i]
			}
		case "--orb-token":
			if i+1 < len(os.Args) {
				i++
				orbToken = os.Args[i]
			}
		default:
			if initialDir == "" {
				if abs, err := filepath.Abs(arg); err == nil {
					if info, err := os.Stat(abs); err == nil && info.IsDir() {
						initialDir = abs
					}
				}
			}
		}
	}
	if showVersion {
		fmt.Println("hivemind-ide", version.String())
		return
	}
	if orbMode {
		if err := runOrbWindow(orbAddr, orbToken); err != nil {
			fmt.Fprintf(os.Stderr, "erro ao iniciar HiveMind Orb: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if initialDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			if smokeCheck || serveBridge || !isNonProjectLaunchDir(cwd) {
				initialDir = cwd
			}
		}
	}

	// Headless Remote Bridge (Marco 3): keeps the mobile bridge running
	// without the IDE window. The runtime is the root-module remote package;
	// the IDE contributes its full service adapters here.
	if serveBridge {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		opts := idecore.HeadlessBridgeOptions()
		if err := remote.RunHeadless(ctx, initialDir, opts); err != nil {
			fmt.Fprintf(os.Stderr, "hivemind serve: %v\n", err)
			os.Exit(1)
		}
		return
	}

	app := NewApp(initialDir, smokeCheck)
	if smokeCheck {
		state := app.GetInitialState()
		fmt.Printf("HiveMind IDE smoke-check OK (%s): workspace=%s stack=%s\n", version.String(), state.InitialWorkspace, state.InitialProject.Stack.Summary)
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
		OnStartup:     app.startup,
		OnBeforeClose: app.beforeClose,
		OnShutdown:    app.shutdown,
		Bind: []any{
			app,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "erro ao iniciar HiveMind IDE: %v\n", err)
		os.Exit(1)
	}
}
