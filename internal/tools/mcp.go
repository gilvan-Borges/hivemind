package tools

import (
	"fmt"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/gilvan-Borges/HiveMind/internal/mcpx"
)

func (d Deps) addMCP(out *[]tool.Tool) error {
	if d.MCP == nil {
		return nil
	}
	listTool, err := functiontool.New(functiontool.Config{
		Name:        "mcp_list_servers",
		Description: "Lista os servidores MCP do HiveMind. Servidores desligados aparecem só com nome e descrição: o esquema das ferramentas deles não entra no contexto. Não liga nada.",
	}, func(agent.Context, struct{}) (textResult, error) {
		return textResult{Text: formatMCPList(d.MCP.Snapshot())}, nil
	})
	if err != nil {
		return err
	}
	enableTool, err := functiontool.New(functiontool.Config{
		Name:        "mcp_enable_server",
		Description: "Pede permissão e liga um servidor MCP desligado só para esta tarefa. Playwright não convive com chrome-devtools. Code Review Graph não convive com codebase-memory. O Toolbox só aceita banco em 127.0.0.1. Ao terminar a execução o servidor pesado desliga de novo.",
	}, func(ctx agent.Context, args struct {
		Name string `json:"name" jsonschema:"Nome do servidor, como em mcp_list_servers."`
	}) (textResult, error) {
		name := strings.TrimSpace(args.Name)
		if name == "" {
			return textResult{}, fmt.Errorf("informe o nome do servidor MCP")
		}
		approve := d.Approve
		if approve == nil && d.Confirm != nil {
			approve = func(prompt string) bool { return d.Confirm(prompt) }
		}
		prompt := fmt.Sprintf("Ligar o servidor MCP %q só nesta tarefa? Ele pode ler documentação, o repositório ou um serviço externo, conforme a descrição no painel MCP.", name)
		if approve == nil || !approve(prompt) {
			return textResult{}, fmt.Errorf("permissão recusada para ligar o MCP %s", name)
		}
		msg, err := d.MCP.EnableForTask(ctx, name)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: msg + "\n" + formatMCPList(d.MCP.Snapshot()) + schemaBlock(d.MCP.ExposedTools(), name)}, nil
	})
	if err != nil {
		return err
	}
	callTool, err := functiontool.New(functiontool.Config{
		Name:        "mcp_call",
		Description: "Chama uma ferramenta de um servidor MCP que já está ligado nesta execução. Servidor desligado é recusado. arguments é um objeto JSON.",
	}, func(ctx agent.Context, args struct {
		Server    string `json:"server" jsonschema:"Nome do servidor MCP."`
		Tool      string `json:"tool" jsonschema:"Nome da ferramenta nesse servidor."`
		Arguments string `json:"arguments,omitempty" jsonschema:"Objeto JSON com os argumentos. Vazio significa {}."`
	}) (textResult, error) {
		parsed, err := mcpx.ParseArgs(args.Arguments)
		if err != nil {
			return textResult{}, err
		}
		text, err := d.MCP.Call(ctx, strings.TrimSpace(args.Server), strings.TrimSpace(args.Tool), parsed)
		if err != nil {
			return textResult{}, err
		}
		return textResult{Text: text}, nil
	})
	if err != nil {
		return err
	}
	*out = append(*out, listTool, enableTool, callTool)

	for _, remote := range d.MCP.ExposedTools() {
		remote := remote
		toolName := mcpToolName(remote.Server, remote.Name)
		desc := fmt.Sprintf("MCP %s · %s. %s", remote.Server, remote.Name, strings.TrimSpace(remote.Description))
		if remote.Schema != "" {
			desc += " Esquema: " + remote.Schema
		}
		exposed, err := functiontool.New(functiontool.Config{
			Name:        toolName,
			Description: desc,
		}, func(ctx agent.Context, args struct {
			Arguments string `json:"arguments,omitempty" jsonschema:"Objeto JSON com os argumentos da ferramenta. Vazio significa {}."`
		}) (textResult, error) {
			parsed, err := mcpx.ParseArgs(args.Arguments)
			if err != nil {
				return textResult{}, err
			}
			text, err := d.MCP.Call(ctx, remote.Server, remote.Name, parsed)
			if err != nil {
				return textResult{}, err
			}
			return textResult{Text: text}, nil
		})
		if err != nil {
			return err
		}
		*out = append(*out, exposed)
	}
	return nil
}

func formatMCPList(views []mcpx.ServerView) string {
	if len(views) == 0 {
		return "nenhum servidor MCP configurado"
	}
	var b strings.Builder
	for _, v := range views {
		state := "desligado"
		if v.Enabled || v.Status == "ready" {
			state = "ligado"
		}
		fmt.Fprintf(&b, "- %s [%s, %s]: %s\n", v.Name, state, v.Status, v.Description)
		if v.Detail != "" {
			fmt.Fprintf(&b, "  %s\n", v.Detail)
		}
		if len(v.Tools) > 0 {
			fmt.Fprintf(&b, "  ferramentas: %s\n", strings.Join(v.Tools, ", "))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func schemaBlock(tools []mcpx.RemoteTool, server string) string {
	var b strings.Builder
	for _, tool := range tools {
		if tool.Server != server {
			continue
		}
		fmt.Fprintf(&b, "\n- %s: %s", tool.Name, strings.TrimSpace(tool.Description))
		if tool.Schema != "" {
			fmt.Fprintf(&b, "\n  esquema: %s", tool.Schema)
		}
	}
	return b.String()
}

func mcpToolName(server, name string) string {
	raw := "mcp_" + server + "__" + name
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
