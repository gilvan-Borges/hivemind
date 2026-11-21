package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/ide/internal/orb"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	orbCollapsedW = 112
	orbCollapsedH = 118
	orbExpandedW  = 356
	orbExpandedH  = 430
)

// OrbApp is the Wails backend bound inside the independent floating Orb window (`--orb`).
type OrbApp struct {
	ctx      context.Context
	addr     string
	token    string
	client   *http.Client
	mu       sync.Mutex
	snap     orb.OrbSnapshot
	expanded bool
	cancel   context.CancelFunc
}

func newOrbApp(addr, token string) *OrbApp {
	return &OrbApp{
		addr:   strings.TrimSpace(addr),
		token:  strings.TrimSpace(token),
		client: &http.Client{Timeout: 4 * time.Second},
		snap: orb.OrbSnapshot{
			Mood:       "idle",
			IDEVisible: true,
			Items:      []orb.OrbNotice{},
		},
	}
}

func (o *OrbApp) startup(ctx context.Context) {
	o.ctx = ctx
	o.positionBottomRight(orbCollapsedW, orbCollapsedH)
	streamCtx, cancel := context.WithCancel(ctx)
	o.mu.Lock()
	o.cancel = cancel
	o.mu.Unlock()
	go o.streamLoop(streamCtx)
}

func (o *OrbApp) shutdown(_ context.Context) {
	o.mu.Lock()
	cancel := o.cancel
	o.cancel = nil
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (o *OrbApp) positionBottomRight(w, h int) {
	if o.ctx == nil {
		return
	}
	screens, err := wailsrt.ScreenGetAll(o.ctx)
	if err != nil || len(screens) == 0 {
		return
	}
	target := screens[0]
	for _, sc := range screens {
		if sc.IsCurrent || sc.IsPrimary {
			target = sc
			break
		}
	}
	sw := target.Size.Width
	sh := target.Size.Height
	if sw <= 0 || sh <= 0 {
		return
	}
	x := sw - w - 24
	y := sh - h - 72
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	wailsrt.WindowSetPosition(o.ctx, x, y)
}

func (o *OrbApp) streamLoop(ctx context.Context) {
	if o.addr == "" {
		return
	}
	url := fmt.Sprintf("http://%s/events?token=%s", o.addr, o.token)
	failCount := 0
	for {
		if ctx.Err() != nil {
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			failCount++
			if failCount >= 4 {
				// Main IDE process exited; close Orb so no orphan window remains.
				if o.ctx != nil {
					wailsrt.Quit(o.ctx)
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
				continue
			}
		}
		failCount = 0
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			raw := strings.TrimPrefix(line, "data: ")
			var snap orb.OrbSnapshot
			if json.Unmarshal([]byte(raw), &snap) == nil {
				o.mu.Lock()
				o.snap = snap
				o.mu.Unlock()
				if o.ctx != nil {
					wailsrt.EventsEmit(o.ctx, "hivemind:orb-state", snap)
				}
			}
		}
		_ = resp.Body.Close()
		if ctx.Err() != nil {
			return
		}
		failCount++
		if failCount >= 3 {
			if o.ctx != nil {
				wailsrt.Quit(o.ctx)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(800 * time.Millisecond):
		}
	}
}

func (o *OrbApp) postAction(act orb.OrbAction) error {
	if o.addr == "" {
		return nil
	}
	data, err := json.Marshal(act)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("http://%s/action", o.addr), bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+o.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// IsOrbMode lets the frontend detect it is running inside the floating Orb window.
func (o *OrbApp) IsOrbMode() bool {
	return true
}

// GetOrbState returns the latest synchronized snapshot from the main IDE.
func (o *OrbApp) GetOrbState() orb.OrbSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.snap
}

// SetOrbExpanded resizes the floating Orb window while keeping its bottom-right anchor stable.
func (o *OrbApp) SetOrbExpanded(expanded bool) {
	if o.ctx == nil {
		return
	}
	o.mu.Lock()
	prev := o.expanded
	o.expanded = expanded
	o.mu.Unlock()
	if prev == expanded {
		return
	}
	curX, curY := wailsrt.WindowGetPosition(o.ctx)
	curW, curH := wailsrt.WindowGetSize(o.ctx)
	targetW, targetH := orbCollapsedW, orbCollapsedH
	if expanded {
		targetW, targetH = orbExpandedW, orbExpandedH
	}
	nextX := curX + (curW - targetW)
	nextY := curY + (curH - targetH)
	if nextX < 0 {
		nextX = 0
	}
	if nextY < 0 {
		nextY = 0
	}
	wailsrt.WindowSetSize(o.ctx, targetW, targetH)
	wailsrt.WindowSetPosition(o.ctx, nextX, nextY)
}

// ShowIDE restores and focuses the main HiveMind IDE window.
func (o *OrbApp) ShowIDE() error {
	return o.postAction(orb.OrbAction{Action: "show_ide"})
}

// OpenNotice restores the IDE and navigates straight to the clicked notification target.
func (o *OrbApp) OpenNotice(id string) error {
	return o.postAction(orb.OrbAction{Action: "open_notice", NoticeID: id})
}

// DismissNotice removes one item from the Orb feed.
func (o *OrbApp) DismissNotice(id string) error {
	return o.postAction(orb.OrbAction{Action: "dismiss_notice", NoticeID: id})
}

// ClearNotices clears all items from the Orb feed.
func (o *OrbApp) ClearNotices() error {
	return o.postAction(orb.OrbAction{Action: "clear_notices"})
}

// QuitAll shuts down both the main IDE and the floating Orb.
func (o *OrbApp) QuitAll() error {
	_ = o.postAction(orb.OrbAction{Action: "quit_all"})
	if o.ctx != nil {
		wailsrt.Quit(o.ctx)
	}
	return nil
}

// CloseOrbOnly closes just the floating Orb window while keeping the IDE open.
func (o *OrbApp) CloseOrbOnly() {
	if o.ctx != nil {
		wailsrt.Quit(o.ctx)
	}
}

func runOrbWindow(addr, token string) error {
	orbApp := newOrbApp(addr, token)
	return wails.Run(&options.App{
		Title:            "HiveMind Orb",
		Width:            orbCollapsedW,
		Height:           orbCollapsedH,
		MinWidth:         orbCollapsedW,
		MinHeight:        orbCollapsedH,
		Frameless:        true,
		AlwaysOnTop:      true,
		BackgroundColour: &options.RGBA{R: 0, G: 0, B: 0, A: 0},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  orbApp.startup,
		OnShutdown: orbApp.shutdown,
		Bind: []any{
			orbApp,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    true,
		},
		Mac: &mac.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  false,
		},
	})
}
