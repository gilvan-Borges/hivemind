package modelx

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/openai/openai-go/v3"

	"github.com/gilvan-Borges/HiveMind/internal/jev"
)

// FormatUpstreamError builds the user-facing provider error. openai-go's
// Error() keeps only the HTTP status ("OpenAI API error: 400 Bad Request")
// and drops the body; this puts the provider, the model and the sanitized
// body back. extraSecrets are masked the same way as orchestrator events.
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
	var apiErr *openai.Error
	if errors.As(err, &apiErr) && apiErr != nil {
		body := strings.TrimSpace(apiErr.Message)
		if body == "" {
			body = strings.TrimSpace(apiErr.RawJSON())
		}
		status := strings.TrimSpace(http.StatusText(apiErr.StatusCode))
		head := "OpenAI API error"
		if apiErr.StatusCode != 0 {
			if status != "" {
				head = "OpenAI API error: " + strconv.Itoa(apiErr.StatusCode) + " " + status
			} else {
				head = "OpenAI API error: " + strconv.Itoa(apiErr.StatusCode)
			}
		}
		if body == "" {
			return head
		}
		return head + ": " + body
	}
	return err.Error()
}
