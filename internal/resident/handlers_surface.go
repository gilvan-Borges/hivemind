package resident

import (
	"context"
	"log"
)

// Surface handlers (browser/desktop) run on the REAL user session behind the
// experimental_unisolated flag. The vault is never consulted on this path.

func (b *Broker) handleBrowserOpen(ctx context.Context, args map[string]any) (string, error) {
	if err := b.refuseIfSurfaceOff(); err != nil {
		return "", err
	}
	log.Printf("residente: browser_open SEM isolamento (experimentalUnisolated); o cofre não é consultado")
	target, err := validatePublicURL(strArg(args, "url"))
	if err != nil {
		return "", err
	}
	if err := b.surface().OpenURL(ctx, target); err != nil {
		return "", err
	}
	return "aberto SEM isolamento", nil
}

func (b *Broker) handleDesktopClick(ctx context.Context, args map[string]any) (string, error) {
	if err := b.refuseIfSurfaceOff(); err != nil {
		return "", err
	}
	log.Printf("residente: desktop_click SEM isolamento (experimentalUnisolated); o cofre não é consultado")
	x, y := intArg(args, "x"), intArg(args, "y")
	if err := b.surface().Click(ctx, x, y); err != nil {
		return "", err
	}
	return "clique SEM isolamento", nil
}

func (b *Broker) handleDesktopType(ctx context.Context, args map[string]any) (string, error) {
	if err := b.refuseIfSurfaceOff(); err != nil {
		return "", err
	}
	log.Printf("residente: desktop_type SEM isolamento (experimentalUnisolated); o cofre não é consultado")
	text := strArg(args, "text")
	if err := b.surface().TypeText(ctx, text); err != nil {
		return "", err
	}
	return "digitação SEM isolamento", nil
}
