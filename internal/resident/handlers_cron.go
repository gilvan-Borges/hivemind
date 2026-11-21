package resident

import (
	"context"
	"fmt"
	"strings"
)

func (b *Broker) handleCronAdd(_ context.Context, args map[string]any) (string, error) {
	base := strings.TrimSpace(b.cfg.BaseDir)
	if base == "" {
		return "", fmt.Errorf("diretório do residente ausente")
	}
	rt, err := SaveRoutine(base, Routine{
		Expr:       strArg(args, "expr"),
		Timezone:   strArg(args, "timezone"),
		Text:       strArg(args, "text"),
		Workstream: firstNonEmptyStr(strArg(args, "workstream"), b.cfg.Workstream),
		Classes:    strSliceArg(args, "classes"),
		Enabled:    true,
	})
	if err != nil {
		return "", err
	}
	if b.cfg.OnRoutineSaved != nil {
		b.cfg.OnRoutineSaved()
	}
	return "rotina gravada: " + rt.ID, nil
}
