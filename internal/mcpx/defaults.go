package mcpx

// DefaultServers é o catálogo que o produto conhece.
// Context7 e o Code Review Graph nascem ligados. O resto fica presente e
// desligado: o processo não sobe e o esquema das ferramentas não entra no
// contexto do agente até alguém ligar.
func DefaultServers() []Server {
	return []Server{
		{
			Name:        "context7",
			Description: "Documentação atual de bibliotecas (Go, Wails, ADK, npm).",
			Transport:   TransportHTTP,
			URL:         "https://mcp.context7.com/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer ${env:CONTEXT7_API_KEY}",
			},
			Enabled: true,
			Builtin: true,
		},
		{
			Name:        "code-review-graph",
			Description: "Grafo local do código e impacto de uma mudança. Exige uvx e Python 3.10+.",
			Transport:   TransportStdio,
			Command:     "uvx",
			Args:        []string{"code-review-graph", "serve"},
			Enabled:     true,
			Builtin:     true,
		},
		{
			Name:        "playwright",
			Description: "Automação do browser para a UI web. Não ligue junto com chrome-devtools.",
			Transport:   TransportStdio,
			Command:     "npx",
			Args:        []string{"-y", "@playwright/mcp@latest"},
			Enabled:     false,
			Builtin:     true,
		},
		{
			Name:        "chrome-devtools",
			Description: "Depura uma página já aberta no Chrome. Não ligue junto com playwright.",
			Transport:   TransportStdio,
			Command:     "npx",
			Args:        []string{"-y", "chrome-devtools-mcp@latest"},
			Enabled:     false,
			Builtin:     true,
		},
		{
			Name:        "toolbox",
			Description: "Postgres só em 127.0.0.1. O HiveMind não tem banco de aplicação. Nunca use credencial de produção.",
			Transport:   TransportStdio,
			Command:     "npx",
			Args:        []string{"-y", "@toolbox-sdk/server", "--prebuilt=postgres", "--stdio"},
			Env: map[string]string{
				"POSTGRES_HOST":     "127.0.0.1",
				"POSTGRES_PORT":     "5432",
				"POSTGRES_DATABASE": "${env:HIVEMIND_DEV_DB_NAME}",
				"POSTGRES_USER":     "${env:HIVEMIND_DEV_DB_USER}",
				"POSTGRES_PASSWORD": "${env:HIVEMIND_DEV_DB_PASSWORD}",
			},
			Enabled: false,
			Builtin: true,
		},
		{
			Name:        "github",
			Description: "Issues, repositórios e pull requests, com toolsets restritos. Token fino em GITHUB_MCP_TOKEN.",
			Transport:   TransportHTTP,
			URL:         "https://api.githubcopilot.com/mcp/",
			Headers: map[string]string{
				"Authorization":  "Bearer ${env:GITHUB_MCP_TOKEN}",
				"X-MCP-Toolsets": "repos,issues,pull_requests",
			},
			Enabled: false,
			Builtin: true,
		},
		{
			Name:        "figma-context",
			Description: "Lê um frame do Figma para implementar UI. Exige FIGMA_API_KEY.",
			Transport:   TransportStdio,
			Command:     "npx",
			Args:        []string{"-y", "figma-developer-mcp", "--stdio"},
			Env: map[string]string{
				"FIGMA_API_KEY": "${env:FIGMA_API_KEY}",
			},
			Enabled: false,
			Builtin: true,
		},
		{
			Name:        "codebase-memory",
			Description: "Grafo alternativo ao Code Review Graph. Nunca ligue os dois juntos.",
			Transport:   TransportStdio,
			Command:     "npx",
			Args:        []string{"-y", "codebase-memory-mcp"},
			Enabled:     false,
			Builtin:     true,
		},
	}
}
