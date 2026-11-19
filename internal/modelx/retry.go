package modelx

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Retry policy for transient model API failures (HTTP 408/429/5xx and network
// errors). A single failed request must not take down a whole multi-agent run.
const (
	retryMaxAttempts     = 4
	retryBaseDelay       = 700 * time.Millisecond
	retryMaxDelay        = 15 * time.Second
	retryJitter          = 250 * time.Millisecond
	retryAfterCap        = 30 * time.Second
	retryBodyBufferLimit = 8 << 20 // 8 MiB; larger request bodies are not retried
)

// newRetryTransport wraps base (http.DefaultTransport when nil) with a
// RoundTripper that retries transient model API failures: HTTP 408, 429 and
// 5xx responses, plus transport-level network errors. It honors Retry-After
// when present (capped at 30s) and otherwise backs off exponentially with
// jitter. Request bodies are buffered once so retries can replay them.
func newRetryTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &retryTransport{base: base, baseDelay: retryBaseDelay, jitter: retryJitter}
}

// retryHTTPClient returns an *http.Client with transient-failure retry built
// in, for ADK SDK client configs (Gemini, Anthropic, OpenAI, OpenCode).
func retryHTTPClient() *http.Client {
	return &http.Client{Transport: newRetryTransport(nil)}
}

type retryTransport struct {
	base      http.RoundTripper
	baseDelay time.Duration
	jitter    time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req.Body != nil && req.GetBody == nil {
		if err := makeBodyReplayable(req); err != nil {
			return nil, err
		}
	}
	var resp *http.Response
	var err error
	for attempt := 0; ; attempt++ {
		resp, err = base.RoundTrip(req)
		if req.Context().Err() != nil || attempt >= retryMaxAttempts-1 || !shouldRetry(resp, err) {
			return resp, err
		}
		delay := t.delayFor(attempt, resp)
		drainClose(resp)
		if resetErr := resetBody(req); resetErr != nil {
			return nil, resetErr
		}
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(delay):
		}
	}
}

func shouldRetry(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	if resp == nil {
		return true
	}
	switch resp.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func (t *retryTransport) delayFor(attempt int, resp *http.Response) time.Duration {
	delay := t.baseDelay << attempt // 0.7s, 1.4s, 2.8s
	if delay > retryMaxDelay {
		delay = retryMaxDelay
	}
	if resp != nil {
		if after, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok && after > delay {
			delay = after
		}
	}
	if t.jitter > 0 {
		delay += time.Duration(rand.Int64N(int64(t.jitter)))
	}
	return delay
}

func parseRetryAfter(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		if secs < 0 {
			return 0, false
		}
		if secs > int(retryAfterCap/time.Second) {
			return retryAfterCap, true
		}
		return time.Duration(secs) * time.Second, true
	}
	if at, err := http.ParseTime(raw); err == nil {
		d := time.Until(at)
		if d <= 0 {
			return 0, true
		}
		if d > retryAfterCap {
			d = retryAfterCap
		}
		return d, true
	}
	return 0, false
}

func drainClose(resp *http.Response) {
	if resp == nil {
		return
	}
	if resp.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
	}
}

func makeBodyReplayable(req *http.Request) error {
	data, err := io.ReadAll(io.LimitReader(req.Body, retryBodyBufferLimit+1))
	_ = req.Body.Close()
	if err != nil {
		return err
	}
	if len(data) > retryBodyBufferLimit {
		return fmt.Errorf("corpo da requisição excede %d bytes e não pode ser repetido em retry", retryBodyBufferLimit)
	}
	buf := bytes.NewReader(data)
	req.Body = io.NopCloser(buf)
	req.GetBody = func() (io.ReadCloser, error) {
		_, _ = buf.Seek(0, io.SeekStart)
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	return nil
}

func resetBody(req *http.Request) error {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	if req.GetBody == nil {
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		return err
	}
	req.Body = body
	return nil
}
