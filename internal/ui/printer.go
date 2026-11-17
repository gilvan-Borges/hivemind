// Package ui prints agent events in Brazilian Portuguese.
package ui

import (
	"fmt"
	"io"
	"strings"

	"google.golang.org/adk/v2/session"
)

// Printer streams ADK events to a terminal. Partial chunks are written as they
// arrive; the final event is not printed again when it repeats that text.
type Printer struct {
	Out    io.Writer
	Labels map[string]string

	partial       strings.Builder
	partialAuthor string
}

// Event renders one runner event.
func (p *Printer) Event(ev *session.Event) {
	if p == nil || p.Out == nil || ev == nil || ev.LLMResponse.Content == nil {
		return
	}
	label := p.label(ev.Author)
	for _, part := range ev.LLMResponse.Content.Parts {
		if part == nil {
			continue
		}
		switch {
		case part.FunctionCall != nil:
			p.endPartial()
			fmt.Fprintf(p.Out, "[%s] ferramenta %s\n", label, part.FunctionCall.Name)
		case part.FunctionResponse != nil:
			p.endPartial()
			fmt.Fprintf(p.Out, "[%s] resultado %s\n", label, part.FunctionResponse.Name)
		case part.Text != "" && !part.Thought:
			p.text(label, ev.Author, part.Text, ev.Partial)
		}
	}
	p.flush()
}

func (p *Printer) label(author string) string {
	if author == "" || author == "user" {
		return "você"
	}
	if p.Labels != nil {
		if label, ok := p.Labels[author]; ok && label != "" {
			return label
		}
	}
	return author
}

func (p *Printer) text(label, author, text string, partial bool) {
	if partial {
		if p.partialAuthor != author {
			p.endPartial()
			fmt.Fprintf(p.Out, "[%s] ", label)
			p.partialAuthor = author
		}
		fmt.Fprint(p.Out, text)
		p.partial.WriteString(text)
		return
	}
	already := p.partial.String()
	if already != "" && (text == already || strings.HasPrefix(text, already)) {
		fmt.Fprintln(p.Out)
		p.partial.Reset()
		p.partialAuthor = ""
		return
	}
	p.endPartial()
	fmt.Fprintf(p.Out, "[%s] %s\n", label, strings.TrimSpace(text))
}

func (p *Printer) endPartial() {
	if p.partial.Len() == 0 {
		return
	}
	fmt.Fprintln(p.Out)
	p.partial.Reset()
	p.partialAuthor = ""
}

func (p *Printer) flush() {
	if f, ok := p.Out.(interface{ Flush() error }); ok {
		_ = f.Flush()
	}
}
