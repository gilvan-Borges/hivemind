package extsess

import "strings"

// ExternalState is only what this repository can prove from the process list.
type ExternalState string

const (
	StateRunning   ExternalState = "running"
	StateCompleted ExternalState = "completed"
	StateFailed    ExternalState = "failed"
	StateUnknown   ExternalState = "unknown"
)

// ExternalSession is a process the person opened outside a Hive run.
// Actions stays empty: this phase does not claim it can act.
type ExternalSession struct {
	ID       string        `json:"id"`
	Source   string        `json:"source"`
	State    ExternalState `json:"state"`
	PID      int           `json:"pid"`
	CreateMs int64         `json:"createMs"`
	Title    string        `json:"title,omitempty"`
	Actions  []string      `json:"actions"`
}

type processInfo struct {
	Name      string
	PID       int
	CreateMs  int64
	KnownTime bool
}

// builtinNames maps the executable name to a source id.
var builtinNames = map[string]string{
	"agy.exe":      "agy",
	"claude.exe":   "claude-code",
	"codex.exe":    "codex",
	"opencode.exe": "opencode",
}

// List returns matching processes. An empty machine returns an empty list.
func List() ([]ExternalSession, error) {
	procs, err := listProcesses()
	if err != nil {
		return nil, err
	}
	return sessionsFrom(procs, mergedNames()), nil
}

func sessionsFrom(procs []processInfo, names map[string]string) []ExternalSession {
	out := make([]ExternalSession, 0)
	for _, p := range procs {
		source, ok := names[strings.ToLower(p.Name)]
		if !ok {
			continue
		}
		state := StateRunning
		if !p.KnownTime {
			state = StateUnknown
		}
		out = append(out, ExternalSession{
			ID:       source + ":" + itoa(p.PID) + ":" + itoa64(p.CreateMs),
			Source:   source,
			State:    state,
			PID:      p.PID,
			CreateMs: p.CreateMs,
			Actions:  []string{},
		})
	}
	return out
}

func itoa(n int) string {
	return itoa64(int64(n))
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
