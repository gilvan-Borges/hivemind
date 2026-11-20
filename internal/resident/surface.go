package resident

import "context"

// Surface runs browser and desktop actions on the host session.
// Implementations must not read the vault or accept a shell command line.
type Surface interface {
	OpenURL(ctx context.Context, rawURL string) error
	Click(ctx context.Context, x, y int) error
	TypeText(ctx context.Context, text string) error
}

func defaultSurface() Surface { return osSurface{} }

type osSurface struct{}
