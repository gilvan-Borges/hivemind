package resident

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/strx"
)

// ValidateAllowedHost checks whether host pattern is valid for the network allowlist.
// Wildcard "*" or "*.*" is strictly forbidden (fail closed).
func ValidateAllowedHost(pattern string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(pattern))
	if p == "" {
		return "", errors.New("host da allowlist não pode ser vazio")
	}
	if strings.ContainsAny(p, " \t\r\n/\\:@") {
		return "", fmt.Errorf("formato de host inválido na allowlist: %q", pattern)
	}
	if p == "*" || p == "*.*" || strings.Count(p, "*") > 1 || (strings.Contains(p, "*") && !strings.HasPrefix(p, "*.")) {
		return "", fmt.Errorf("curinga amplo %q não permitido na allowlist de rede", pattern)
	}
	if strings.HasPrefix(p, "*.") && len(strings.TrimPrefix(p, "*.")) < 3 {
		return "", fmt.Errorf("subdomínio curinga muito curto: %q", pattern)
	}
	return p, nil
}

// HostMatchesAllowlist verifies if host matches an entry in allowedHosts.
// Empty allowlist always denies (outbound network blocked by default, §12.3).
func HostMatchesAllowlist(host string, allowedHosts []string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" || len(allowedHosts) == 0 {
		return false
	}
	h = strings.TrimSuffix(h, ".")
	for _, raw := range allowedHosts {
		pat, err := ValidateAllowedHost(raw)
		if err != nil {
			continue
		}
		if strings.HasPrefix(pat, "*.") {
			base := strings.TrimPrefix(pat, "*.")
			if strings.HasSuffix(h, "."+base) && len(h) > len(base)+1 {
				return true
			}
			continue
		}
		if h == pat {
			return true
		}
	}
	return false
}

// ValidateOutboundURL verifies scheme, host allowlist membership, and literal IP safety.
func ValidateOutboundURL(rawURL string, allowedHosts []string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("URL obrigatória para net_fetch")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("URL inválida: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return nil, fmt.Errorf("esquema %q não permitido (apenas http/https)", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return nil, errors.New("host ausente na URL")
	}
	if len(allowedHosts) == 0 {
		return nil, errors.New("rede de saída bloqueada por padrão: configure allowedHosts para liberar este domínio")
	}
	if !HostMatchesAllowlist(host, allowedHosts) {
		return nil, fmt.Errorf("host %q não consta na allowlist de rede", host)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if reason := blockedIPReason(ip); reason != "" {
			return nil, fmt.Errorf("IP bloqueado por proteção SSRF (%s): %s", reason, host)
		}
	} else if isLocalHostname(host) {
		return nil, fmt.Errorf("hostname local bloqueado (%s)", host)
	}
	return u, nil
}

func isLocalHostname(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	return h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal")
}

func blockedIPReason(addr netip.Addr) string {
	addr = addr.Unmap()
	if addr.IsLoopback() {
		return "loopback"
	}
	if addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		// Covers 169.254.0.0/16 (cloud metadata 169.254.169.254) and fe80::/10.
		return "link-local/metadata"
	}
	if addr.IsPrivate() {
		return "rede privada"
	}
	if addr.IsMulticast() || addr.IsUnspecified() {
		return "multicast/não especificado"
	}
	// CGNAT 100.64.0.0/10
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	if cgnat.Contains(addr) {
		return "CGNAT"
	}
	return ""
}

// NewGuardedHTTPClient creates an HTTP client that re-checks allowlist, DNS IPs
// before dialing, and every HTTP redirect target against SSRF and exfiltration.
func NewGuardedHTTPClient(allowedHosts []string, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	tr := &http.Transport{
		Proxy: nil, // Never inherit unchecked HTTP_PROXY for resident requests.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if !HostMatchesAllowlist(host, allowedHosts) {
				return nil, fmt.Errorf("conexão recusada: host %q fora da allowlist", host)
			}
			if isLocalHostname(host) {
				return nil, fmt.Errorf("conexão recusada: host local %q bloqueado", host)
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("DNS sem endereços para %q", host)
			}
			for _, ip := range ips {
				if reason := blockedIPReason(ip); reason != "" {
					return nil, fmt.Errorf("conexão bloqueada por proteção SSRF (%s -> %s)", host, reason)
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("excesso de redirecionamentos HTTP")
			}
			if _, err := ValidateOutboundURL(req.URL.String(), allowedHosts); err != nil {
				return fmt.Errorf("redirecionamento bloqueado: %w", err)
			}
			return nil
		},
	}
}

// ExecuteAllowlistedFetch performs an HTTP GET/HEAD request guarded by ValidateOutboundURL,
// NewGuardedHTTPClient (DNS/IP SSRF blocking + redirect checks), optional auth header, and bounded body.
func ExecuteAllowlistedFetch(ctx context.Context, client *http.Client, rawURL, method string, allowedHosts []string, authHeaderName, authHeaderVal string) (string, error) {
	u, err := ValidateOutboundURL(rawURL, allowedHosts)
	if err != nil {
		return "", err
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead {
		return "", fmt.Errorf("método HTTP %q não permitido (apenas GET/HEAD)", method)
	}
	if client == nil {
		client = NewGuardedHTTPClient(allowedHosts, 15*time.Second)
	}
	guarded := *client
	if guarded.Timeout == 0 {
		guarded.Timeout = 15 * time.Second
	}
	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("excesso de redirecionamentos HTTP")
		}
		if _, rErr := ValidateOutboundURL(req.URL.String(), allowedHosts); rErr != nil {
			return fmt.Errorf("redirecionamento bloqueado: %w", rErr)
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "HiveMind-Resident/2.0")
	if authHeaderName != "" && authHeaderVal != "" {
		req.Header.Set(authHeaderName, authHeaderVal)
	}

	resp, err := guarded.Do(req)
	if err != nil {
		return "", fmt.Errorf("falha na requisição HTTP: %w", err)
	}
	defer resp.Body.Close()

	const maxBody = 64 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return "", fmt.Errorf("falha ao ler resposta HTTP: %w", err)
	}
	text := string(body)
	if len(body) > maxBody {
		text = strx.TruncateBytes(text, maxBody, "\n… [truncado]")
	}
	return fmt.Sprintf("HTTP %d %s\n\n%s", resp.StatusCode, resp.Status, text), nil
}
