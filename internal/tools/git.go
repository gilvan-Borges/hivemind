package tools

import (
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

func (d Deps) gitStatus() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_status",
		Description: "Mostra o status curto do git no diretório de trabalho.",
	}, func(ctx agent.Context, _ struct{}) (textResult, error) {
		out, err := d.Git.Status(ctx, d.Root)
		return textResult{Text: out}, err
	})
}

func (d Deps) gitDiff() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_diff",
		Description: "Mostra o resumo do diff em relação ao HEAD (incluindo arquivos novos não rastreados).",
	}, func(ctx agent.Context, _ struct{}) (textResult, error) {
		out, err := d.Git.Diff(ctx, d.Root)
		if untracked, uErr := d.Git.Run(ctx, d.Root, "ls-files", "--others", "--exclude-standard"); uErr == nil && strings.TrimSpace(untracked) != "" {
			if strings.TrimSpace(out) != "" {
				out += "\n"
			}
			out += "[arquivos novos na worktree]\n" + strings.TrimSpace(untracked)
		}
		out = strx.TruncateBytes(out, 16*1024, "\n…(diff truncado)")
		return textResult{Text: out}, err
	})
}

func (d Deps) gitCommit() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_commit",
		Description: "Exclusivo do Chefe/Arquiteto: faz stage de todas as mudanças da worktree e cria um commit.",
	}, func(ctx agent.Context, args struct {
		Message string `json:"message" jsonschema:"Mensagem do commit. Inclua a chave Jira quando houver."`
	}) (textResult, error) {
		ok, err := d.Git.CommitAll(ctx, d.Root, args.Message)
		if err != nil {
			return textResult{}, err
		}
		if !ok {
			return textResult{Text: "nada para commitar"}, nil
		}
		return textResult{Text: "commit criado"}, nil
	})
}

func (d Deps) gitPush() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_push",
		Description: "Exclusivo do Chefe/Arquiteto: envia a branch atual da worktree para origin (sem --force).",
	}, func(ctx agent.Context, _ struct{}) (textResult, error) {
		branch, err := d.Git.Run(ctx, d.Root, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return textResult{}, err
		}
		branch = strings.TrimSpace(branch)
		if err := d.Git.Push(ctx, d.Root, "origin", branch); err != nil {
			return textResult{}, err
		}
		return textResult{Text: "push de " + branch}, nil
	})
}

func (d Deps) gitPull() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "git_pull",
		Description: "Exclusivo do Chefe/Arquiteto: atualiza a branch atual com git pull --ff-only.",
	}, func(ctx agent.Context, _ struct{}) (textResult, error) {
		out, err := d.Git.Run(ctx, d.Root, "pull", "--ff-only")
		return textResult{Text: out}, err
	})
}
