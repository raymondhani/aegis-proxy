package server

import (
	"sync"
	"time"
)

// StaticRateLimiter is the OSS default RateLimiter implementation: a
// thread-safe token bucket, unchanged in behavior from the inline
// tokenBucket it replaces (rate RateLimitRPM/60 tokens/sec, capacity
// RateLimitRPM, starting full).
type StaticRateLimiter struct {
	rate         float64 // tokens added per second
	capacity     float64
	tokens       float64
	lastRefilled time.Time
	mu           sync.Mutex
}

// NewStaticRateLimiter constructs a StaticRateLimiter for the given
// requests-per-minute budget.
func NewStaticRateLimiter(limitPerMin int) *StaticRateLimiter {
	rate := float64(limitPerMin) / 60.0
	return &StaticRateLimiter{
		rate:         rate,
		capacity:     float64(limitPerMin),
		tokens:       float64(limitPerMin),
		lastRefilled: time.Now(),
	}
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
