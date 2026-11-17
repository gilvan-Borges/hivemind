package ui

import (
	"bytes"
	"strings"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
)

func TestPrinterPartialsAndTools(t *testing.T) {
	var buf bytes.Buffer
	p := &Printer{Out: &buf, Labels: map[string]string{"pm": "Gerente de projeto · pm"}}
	p.Event(&session.Event{
		Author: "pm",
		LLMResponse: model.LLMResponse{
			Partial: true,
			Content: &genai.Content{Parts: []*genai.Part{{Text: "pla"}}},
		},
	})
	p.Event(&session.Event{
		Author: "pm",
		LLMResponse: model.LLMResponse{
			Partial: true,
			Content: &genai.Content{Parts: []*genai.Part{{Text: "no"}}},
		},
	})
	p.Event(&session.Event{
		Author: "pm",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{Parts: []*genai.Part{{Text: "plano"}}},
		},
	})
	p.Event(&session.Event{
		Author: "pm",
		LLMResponse: model.LLMResponse{
			Content: &genai.Content{Parts: []*genai.Part{
				{FunctionCall: &genai.FunctionCall{Name: "jira_create_subtask"}},
			}},
		},
	})
	got := buf.String()
	if strings.Count(got, "plano") != 1 {
		t.Fatalf("texto duplicado:\n%s", got)
	}
	if !strings.Contains(got, "Gerente de projeto · pm") || !strings.Contains(got, "ferramenta jira_create_subtask") {
		t.Fatalf("saída:\n%s", got)
	}
}
