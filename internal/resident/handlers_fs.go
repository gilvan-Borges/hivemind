package resident

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/tools"
)

// requireWorkspace resolves the active workspace or fails.
func (b *Broker) requireWorkspace() (string, error) {
	ws := strings.TrimSpace(b.cfg.Workspace)
	if ws == "" {
		return "", fmt.Errorf("nenhum workspace aberto")
	}
	return ws, nil
}

func (b *Broker) handleReadFile(_ context.Context, args map[string]any) (string, error) {
	if b.cfg.FullAccess {
		return b.readFull(strArg(args, "path"))
	}
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	rel := strArg(args, "path")
	if rel == "" {
		return "", fmt.Errorf("parâmetro path é obrigatório")
	}
	if IsSensitivePath(rel) {
		return "", fmt.Errorf("caminho sensível recusado: %s", RedactTarget(rel))
	}
	return tools.ReadFileReadOnly(ws, rel, intArg(args, "start_line"), intArg(args, "end_line"))
}

func (b *Broker) handleListDir(_ context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	rel := strArg(args, "path")
	if IsSensitivePath(rel) {
		return "", fmt.Errorf("caminho sensível recusado: %s", RedactTarget(rel))
	}
	entries, err := tools.ListDirReadOnly(ws, rel)
	if err != nil {
		return "", err
	}
	filtered := make([]string, 0, len(entries))
	for _, e := range entries {
		if IsSensitivePath(e) {
			continue
		}
		filtered = append(filtered, e)
	}
	if len(filtered) == 0 {
		return "(diretório vazio)", nil
	}
	return strings.Join(filtered, "\n"), nil
}

func (b *Broker) handleSearchText(ctx context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	sub := strArg(args, "path")
	if IsSensitivePath(sub) {
		return "", fmt.Errorf("caminho sensível recusado: %s", RedactTarget(sub))
	}
	return tools.SearchTextReadOnly(
		ctx,
		ws,
		strArg(args, "query"),
		sub,
		strArg(args, "glob"),
		boolArg(args, "regex"),
		boolArg(args, "case_sensitive"),
		intArg(args, "max_results"),
	)
}

func (b *Broker) handleFindFiles(ctx context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	sub := strArg(args, "path")
	if IsSensitivePath(sub) {
		return "", fmt.Errorf("caminho sensível recusado: %s", RedactTarget(sub))
	}
	return tools.FindFilesReadOnly(ctx, ws, strArg(args, "pattern"), sub, intArg(args, "max_results"))
}

func (b *Broker) handleWriteFile(_ context.Context, args map[string]any) (string, error) {
	if b.cfg.FullAccess {
		content, _ := args["content"].(string)
		return b.writeFull(strArg(args, "path"), content)
	}
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	rel := strArg(args, "path")
	if rel == "" {
		return "", errors.New("parâmetro path é obrigatório")
	}
	if IsSensitivePath(rel) {
		return "", fmt.Errorf("caminho sensível recusado: %s", RedactTarget(rel))
	}
	content, _ := args["content"].(string)
	return tools.WriteFileWorkspace(ws, rel, content)
}

func (b *Broker) handleEditFile(_ context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	rel := strArg(args, "path")
	if rel == "" {
		return "", errors.New("parâmetro path é obrigatório")
	}
	if IsSensitivePath(rel) {
		return "", fmt.Errorf("caminho sensível recusado: %s", RedactTarget(rel))
	}
	oldText, _ := args["old_text"].(string)
	newText, _ := args["new_text"].(string)
	return tools.EditFileWorkspace(ws, rel, oldText, newText, boolArg(args, "replace_all"))
}

func (b *Broker) handleDeleteFile(_ context.Context, args map[string]any) (string, error) {
	ws, err := b.requireWorkspace()
	if err != nil {
		return "", err
	}
	rel := strArg(args, "path")
	if rel == "" {
		return "", errors.New("parâmetro path é obrigatório")
	}
	if IsSensitivePath(rel) {
		return "", fmt.Errorf("remoção de caminho sensível recusada: %s", RedactTarget(rel))
	}
	return tools.DeleteFileWorkspace(ws, rel)
}
