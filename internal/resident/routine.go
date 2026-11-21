package resident

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gilvan-Borges/HiveMind/internal/atomicx"
)

const (
	routinesFileName      = "routines.json"
	maxRoutineTextBytes   = 4 << 10
	maxRoutineTurnsPerDay = 20
)

var routinesMu sync.Mutex

// Routine is a structured schedule. It has no shell command field.
type Routine struct {
	ID         string    `json:"id"`
	Expr       string    `json:"expr"`
	Timezone   string    `json:"timezone,omitempty"`
	Text       string    `json:"text"`
	Workstream string    `json:"workstream,omitempty"`
	Classes    []string  `json:"classes,omitempty"`
	Enabled    bool      `json:"enabled"`
	LastFired  time.Time `json:"lastFired,omitempty"`
	Day        string    `json:"day,omitempty"`
	DayTurns   int       `json:"dayTurns,omitempty"`
}

type routineFile struct {
	Version  int       `json:"version"`
	Routines []Routine `json:"routines"`
}

func routinesPath(baseDir string) string {
	return filepath.Join(baseDir, routinesFileName)
}

// LoadRoutines reads routines.json. A missing file is an empty list.
func LoadRoutines(baseDir string) ([]Routine, error) {
	data, err := os.ReadFile(routinesPath(baseDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var f routineFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("routines.json inválido: %w", err)
	}
	return f.Routines, nil
}

func writeRoutines(baseDir string, list []Routine) error {
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(routineFile{Version: 1, Routines: list}, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return atomicx.WriteFile(routinesPath(baseDir), raw, 0o600)
}

// ValidateRoutine rejects shell commands, wildcards and unknown classes.
func ValidateRoutine(rt Routine) (Routine, error) {
	rt.Expr = strings.TrimSpace(rt.Expr)
	rt.Text = strings.TrimSpace(rt.Text)
	rt.Timezone = strings.TrimSpace(rt.Timezone)
	rt.Workstream = SanitizeWorkstream(rt.Workstream)
	if _, err := parseCron(rt.Expr); err != nil {
		return Routine{}, err
	}
	if rt.Text == "" {
		return Routine{}, fmt.Errorf("texto da rotina vazio")
	}
	if len(rt.Text) > maxRoutineTextBytes {
		return Routine{}, fmt.Errorf("texto da rotina acima de 4 KiB")
	}
	if cronLooksLikeShell(map[string]any{"text": rt.Text, "expr": rt.Expr}) {
		return Routine{}, fmt.Errorf("rotina não aceita comando de shell")
	}
	if rt.Timezone != "" {
		if _, err := time.LoadLocation(rt.Timezone); err != nil {
			return Routine{}, fmt.Errorf("fuso inválido")
		}
	}
	classes, err := parseClassList(rt.Classes)
	if err != nil {
		return Routine{}, err
	}
	rt.Classes = tiersToStrings(classes)
	return rt, nil
}

// SaveRoutine inserts or replaces a routine by id. The caller already approved.
func SaveRoutine(baseDir string, rt Routine) (Routine, error) {
	routinesMu.Lock()
	defer routinesMu.Unlock()
	clean, err := ValidateRoutine(rt)
	if err != nil {
		return Routine{}, err
	}
	list, err := LoadRoutines(baseDir)
	if err != nil {
		return Routine{}, err
	}
	if clean.ID == "" {
		clean.ID = newShortID("rt")
	}
	replaced := false
	for i := range list {
		if list[i].ID == clean.ID {
			clean.LastFired = list[i].LastFired
			clean.Day = list[i].Day
			clean.DayTurns = list[i].DayTurns
			list[i] = clean
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, clean)
	}
	if err := writeRoutines(baseDir, list); err != nil {
		return Routine{}, err
	}
	return clean, nil
}

// DeleteRoutine removes one routine by id.
func DeleteRoutine(baseDir, id string) error {
	routinesMu.Lock()
	defer routinesMu.Unlock()
	id = strings.TrimSpace(id)
	list, err := LoadRoutines(baseDir)
	if err != nil {
		return err
	}
	next := list[:0]
	for _, rt := range list {
		if rt.ID != id {
			next = append(next, rt)
		}
	}
	return writeRoutines(baseDir, next)
}

// SetRoutineEnabled pauses or resumes one routine.
func SetRoutineEnabled(baseDir, id string, enabled bool) error {
	routinesMu.Lock()
	defer routinesMu.Unlock()
	list, err := LoadRoutines(baseDir)
	if err != nil {
		return err
	}
	found := false
	for i := range list {
		if list[i].ID == id {
			list[i].Enabled = enabled
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("rotina não encontrada")
	}
	return writeRoutines(baseDir, list)
}

// ClaimDue marks at most one enabled routine whose next fire is at or before now.
// A second call with the same now returns nothing. The daily cap is 20 turns.
func ClaimDue(baseDir string, now time.Time) (Routine, bool, error) {
	routinesMu.Lock()
	defer routinesMu.Unlock()
	list, err := LoadRoutines(baseDir)
	if err != nil {
		return Routine{}, false, err
	}
	var picked int = -1
	var pickedAt time.Time
	for i := range list {
		rt := list[i]
		if !rt.Enabled {
			continue
		}
		loc := time.Local
		if rt.Timezone != "" {
			if l, err := time.LoadLocation(rt.Timezone); err == nil {
				loc = l
			}
		}
		from := rt.LastFired
		if from.IsZero() {
			from = now.Add(-time.Minute)
		}
		next, err := NextCron(rt.Expr, loc, from)
		if err != nil || next.After(now) {
			continue
		}
		day := now.In(loc).Format("2006-01-02")
		turns := rt.DayTurns
		if rt.Day != day {
			turns = 0
		}
		if turns >= maxRoutineTurnsPerDay {
			continue
		}
		if picked < 0 || next.Before(pickedAt) {
			picked = i
			pickedAt = next
		}
	}
	if picked < 0 {
		return Routine{}, false, nil
	}
	loc := time.Local
	if list[picked].Timezone != "" {
		if l, err := time.LoadLocation(list[picked].Timezone); err == nil {
			loc = l
		}
	}
	day := now.In(loc).Format("2006-01-02")
	if list[picked].Day != day {
		list[picked].DayTurns = 0
		list[picked].Day = day
	}
	list[picked].DayTurns++
	list[picked].LastFired = now
	claimed := list[picked]
	if err := writeRoutines(baseDir, list); err != nil {
		return Routine{}, false, err
	}
	return claimed, true, nil
}

// NarrowTiers parses a manifest that can only restrict classes.
func NarrowTiers(in []string) ([]Tier, error) {
	return parseClassList(in)
}

func parseClassList(in []string) ([]Tier, error) {
	if len(in) == 0 {
		return nil, nil
	}
	var out []Tier
	seen := map[Tier]bool{}
	for _, raw := range in {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		if s == "*" {
			return nil, fmt.Errorf("curinga de classe não é aceito")
		}
		t := Tier(s)
		switch t {
		case TierRead, TierWrite, TierExec, TierNetwork, TierCredentials, TierDestructive:
		default:
			return nil, fmt.Errorf("classe desconhecida %q", s)
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out, nil
}

func tiersToStrings(in []Tier) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, t := range in {
		out[i] = string(t)
	}
	return out
}

func tierAllowed(list []Tier, t Tier) bool {
	if len(list) == 0 {
		return true
	}
	for _, item := range list {
		if item == t {
			return true
		}
	}
	return false
}

// Scheduler sleeps until the next routine. It does not tick once a second.
type Scheduler struct {
	BaseDir string
	Now     func() time.Time
	OnFire  func(Routine)
	mu      sync.Mutex
	timer   *time.Timer
	stopped bool
}

// NewScheduler builds a scheduler. OnFire runs on the timer goroutine.
func NewScheduler(baseDir string, onFire func(Routine)) *Scheduler {
	return &Scheduler{BaseDir: baseDir, OnFire: onFire, Now: time.Now}
}

// Arm recomputes the next timer from disk. No routines means no timer.
func (s *Scheduler) Arm() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	when, ok := s.nextLocked()
	if !ok {
		return
	}
	d := when.Sub(s.now())
	if d < time.Second {
		d = time.Second
	}
	s.timer = time.AfterFunc(d, s.fire)
}

// Stop cancels the pending timer.
func (s *Scheduler) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

func (s *Scheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scheduler) nextLocked() (time.Time, bool) {
	list, err := LoadRoutines(s.BaseDir)
	if err != nil || len(list) == 0 {
		return time.Time{}, false
	}
	now := s.now()
	var best time.Time
	found := false
	for _, rt := range list {
		if !rt.Enabled {
			continue
		}
		loc := time.Local
		if rt.Timezone != "" {
			if l, err := time.LoadLocation(rt.Timezone); err == nil {
				loc = l
			}
		}
		from := rt.LastFired
		if from.IsZero() {
			from = now
		}
		next, err := NextCron(rt.Expr, loc, from)
		if err != nil {
			continue
		}
		if !found || next.Before(best) {
			best = next
			found = true
		}
	}
	return best, found
}

func (s *Scheduler) fire() {
	rt, ok, err := ClaimDue(s.BaseDir, s.now())
	if err == nil && ok && s.OnFire != nil {
		s.OnFire(rt)
	}
	s.Arm()
}
