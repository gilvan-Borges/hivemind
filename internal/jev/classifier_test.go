package jev

import (
	"strings"
	"testing"
)

func TestParseClassifierKeepsHeuristicOnInvalidEnum(t *testing.T) {
	if _, _, ok := ParseClassifier("use o modelo mais caro"); ok {
		t.Fatal("texto fora do enum não pode virar classe")
	}
	if _, _, ok := ParseClassifier("coding"); ok {
		t.Fatal("classe sem complexidade não pode passar")
	}
	class, comp, ok := ParseClassifier("qa high")
	if !ok || class != ClassQA || comp != ComplexityHigh {
		t.Fatalf("enum válido: %s %s %v", class, comp, ok)
	}
}

func TestClassifierPromptHasNoSecretOrPolicy(t *testing.T) {
	prompt := ClassifierPrompt("objetivo longo sem verbo reconhecido " + strings.Repeat("lorem ", 20))
	for _, banned := range []string{"policy.json", "api-keys", "sk-", "credentials.json"} {
		if strings.Contains(prompt, banned) {
			t.Fatalf("prompt contém %q", banned)
		}
	}
}

func TestLowConfidenceOnlyForAmbiguousLongText(t *testing.T) {
	long := strings.Repeat("lorem ipsum dolor sit amet ", 40)
	if LowConfidence(ClassifyInput{Role: "developer", ObjectiveLen: len(long), Objective: long}) {
		t.Fatal("papel de dev não é ambíguo")
	}
	if LowConfidence(ClassifyInput{Role: "analyst", ObjectiveLen: 10, Objective: "oi"}) {
		t.Fatal("objetivo curto não dispara")
	}
	if !LowConfidence(ClassifyInput{Role: "analyst", ObjectiveLen: len(long), Objective: long}) {
		t.Fatal("papel ambíguo e texto longo sem verbo deveria ter confiança baixa")
	}
}
