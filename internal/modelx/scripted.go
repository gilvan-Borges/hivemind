package modelx

import (
	"context"
	"iter"
	"sync"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

// Scripted is a model.LLM that returns scripted responses. Tests use it in
// place of Gemini, Anthropic or OpenAI.
type Scripted struct {
	mu    sync.Mutex
	Calls int
	Fn    func(req *model.LLMRequest, call int) *model.LLMResponse
}

// Name implements model.LLM.
func (s *Scripted) Name() string { return "scripted" }

// GenerateContent implements model.LLM.
func (s *Scripted) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		s.mu.Lock()
		s.Calls++
		n := s.Calls
		fn := s.Fn
		s.mu.Unlock()
		resp := (*model.LLMResponse)(nil)
		if fn != nil {
			resp = fn(req, n)
		}
		if resp == nil {
			resp = Text("concluído")
		}
		yield(resp, nil)
	}
}

// Text builds a final model message.
func Text(s string) *model.LLMResponse {
	return &model.LLMResponse{
		Content: &genai.Content{
			Role:  "model",
			Parts: []*genai.Part{{Text: s}},
		},
		TurnComplete: true,
	}
}

// FunctionCall builds a tool call the ADK runner will execute.
func FunctionCall(name, id string, args map[string]any) *model.LLMResponse {
	return &model.LLMResponse{
		Content: &genai.Content{
			Role: "model",
			Parts: []*genai.Part{{
				FunctionCall: &genai.FunctionCall{ID: id, Name: name, Args: args},
			}},
		},
		TurnComplete: true,
	}
}

// LastIsFunctionResponse reports whether the newest content is a tool result.
// Checking only the tail avoids treating older tool results in the history as
// the current step.
func LastIsFunctionResponse(req *model.LLMRequest) bool {
	if req == nil || len(req.Contents) == 0 {
		return false
	}
	last := req.Contents[len(req.Contents)-1]
	if last == nil {
		return false
	}
	for _, part := range last.Parts {
		if part != nil && part.FunctionResponse != nil {
			return true
		}
	}
	return false
}
