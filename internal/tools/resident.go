package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/workspace"
)

// ResidentBroker is the single policy-enforcing gateway for resident agent tool
// calls (see docs/SPEC_HIVE_AGENT.md §4). ADK function tools registered on the
// resident never touch the filesystem or OS directly; they only construct a
// structured call and return the broker's fenced result.
type ResidentBroker interface {
	Execute(ctx context.Context, name string, args map[string]any) (string, error)
}

type residentListProjectsArgs struct{}

type residentMemorySearchArgs struct {
	Query      string `json:"query" jsonschema:"Termos ou assunto para consultar na memória pessoal local desta conta."`
	Workstream string `json:"workstream,omitempty" jsonschema:"Workstream opcional para filtrar fatos específicos."`
}

// ForResidentReadOnly returns the Phase 1 read-only toolset for the Hive
// resident agent. Every tool is a thin wrapper over broker.Execute — no raw
// write, exec, network, credentials, or destructive tool is registered.
func ForResidentReadOnly(broker ResidentBroker) ([]tool.Tool, error) {
	if broker == nil {
		return nil, fmt.Errorf("broker do agente residente é obrigatório")
	}

	var out []tool.Tool
	add := func(t tool.Tool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "read_file",
		Description: "Lê um arquivo de texto dentro do workspace ativo (somente leitura). Aceita start_line e end_line opcionais (1-based, até 400 linhas).",
	}, func(ctx agent.Context, args readFileArgs) (textResult, error) {
		m := map[string]any{"path": args.Path}
		if args.StartLine > 0 {
			m["start_line"] = args.StartLine
		}
		if args.EndLine > 0 {
			m["end_line"] = args.EndLine
		}
		res, err := broker.Execute(ctx, "read_file", m)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "list_dir",
		Description: "Lista arquivos e subdiretórios dentro do workspace ativo (somente leitura).",
	}, func(ctx agent.Context, args pathArgs) (textResult, error) {
		res, err := broker.Execute(ctx, "list_dir", map[string]any{"path": args.Path})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "search_text",
		Description: "Busca ocorrências de texto ou regex (Go RE2) nos arquivos do workspace ativo (somente leitura).",
	}, func(ctx agent.Context, args searchTextArgs) (textResult, error) {
		m := map[string]any{
			"query":          args.Query,
			"path":           args.Path,
			"glob":           args.Glob,
			"regex":          args.Regex,
			"case_sensitive": args.CaseSensitive,
		}
		if args.MaxResults > 0 {
			m["max_results"] = args.MaxResults
		}
		res, err := broker.Execute(ctx, "search_text", m)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "find_files",
		Description: "Encontra arquivos no workspace ativo por nome ou padrão glob (somente leitura).",
	}, func(ctx agent.Context, args findFilesArgs) (textResult, error) {
		m := map[string]any{
			"pattern": args.Pattern,
			"path":    args.Path,
		}
		if args.MaxResults > 0 {
			m["max_results"] = args.MaxResults
		}
		res, err := broker.Execute(ctx, "find_files", m)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "list_projects",
		Description: "Lista os projetos/workspaces conhecidos e abertos na IDE neste computador (somente leitura).",
	}, func(ctx agent.Context, _ residentListProjectsArgs) (textResult, error) {
		res, err := broker.Execute(ctx, "list_projects", nil)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "memory_search",
		Description: "Consulta fatos salvos na memória pessoal local desta conta (somente leitura).",
	}, func(ctx agent.Context, args residentMemorySearchArgs) (textResult, error) {
		m := map[string]any{
			"query":      args.Query,
			"workstream": args.Workstream,
		}
		res, err := broker.Execute(ctx, "memory_search", m)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	return out, nil
}

type residentMemoryWriteArgs struct {
	Workstream string `json:"workstream,omitempty" jsonschema:"Workstream onde o fato será salvo."`
	Key        string `json:"key" jsonschema:"Identificador curto e estável do fato pessoal."`
	Text       string `json:"text" jsonschema:"Conteúdo factual conciso a registrar na memória pessoal local (nunca segredos)."`
	Pin        bool   `json:"pin,omitempty" jsonschema:"Fixa este fato no contexto futuro do workstream."`
}

type residentMemoryForgetArgs struct {
	Workstream string `json:"workstream,omitempty" jsonschema:"Workstream do fato a remover."`
	Key        string `json:"key" jsonschema:"Chave exata do fato pessoal a esquecer."`
}

type residentExecArgs struct {
	Argv       []string `json:"argv" jsonschema:"Lista explícita [executavel, arg1, arg2...] sem shell (nunca use string única ou sh -c)."`
	TimeoutSec int      `json:"timeout_sec,omitempty" jsonschema:"Tempo limite opcional em segundos (padrão 60, máximo 120)."`
}

type residentNetFetchArgs struct {
	URL            string `json:"url" jsonschema:"URL https/http em host explicitamente liberado na allowlist de rede."`
	Method         string `json:"method,omitempty" jsonschema:"Método HTTP seguro (GET ou HEAD)."`
	CredentialName string `json:"credential_name,omitempty" jsonschema:"Nome opcional de credencial salva no cofre do sistema (enviada apenas no cabeçalho Authorization sem expor o valor)."`
}

// ForResidentPhase2 returns the Phase 2 toolset for the Hive resident agent:
// all Phase 1 read tools plus gated workspace writes, surgical edits, deletions,
// personal memory mutation, sandboxed explicit-argv execution, and allowlisted network fetch.
// Every call passes exclusively through broker.Execute (never raw OS access here).
func ForResidentPhase2(broker ResidentBroker) ([]tool.Tool, error) {
	out, err := ForResidentReadOnly(broker)
	if err != nil {
		return nil, err
	}
	add := func(t tool.Tool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "write_file",
		Description: "Cria ou substitui um arquivo de texto no workspace ativo (exige aprovação humana e sandbox ativa).",
	}, func(ctx agent.Context, args writeArgs) (textResult, error) {
		res, err := broker.Execute(ctx, "write_file", map[string]any{
			"path":    args.Path,
			"content": args.Content,
		})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "edit_file",
		Description: "Edita cirurgicamente um arquivo existente no workspace substituindo old_text por new_text (exige aprovação humana e sandbox ativa).",
	}, func(ctx agent.Context, args editArgs) (textResult, error) {
		res, err := broker.Execute(ctx, "edit_file", map[string]any{
			"path":        args.Path,
			"old_text":    args.OldText,
			"new_text":    args.NewText,
			"replace_all": args.ReplaceAll,
		})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "delete_file",
		Description: "Remove um arquivo específico do workspace ativo (ação destrutiva: sempre exige aprovação humana).",
	}, func(ctx agent.Context, args pathArgs) (textResult, error) {
		res, err := broker.Execute(ctx, "delete_file", map[string]any{"path": args.Path})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "memory_write",
		Description: "Salva ou atualiza um fato na memória pessoal local desta conta (exige aprovação humana).",
	}, func(ctx agent.Context, args residentMemoryWriteArgs) (textResult, error) {
		res, err := broker.Execute(ctx, "memory_write", map[string]any{
			"workstream": args.Workstream,
			"key":        args.Key,
			"text":       args.Text,
			"pin":        args.Pin,
		})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "memory_forget",
		Description: "Remove um fato específico da memória pessoal local desta conta (exige aprovação humana).",
	}, func(ctx agent.Context, args residentMemoryForgetArgs) (textResult, error) {
		res, err := broker.Execute(ctx, "memory_forget", map[string]any{
			"workstream": args.Workstream,
			"key":        args.Key,
		})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "exec_command",
		Description: "Executa um binário autorizado dentro da sandbox obrigatória do SO usando argv explícito (sem shell, exige aprovação humana).",
	}, func(ctx agent.Context, args residentExecArgs) (textResult, error) {
		m := map[string]any{"argv": args.Argv}
		if args.TimeoutSec > 0 {
			m["timeout_sec"] = args.TimeoutSec
		}
		res, err := broker.Execute(ctx, "exec_command", m)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "browser_open",
		Description: "Abre uma URL http(s) no browser da sessão real. Só funciona com experimentalUnisolated ligado. Roda SEM isolamento, pede confirmação fora de Full Acesso, ignora perfil e não lê o cofre.",
	}, func(ctx agent.Context, args struct {
		URL string `json:"url" jsonschema:"URL http ou https. Sem usuário nem senha na URL."`
	}) (textResult, error) {
		res, err := broker.Execute(ctx, "browser_open", map[string]any{"url": args.URL})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "desktop_click",
		Description: "Clica na sessão real nas coordenadas x,y. Só funciona com experimentalUnisolated ligado. Roda SEM isolamento, pede confirmação fora de Full Acesso e não lê o cofre.",
	}, func(ctx agent.Context, args struct {
		X int `json:"x" jsonschema:"Coordenada horizontal em pixels."`
		Y int `json:"y" jsonschema:"Coordenada vertical em pixels."`
	}) (textResult, error) {
		res, err := broker.Execute(ctx, "desktop_click", map[string]any{"x": args.X, "y": args.Y})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "desktop_type",
		Description: "Digita texto na sessão real. Só funciona com experimentalUnisolated ligado. Roda SEM isolamento, pede confirmação fora de Full Acesso e não lê o cofre.",
	}, func(ctx agent.Context, args struct {
		Text string `json:"text" jsonschema:"Texto a digitar na janela em foco. Não envie segredo."`
	}) (textResult, error) {
		res, err := broker.Execute(ctx, "desktop_type", map[string]any{"text": args.Text})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "net_fetch",
		Description: "Faz uma requisição HTTP/HTTPS GET ou HEAD exclusivamente para hosts na allowlist configurada (exige aprovação humana).",
	}, func(ctx agent.Context, args residentNetFetchArgs) (textResult, error) {
		m := map[string]any{
			"url":             args.URL,
			"method":          args.Method,
			"credential_name": args.CredentialName,
		}
		res, err := broker.Execute(ctx, "net_fetch", m)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	if err := add(functiontool.New(functiontool.Config{
		Name:        "browser_headless",
		Description: "Lê uma página http(s) num navegador invisível (sem janela). Só hosts da allowlist. Perfil temporário: não usa o perfil da pessoa e não lê o cofre. Devolve o DOM já renderizado, com teto de tamanho.",
	}, func(ctx agent.Context, args struct {
		URL string `json:"url" jsonschema:"URL http ou https na allowlist. Sem usuário nem senha na URL."`
	}) (textResult, error) {
		res, err := broker.Execute(ctx, "browser_headless", map[string]any{"url": args.URL})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}

	return out, nil
}

type residentCronArgs struct {
	Expr       string   `json:"expr" jsonschema:"Expressão de cinco campos (minuto hora dia mês dia-da-semana). Sem comando."`
	Timezone   string   `json:"timezone,omitempty" jsonschema:"Fuso IANA opcional, por exemplo America/Sao_Paulo."`
	Text       string   `json:"text" jsonschema:"Texto que a rotina envia ao residente. Não é um comando."`
	Workstream string   `json:"workstream,omitempty" jsonschema:"Workstream da rotina."`
	Classes    []string `json:"classes,omitempty" jsonschema:"Classes permitidas. Só estreita. Vazio não amplia."`
}

type residentSpawnArgs struct {
	Objective    string   `json:"objective" jsonschema:"Encargo objetivo do subagente, sem o transcript."`
	Roots        []string `json:"roots,omitempty" jsonschema:"Raízes de arquivo que o subagente pode usar."`
	Classes      []string `json:"classes,omitempty" jsonschema:"Classes máximas do subagente. Só estreita."`
	ContextBrief string   `json:"context_brief,omitempty" jsonschema:"Fatos curtos já apurados por você que o subagente não precisa re-ler (máx. 2 KiB). Sem comandos."`
	Readonly     bool     `json:"readonly,omitempty" jsonschema:"Quando true, o subagente fica travado em leitura (nenhuma escrita/execução) — ideal para pesquisa e análise barata."`
}

type residentHandoffArgs struct {
	Objective string   `json:"objective" jsonschema:"Objetivo sanitizado que o time de código deve executar."`
	Facts     []string `json:"facts,omitempty" jsonschema:"Fatos curtos já lidos. Sem comando de shell."`
	Team      string   `json:"team,omitempty" jsonschema:"Nome do time em team.yaml. Vazio usa o time ativo."`
}

// ForResidentPhase3 adds structured routines, one-level subagents and a squad
// handoff. Every call still goes through the broker. There is no shell cron.
func ForResidentPhase3(broker ResidentBroker) ([]tool.Tool, error) {
	out, err := ForResidentPhase2(broker)
	if err != nil {
		return nil, err
	}
	add := func(t tool.Tool, err error) error {
		if err != nil {
			return err
		}
		out = append(out, t)
		return nil
	}
	if err := add(functiontool.New(functiontool.Config{
		Name:        "ide_action",
		Description: "Age na IDE aberta como o usuário. action: list_sessions, open_file, save_file, start_run, steer, run_command ou git. Pede aprovação. path, content, message, session_id, lane_id, team e command conforme a ação.",
	}, func(ctx agent.Context, args struct {
		Action    string `json:"action" jsonschema:"list_sessions, open_file, save_file, start_run, steer, run_command ou git."`
		Path      string `json:"path,omitempty" jsonschema:"Caminho relativo para open_file ou save_file."`
		Content   string `json:"content,omitempty" jsonschema:"Conteúdo completo para save_file."`
		Message   string `json:"message,omitempty" jsonschema:"Texto para start_run ou steer."`
		SessionID string `json:"session_id,omitempty" jsonschema:"Sessão da IDE para start_run."`
		LaneID    string `json:"lane_id,omitempty" jsonschema:"Lane para start_run ou steer."`
		Team      string `json:"team,omitempty" jsonschema:"Time para start_run. Vazio usa o time ativo."`
		Command   string `json:"command,omitempty" jsonschema:"Comando para run_command, ou ação git (status, diff, pull)."`
	}) (textResult, error) {
		res, err := broker.Execute(ctx, "ide_action", map[string]any{
			"action":     args.Action,
			"path":       args.Path,
			"content":    args.Content,
			"message":    args.Message,
			"session_id": args.SessionID,
			"lane_id":    args.LaneID,
			"team":       args.Team,
			"command":    args.Command,
		})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}
	if err := add(functiontool.New(functiontool.Config{
		Name:        "cron_add",
		Description: "Grava uma rotina estruturada (expressão de 5 campos, fuso e texto). Não aceita comando de shell e pede aprovação humana.",
	}, func(ctx agent.Context, args residentCronArgs) (textResult, error) {
		res, err := broker.Execute(ctx, "cron_add", map[string]any{
			"expr":       args.Expr,
			"timezone":   args.Timezone,
			"text":       args.Text,
			"workstream": args.Workstream,
			"classes":    args.Classes,
		})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}
	if err := add(functiontool.New(functiontool.Config{
		Name:        "spawn_subagent",
		Description: "Abre um subagente com objetivo, raízes e classes. No máximo dois ao mesmo tempo. O filho não cria rotina nem outro subagente. Use readonly=true para subagentes de pesquisa (travados em leitura) e context_brief para passar fatos que ele não precisa re-ler — o relatório volta com cabeçalho de custo (chamadas, tokens, duração).",
	}, func(ctx agent.Context, args residentSpawnArgs) (textResult, error) {
		res, err := broker.Execute(ctx, "spawn_subagent", map[string]any{
			"objective":     args.Objective,
			"roots":         args.Roots,
			"classes":       args.Classes,
			"context_brief": args.ContextBrief,
			"readonly":      args.Readonly,
		})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}
	if err := add(functiontool.New(functiontool.Config{
		Name:        "delegate_to_hive_team",
		Description: "Pede aprovação e entrega um objetivo sanitizado ao time de código. O cartão mostra o permission_mode daquele time.",
	}, func(ctx agent.Context, args residentHandoffArgs) (textResult, error) {
		res, err := broker.Execute(ctx, "delegate_to_hive_team", map[string]any{
			"objective": args.Objective,
			"facts":     args.Facts,
			"team":      args.Team,
		})
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: res}, nil
	})); err != nil {
		return nil, err
	}
	return out, nil
}

// WriteFileWorkspace writes a text file jailed inside root (1 MiB cap).
func WriteFileWorkspace(root, rel, content string) (string, error) {
	if len(content) > 1<<20 {
		return "", fmt.Errorf("conteúdo acima de 1 MiB")
	}
	path, err := workspace.Resolve(root, rel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return "arquivo gravado: " + rel, nil
}

// EditFileWorkspace applies a surgical edit jailed inside root.
func EditFileWorkspace(root, rel, oldText, newText string, replaceAll bool) (string, error) {
	return editFileSurgical(root, editArgs{
		Path:       rel,
		OldText:    oldText,
		NewText:    newText,
		ReplaceAll: replaceAll,
	})
}

// DeleteFileWorkspace deletes a single regular file jailed inside root.
func DeleteFileWorkspace(root, rel string) (string, error) {
	path, err := workspace.Resolve(root, rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("delete_file não remove diretórios inteiros: %s", rel)
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return "arquivo removido: " + rel, nil
}

// ReadFileReadOnly reads a jailed workspace file (reusing the same range and
// truncation guards as the squad's read_file tool).
func ReadFileReadOnly(root, rel string, startLine, endLine int) (string, error) {
	return readFileRange(root, readFileArgs{
		Path:      rel,
		StartLine: startLine,
		EndLine:   endLine,
	})
}

// ListDirReadOnly lists directory entries inside root, omitting .git, .hivemind,
// and .worktrees.
func ListDirReadOnly(root, rel string) ([]string, error) {
	path, err := workspace.Resolve(root, rel)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.EqualFold(name, ".git") || strings.EqualFold(name, ".hivemind") || strings.EqualFold(name, ".worktrees") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		out = append(out, name)
		if len(out) >= 400 {
			out = append(out, "…")
			break
		}
	}
	return out, nil
}

// SearchTextReadOnly searches text or regex matches inside root.
func SearchTextReadOnly(ctx context.Context, root, query, subPath, glob string, regex, caseSensitive bool, maxResults int) (string, error) {
	return runSearchText(ctx, root, searchTextArgs{
		Query:         query,
		Path:          subPath,
		Glob:          glob,
		Regex:         regex,
		CaseSensitive: caseSensitive,
		MaxResults:    maxResults,
	})
}

// FindFilesReadOnly finds files matching pattern inside root.
func FindFilesReadOnly(ctx context.Context, root, pattern, subPath string, maxResults int) (string, error) {
	return runFindFiles(ctx, root, findFilesArgs{
		Pattern:    pattern,
		Path:       subPath,
		MaxResults: maxResults,
	})
}
