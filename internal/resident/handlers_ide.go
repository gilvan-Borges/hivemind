package resident

import (
	"context"
	"errors"
	"strings"
)

var (
	errIDEOffline     = errors.New("IDE não está ligada a este processo")
	errIDEActionEmpty = errors.New("ação de IDE vazia")
)

func (b *Broker) handleIDEAction(ctx context.Context, args map[string]any) (string, error) {
	if b.cfg.IDE == nil {
		return "", errIDEOffline
	}
	action := strings.TrimSpace(strArg(args, "action"))
	if action == "" {
		return "", errIDEActionEmpty
	}
	cloned := make(map[string]any, len(args)+1)
	for k, v := range args {
		cloned[k] = v
	}
	if strings.TrimSpace(strArg(cloned, "workspace")) == "" {
		cloned["workspace"] = b.cfg.Workspace
	}
	return b.cfg.IDE(ctx, "ide_action", cloned)
}
