package modelx

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func fastRetry(base http.RoundTripper) *retryTransport {
	return &retryTransport{base: base, baseDelay: time.Millisecond, jitter: time.Millisecond}
}

func TestRetryTransportRetries429ThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	resp, err := fastRetry(nil).RoundTrip(mustRequest(t, srv.URL))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, queria 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("chamadas = %d, queria 3", got)
	}
}

func TestRetryTransportRetries503WithBodyReplay(t *testing.T) {
	var bodies []string
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		bodies = append(bodies, string(buf[:n]))
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body := "payload-objetivo-123"
	req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := fastRetry(nil).RoundTrip(req)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	defer resp.Body.Close()
	if len(bodies) != 2 || bodies[0] != body || bodies[1] != body {
		t.Fatalf("corpo não foi repetido no retry: %q", bodies)
	}
}

func TestRetryTransportHonorsRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	start := time.Now()
	resp, err := fastRetry(nil).RoundTrip(mustRequest(t, srv.URL))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	defer resp.Body.Close()
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("Retry-After=1 ignorado: esperava >= 1s, levou %v", elapsed)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status final = %d, queria 429 (todas as tentativas falham)", resp.StatusCode)
	}
}

func TestRetryTransportDoesNotRetry400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	resp, err := fastRetry(nil).RoundTrip(mustRequest(t, srv.URL))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	defer resp.Body.Close()
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("400 não deve ser repetido; chamadas = %d", calls)
	}
}

func TestRetryTransportStopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	req := reqWithContext(t, ctx, srv.URL)
	_, err := fastRetry(nil).RoundTrip(req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("erro = %v, queria context.Canceled", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		raw      string
		wantOK   bool
		minDelay time.Duration
	}{
		{"", false, 0},
		{"0", true, 0},
		{"2", true, 2 * time.Second},
		{"-3", false, 0},
		{"99999", true, retryAfterCap},
		{"not-a-date", false, 0},
	}
	for _, tc := range cases {
		got, ok := parseRetryAfter(tc.raw)
		if ok != tc.wantOK {
			t.Fatalf("parseRetryAfter(%q) ok = %v, queria %v", tc.raw, ok, tc.wantOK)
		}
		if ok && got < tc.minDelay {
			t.Fatalf("parseRetryAfter(%q) = %v, queria >= %v", tc.raw, got, tc.minDelay)
		}
	}
}

func mustRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func reqWithContext(t *testing.T, ctx context.Context, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
