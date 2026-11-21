package modelx

import (
	"context"
	"time"
)

// Spec 06 §3.1: JevCredentialPresence/JevCredentialFingerprints run LookAgy and
// LookCursor (full PATH scans) on EVERY routing pass. These cached variants
// keep the hot path in memory: a positive hit lasts 60s, a miss only 10s (so a
// freshly installed CLI shows up quickly). User-facing probes (ProbeAgy,
// model construction) keep calling the uncached LookAgy/LookCursor directly.
var (
	lookAgyCache    = newSWRCache[string](60*time.Second, 10*time.Second)
	lookCursorCache = newSWRCache[string](60*time.Second, 10*time.Second)
)

// LookAgyCached is the hot-path variant of LookAgy.
func LookAgyCached() (string, error) {
	return lookAgyCache.fetch(context.Background(), "agy", func(context.Context) (string, error) {
		return LookAgy()
	})
}

// AgyAvailableCached is the hot-path variant of AgyAvailable.
func AgyAvailableCached() bool {
	_, err := LookAgyCached()
	return err == nil
}

// LookCursorCached is the hot-path variant of LookCursor.
func LookCursorCached() (string, error) {
	return lookCursorCache.fetch(context.Background(), "cursor", func(context.Context) (string, error) {
		return LookCursor()
	})
}
