// Package mcpx é o cliente MCP do HiveMind: configuração por usuário e por
// projeto, com servidores pesados desligados até alguém pedir.
package mcpx

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

// Server é um servidor MCP configurável. Segredos ficam como ${env:NOME},
// nunca expandidos no arquivo gravado.
type Server struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Transport   string            `json:"transport"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Enabled     bool              `json:"enabled"`
	Builtin     bool              `json:"builtin,omitempty"`
}

// File é a lista persistida. Um arquivo de override só precisa repetir os
// servidores que muda.
type File struct {
	Servers []Server `json:"servers"`
}

// ServerView é o que a IDE e a ferramenta leve mostram ao agente.
// Servidores desligados não carregam esquema de ferramenta.
type ServerView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Transport   string   `json:"transport"`
	Enabled     bool     `json:"enabled"`
	Builtin     bool     `json:"builtin"`
	Status      string   `json:"status"`
	Detail      string   `json:"detail,omitempty"`
	Tools       []string `json:"tools,omitempty"`
}

// RemoteTool é uma ferramenta de um servidor que está ligado agora.
type RemoteTool struct {
	Server      string
	Name        string
	Description string
	Schema      string
}

// userConfigPathFn is replaced in tests so they never write the developer profile.
// On Windows UserConfigDir follows APPDATA, not XDG_CONFIG_HOME.
var userConfigPathFn = defaultUserConfigPath

func userConfigPath() (string, error) {
	return userConfigPathFn()
}

func defaultUserConfigPath() (string, error) {
	// XDG_CONFIG_HOME is honored on every OS. os.UserConfigDir already does
	// that on Unix and ignores it on Windows, which made the tests (and any
	// explicit override) write to %AppData% instead of the temp dir.
	if base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); base != "" {
		return filepath.Join(base, "HiveMind", "mcp-servers.json"), nil
	}
	if base, err := os.UserConfigDir(); err == nil && strings.TrimSpace(base) != "" {
		return filepath.Join(base, "HiveMind", "mcp-servers.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".hivemind", "mcp-servers.json"), nil
}

func projectConfigPath(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ""
	}
	return filepath.Join(workspace, ".hivemind", "mcp-servers.json")
}

// Load junta os padrões, o arquivo do usuário e o overlay do projeto.
// O projeto só cobre o que declara. Nada aqui é commitado: o do usuário fica
// fora do repo e o do projeto fica em .hivemind/, que já está no gitignore.
func Load(workspace string) (File, error) {
	merged := File{Servers: cloneServers(DefaultServers())}
	userPath, err := userConfigPath()
	if err != nil {
		return File{}, err
	}
	user, err := readFile(userPath)
	if err != nil {
		return File{}, err
	}
	merged = merge(merged, user)
	proj, err := readFile(projectConfigPath(workspace))
	if err != nil {
		return File{}, err
	}
	return merge(merged, proj), nil
}

// SetEnabled grava o interruptor. projectOnly escreve em .hivemind/ do
// workspace; senão, no config do usuário. Ligar um lado de um par exclusivo
// desliga o outro no mesmo arquivo.
func SetEnabled(workspace, name string, enabled, projectOnly bool) (File, error) {
	name = strings.TrimSpace(name)
	current, err := Load(workspace)
	if err != nil {
		return File{}, err
	}
	if findServer(current.Servers, name) == nil {
		return File{}, fmt.Errorf("servidor MCP desconhecido: %s", name)
	}
	path := ""
	if projectOnly {
		path = projectConfigPath(workspace)
		if path == "" {
			return File{}, fmt.Errorf("informe a pasta do projeto para gravar o MCP só nele")
		}
	} else {
		path, err = userConfigPath()
		if err != nil {
			return File{}, err
		}
	}
	overlay, err := readFile(path)
	if err != nil {
		return File{}, err
	}
	overlay = upsertEnabled(overlay, name, enabled)
	if enabled {
		if other := exclusivePartner(name); other != "" {
			overlay = upsertEnabled(overlay, other, false)
		}
	}
	if err := writeFile(path, overlay); err != nil {
		return File{}, err
	}
	return Load(workspace)
}

func upsertEnabled(f File, name string, enabled bool) File {
	for i := range f.Servers {
		if f.Servers[i].Name == name {
			f.Servers[i].Enabled = enabled
			return f
		}
	}
	f.Servers = append(f.Servers, Server{Name: name, Enabled: enabled})
	return f
}

func merge(base, overlay File) File {
	if len(overlay.Servers) == 0 {
		return base
	}
	out := File{Servers: cloneServers(base.Servers)}
	for _, extra := range overlay.Servers {
		name := strings.TrimSpace(extra.Name)
		if name == "" {
			continue
		}
		extra.Name = name
		if i := indexServer(out.Servers, name); i >= 0 {
			dst := &out.Servers[i]
			dst.Enabled = extra.Enabled
			if extra.Description != "" {
				dst.Description = extra.Description
			}
			if extra.Transport != "" {
				dst.Transport = extra.Transport
			}
			if extra.Command != "" {
				dst.Command = extra.Command
			}
			if len(extra.Args) > 0 {
				dst.Args = append([]string(nil), extra.Args...)
			}
			if len(extra.Env) > 0 {
				dst.Env = cloneMap(extra.Env)
			}
			if extra.URL != "" {
				dst.URL = extra.URL
			}
			if len(extra.Headers) > 0 {
				dst.Headers = cloneMap(extra.Headers)
			}
			continue
		}
		out.Servers = append(out.Servers, extra)
	}
	return out
}

func readFile(path string) (File, error) {
	if strings.TrimSpace(path) == "" {
		return File{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, nil
		}
		return File{}, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return File{}, nil
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("ler %s: %w", path, err)
	}
	return f, nil
}

func writeFile(path string, f File) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func findServer(list []Server, name string) *Server {
	i := indexServer(list, name)
	if i < 0 {
		return nil
	}
	return &list[i]
}

func indexServer(list []Server, name string) int {
	for i := range list {
		if list[i].Name == name {
			return i
		}
	}
	return -1
}

func cloneServers(in []Server) []Server {
	out := make([]Server, len(in))
	for i, s := range in {
		s.Args = append([]string(nil), s.Args...)
		s.Env = cloneMap(s.Env)
		s.Headers = cloneMap(s.Headers)
		out[i] = s
	}
	return out
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func expand(raw, workspace string) (string, bool) {
	const prefix = "${env:"
	if !strings.Contains(raw, "${") {
		return raw, true
	}
	var b strings.Builder
	ok := true
	rest := raw
	for {
		i := strings.Index(rest, "${")
		if i < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:i])
		rest = rest[i:]
		end := strings.Index(rest, "}")
		if end < 0 {
			b.WriteString(rest)
			break
		}
		token := rest[:end+1]
		rest = rest[end+1:]
		switch {
		case token == "${workspaceFolder}":
			b.WriteString(workspace)
		case strings.HasPrefix(token, prefix) && strings.HasSuffix(token, "}"):
			key := strings.TrimSuffix(strings.TrimPrefix(token, prefix), "}")
			val := os.Getenv(key)
			if strings.TrimSpace(val) == "" {
				ok = false
			}
			b.WriteString(val)
		default:
			b.WriteString(token)
		}
	}
	return b.String(), ok
}

func expandServer(s Server, workspace string) (Server, bool) {
	ok := true
	if cmd, good := expand(s.Command, workspace); !good {
		ok = false
	} else {
		s.Command = cmd
	}
	for i, arg := range s.Args {
		v, good := expand(arg, workspace)
		s.Args[i] = v
		if !good {
			ok = false
		}
	}
	for k, v := range s.Env {
		ev, good := expand(v, workspace)
		s.Env[k] = ev
		if !good {
			ok = false
		}
	}
	if u, good := expand(s.URL, workspace); !good {
		ok = false
	} else {
		s.URL = u
	}
	for k, v := range s.Headers {
		ev, good := expand(v, workspace)
		s.Headers[k] = ev
		if !good {
			ok = false
		}
	}
	return s, ok
}

func exclusivePartner(name string) string {
	pairs := [][2]string{
		{"playwright", "chrome-devtools"},
		{"code-review-graph", "codebase-memory"},
	}
	for _, p := range pairs {
		if name == p[0] {
			return p[1]
		}
		if name == p[1] {
			return p[0]
		}
	}
	return ""
}

func validateLocal(s Server) error {
	if s.Name != "toolbox" {
		return nil
	}
	host := strings.TrimSpace(s.Env["POSTGRES_HOST"])
	if host == "" {
		host = "127.0.0.1"
	}
	if !isLoopback(host) {
		return fmt.Errorf("o Toolbox só pode falar com banco local (127.0.0.1 ou localhost); %q foi recusado", host)
	}
	return nil
}

func isLoopback(host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	h = strings.Trim(h, "[]")
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}
