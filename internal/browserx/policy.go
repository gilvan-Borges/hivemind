package browserx

import (
	"fmt"
	"net/url"
	"strings"
)

// Palavras que disparam aprovação. A extensão repete a mesma lista: se o
// nome acessível do elemento cair aqui, o clique não acontece sem _approved.
var sensitiveWords = []string{
	"submit",
	"enviar",
	"comprar",
	"buy now",
	"buy",
	"pay",
	"pagar",
	"checkout",
	"purchase",
	"place order",
	"assinar",
	"subscribe",
	"transferir",
	"sacar",
	"withdraw",
	"confirmar compra",
	"excluir conta",
	"delete account",
}

func sensitiveText(s string) bool {
	s = strings.ToLower(s)
	for _, w := range sensitiveWords {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		s := strings.TrimSpace(strings.ToLower(t))
		return s == "true" || s == "1" || s == "yes"
	case float64:
		return t != 0
	default:
		return false
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// classify decide se a ferramenta pede aprovação antes de ir ao Chrome.
// host, quando preenchido, entra na allowlist só depois do sim.
func classify(tool string, args map[string]any, allowed func(string) bool) (needs bool, prompt, host string, err error) {
	switch tool {
	case "javascript_exec":
		expr := strings.TrimSpace(asString(args["expression"]))
		if expr == "" {
			return false, "", "", fmt.Errorf("expression vazia")
		}
		if len(expr) > 20_000 {
			return false, "", "", fmt.Errorf("expression longa demais")
		}
		return true, "Executar JavaScript na página aberta do Chrome? O resultado volta para o agente.", "", nil
	case "form_input":
		if truthy(args["submit"]) || sensitiveText(asString(args["name"])) {
			return true, "Enviar ou confirmar um formulário no Chrome?", "", nil
		}
	case "click":
		if truthy(args["submit"]) || sensitiveText(asString(args["name"])) {
			return true, "Clique que pode enviar, comprar ou confirmar no Chrome?", "", nil
		}
	case "navigate", "tabs_create":
		raw := strings.TrimSpace(asString(args["url"]))
		if raw == "" || raw == "about:blank" {
			return false, "", "", nil
		}
		h, herr := httpHost(raw)
		if herr != nil {
			return false, "", "", herr
		}
		if !allowed(h) {
			return true, fmt.Sprintf("O Agent Hive quer abrir %s no seu Chrome. Esse site ainda não está na lista permitida.", h), h, nil
		}
	}
	return false, "", "", nil
}

func httpHost(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("url inválida")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("só http e https; %q foi recusado", scheme)
	}
	if u.User != nil {
		return "", fmt.Errorf("url com usuário ou senha foi recusada")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "" {
		return "", fmt.Errorf("url sem host")
	}
	return host, nil
}

func activityText(tool string, args map[string]any) string {
	switch tool {
	case "navigate", "tabs_create":
		raw := strings.TrimSpace(asString(args["url"]))
		if h, err := httpHost(raw); err == nil {
			return tool + " " + h
		}
		if raw == "" {
			return tool
		}
		return tool + " about:blank"
	case "javascript_exec", "type", "form_input":
		// O texto digitado e o script podem ser senha ou token.
		return tool
	default:
		return tool
	}
}
