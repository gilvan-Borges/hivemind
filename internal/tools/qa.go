package tools

import (
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// qaApproveArgs carries the structured evidence a QA agent MUST attach to an
// approval (ROADMAP F5: "QA com verificação executável"). An approval without
// a verified command/test run — or an explicit justification for pure-docs
// deliveries — is rejected.
type qaApproveArgs struct {
	// Command is the exact command line (argv) the QA executed to validate the
	// delivery, e.g. "go test ./...". Required unless PureDocsJustification is set.
	Command string `json:"command,omitempty" jsonschema:"Comando exato de teste/verificação executado, por exemplo: go test ./..."`
	// ExitCode is the observed exit code of Command. 0 means success.
	ExitCode int `json:"exit_code,omitempty" jsonschema:"Código de saída observado do comando executado (0 = sucesso)."`
	// Evidence is a short excerpt of the command output proving the outcome.
	Evidence string `json:"evidence,omitempty" jsonschema:"Trecho curto da saída do comando que comprova o resultado (ex.: ok  ./internal/tools   1.2s)."`
	// PureDocsJustification is REQUIRED for pure-docs deliveries with nothing
	// to execute; it must explain why no command was run.
	PureDocsJustification string `json:"pure_docs_justification,omitempty" jsonschema:"OBRIGATÓRIO apenas em entregas pure-docs: justificativa explícita do porquê nenhum comando/teste foi executado."`
	// Note summarizes what was verified.
	Note string `json:"note" jsonschema:"Resumo do que foi verificado."`
}

// validateQAApproval enforces the structured-evidence gate. It is pure so the
// rejection path is unit-testable without an LLM.
func validateQAApproval(args qaApproveArgs) error {
	hasCommand := strings.TrimSpace(args.Command) != ""
	hasPureDocs := strings.TrimSpace(args.PureDocsJustification) != ""
	if hasCommand && hasPureDocs {
		return fmt.Errorf("qa_approve: informe OU command (+ exit_code) OU pure_docs_justification, nunca os dois")
	}
	if !hasCommand && !hasPureDocs {
		return fmt.Errorf("qa_approve recusado: aprovação exige evidência estruturada — informe command (comando de teste/verificação executado) com exit_code e trecho da saída em evidence; apenas entregas pure-docs (nada executável) podem aprovar com pure_docs_justification explícita")
	}
	if hasPureDocs {
		if len(strings.TrimSpace(args.PureDocsJustification)) < 15 {
			return fmt.Errorf("qa_approve recusado: pure_docs_justification muito curta — explique concretamente por que esta entrega não tem nada executável para validar")
		}
		return nil
	}
	if strings.TrimSpace(args.Evidence) == "" {
		return fmt.Errorf("qa_approve recusado: command informado sem evidence — cole o trecho da saída que comprova o resultado do comando")
	}
	if args.ExitCode != 0 {
		return fmt.Errorf("qa_approve recusado: o comando informado terminou com exit_code=%d (não-zero); corrige as falhas antes de aprovar ou use qa_request_changes", args.ExitCode)
	}
	return nil
}

func (d Deps) qaApprove() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "qa_approve",
		Description: "Encerra o ciclo de QA APROVANDO com evidência obrigatória: informe command (o comando de teste/verificação que VOCÊ executou nesta worktree), exit_code (0) e evidence (trecho da saída). " +
			"Aprovações sem comando executado são recusadas — inclusive as que apenas citam testes hipotéticos. " +
			"ÚNICA exceção: entregas pure-docs (apenas documentação, nada executável) exigem pure_docs_justification explícita explicando por que nada foi executado. " +
			"Não chame se ainda houver falhas (use qa_request_changes).",
	}, func(ctx agent.Context, args qaApproveArgs) (textResult, error) {
		if err := validateQAApproval(args); err != nil {
			return textResult{}, err
		}
		_ = ctx.State().Set("qa_approval_note", args.Note)
		_ = ctx.State().Set("qa_feedback", "")
		_ = ctx.State().Set("qa_approved", "1")
		if actions := ctx.Actions(); actions != nil {
			if actions.StateDelta == nil {
				actions.StateDelta = map[string]any{}
			}
			actions.StateDelta["qa_approval_note"] = args.Note
			actions.StateDelta["qa_feedback"] = ""
			actions.StateDelta["qa_approved"] = "1"
			actions.Escalate = true
		}
		out := "aprovado: " + args.Note
		if strings.TrimSpace(args.Command) != "" {
			out = fmt.Sprintf("aprovado com evidência (exit_code=%d): %s — %s", args.ExitCode, strings.TrimSpace(args.Command), args.Note)
		}
		return textResult{Text: out}, nil
	})
}

func (d Deps) qaChanges() (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        "qa_request_changes",
		Description: "Devolve o trabalho aos desenvolvedores com o que precisa ser corrigido.",
	}, func(ctx agent.Context, args struct {
		Notes string `json:"notes" jsonschema:"O que falhou e como corrigir."`
	}) (textResult, error) {
		if err := ctx.State().Set("qa_feedback", args.Notes); err != nil {
			return textResult{}, err
		}
		_ = ctx.State().Set("qa_approved", "")
		if actions := ctx.Actions(); actions != nil {
			if actions.StateDelta == nil {
				actions.StateDelta = map[string]any{}
			}
			actions.StateDelta["qa_feedback"] = args.Notes
			actions.StateDelta["qa_approved"] = ""
		}
		return textResult{Text: "ajustes pedidos: " + args.Notes}, nil
	})
}
