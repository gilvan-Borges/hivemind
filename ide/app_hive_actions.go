package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// hiveIDEAction is the host behind the resident tool ide_action.
// It reuses the same IDE entry points the user already has.
func (a *App) hiveIDEAction(ctx context.Context, _ string, args map[string]any) (string, error) {
	if a == nil {
		return "", fmt.Errorf("IDE indisponível")
	}
	action := strings.TrimSpace(hiveArg(args, "action"))
	ws := strings.TrimSpace(hiveArg(args, "workspace"))
	switch action {
	case "list_sessions":
		if ws == "" {
			return "", fmt.Errorf("nenhum workspace aberto")
		}
		sum, err := a.ListSessions(ws)
		if err != nil {
			return "", err
		}
		if len(sum.Sessions) == 0 {
			return "nenhuma sessão salva", nil
		}
		var b strings.Builder
		for _, s := range sum.Sessions {
			fmt.Fprintf(&b, "%s\t%s\t%s\n", s.ID, s.Kind, s.Title)
		}
		return strings.TrimRight(b.String(), "\n"), nil
	case "open_file":
		text, err := a.ReadFile(ws, hiveArg(args, "path"))
		if err != nil {
			return "", err
		}
		return strx.TruncateBytes(text, 16*1024, "\n…"), nil
	case "save_file":
		if err := a.SaveFile(ws, hiveArg(args, "path"), hiveArg(args, "content")); err != nil {
			return "", err
		}
		return "arquivo gravado na IDE", nil
	case "start_run":
		msg := strings.TrimSpace(hiveArg(args, "message"))
		if msg == "" {
			return "", fmt.Errorf("mensagem vazia")
		}
		lane := hiveArg(args, "lane_id")
		if lane == "" {
			lane = "lane-hive"
		}
		team := hiveArg(args, "team")
		sessionID := hiveArg(args, "session_id")
		go func() {
			_, _ = a.StartRun(lane, ws, team, msg, "", sessionID)
		}()
		return "turno da equipe iniciado na IDE", nil
	case "steer":
		lane := hiveArg(args, "lane_id")
		if lane == "" {
			lane = "lane-hive"
		}
		if !a.SteerRun(lane, hiveArg(args, "message")) {
			return "", fmt.Errorf("nenhuma execução ativa nessa lane")
		}
		return "instrução enviada para a execução", nil
	case "run_command":
		out, err := a.RunCommand(ws, hiveArg(args, "command"))
		text := out.Output
		if err != nil && text == "" {
			return "", err
		}
		return strx.TruncateBytes(fmt.Sprintf("exit=%d\n%s", out.ExitCode, text), 16*1024, "\n…"), err
	case "git":
		act := strings.TrimSpace(hiveArg(args, "command"))
		if act == "" || act == "status" || act == "diff" {
			diffs, err := a.GetDiff(ws)
			if err != nil {
				return "", err
			}
			if len(diffs) == 0 {
				return "sem alterações", nil
			}
			var b strings.Builder
			for _, d := range diffs {
				fmt.Fprintf(&b, "%s\n", d.Path)
			}
			return strings.TrimRight(b.String(), "\n"), nil
		}
		return a.RunGitAction(ws, act)
	default:
		return "", fmt.Errorf("ação de IDE desconhecida: %s", action)
	}
}

func hiveArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return s
}
