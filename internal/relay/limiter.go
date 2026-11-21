package relay

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// LimitConfig tunes brute-force and request-rate protection.
type LimitConfig struct {
	MaxFails    int
	FailWindow  time.Duration
	BlockFor    time.Duration
	LoginPerMin int
	RPS         rate.Limit
	Burst       int
}

// DefaultLimits is sized for a single-core Always Free VM.
func DefaultLimits() LimitConfig {
	return LimitConfig{
		MaxFails:    5,
		FailWindow:  10 * time.Minute,
		BlockFor:    15 * time.Minute,
		LoginPerMin: 10,
		RPS:         20,
		Burst:       40,
	}
}

type failState struct {
	fails        int
	windowStart  time.Time
	blockedUntil time.Time
}

// Limiter tracks login failures (by IP and by email) and a token bucket per IP.
type Limiter struct {
	cfg    LimitConfig
	mu     sync.Mutex
	fails  map[string]*failState
	hits   map[string]*rate.Limiter
	logins map[string]*rate.Limiter
	now    func() time.Time
}

// NewLimiter builds a limiter. Zero cfg fields fall back to DefaultLimits.
func NewLimiter(cfg LimitConfig) *Limiter {
	def := DefaultLimits()
	if cfg.MaxFails <= 0 {
		cfg.MaxFails = def.MaxFails
	}
	if cfg.FailWindow <= 0 {
		cfg.FailWindow = def.FailWindow
	}
	if cfg.BlockFor <= 0 {
		cfg.BlockFor = def.BlockFor
	}
	if cfg.LoginPerMin <= 0 {
		cfg.LoginPerMin = def.LoginPerMin
	}
	if cfg.RPS <= 0 {
		cfg.RPS = def.RPS
	}
	if cfg.Burst <= 0 {
		cfg.Burst = def.Burst
	}
	return &Limiter{
		cfg:    cfg,
		fails:  make(map[string]*failState),
		hits:   make(map[string]*rate.Limiter),
		logins: make(map[string]*rate.Limiter),
		now:    time.Now,
	}
}

// AllowLogin applies a per-key budget before any password hash runs, so a
// caller cannot burn the VM's CPU on argon2id.
func (l *Limiter) AllowLogin(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, ok := l.logins[key]
	if !ok {
		perSec := rate.Limit(float64(l.cfg.LoginPerMin) / 60.0)
		if perSec <= 0 {
			perSec = rate.Limit(1)
		}
		lim = rate.NewLimiter(perSec, l.cfg.LoginPerMin)
		l.logins[key] = lim
		if len(l.logins) > 4096 {
			evicted := 0
			for k := range l.logins {
				if k == key {
					continue
				}
				delete(l.logins, k)
				evicted++
				if evicted >= 512 {
					break
				}
			}
		}
	}
	return lim.Allow()
}

// AllowRequest applies the per-IP token bucket. Keys should already include
// a prefix (for example "ip:203.0.113.4").
func (l *Limiter) AllowRequest(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, ok := l.hits[key]
	if !ok {
		lim = rate.NewLimiter(l.cfg.RPS, l.cfg.Burst)
		l.hits[key] = lim
		if len(l.hits) > 4096 {
			evicted := 0
			for k := range l.hits {
				if k == key {
					continue
				}
				delete(l.hits, k)
				evicted++
				if evicted >= 512 {
					break
				}
			}
		}
	}
	return lim.Allow()
}

// LoginBlocked reports whether this key is currently blocked and for how long.
func (l *Limiter) LoginBlocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.fails[key]
	if st == nil {
		return false, 0
	}
	now := l.now()
	if !st.blockedUntil.IsZero() && now.Before(st.blockedUntil) {
		return true, st.blockedUntil.Sub(now)
	}
	return false, 0
}

// Fail records a failed login. The fifth failure inside the window blocks the key.
func (l *Limiter) Fail(key string) (blocked bool, retry time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	st := l.fails[key]
	if st == nil || (!st.blockedUntil.IsZero() && !now.Before(st.blockedUntil) && now.Sub(st.windowStart) > l.cfg.FailWindow) || (st.blockedUntil.IsZero() && now.Sub(st.windowStart) > l.cfg.FailWindow) {
		st = &failState{windowStart: now}
		l.fails[key] = st
	}
	if !st.blockedUntil.IsZero() && now.Before(st.blockedUntil) {
		return true, st.blockedUntil.Sub(now)
	}
	st.fails++
	if st.fails >= l.cfg.MaxFails {
		st.blockedUntil = now.Add(l.cfg.BlockFor)
		l.evictStaleFailsLocked(key, now)
		return true, l.cfg.BlockFor
	}
	l.evictStaleFailsLocked(key, now)
	return false, 0
}

func (l *Limiter) evictStaleFailsLocked(keep string, now time.Time) {
	if len(l.fails) <= 4096 {
		return
	}
	for k, v := range l.fails {
		if k == keep {
			continue
		}
		if !v.blockedUntil.IsZero() && !now.Before(v.blockedUntil) {
			delete(l.fails, k)
		} else if v.blockedUntil.IsZero() && now.Sub(v.windowStart) > l.cfg.FailWindow {
			delete(l.fails, k)
		}
	}
	if len(l.fails) <= 4096 {
		return
	}
	for k, v := range l.fails {
		if k == keep {
			continue
		}
		if v.blockedUntil.IsZero() || !now.Before(v.blockedUntil) {
			delete(l.fails, k)
			if len(l.fails) <= 4096 {
				break
			}
		}
	}
}

// Success clears failure state for the key.
func (l *Limiter) Success(key string) {
	l.mu.Lock()
	delete(l.fails, key)
	l.mu.Unlock()
}
