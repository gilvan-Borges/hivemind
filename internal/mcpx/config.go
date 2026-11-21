// Package mcpx é o cliente MCP do HiveMind: configuração por usuário e por
// projeto, com servidores pesados desligados até alguém pedir.
package mcpx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
	"github.com/gilvan-Borges/HiveMind/internal/procx"
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
	if len(proj.Servers) > 0 && !IsWorkspaceConfigTrusted(workspace) {
		proj = stripUntrustedProjectExec(merged, proj)
	}
	return merge(merged, proj), nil
}

type trustedWorkspacesFile struct {
	Hashes map[string]string `json:"hashes,omitempty"`
}

func trustedConfigPath() (string, error) {
	up, err := userConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(up), "trusted-mcp.json"), nil
}

func workspaceConfigDigest(workspace string) (cleanWS, digest string, exists bool, err error) {
	projPath := projectConfigPath(workspace)
	if projPath == "" {
		return "", "", false, nil
	}
	data, err := os.ReadFile(projPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	cleanWS = filepath.Clean(workspace)
	if abs, absErr := filepath.Abs(cleanWS); absErr == nil {
		cleanWS = abs
	}
	sum := sha256.Sum256(data)
	return cleanWS, hex.EncodeToString(sum[:]), true, nil
}

// IsWorkspaceConfigTrusted reports whether the repository's .hivemind/mcp-servers.json
// either does not exist or matches the SHA-256 explicitly trusted by the user.
func IsWorkspaceConfigTrusted(workspace string) bool {
	cleanWS, digest, exists, err := workspaceConfigDigest(workspace)
	if err != nil || !exists {
		return err == nil && !exists
	}
	trustPath, err := trustedConfigPath()
	if err != nil {
		return false
	}
	raw, err := os.ReadFile(trustPath)
	if err != nil {
		return false
	}
	var tf trustedWorkspacesFile
	if err := json.Unmarshal(raw, &tf); err != nil {
		return false
	}
	return tf.Hashes[cleanWS] != "" && tf.Hashes[cleanWS] == digest
}

// TrustWorkspaceConfig records the current SHA-256 digest of <workspace>/.hivemind/mcp-servers.json
// as explicitly approved by the user.
func TrustWorkspaceConfig(workspace string) error {
	cleanWS, digest, exists, err := workspaceConfigDigest(workspace)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	trustPath, err := trustedConfigPath()
	if err != nil {
		return err
	}
	var tf trustedWorkspacesFile
	if raw, rErr := os.ReadFile(trustPath); rErr == nil {
		_ = json.Unmarshal(raw, &tf)
	}
	if tf.Hashes == nil {
		tf.Hashes = make(map[string]string)
	}
	tf.Hashes[cleanWS] = digest
	data, err := json.MarshalIndent(tf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(trustPath), 0o700); err != nil {
		return err
	}
	return atomicx.WriteFile(trustPath, append(data, '\n'), 0o600)
}

func stripUntrustedProjectExec(base, proj File) File {
	var safe []Server
	for _, s := range proj.Servers {
		name := strings.TrimSpace(s.Name)
		if name == "" || findServer(base.Servers, name) == nil {
			continue
		}
		if s.Transport != "" || s.Command != "" || len(s.Args) > 0 || len(s.Env) > 0 || s.URL != "" || len(s.Headers) > 0 {
			continue
		}
		safe = append(safe, Server{
			Name:        name,
			Description: s.Description,
			Enabled:     s.Enabled,
		})
	}
	return File{Servers: safe}
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicx.WriteFile(path, data, 0o600)
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
	out, missing := expandKeys(raw, workspace)
	return out, len(missing) == 0
}

func expandKeys(raw, workspace string) (string, []string) {
	const (
		envPrefix         = "${env:"
		envOptionalPrefix = "${env?:"
	)
	if !strings.Contains(raw, "${") {
		return raw, nil
	}
	var b strings.Builder
	var missing []string
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
		case strings.HasPrefix(token, envOptionalPrefix) && strings.HasSuffix(token, "}"):
			key := strings.TrimSuffix(strings.TrimPrefix(token, envOptionalPrefix), "}")
			val, _ := lookupEnv(key)
			b.WriteString(val)
		case strings.HasPrefix(token, envPrefix) && strings.HasSuffix(token, "}"):
			key := strings.TrimSuffix(strings.TrimPrefix(token, envPrefix), "}")
			val, ok := lookupEnv(key)
			if !ok {
				missing = append(missing, key)
			}
			b.WriteString(val)
		default:
			b.WriteString(token)
		}
	}
	return b.String(), missing
}

// lookupEnv lê a variável. GITHUB_MCP_TOKEN ausente cai para `gh auth token`
// só na memória: o valor não é gravado e quem chama não pode logar o retorno.
func lookupEnv(key string) (string, bool) {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v, true
	}
	if key == "GITHUB_MCP_TOKEN" && ghAuthToken != nil {
		if v := strings.TrimSpace(ghAuthToken()); v != "" {
			return v, true
		}
	}
	return "", false
}

// ghAuthToken é trocado nos testes. O padrão chama o gh e descarta o stderr.
var ghAuthToken = readGHAuthToken

var (
	ghTokenMu     sync.Mutex
	ghTokenCached string
	ghTokenAt     time.Time
	ghTokenTTL    = 2 * time.Minute
)

// readGHAuthToken guarda o token só na memória por alguns minutos.
// A lista MCP e a conexão compartilham esse cache para não disparar `gh` a cada chamada.
func readGHAuthToken() string {
	ghTokenMu.Lock()
	defer ghTokenMu.Unlock()
	if !ghTokenAt.IsZero() && time.Since(ghTokenAt) < ghTokenTTL {
		return ghTokenCached
	}
	ghTokenCached = execGHAuthToken()
	ghTokenAt = time.Now()
	return ghTokenCached
}

func execGHAuthToken() string {
	ghBin, err := lookPath("gh")
	if err != nil || strings.TrimSpace(ghBin) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ghBin, "auth", "token", "--hostname", "github.com")
	procx.ConfigureContext(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

func expandServer(s Server, workspace string) (Server, []string) {
	var missing []string
	take := func(v string, miss []string) string {
		missing = append(missing, miss...)
		return v
	}
	s.Command = take(expandKeys(s.Command, workspace))
	for i, arg := range s.Args {
		s.Args[i] = take(expandKeys(arg, workspace))
	}
	for k, v := range s.Env {
		s.Env[k] = take(expandKeys(v, workspace))
	}
	s.URL = take(expandKeys(s.URL, workspace))
	for k, v := range s.Headers {
		s.Headers[k] = take(expandKeys(v, workspace))
	}
	s.Headers = stripEmptyBearer(s.Headers)
	return s, missing
}

func stripEmptyBearer(headers map[string]string) map[string]string {
	for k, v := range headers {
		if !strings.EqualFold(k, "Authorization") {
			continue
		}
		rest := strings.TrimSpace(v)
		low := strings.ToLower(rest)
		if strings.HasPrefix(low, "bearer") {
			rest = strings.TrimSpace(rest[len("bearer"):])
		}
		if rest == "" {
			delete(headers, k)
		}
	}
	return headers
}

func secretMessage(missing []string) string {
	if len(missing) == 0 {
		return "falta variável de ambiente referenciada na configuração (nada foi gravado no Git)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "faltam variáveis: %s. Nada foi gravado no Git.", strings.Join(missing, ", "))
	for _, k := range missing {
		switch k {
		case "CONTEXT7_API_KEY":
			b.WriteString(" Defina CONTEXT7_API_KEY (chave grátis no painel do Context7).")
		case "GITHUB_MCP_TOKEN":
			b.WriteString(" Defina GITHUB_MCP_TOKEN ou rode `gh auth login`. O token do gh é lido na hora, não é gravado e não entra no log.")
		case "FIGMA_API_KEY":
			b.WriteString(" Defina FIGMA_API_KEY.")
		}
	}
	return b.String()
}

var (
	bearerValue  = regexp.MustCompile(`(?i)bearer\s+\S+`)
	ghTokenValue = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]+|github_pat_[A-Za-z0-9_]+`)
)

func redactSecrets(s string) string {
	s = bearerValue.ReplaceAllString(s, "Bearer [redigido]")
	s = ghTokenValue.ReplaceAllString(s, "[redigido]")
	return s
}

// RuntimeLabel é o que a lista mostra. Erro, segredo ausente e binário
// ausente não aparecem como "ligado".
func (v ServerView) RuntimeLabel() string {
	switch v.Status {
	case "ready":
		return "ligado"
	case "needs-secret", "error", "unavailable":
		return "indisponível"
	case "off":
		return "desligado"
	case "pending":
		if v.Enabled {
			return "ligado"
		}
		return "desligado"
	default:
		if v.Enabled {
			return "ligado"
		}
		return "desligado"
	}
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
