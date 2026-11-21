package resident

import (
	"context"
	"fmt"
	"strings"
)

func (b *Broker) handleSpawn(ctx context.Context, args map[string]any) (string, error) {
	if b.cfg.Spawn == nil {
		return "", fmt.Errorf("subagente indisponível")
	}
	out, err := b.cfg.Spawn(ctx, strArg(args, "objective"), strSliceArg(args, "roots"), strSliceArg(args, "classes"))
	if err != nil {
		return "", err
	}
	return out, nil
}

func (b *Broker) handleHandoff(ctx context.Context, args map[string]any) (string, error) {
	if b.cfg.SquadHandoff == nil {
		return "", fmt.Errorf("handoff para o time indisponível")
	}
	mode := ""
	if b.cfg.PermissionMode != nil {
		mode = b.cfg.PermissionMode(b.cfg.Workspace)
	}
	facts := strSliceArg(args, "facts")
	if len(facts) == 0 {
		if s, ok := args["facts"].(string); ok && strings.TrimSpace(s) != "" {
			facts = []string{strings.TrimSpace(s)}
		}
	}
	hash, err := b.cfg.SquadHandoff(ctx, SquadHandoff{
		Workspace:      b.cfg.Workspace,
		Team:           strArg(args, "team"),
		Objective:      strArg(args, "objective"),
		Facts:          facts,
		PermissionMode: mode,
	})
	if err != nil {
		return "", err
	}
	return "handoff aceito: " + hash, nil
}
