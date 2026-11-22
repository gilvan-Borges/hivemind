package mcpx

import (
	"context"
	"testing"
	"time"
)

func TestCallTimeoutForClamps(t *testing.T) {
	cases := []struct {
		srv  *Server
		want time.Duration
	}{
		{nil, DefaultCallTimeout},
		{&Server{}, DefaultCallTimeout},
		{&Server{TimeoutSeconds: -5}, DefaultCallTimeout},
		{&Server{TimeoutSeconds: 1}, minCallTimeout},
		{&Server{TimeoutSeconds: 30}, 30 * time.Second},
		{&Server{TimeoutSeconds: 100_000}, maxCallTimeout},
	}
	for i, tc := range cases {
		if got := callTimeoutFor(tc.srv); got != tc.want {
			t.Fatalf("case %d: got %v want %v", i, got, tc.want)
		}
	}
}

// timeoutRecordingSession captures the ctx one Call receives.
type timeoutRecordingSession struct {
	gotDeadline bool
}

func (s *timeoutRecordingSession) Tools(context.Context) ([]RemoteTool, error) { return nil, nil }
func (s *timeoutRecordingSession) Close() error                               { return nil }
func (s *timeoutRecordingSession) Call(ctx context.Context, _ string, _ map[string]any) (string, error) {
	dl, ok := ctx.Deadline()
	s.gotDeadline = ok && time.Until(dl) > 0
	return "ok", nil
}

func TestCallWrapsSessionWithDeadline(t *testing.T) {
	h := &Host{
		servers: []Server{{Name: "toolbox"}},
		live:    map[string]session{"toolbox": &timeoutRecordingSession{}},
	}
	if _, err := h.Call(context.Background(), "toolbox", "query", nil); err != nil {
		t.Fatalf("call failed: %v", err)
	}
	sess := h.live["toolbox"].(*timeoutRecordingSession)
	if !sess.gotDeadline {
		t.Fatalf("session Call ran without a per-call deadline")
	}
}
