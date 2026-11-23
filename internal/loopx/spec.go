// Package loopx is the durable loop controller for the Agent Hive.
// The model proposes work. Decide, in this package, is what ends a loop.
package loopx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
)

const (
	KindGoal      = "goal"
	KindTime      = "time"
	KindProactive = "proactive"

	DefaultMaxIterations = 10
	DefaultMaxDuration   = 2 * time.Hour
	DefaultMaxCostUSD    = 5.0
	DefaultStallAfter    = 3
	MaxIterationsCap     = 1000
	MaxDurationCap       = 24 * time.Hour
	MaxCostUSDCap        = 100.0
)

// Spec is the immutable loop contract stored in loop.yaml.
// The model does not edit it. A changed file blocks the loop.
type Spec struct {
	Version        int          `yaml:"version"`
	ID             string       `yaml:"id"`
	Name           string       `yaml:"name"`
	Kind           string       `yaml:"kind"`
	Objective      string       `yaml:"objective"`
	DoneWhen       []string     `yaml:"done_when,omitempty"`
	Checks         []Check      `yaml:"checks,omitempty"`
	Features       string       `yaml:"features,omitempty"`
	ProtectedPaths []string     `yaml:"protected_paths,omitempty"`
	Executor       ExecutorSpec `yaml:"executor,omitempty"`
	Judge          JudgeSpec    `yaml:"judge,omitempty"`
	Limits         LimitSpec    `yaml:"limits,omitempty"`
	Trigger        TriggerSpec  `yaml:"trigger,omitempty"`
	Permissions    PermSpec     `yaml:"permissions,omitempty"`
	Git            GitSpec      `yaml:"git,omitempty"`
	Notify         []string     `yaml:"notify,omitempty"`
}

// Check is a command the host runs. The model cannot change Run.
type Check struct {
	Name    string `yaml:"name"`
	Run     string `yaml:"run"`
	Expect  Expect `yaml:"expect,omitempty"`
	Timeout string `yaml:"timeout,omitempty"`
}

// Expect is the deterministic bar for a check.
type Expect struct {
	ExitCode *int `yaml:"exit_code,omitempty"`
}

// ExecutorSpec selects who does the work. Only hive runs today.
type ExecutorSpec struct {
	Backend      string `yaml:"backend,omitempty"`
	RunMode      string `yaml:"run_mode,omitempty"`
	Model        string `yaml:"model,omitempty"`
	FreshContext *bool  `yaml:"fresh_context,omitempty"`
}

// JudgeSpec is the read-only reviewer. Nil Enabled means on.
type JudgeSpec struct {
	Enabled *bool  `yaml:"enabled,omitempty"`
	Model   string `yaml:"model,omitempty"`
}

// LimitSpec is the hard ceiling. Zero values become the defaults.
type LimitSpec struct {
	MaxIterations int     `yaml:"max_iterations,omitempty"`
	MaxDuration   string  `yaml:"max_duration,omitempty"`
	MaxCostUSD    float64 `yaml:"max_cost_usd,omitempty"`
	MaxTokens     int64   `yaml:"max_tokens,omitempty"`
	StallAfter    int     `yaml:"stall_after,omitempty"`
}

// TriggerSpec records how the loop is woken. Cron and events are opt-in.
type TriggerSpec struct {
	Manual   bool     `yaml:"manual,omitempty"`
	Cron     string   `yaml:"cron,omitempty"`
	Timezone string   `yaml:"timezone,omitempty"`
	Events   []string `yaml:"events,omitempty"`
	Every    string   `yaml:"every,omitempty"`
}

// PermSpec can only inherit the project mode or ask. It cannot request Full.
type PermSpec struct {
	Mode string `yaml:"mode,omitempty"`
}

// GitSpec describes the branch and the PR. AutoMerge is rejected.
type GitSpec struct {
	Branch              string `yaml:"branch,omitempty"`
	CommitEachIteration *bool  `yaml:"commit_each_iteration,omitempty"`
	OpenPRWhenDone      *bool  `yaml:"open_pr_when_done,omitempty"`
	AutoMerge           bool   `yaml:"auto_merge,omitempty"`
}

// Limits is the parsed ceiling Decide reads.
type Limits struct {
	MaxIterations   int
	MaxDuration     time.Duration
	MaxCostMicroUSD int64
	MaxTokens       int64
	StallAfter      int
}

// On reports whether the judge should run. The default is on.
func (j JudgeSpec) On() bool {
	if j.Enabled == nil {
		return true
	}
	return *j.Enabled
}

// Fresh reports whether each iteration starts a new context. The default is yes.
func (e ExecutorSpec) Fresh() bool {
	if e.FreshContext == nil {
		return true
	}
	return *e.FreshContext
}

// CommitEach reports whether the host commits after an iteration. The default is yes.
func (g GitSpec) CommitEach() bool {
	if g.CommitEachIteration == nil {
		return true
	}
	return *g.CommitEachIteration
}

// OpenPR reports whether a finished loop opens a pull request. The default is yes.
func (g GitSpec) OpenPR() bool {
	if g.OpenPRWhenDone == nil {
		return true
	}
	return *g.OpenPRWhenDone
}

// Access is inherit or ask. Anything else, including full, is invalid.
func (p PermSpec) Access() string {
	switch strings.ToLower(strings.TrimSpace(p.Mode)) {
	case "", "inherit":
		return "inherit"
	case "ask":
		return "ask"
	default:
		return "invalid"
	}
}

// Normalize fills defaults and rejects contracts that would escalate permission
// or merge on their own.
func Normalize(spec Spec) (Spec, error) {
	spec.ID = strings.TrimSpace(spec.ID)
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	spec.Objective = strings.TrimSpace(spec.Objective)
	if spec.Version == 0 {
		spec.Version = 1
	}
	if spec.Version != 1 {
		return Spec{}, fmt.Errorf("versão de loop não suportada: %d", spec.Version)
	}
	if spec.ID == "" || !ValidID(spec.ID) {
		return Spec{}, fmt.Errorf("id de loop inválido")
	}
	switch spec.Kind {
	case KindGoal, KindTime, KindProactive:
	default:
		return Spec{}, fmt.Errorf("kind de loop inválido")
	}
	if spec.Objective == "" {
		return Spec{}, fmt.Errorf("objetivo do loop vazio")
	}
	if spec.Name == "" {
		spec.Name = spec.Objective
		if len(spec.Name) > 80 {
			spec.Name = spec.Name[:80]
		}
	}
	backend := strings.ToLower(strings.TrimSpace(spec.Executor.Backend))
	switch backend {
	case "", "hive":
		spec.Executor.Backend = "hive"
	default:
		// squad e race ficam no contrato para uma spec futura; este processo só executa hive.
		return Spec{}, fmt.Errorf("backend %q ainda não existe; use hive", backend)
	}
	if spec.Permissions.Access() == "invalid" {
		return Spec{}, fmt.Errorf("permissão do loop só pode ser inherit ou ask")
	}
	spec.Permissions.Mode = spec.Permissions.Access()
	if spec.Git.AutoMerge {
		return Spec{}, fmt.Errorf("loop não faz merge automático")
	}
	if strings.TrimSpace(spec.Git.Branch) == "" {
		spec.Git.Branch = "loop/" + spec.ID
	}
	if spec.Limits.MaxIterations <= 0 {
		spec.Limits.MaxIterations = DefaultMaxIterations
	}
	if spec.Limits.MaxIterations > MaxIterationsCap {
		return Spec{}, fmt.Errorf("max_iterations acima de %d", MaxIterationsCap)
	}
	if strings.TrimSpace(spec.Limits.MaxDuration) == "" {
		spec.Limits.MaxDuration = DefaultMaxDuration.String()
	}
	d, err := time.ParseDuration(strings.TrimSpace(spec.Limits.MaxDuration))
	if err != nil || d <= 0 {
		return Spec{}, fmt.Errorf("max_duration inválida")
	}
	if d > MaxDurationCap {
		return Spec{}, fmt.Errorf("max_duration acima de %s", MaxDurationCap)
	}
	spec.Limits.MaxDuration = d.String()
	if spec.Limits.MaxCostUSD <= 0 {
		spec.Limits.MaxCostUSD = DefaultMaxCostUSD
	}
	if spec.Limits.MaxCostUSD > MaxCostUSDCap {
		return Spec{}, fmt.Errorf("max_cost_usd acima de %.0f", MaxCostUSDCap)
	}
	if spec.Limits.StallAfter <= 0 {
		spec.Limits.StallAfter = DefaultStallAfter
	}
	if spec.Limits.StallAfter > 100 {
		return Spec{}, fmt.Errorf("stall_after acima de 100")
	}
	spec.ProtectedPaths = withLoopProtection(spec.ProtectedPaths)
	for i := range spec.Checks {
		spec.Checks[i].Name = strings.TrimSpace(spec.Checks[i].Name)
		spec.Checks[i].Run = strings.TrimSpace(spec.Checks[i].Run)
		if spec.Checks[i].Name == "" || spec.Checks[i].Run == "" {
			return Spec{}, fmt.Errorf("check sem nome ou comando")
		}
		if strings.ContainsAny(spec.Checks[i].Run, "\n\r") {
			return Spec{}, fmt.Errorf("check %s tem mais de uma linha", spec.Checks[i].Name)
		}
		if spec.Checks[i].Timeout != "" {
			td, terr := time.ParseDuration(spec.Checks[i].Timeout)
			if terr != nil || td <= 0 || td > 15*time.Minute {
				return Spec{}, fmt.Errorf("timeout do check %s inválido", spec.Checks[i].Name)
			}
		}
	}
	return spec, nil
}

func withLoopProtection(paths []string) []string {
	const loopTree = ".hivemind/loops/**"
	seen := map[string]bool{}
	out := make([]string, 0, len(paths)+1)
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if !seen[loopTree] {
		out = append(out, loopTree)
	}
	return out
}

// ParsedLimits returns the ceilings Decide compares against.
func (spec Spec) ParsedLimits() (Limits, error) {
	d, err := time.ParseDuration(strings.TrimSpace(spec.Limits.MaxDuration))
	if err != nil {
		return Limits{}, err
	}
	micro := int64(spec.Limits.MaxCostUSD*1_000_000 + 0.5)
	return Limits{
		MaxIterations:   spec.Limits.MaxIterations,
		MaxDuration:     d,
		MaxCostMicroUSD: micro,
		MaxTokens:       spec.Limits.MaxTokens,
		StallAfter:      spec.Limits.StallAfter,
	}, nil
}

// ExitCode is the expected process status. The default is 0.
func (c Check) ExitCode() int {
	if c.Expect.ExitCode == nil {
		return 0
	}
	return *c.Expect.ExitCode
}

// CheckTimeout bounds one host command. The default is 5 minutes.
func (c Check) CheckTimeout() time.Duration {
	if strings.TrimSpace(c.Timeout) == "" {
		return 5 * time.Minute
	}
	d, err := time.ParseDuration(c.Timeout)
	if err != nil || d <= 0 {
		return 5 * time.Minute
	}
	return d
}

// Hash is the identity of the spec bytes the loop started with.
func Hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// SaveSpec writes loop.yaml and returns the hash of the bytes on disk.
func SaveSpec(workspace string, spec Spec) (string, error) {
	spec, err := Normalize(spec)
	if err != nil {
		return "", err
	}
	dir, err := Dir(workspace, spec.ID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	raw, err := yaml.Marshal(spec)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "loop.yaml")
	if err := atomicx.WriteFile(path, raw, 0o644); err != nil {
		return "", err
	}
	return Hash(raw), nil
}

// LoadSpec reads and normalizes loop.yaml.
func LoadSpec(workspace, id string) (Spec, string, error) {
	dir, err := Dir(workspace, id)
	if err != nil {
		return Spec{}, "", err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "loop.yaml"))
	if err != nil {
		return Spec{}, "", err
	}
	var spec Spec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return Spec{}, "", fmt.Errorf("loop.yaml inválido: %w", err)
	}
	spec, err = Normalize(spec)
	if err != nil {
		return Spec{}, "", err
	}
	return spec, Hash(raw), nil
}

// safeWorkspace rejects parent-directory segments before any file join.
func safeWorkspace(workspace string) (string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || strings.Contains(workspace, "..") {
		return "", fmt.Errorf("workspace do loop inválido")
	}
	return filepath.Clean(workspace), nil
}

// Dir is .hivemind/loops/<id> inside the workspace.
func Dir(workspace, id string) (string, error) {
	root, err := safeWorkspace(workspace)
	if err != nil {
		return "", err
	}
	if !ValidID(id) || strings.Contains(id, "..") || !filepath.IsLocal(id) {
		return "", fmt.Errorf("id de loop inválido")
	}
	full := filepath.Join(root, ".hivemind", "loops", id)
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("caminho do loop fora do workspace")
	}
	return filepath.Join(root, rel), nil
}

// ValidID rejects path separators and anything that is not a short slug.
func ValidID(id string) bool {
	if id == "" || len(id) > 48 {
		return false
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0:
		default:
			return false
		}
	}
	return !strings.HasSuffix(id, "-")
}

// Slug builds a loop id from a free-text objective.
func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "loop"
	}
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

// EnsureIgnored appends .hivemind/ to the workspace gitignore when it is missing.
func EnsureIgnored(workspace string) error {
	root, err := safeWorkspace(workspace)
	if err != nil {
		return err
	}
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	text := string(data)
	if strings.Contains(text, ".hivemind/") || strings.Contains(text, ".hivemind\n") {
		return nil
	}
	if len(data) > 0 && !strings.HasSuffix(text, "\n") {
		data = append(data, '\n')
	}
	data = append(data, []byte(".hivemind/\n")...)
	return os.WriteFile(path, data, 0o644)
}
