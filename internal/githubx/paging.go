package githubx

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// defaultPageSize is the REST per_page used by paginated list endpoints.
	defaultPageSize = 100
	// maxPages caps Link/cursor pagination so a hostile or looping server can
	// never turn one list call into an unbounded crawl.
	maxPages = 10
)

var (
	sharedHTTPOnce sync.Once
	sharedClient   *http.Client
)

// SharedHTTPClient returns the pooled HTTP client every githubx transport
// reuses (REST requests, GraphQL calls made by consumers such as idecore).
// A per-request `&http.Client{}` would rebuild the transport — and therefore
// the connection pool — on every call (Spec 08).
func SharedHTTPClient() *http.Client {
	sharedHTTPOnce.Do(func() {
		sharedClient = &http.Client{Timeout: 30 * time.Second}
	})
	return sharedClient
}

// ParseLinkNext extracts the rel="next" URL from an RFC 5988 `Link` header:
//
//	Link: <https://api.github.com/repos/o/r/pulls?page=2>; rel="next", <…>; rel="first"
//
// It returns ("", false) when the current page is the last one.
func ParseLinkNext(linkHeader string) (string, bool) {
	for _, seg := range strings.Split(linkHeader, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		start := strings.IndexByte(seg, '<')
		end := strings.IndexByte(seg, '>')
		if start < 0 || end < start+1 {
			continue
		}
		target := seg[start+1 : end]
		attrs := seg[end+1:]
		for _, attr := range strings.Split(attrs, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(attr), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(k), "rel") {
				continue
			}
			v = strings.Trim(strings.TrimSpace(v), `"' `)
			if strings.EqualFold(v, "next") && target != "" {
				return target, true
			}
		}
	}
	return "", false
}

// nextPagePath converts an absolute `Link` next URL into the path+query that
// r.do expects (it prefixes r.base() itself). Returns ("", false) when next is
// on a different host (never follow cross-origin pagination).
func nextPagePath(nextURL, base string) (string, bool) {
	u, err := url.Parse(nextURL)
	if err != nil || u.Path == "" {
		return "", false
	}
	bu, bErr := url.Parse(base)
	if bErr == nil && u.Host != "" && bu.Host != "" && !strings.EqualFold(u.Host, bu.Host) {
		return "", false
	}
	if u.RawQuery != "" {
		return u.Path + "?" + u.RawQuery, true
	}
	return u.Path, true
}
