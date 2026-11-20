package jev

import (
	"context"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// LowConfidence is the only case that may call the optional classifier.
// It needs a long objective, no recognized verb and a role that is not a squad role.
func LowConfidence(in ClassifyInput) bool {
	if in.ObjectiveLen < shortObjectiveChars || strings.TrimSpace(in.Objective) == "" {
		return false
	}
	switch team.NormalizeRole(in.Role) {
	case team.RoleDeveloper, team.RoleQA, team.RoleArchitect, team.RoleTechLead, team.RoleProjectManager:
		return false
	}
	return !hasRecognizedVerb(in.Objective)
}

func hasRecognizedVerb(s string) bool {
	low := strings.ToLower(s)
	for _, stem := range []string{
		"corrig", "implement", "adicion", "remov", "revis", "explic", "cria", "refator",
		"fix", "add", "update", "refactor", "write", "test",
	} {
		if strings.Contains(low, stem) {
			return true
		}
	}
	return false
}

// ClassifierPrompt is the only text sent to the optional classifier.
func ClassifierPrompt(objective string) string {
	obj := strings.TrimSpace(objective)
	if len(obj) > 2000 {
		obj = obj[:2000]
	}
	return "Responda só duas palavras: classe complexidade.\n" +
		"classe: trivial planning coding review qa vision\n" +
		"complexidade: low medium high\n" +
		obj
}

// ParseClassifier accepts only the closed enums. Anything else is discarded.
func ParseClassifier(raw string) (Class, Complexity, bool) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(raw)))
	if len(fields) != 2 {
		return "", "", false
	}
	class := knownClass(Class(fields[0]))
	comp := knownComplexity(Complexity(fields[1]))
	if class == "" || comp == "" {
		return "", "", false
	}
	return class, comp, true
}

// CompleteClassifier reads one cheap-model reply. The prompt is only the objective.
func CompleteClassifier(ctx context.Context, llm model.LLM, objective string) (string, error) {
	if llm == nil {
		return "", errNoClassifier
	}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{
			Role:  "user",
			Parts: []*genai.Part{{Text: ClassifierPrompt(objective)}},
		}},
	}
	var b strings.Builder
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			return "", err
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		for _, part := range resp.Content.Parts {
			if part != nil && part.Text != "" {
				b.WriteString(part.Text)
			}
		}
	}
	return b.String(), nil
}

var errNoClassifier = errString("classificador ausente")

type errString string

func (e errString) Error() string { return string(e) }
