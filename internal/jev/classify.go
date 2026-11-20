package jev

import (
	"strings"

	"github.com/gilvan-Borges/HiveMind/internal/team"
)

// ClassifyInput holds host-derived signals used to classify a turn without calling any LLM.
type ClassifyInput struct {
	Role         string
	RunMode      string
	Step         string
	ObjectiveLen int
	InputTokens  int
	HasImage     bool
	QAStall      int
	NeedsTools   bool
}

const (
	shortObjectiveChars = 800
	lowTokenThreshold   = 20_000
	highTokenThreshold  = 100_000
)

// Classify derives Class and Complexity strictly from host signals in the exact
// priority order specified in SPEC_JEV_ORQUESTRADOR §5.
func Classify(in ClassifyInput) (Class, Complexity) {
	role := team.NormalizeRole(in.Role)
	runMode := strings.ToLower(strings.TrimSpace(in.RunMode))
	step := strings.ToLower(strings.TrimSpace(in.Step))

	baseClass, baseComp := classifyWithoutVision(role, runMode, step, in)
	if in.HasImage {
		return ClassVision, baseComp
	}
	return baseClass, baseComp
}

func classifyWithoutVision(role, runMode, step string, in ClassifyInput) (Class, Complexity) {
	if role == team.RoleQA || runMode == "auto-review" {
		comp := ComplexityLow
		if in.QAStall >= 1 || in.InputTokens > highTokenThreshold {
			comp = ComplexityHigh
		}
		return ClassQA, comp
	}

	if role == team.RoleProjectManager || role == team.RoleTechLead || role == team.RoleArchitect {
		if isTriageStep(step) && isShortAndSmall(in) {
			return ClassTrivial, ComplexityLow
		}
		if isPlanningStep(step) || isTriageStep(step) || step == "" {
			return ClassPlanning, complexityFromSize(in)
		}
		if strings.Contains(step, "review") || runMode == "review" {
			return ClassReview, complexityFromSize(in)
		}
		return ClassPlanning, complexityFromSize(in)
	}

	if role == team.RoleDeveloper {
		comp := complexityFromSize(in)
		if in.QAStall >= 1 {
			comp = ComplexityHigh
		}
		return ClassCoding, comp
	}

	return ClassCoding, complexityFromSize(in)
}

func isTriageStep(step string) bool {
	return step == "triage_chief" || step == "triage" || step == "runtriagechief"
}

func isPlanningStep(step string) bool {
	return step == "chief_plan" || step == "pm_plan" || step == "tech_lead_plan" || step == "architect_plan" || step == "plan" || step == "closing_summary"
}

func isShortAndSmall(in ClassifyInput) bool {
	return in.ObjectiveLen > 0 && in.ObjectiveLen < shortObjectiveChars && in.InputTokens <= lowTokenThreshold
}

func complexityFromSize(in ClassifyInput) Complexity {
	if in.InputTokens > highTokenThreshold {
		return ComplexityHigh
	}
	if in.ObjectiveLen < shortObjectiveChars && in.InputTokens <= lowTokenThreshold {
		return ComplexityLow
	}
	return ComplexityMedium
}

// EstimateFirstStepInputTokens computes a conservative initial token estimate from
// the objective, optional <= 4 KiB memory snippet, and whether tools are attached.
func EstimateFirstStepInputTokens(objectiveLen, memoryBytes int, needsTools bool) int {
	// ~4 bytes per token conservatively (use /3 so estimates never under-shoot context window).
	if memoryBytes > 4096 {
		memoryBytes = 4096
	}
	base := 900 // system prompt overhead
	if needsTools {
		base += 1400
	}
	objTok := (objectiveLen + 2) / 3
	memTok := (memoryBytes + 2) / 3
	return base + objTok + memTok
}
