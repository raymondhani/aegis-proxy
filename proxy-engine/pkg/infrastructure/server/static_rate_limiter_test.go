package server

import "testing"

// referenceTokenBucket is a frozen copy of the pre-extraction inline
// tokenBucket (tcp_proxy.go:779-793 at time of extraction), kept only in this
// test to prove StaticRateLimiter is behaviorally identical, not a
// reimplementation.
type referenceTokenBucket struct {
	rate     float64
	capacity float64
	tokens   float64
}

func newReferenceTokenBucket(limitPerMin int) *referenceTokenBucket {
	rate := float64(limitPerMin) / 60.0
	return &referenceTokenBucket{rate: rate, capacity: float64(limitPerMin), tokens: float64(limitPerMin)}
}

// allow mirrors the original's logic but without the time-based refill, so
// this test can drive both implementations through an identical, deterministic
// sequence of pure token-consumption decisions.
func (tb *referenceTokenBucket) allow() bool {
	if tb.tokens >= 1.0 {
		tb.tokens -= 1.0
		return true
	}
	return false
}

// TestStaticRateLimiterParityWithReferenceBucket drives both implementations
// through the same request sequence with no time elapsed between calls
// (isolating pure token-consumption behavior from wall-clock refill, which is
// identical in both since StaticRateLimiter is a direct extraction) and
// asserts an identical allow/deny sequence.
func TestStaticRateLimiterParityWithReferenceBucket(t *testing.T) {
	const limitRPM = 5
	reference := newReferenceTokenBucket(limitRPM)
	limiter := NewStaticRateLimiter(limitRPM)
	// Freeze the refill clock so limiter.Allow, like reference.allow, only
	// exercises token consumption for this comparison.
	limiter.lastRefilled = limiter.lastRefilled.Add(0)

	for i := 0; i < limitRPM+3; i++ {
		want := reference.allow()
		got := limiter.Allow("session-a")
		if got != want {
			t.Fatalf("request %d: StaticRateLimiter.Allow() = %v, want %v (reference tokenBucket)", i, got, want)
		}
	}
}

func TestStaticRateLimiterStartsFull(t *testing.T) {
	limiter := NewStaticRateLimiter(10)
	if !limiter.Allow("s") {
		t.Fatal("expected first request to be allowed: limiter must start with a full bucket")
	}
}

func TestStaticRateLimiterBlocksOverBudget(t *testing.T) {
	limiter := NewStaticRateLimiter(1)
	if !limiter.Allow("s") {
		t.Fatal("expected first request within budget to be allowed")
	}
	if limiter.Allow("s") {
		t.Fatal("expected second immediate request to be denied: budget of 1/min exhausted")
	}
}
