package loopx

import "time"

const (
	OutcomeContinue        = "continue"
	OutcomeDone            = "done"
	OutcomeImpossible      = "impossible"
	OutcomeStalled         = "stalled"
	OutcomeBudgetExhausted = "budget_exhausted"
	OutcomeBlocked         = "blocked"

	JudgeMet        = "met"
	JudgeNotMet     = "not_met"
	JudgeImpossible = "impossible"
	JudgeSkipped    = "skipped"
)

// Snapshot is everything Decide needs. It has no model and no disk.
type Snapshot struct {
	ChecksOK              bool
	IntegrityOK           bool
	FeaturesRequired      bool
	FeaturesAllPass       bool
	Judge                 string
	JudgeRequired         bool
	Blocked               bool
	Iterations            int
	Elapsed               time.Duration
	SpentMicroUSD         int64
	SpentTokens           int64
	ConsecutiveNoProgress int
	ConsecutiveImpossible int
	Limits                Limits
}

// Decide ends the loop. The model does not.
// Order: done, blocked, impossible, budget, stalled, continue.
func Decide(s Snapshot) string {
	if done(s) {
		return OutcomeDone
	}
	if s.Blocked {
		return OutcomeBlocked
	}
	if s.ConsecutiveImpossible >= 2 {
		return OutcomeImpossible
	}
	if budgetHit(s) {
		return OutcomeBudgetExhausted
	}
	if s.Limits.StallAfter > 0 && s.ConsecutiveNoProgress >= s.Limits.StallAfter {
		return OutcomeStalled
	}
	return OutcomeContinue
}

func done(s Snapshot) bool {
	if s.Blocked || !s.ChecksOK || !s.IntegrityOK {
		return false
	}
	if s.FeaturesRequired && !s.FeaturesAllPass {
		return false
	}
	if s.JudgeRequired && s.Judge != JudgeMet {
		return false
	}
	return true
}

func budgetHit(s Snapshot) bool {
	lim := s.Limits
	if lim.MaxIterations > 0 && s.Iterations >= lim.MaxIterations {
		return true
	}
	if lim.MaxDuration > 0 && s.Elapsed >= lim.MaxDuration {
		return true
	}
	if lim.MaxCostMicroUSD > 0 && s.SpentMicroUSD >= lim.MaxCostMicroUSD {
		return true
	}
	if lim.MaxTokens > 0 && s.SpentTokens >= lim.MaxTokens {
		return true
	}
	return false
}

// NoteProgress counts iterations that neither raised the passing-check count
// nor changed the worktree fingerprint.
func NoteProgress(st *State, fingerprint string, haveFP bool, passing int) {
	if st == nil {
		return
	}
	progressed := passing > st.PassingChecks
	if haveFP && fingerprint != st.Fingerprint {
		progressed = true
	}
	if progressed {
		st.ConsecutiveNoProgress = 0
	} else {
		st.ConsecutiveNoProgress++
	}
	if haveFP {
		st.Fingerprint = fingerprint
		st.HaveFingerprint = true
	}
	st.PassingChecks = passing
}
