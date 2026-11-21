package resident

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (b *Broker) handleListProjects(_ context.Context, _ map[string]any) (string, error) {
	if b.cfg.ListProjects == nil {
		return "[]", nil
	}
	refs := b.cfg.ListProjects()
	raw, err := json.MarshalIndent(refs, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (b *Broker) handleMemorySearch(_ context.Context, args map[string]any) (string, error) {
	if b.cfg.Memory == nil {
		return "Memória pessoal indisponível.", nil
	}
	ws := strArg(args, "workstream")
	if ws == "" {
		ws = b.cfg.Workstream
	}
	query := strArg(args, "query")
	res, err := b.cfg.Memory.Retrieve(ws, query)
	if err != nil {
		return "", err
	}
	if len(res.Pinned) == 0 && len(res.Matches) == 0 {
		return "Nenhum fato encontrado na memória pessoal desta conta.", nil
	}
	var sb strings.Builder
	if len(res.Pinned) > 0 {
		sb.WriteString("# Fatos fixados\n")
		for _, f := range res.Pinned {
			fmt.Fprintf(&sb, "- [%s · id=%s] %s\n", f.Workstream, f.ID, f.Text)
		}
	}
	if len(res.Matches) > 0 {
		sb.WriteString("# Fatos relevantes\n")
		for _, f := range res.Matches {
			fmt.Fprintf(&sb, "- [%s · id=%s] %s\n", f.Workstream, f.ID, f.Text)
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

func (b *Broker) handleMemoryWrite(_ context.Context, args map[string]any) (string, error) {
	if b.cfg.Memory == nil {
		return "", errors.New("memória pessoal indisponível")
	}
	ws := strArg(args, "workstream")
	if ws == "" {
		ws = b.cfg.Workstream
	}
	text := strArg(args, "text")
	if text == "" {
		return "", errors.New("parâmetro text é obrigatório")
	}
	fact, err := b.cfg.Memory.AddFact(ws, text, boolArg(args, "pin"))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("fato salvo na memória pessoal (id=%s · workstream=%s)", fact.ID, fact.Workstream), nil
}

func (b *Broker) handleMemoryForget(_ context.Context, args map[string]any) (string, error) {
	if b.cfg.Memory == nil {
		return "", errors.New("memória pessoal indisponível")
	}
	id := firstNonEmptyStr(strArg(args, "id"), strArg(args, "key"))
	if id == "" {
		return "", errors.New("parâmetro key/id é obrigatório")
	}
	removed, err := b.cfg.Memory.DeleteFact(id)
	if err != nil {
		return "", err
	}
	if !removed {
		return fmt.Sprintf("nenhum fato encontrado com id=%s", id), nil
	}
	return fmt.Sprintf("fato %s removido da memória pessoal", id), nil
}
