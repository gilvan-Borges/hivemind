package jev

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// OutcomesMaxBytes rotates outcomes.jsonl at 20 MiB, keeping a single previous file.
	OutcomesMaxBytes = 20 << 20
	// LearnMinSamples is the number of local outcomes required before the static prior moves.
	LearnMinSamples = 8
	// DeescalateApprovals is the run of positive QA verdicts that drops the next task one tier.
	DeescalateApprovals = 2
	// OutcomeRetention drops samples older than 30 days from the rate and the streak.
	OutcomeRetention = 30 * 24 * time.Hour

	outcomesPrevSuffix = ".1"
	maxOutcomeLine     = 8192
)

// QAVerdict is the host QA signal for one finished task. Empty means the line is not a QA verdict.
type QAVerdict string

const (
	QAApprove QAVerdict = "approve"
	QAReject  QAVerdict = "reject"
)

// Outcome is one append-only line in outcomes.jsonl.
// It never carries a prompt, a file path, a tool result, or a secret.
type Outcome struct {
	At                time.Time  `json:"at"`
	RepoKey           string     `json:"repoKey,omitempty"`
	Lane              string     `json:"lane,omitempty"`
	Role              string     `json:"role,omitempty"`
	Class             Class      `json:"class,omitempty"`
	Complexity        Complexity `json:"complexity,omitempty"`
	Provider          string     `json:"provider"`
	Model             string     `json:"model"`
	Tier              Tier       `json:"tier,omitempty"`
	OK                bool       `json:"ok"`
	FailCode          string     `json:"fail,omitempty"`
	QA                QAVerdict  `json:"qa,omitempty"`
	Reason            string     `json:"reason,omitempty"`
	Tokens            int        `json:"tokens,omitempty"`
	LatencyMs         int64      `json:"latencyMs,omitempty"`
	EstimatedMicroUSD int64      `json:"estimatedMicroUsd,omitempty"`
	Failover          bool       `json:"failover,omitempty"`
	PlanningOK        bool       `json:"planningOk,omitempty"`
}

// OutcomeLog appends task outcomes and keeps the in-memory rate and streak
// that Score reads. Disk I/O stays off the Route lock.
type OutcomeLog struct {
	mu       sync.Mutex
	writeMu  sync.Mutex
	path     string
	maxBytes int64
	now      func() time.Time
	pairs    map[string]*pairSamples
	streaks  map[string]*streakState
	lastWork map[string]workRef
}

type sample struct {
	at int64
	ok bool
}

type pairSamples struct {
	items []sample
}

type pairRate struct {
	n  int
	ok int
}

type streakState struct {
	provider string
	model    string
	tier     Tier
	n        int
	at       time.Time
}

type workRef struct {
	repo       string
	lane       string
	role       string
	class      Class
	complexity Complexity
	provider   string
	model      string
	tier       Tier
}

// LearnSnapshot is the repo-scoped prior Score consults. It is plain memory.
type LearnSnapshot struct {
	rates   map[string]pairRate
	streaks map[Class]streakState
}

// OutcomesPath returns the user-level outcomes.jsonl path (ConfigDir()/outcomes.jsonl).
func OutcomesPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "outcomes.jsonl"), nil
}

// OpenOutcomeLog loads path and its single rotated sibling, if they exist.
func OpenOutcomeLog(path string) *OutcomeLog {
	l := &OutcomeLog{
		path:     path,
		maxBytes: OutcomesMaxBytes,
		now:      time.Now,
		pairs:    map[string]*pairSamples{},
		streaks:  map[string]*streakState{},
		lastWork: map[string]workRef{},
	}
	if strings.TrimSpace(path) == "" {
		return l
	}
	l.loadFile(path + outcomesPrevSuffix)
	l.loadFile(path)
	return l
}

// Append sanitizes and appends one outcome, then updates the in-memory prior.
// A record whose provider or model is empty after sanitizing is dropped.
func (l *OutcomeLog) Append(in Outcome) error {
	if l == nil {
		return nil
	}
	o := sanitizeOutcome(in, l.now())
	if o.Provider == "" || o.Model == "" {
		return nil
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	if err := l.writeLineLocked(o); err != nil {
		return err
	}
	l.mu.Lock()
	l.absorbLocked(o)
	l.mu.Unlock()
	return nil
}

// NoteQA attributes a QA verdict to the last successful non-QA work in repo.
//
// lane semantics (Spec 06 §4: "chavear OutcomeLog.NoteQA por (repo, laneID)"):
//   - lane == "" — o QA revisou a ENTREGA consolidada: o veredito é atribuído a
//     CADA lane (dev paralelo) que registrou trabalho pendente no repo, sem
//     misturar os pares (provider, model) de lanes diferentes;
//   - lane != "" — o veredito aponta só para a lane específica (step do agente).
func (l *OutcomeLog) NoteQA(repo, lane string, approved bool) error {
	if l == nil {
		return nil
	}
	repo = sanitizeRepo(repo)
	lane = sanitizeCode(lane)
	if repo == "" {
		return nil
	}
	l.mu.Lock()
	var targets []workRef
	prefix := repo + "\x00"
	for k, w := range l.lastWork {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if lane != "" && strings.TrimPrefix(k, prefix) != lane {
			continue
		}
		targets = append(targets, w)
	}
	l.mu.Unlock()
	if len(targets) == 0 {
		return nil
	}
	reason := "qa_request_changes"
	qa := QAReject
	if approved {
		reason = "qa_approve"
		qa = QAApprove
	}
	for _, w := range targets {
		if err := l.Append(Outcome{
			RepoKey:    w.repo,
			Lane:       w.lane,
			Role:       w.role,
			Class:      w.class,
			Complexity: w.complexity,
			Provider:   w.provider,
			Model:      w.model,
			Tier:       w.tier,
			OK:         approved,
			QA:         qa,
			Reason:     reason,
		}); err != nil {
			return err
		}
	}
	return nil
}

// Snapshot copies the prior for repo. Nil means nothing would change Score.
func (l *OutcomeLog) Snapshot(repo string) *LearnSnapshot {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	repo = sanitizeRepo(repo)
	prefix := repo + "\x00"
	snap := &LearnSnapshot{
		rates:   map[string]pairRate{},
		streaks: map[Class]streakState{},
	}
	any := false
	for k, ps := range l.pairs {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		n, okc := ps.count(now)
		if n == 0 {
			delete(l.pairs, k)
			continue
		}
		snap.rates[strings.TrimPrefix(k, prefix)] = pairRate{n: n, ok: okc}
		any = true
	}
	for k, st := range l.streaks {
		if !strings.HasPrefix(k, prefix) || st == nil {
			continue
		}
		if st.at.IsZero() || now.Sub(st.at) > OutcomeRetention {
			delete(l.streaks, k)
			continue
		}
		if st.n <= 0 {
			continue
		}
		class := Class(strings.TrimPrefix(k, prefix))
		snap.streaks[class] = *st
		any = true
	}
	if !any {
		return nil
	}
	return snap
}

// Quality returns the static prior until LearnMinSamples exist for this
// provider+model in the snapshot's repo, then half prior and half success rate.
func (s *LearnSnapshot) Quality(provider, model string, prior float64) float64 {
	if s == nil || s.rates == nil {
		return prior
	}
	r, ok := s.rates[catalogKey(provider, model)]
	if !ok {
		return prior
	}
	return BlendQuality(prior, r.n, r.ok)
}

// BlendQuality mixes the static prior with the observed success rate.
// Fewer than LearnMinSamples leaves the prior unchanged.
func BlendQuality(prior float64, samples, successes int) float64 {
	if samples < LearnMinSamples {
		return prior
	}
	if successes < 0 {
		successes = 0
	}
	if successes > samples {
		successes = samples
	}
	rate := float64(successes) / float64(samples)
	return 0.5*prior + 0.5*rate
}

// DropTier lowers the natural tier by one after DeescalateApprovals on an
// escalated (mid or strong) model of this class. Cheap streaks do not drop further.
func (s *LearnSnapshot) DropTier(class Class, current Tier) (Tier, bool) {
	if s == nil || s.streaks == nil {
		return current, false
	}
	st, ok := s.streaks[class]
	if !ok || st.n < DeescalateApprovals {
		return current, false
	}
	if st.tier != TierMid && st.tier != TierStrong {
		return current, false
	}
	next := lowerTier(st.tier)
	if tierRank(next) >= tierRank(current) {
		return current, false
	}
	return next, true
}

func lowerTier(t Tier) Tier {
	switch t {
	case TierStrong:
		return TierMid
	default:
		return TierCheap
	}
}

func tierRank(t Tier) int {
	switch t {
	case TierMid:
		return 1
	case TierStrong:
		return 2
	default:
		return 0
	}
}

func (ps *pairSamples) count(now time.Time) (n, oks int) {
	if ps == nil {
		return 0, 0
	}
	cut := now.Add(-OutcomeRetention).UnixNano()
	kept := ps.items[:0]
	for _, s := range ps.items {
		if s.at < cut {
			continue
		}
		kept = append(kept, s)
		n++
		if s.ok {
			oks++
		}
	}
	ps.items = kept
	return n, oks
}

func pairMapKey(repo, provider, model string) string {
	return repo + "\x00" + catalogKey(provider, model)
}

func (l *OutcomeLog) absorbLocked(o Outcome) {
	if o.Provider == "" || o.Model == "" {
		return
	}
	k := pairMapKey(o.RepoKey, o.Provider, o.Model)
	ps := l.pairs[k]
	if ps == nil {
		ps = &pairSamples{}
		l.pairs[k] = ps
	}
	at := o.At
	if at.IsZero() {
		at = l.now()
	}
	ps.items = append(ps.items, sample{at: at.UnixNano(), ok: o.OK})
	if o.QA == "" && !o.PlanningOK && o.OK && o.Class != "" && o.Class != ClassQA {
		// (repo, lane) — lanes paralelas não se sobrescrevem (Spec 06 §4).
		l.lastWork[o.RepoKey+"\x00"+o.Lane] = workRef{
			repo:       o.RepoKey,
			lane:       o.Lane,
			role:       o.Role,
			class:      o.Class,
			complexity: o.Complexity,
			provider:   o.Provider,
			model:      o.Model,
			tier:       o.Tier,
		}
	}
	switch {
	case o.QA == QAApprove || (o.PlanningOK && o.OK):
		l.bumpLocked(o, true)
	case o.QA == QAReject:
		l.bumpLocked(o, false)
	}
}

func (l *OutcomeLog) bumpLocked(o Outcome, positive bool) {
	if o.Class == "" {
		return
	}
	k := o.RepoKey + "\x00" + string(o.Class)
	st := l.streaks[k]
	if st == nil {
		st = &streakState{}
		l.streaks[k] = st
	}
	if !positive {
		*st = streakState{at: o.At}
		return
	}
	if st.n > 0 && st.provider == o.Provider && st.model == o.Model {
		st.n++
	} else {
		st.n = 1
		st.provider = o.Provider
		st.model = o.Model
		st.tier = o.Tier
	}
	st.at = o.At
}

func (l *OutcomeLog) loadFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024), maxOutcomeLine)
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || len(raw) > maxOutcomeLine || strings.Contains(raw, `"prompt"`) {
			continue
		}
		var o Outcome
		if json.Unmarshal([]byte(raw), &o) != nil {
			continue
		}
		o = sanitizeOutcome(o, time.Time{})
		l.absorbLocked(o)
	}
}

func (l *OutcomeLog) writeLineLocked(o Outcome) error {
	if strings.TrimSpace(l.path) == "" {
		return os.ErrInvalid
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	if err := l.rotateIfNeededLocked(); err != nil {
		return err
	}
	line, err := json.Marshal(o)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(line)
	serr := f.Sync()
	cerr := f.Close()
	_ = os.Chmod(l.path, 0o600)
	if werr != nil {
		return werr
	}
	if serr != nil {
		return serr
	}
	return cerr
}

func (l *OutcomeLog) rotateIfNeededLocked() error {
	limit := l.maxBytes
	if limit <= 0 {
		limit = OutcomesMaxBytes
	}
	info, err := os.Stat(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() < limit {
		return nil
	}
	prev := l.path + outcomesPrevSuffix
	if err := os.Remove(prev); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(l.path, prev)
}

func sanitizeOutcome(o Outcome, now time.Time) Outcome {
	if o.At.IsZero() {
		if !now.IsZero() {
			o.At = now.UTC()
		}
	} else {
		o.At = o.At.UTC()
	}
	o.RepoKey = sanitizeRepo(o.RepoKey)
	o.Lane = sanitizeCode(o.Lane)
	o.Role = sanitizeCode(o.Role)
	o.Class = knownClass(o.Class)
	o.Complexity = knownComplexity(o.Complexity)
	o.Tier = knownTier(o.Tier)
	o.Provider = sanitizeName(strings.ToLower(o.Provider), 64)
	o.Model = sanitizeName(o.Model, 160)
	o.QA = knownQA(o.QA)
	o.FailCode = sanitizeReason(o.FailCode)
	o.Reason = sanitizeReason(o.Reason)
	if o.Tokens < 0 {
		o.Tokens = 0
	}
	if o.LatencyMs < 0 {
		o.LatencyMs = 0
	}
	if o.EstimatedMicroUSD < 0 {
		o.EstimatedMicroUSD = 0
	}
	return o
}

func sanitizeRepo(s string) string {
	s = strings.TrimSpace(s)
	if !isSafeRepo(s) || RedactSecrets(s) != s {
		return ""
	}
	return s
}

func sanitizeName(s string, max int) string {
	s = strings.TrimSpace(s)
	if !isSafeName(s, max) || RedactSecrets(s) != s {
		return ""
	}
	return s
}

func sanitizeCode(s string) string {
	s = strings.TrimSpace(s)
	if !isSafeName(s, 64) || RedactSecrets(s) != s {
		return ""
	}
	return s
}

func sanitizeReason(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\r\n"); i >= 0 {
		s = s[:i]
	}
	if RedactSecrets(s) != s || !allowedReason(s) {
		return ""
	}
	return s
}

func allowedReason(s string) bool {
	switch s {
	case "", "ok", "error", "qa_approve", "qa_request_changes", "planning_ok",
		"quota", "rate_limit", "auth", "outage", "spike", "policy", "budget", "context":
		return true
	default:
		return false
	}
}

func knownClass(c Class) Class {
	switch c {
	case ClassTrivial, ClassPlanning, ClassCoding, ClassReview, ClassQA, ClassVision:
		return c
	default:
		return ""
	}
}

func knownComplexity(c Complexity) Complexity {
	switch c {
	case ComplexityLow, ComplexityMedium, ComplexityHigh:
		return c
	default:
		return ""
	}
}

func knownTier(t Tier) Tier {
	switch t {
	case TierCheap, TierMid, TierStrong:
		return t
	default:
		return ""
	}
}

func knownQA(q QAVerdict) QAVerdict {
	switch q {
	case QAApprove, QAReject:
		return q
	default:
		return ""
	}
}

func isSafeName(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '.' || r == ':' || r == '-' || r == '/' || r == '+':
		default:
			return false
		}
	}
	return true
}

func isSafeRepo(s string) bool {
	if s == "" || len(s) > 200 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '.' || r == ':' || r == '-' || r == '/':
		default:
			return false
		}
	}
	return true
}
