package modelx

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
)

// ProviderError is the structured upstream failure (Spec 06 §3.2): the HTTP
// status, the provider error code and the sanitized body extracted per SDK —
// openai-go keeps only "OpenAI API error: 400 Bad Request" in Error(); the
// Anthropic SDK and genai (Gemini/AGY/TypeSafe shapes) hide their bodies the
// same way. ClassifyError consumes this instead of substring-matching raw
// PT/EN text.
type ProviderError struct {
	Provider   string
	Model      string
	StatusCode int
	Code       string
	Detail     string
}

func (e *ProviderError) Error() string {
	prefix := strings.TrimSpace(e.Provider) + "/" + strings.TrimSpace(e.Model)
	head := strings.TrimSpace(e.Code)
	if e.StatusCode != 0 {
		status := strings.TrimSpace(http.StatusText(e.StatusCode))
		if head != "" {
			head = "HTTP " + strconv.Itoa(e.StatusCode) + " " + status + " " + head
		} else {
			head = "HTTP " + strconv.Itoa(e.StatusCode) + " " + status
		}
	}
	if prefix == "/" || prefix == "" {
		if head == "" {
			return e.Detail
		}
		if e.Detail == "" {
			return head
		}
		return head + ": " + e.Detail
	}
	switch {
	case head == "" && e.Detail == "":
		return prefix
	case head == "":
		return prefix + ": " + e.Detail
	case e.Detail == "":
		return prefix + ": " + head
	default:
		return prefix + ": " + head + ": " + e.Detail
	}
}

// UnwrapProviderError extracts a structured ProviderError from the three SDK
// error types (openai, anthropic, genai). It returns nil when err carries none
// of them; callers fall back to the raw message.
func UnwrapProviderError(err error) *ProviderError {
	if err == nil {
		return nil
	}

	var openaiErr *openai.Error
	if errors.As(err, &openaiErr) && openaiErr != nil {
		pe := &ProviderError{StatusCode: openaiErr.StatusCode}
		body := strings.TrimSpace(openaiErr.Message)
		if body == "" {
			body = strings.TrimSpace(openaiErr.RawJSON())
		}
		pe.Detail = jev.RedactSecrets(truncateText(body, 500))
		return pe
	}

	var anthErr *anthropic.Error
	if errors.As(err, &anthErr) && anthErr != nil {
		pe := &ProviderError{StatusCode: anthErr.StatusCode}
		if t := anthErr.Type(); t != "" {
			pe.Code = string(t)
		}
		// DumpResponse(true) recovers the body the SDK hides in Error().
		if raw := anthErr.DumpResponse(true); len(raw) > 0 {
			pe.Detail = jev.RedactSecrets(truncateText(firstNonEmpty(string(raw), anthErr.Error()), 500))
		} else {
			pe.Detail = jev.RedactSecrets(truncateText(anthErr.Error(), 500))
		}
		return pe
	}

	var genErr *genai.APIError
	if errors.As(err, &genErr) && genErr != nil {
		pe := &ProviderError{StatusCode: genErr.Code, Code: genErr.Status}
		pe.Detail = jev.RedactSecrets(truncateText(genErr.Message, 500))
		return pe
	}
	return nil
}

// FormatUpstreamError builds the user-facing provider error from the typed
// ProviderError when one can be extracted, falling back to the raw message.
// extraSecrets are masked the same way as orchestrator events.
func FormatUpstreamError(provider, model string, err error, extraSecrets ...string) string {
	if err == nil {
		return ""
	}
	detail := upstreamDetail(err)
	detail = jev.RedactSecrets(detail, extraSecrets...)
	detail = truncateText(strings.TrimSpace(detail), 500)
	prefix := strings.TrimSpace(provider) + "/" + strings.TrimSpace(model)
	if prefix == "/" || prefix == "" {
		return detail
	}
	if strings.HasPrefix(detail, prefix+":") {
		return detail
	}
	return prefix + ": " + detail
}

func upstreamDetail(err error) string {
	if pe := UnwrapProviderError(err); pe != nil && pe.Detail != "" {
		if pe.Code != "" || pe.StatusCode != 0 {
			return pe.Error()
		}
		return pe.Detail
	}
	var openaiErr *openai.Error
	if errors.As(err, &openaiErr) && openaiErr != nil {
		body := strings.TrimSpace(openaiErr.Message)
		if body == "" {
			body = strings.TrimSpace(openaiErr.RawJSON())
		}
		status := strings.TrimSpace(http.StatusText(openaiErr.StatusCode))
		head := "OpenAI API error"
		if openaiErr.StatusCode != 0 {
			if status != "" {
				head = "OpenAI API error: " + strconv.Itoa(openaiErr.StatusCode) + " " + status
			} else {
				head = "OpenAI API error: " + strconv.Itoa(openaiErr.StatusCode)
			}
		}
		if body == "" {
			return head
		}
		return head + ": " + body
	}
	return err.Error()
}
