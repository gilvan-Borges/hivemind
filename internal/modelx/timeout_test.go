package modelx

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
)

type blockingLLM struct {
	delay time.Duration
}

func (b *blockingLLM) Name() string { return "blocking" }

func (b *blockingLLM) GenerateContent(ctx context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		select {
		case <-time.After(b.delay):
			yield(nil, errors.New("sem deadline"))
		case <-ctx.Done():
			yield(nil, ctx.Err())
		}
	}
}

func TestPerCallTimeoutFiresOnStalledCall(t *testing.T) {
	inner := &blockingLLM{delay: 5 * time.Second}
	llm := NewPerCallTimeout(inner, 50*time.Millisecond)

	var lastErr error
	for _, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{}, false) {
		lastErr = err
		break
	}
	if !errors.Is(lastErr, context.DeadlineExceeded) {
		t.Fatalf("esperava DeadlineExceeded, veio %v", lastErr)
	}
}

func TestPerCallTimeoutPassesFastCallsThrough(t *testing.T) {
	fast := &blockingLLM{delay: time.Millisecond}
	llm := NewPerCallTimeout(fast, 5*time.Second)

	var lastErr error
	for _, err := range llm.GenerateContent(context.Background(), &model.LLMRequest{}, false) {
		lastErr = err
		break
	}
	if lastErr == nil || lastErr.Error() != "sem deadline" {
		t.Fatalf("chamada rápida deveria passar direto, veio %v", lastErr)
	}
	if llm.Name() != "blocking" {
		t.Fatalf("Name() deveria delegar ao inner, veio %q", llm.Name())
	}
}

func TestPerCallTimeoutNoopOnNilOrZero(t *testing.T) {
	if got := NewPerCallTimeout(nil, time.Second); got != nil {
		t.Fatal("inner nil deveria devolver nil")
	}
	inner := &blockingLLM{}
	if got := NewPerCallTimeout(inner, 0); got != model.LLM(inner) {
		t.Fatal("timeout zero deveria devolver o inner sem wrapper")
	}
}
