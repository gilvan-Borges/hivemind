package modelx

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Spec 06 §3.1: one pooled HTTP transport shared by every modelx client
// (Gemini, Anthropic, OpenAI, OpenRouter, OpenCode, TypeSafe) with keep-alive
// and HTTP/2, instead of ad-hoc &http.Client{} instances per call.
var (
	sharedTransportOnce sync.Once
	sharedTransportInst *http.Transport
)

func sharedHTTPTransport() http.RoundTripper {
	sharedTransportOnce.Do(func() {
		sharedTransportInst = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          64,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DialContext:           nil, // http.DefaultTransport dialer semantics
		}
		sharedTransportInst.DialContext = http.DefaultTransport.(*http.Transport).DialContext
	})
	return sharedTransportInst
}

// swrEntry is one cached value: a success with a positive TTL, or a failure
// with a short negative TTL so an offline/rate-limited endpoint is not hit
// again on every routing iteration (Spec 06 §1: "falhas não gravam cache
// negativo, repetindo os requests em toda iteração").
type swrEntry[V any] struct {
	val       V
	err       error
	fetchedAt time.Time
}

// swrCache is a stale-while-revalidate cache with negative caching and
// singleflight dedup. It is built for the Jev hot path: reads never block on
// network I/O — a stale value is returned immediately while a single
// background refresh runs; a cold read may optionally skip the fetch.
type swrCache[V any] struct {
	mu      sync.Mutex
	entries map[string]*swrEntry[V]
	ttl     time.Duration // success TTL
	negTTL  time.Duration // failure backoff
	sf      singleflight.Group
	// clock is swappable for tests.
	clock func() time.Time
}

func newSWRCache[V any](ttl, negTTL time.Duration) *swrCache[V] {
	return &swrCache[V]{
		entries: map[string]*swrEntry[V]{},
		ttl:     ttl,
		negTTL:  negTTL,
		clock:   time.Now,
	}
}

func (c *swrCache[V]) now() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

// get returns the cached value for key when one exists (fresh or stale).
func (c *swrCache[V]) get(key string) (swrEntry[V], bool) {
	c.mu.Lock()
	e, ok := c.entries[key]
	c.mu.Unlock()
	if !ok || e == nil {
		return swrEntry[V]{}, false
	}
	return *e, true
}

// fresh reports whether the entry is still within its positive/negative TTL.
func (c *swrCache[V]) fresh(e *swrEntry[V]) bool {
	ttl := c.ttl
	if e.err != nil {
		ttl = c.negTTL
	}
	return c.now().Sub(e.fetchedAt) < ttl
}

// fetch returns the fresh cached value, or computes it exactly once per key
// (singleflight): concurrent callers wait for the same in-flight result.
func (c *swrCache[V]) fetch(ctx context.Context, key string, compute func(context.Context) (V, error)) (V, error) {
	if e, ok := c.get(key); ok && c.fresh(&e) {
		return e.val, e.err
	}
	v, err, _ := c.sf.Do(key, func() (any, error) {
		// Re-check under the flight: a concurrent refresh may have landed.
		if e, ok := c.get(key); ok && c.fresh(&e) {
			var zero V
			if e.err != nil {
				return zero, e.err
			}
			return e.val, nil
		}
		val, cerr := compute(ctx)
		c.mu.Lock()
		c.entries[key] = &swrEntry[V]{val: val, err: cerr, fetchedAt: c.now()}
		c.mu.Unlock()
		return val, cerr
	})
	if err != nil {
		var zero V
		return zero, err
	}
	return v.(V), nil
}

// peek returns the cached entry without any computation (hot-path read).
func (c *swrCache[V]) peek(key string) (V, error, bool) {
	e, ok := c.get(key)
	if !ok {
		var zero V
		return zero, nil, false
	}
	return e.val, e.err, true
}
