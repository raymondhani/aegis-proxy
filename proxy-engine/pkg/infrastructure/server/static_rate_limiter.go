package server

import (
	"math"
	"sync"
	"time"
)

// StaticRateLimiter is the OSS default RateLimiter implementation: a
// thread-safe token bucket. Bucket sizing follows
// contracts/rate-ceiling.md (Spec 004): capacity is about a second's worth
// of the configured budget rather than the whole minute (decision 1), a
// non-positive limit refuses everything (decision 2), and SetLimit lets a
// live ceiling change resize an already-running bucket (decision 4).
type StaticRateLimiter struct {
	rate         float64 // tokens added per second
	capacity     float64
	tokens       float64
	lastRefilled time.Time
	mu           sync.Mutex
}

// burstCapacity mirrors the enterprise adaptiveBucket's helper of the same
// name (internal/throttle/adaptive_limiter.go, aegis-enterprise-guardrail):
// about a second's worth of the requests-per-minute budget, floored at 1 so
// the very first request is never made to wait.
func burstCapacity(limitPerMin int) float64 {
	c := math.Ceil(float64(limitPerMin) / 60.0)
	if c < 1 {
		c = 1
	}
	return c
}

// NewStaticRateLimiter constructs a StaticRateLimiter for the given
// requests-per-minute budget.
func NewStaticRateLimiter(limitPerMin int) *StaticRateLimiter {
	l := &StaticRateLimiter{lastRefilled: time.Now()}
	l.SetLimit(limitPerMin)
	return l
}

// SetLimit applies a (possibly changed) requests-per-minute budget to an existing limiter,
// rescaling any held tokens proportionally to the new capacity so a live ceiling change never
// grants a free burst nor an unfair drain (contracts/rate-ceiling.md decision 4). Safe to call on
// every Allow() resolution, not just at construction -- an unchanged limit is a cheap no-op.
func (l *StaticRateLimiter) SetLimit(limitPerMin int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if limitPerMin <= 0 {
		// contracts/rate-ceiling.md decision 2: an explicit non-positive ceiling
		// refuses everything, on both tiers.
		l.tokens = 0
		l.capacity = 0
		l.rate = 0
		l.lastRefilled = time.Now()
		return
	}

	newCapacity := burstCapacity(limitPerMin)
	switch {
	case l.capacity == 0:
		l.tokens = newCapacity
		l.lastRefilled = time.Now()
	case l.capacity != newCapacity:
		l.tokens = l.tokens / l.capacity * newCapacity
	}
	l.capacity = newCapacity
	l.rate = float64(limitPerMin) / 60.0
}

// Allow implements domain.RateLimiter.
func (l *StaticRateLimiter) Allow(sessionID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(l.lastRefilled).Seconds()
	l.lastRefilled = now

	l.tokens += elapsed * l.rate
	if l.tokens > l.capacity {
		l.tokens = l.capacity
	}

	if l.tokens >= 1.0 {
		l.tokens -= 1.0
		return true
	}
	return false
}
