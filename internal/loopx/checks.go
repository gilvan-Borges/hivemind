package loopx

import (
	"context"
	"strings"
	"time"
)

// CommandRunner runs one host check. The model does not implement it.
type CommandRunner interface {
	Run(ctx context.Context, dir, command string, timeout time.Duration) (exit int, output string, err error)
}

// CheckResult is one host command.
type CheckResult struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Exit  int    `json:"exit"`
	Tail  string `json:"tail,omitempty"`
	Error string `json:"error,omitempty"`
}

// RunChecks executes the spec commands in order. An empty list does not pass:
// there is nothing the host could verify.
func RunChecks(ctx context.Context, run CommandRunner, dir string, checks []Check) []CheckResult {
	if len(checks) == 0 {
		return nil
	}
	out := make([]CheckResult, 0, len(checks))
	for _, chk := range checks {
		res := CheckResult{Name: chk.Name, Exit: -1}
		if run == nil {
			res.Error = "runner de checks ausente"
			out = append(out, res)
			continue
		}
		if ctx.Err() != nil {
			res.Error = ctx.Err().Error()
			out = append(out, res)
			continue
		}
		exit, output, err := run.Run(ctx, dir, chk.Run, chk.CheckTimeout())
		res.Exit = exit
		res.Tail = Tail(output, 4000)
		res.OK = err == nil && exit == chk.ExitCode()
		if err != nil && !res.OK {
			res.Error = err.Error()
		}
		out = append(out, res)
	}
	return out
}

// ChecksOK is true only when every host check passed.
func ChecksOK(results []CheckResult) (ok bool, passing int) {
	if len(results) == 0 {
		return false, 0
	}
	ok = true
	for _, r := range results {
		if r.OK {
			passing++
			continue
		}
		ok = false
	}
	return ok, passing
}

// Tail keeps the end of a command transcript.
func Tail(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}
