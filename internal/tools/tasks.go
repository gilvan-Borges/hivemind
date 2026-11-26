package tools

import (
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// TaskItem representa uma etapa da lista ao vivo exibida na UI da IDE.
type TaskItem struct {
	Title  string `json:"title" jsonschema:"Título curto da etapa (2-8 palavras)."`
	Status string `json:"status" jsonschema:"Estado da etapa: pending, in_progress, done, failed ou skipped."`
}

type updateTasksArgs struct {
	Tasks []TaskItem `json:"tasks" jsonschema:"Lista ordenada completa de tarefas (1 a 12 itens)."`
}

func normalizeTaskStatus(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pending", "":
		return "pending", true
	case "in_progress", "running", "active":
		return "in_progress", true
	case "done", "completed", "complete":
		return "done", true
	case "failed", "error":
		return "failed", true
	case "skipped":
		return "skipped", true
	default:
		return "", false
	}
}

func (d Deps) updateTasks() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "update_tasks",
		Description: "Atualiza a lista de tarefas ao vivo visível para o usuário. Use em tarefas com 3+ etapas: envie a lista completa ordenada (máx. 12 itens) com status pending, in_progress, done, failed ou skipped.",
	}, func(_ agent.Context, args updateTasksArgs) (textResult, error) {
		if len(args.Tasks) == 0 {
			return textResult{}, fmt.Errorf("tasks não pode ser vazio")
		}
		if len(args.Tasks) > 12 {
			return textResult{}, fmt.Errorf("máximo de 12 tarefas permitido (recebido %d)", len(args.Tasks))
		}
		clean := make([]TaskItem, 0, len(args.Tasks))
		doneCount := 0
		for i, item := range args.Tasks {
			title := strings.TrimSpace(item.Title)
			if title == "" {
				return textResult{}, fmt.Errorf("tarefa #%d sem título", i+1)
			}
			st, ok := normalizeTaskStatus(item.Status)
			if !ok {
				return textResult{}, fmt.Errorf("status inválido na tarefa #%d: %q (use pending, in_progress, done, failed ou skipped)", i+1, item.Status)
			}
			if st == "done" {
				doneCount++
			}
			clean = append(clean, TaskItem{Title: title, Status: st})
		}
		if d.OnTasks != nil {
			d.OnTasks(clean)
		}
		return textResult{Text: fmt.Sprintf("Lista de tarefas atualizada (%d/%d concluídas).", doneCount, len(clean))}, nil
	})
}
