package resident

import (
	"context"
	"log"
)

// Surface handlers (browser/desktop) run on the REAL user session behind the
// experimental_unisolated flag. The vault is never consulted on this path.

func (b *Broker) surfaceModeLabel() string {
	if b.cfg.FullAccess {
		return "modo full"
	}
	if b.cfg.ExperimentalUnisolated {
		return "SEM isolamento (experimentalUnisolated)"
	}
	return "Windows Sandbox (.wsb)"
}

func (b *Broker) handleBrowserOpen(ctx context.Context, args map[string]any) (string, error) {
	if err := b.refuseIfSurfaceOff(); err != nil {
		return "", err
	}
	mode := b.surfaceModeLabel()
	log.Printf("residente: browser_open em %s; o cofre não é consultado", mode)
	target, err := validatePublicURL(strArg(args, "url"))
	if err != nil {
		return "", err
	}
	if err := b.surface().OpenURL(ctx, target); err != nil {
		return "", err
	}
	return "aberto em " + mode, nil
}

func (b *Broker) handleDesktopClick(ctx context.Context, args map[string]any) (string, error) {
	if err := b.refuseIfSurfaceOff(); err != nil {
		return "", err
	}
	mode := b.surfaceModeLabel()
	log.Printf("residente: desktop_click em %s; o cofre não é consultado", mode)
	x, y := intArg(args, "x"), intArg(args, "y")
	if err := b.surface().Click(ctx, x, y); err != nil {
		return "", err
	}
	return "clique em " + mode, nil
}

func (b *Broker) handleDesktopType(ctx context.Context, args map[string]any) (string, error) {
	if err := b.refuseIfSurfaceOff(); err != nil {
		return "", err
	}
	mode := b.surfaceModeLabel()
	log.Printf("residente: desktop_type em %s; o cofre não é consultado", mode)
	text := strArg(args, "text")
	if err := b.surface().TypeText(ctx, text); err != nil {
		return "", err
	}
	return "digitação em " + mode, nil
}
