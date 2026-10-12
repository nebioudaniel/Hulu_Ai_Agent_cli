package main

import (
	"sync"
	"time"
)

// rateLimiter is a small in-memory sliding-window limiter. It is enough for a
// single relay process; a shared store (for example Redis) can replace it later.
type rateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	attempts map[string][]time.Time
	now      func() time.Time // injectable for tests
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		limit:    limit,
		window:   window,
		attempts: make(map[string][]time.Time),
		now:      time.Now,
	}
}

// allow records an attempt for key and reports whether it is within the limit.
func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	cutoff := now.Add(-rl.window)

	kept := rl.attempts[key][:0]
	for _, t := range rl.attempts[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}

	if len(kept) >= rl.limit {
		rl.attempts[key] = kept
		return false
	}

	rl.attempts[key] = append(kept, now)
	return true
}
