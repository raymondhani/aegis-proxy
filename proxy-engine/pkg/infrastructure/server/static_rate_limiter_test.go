package server

import "testing"

// referenceTokenBucket is a frozen copy of the pre-extraction inline
// tokenBucket (tcp_proxy.go:779-793 at time of extraction), kept only in this
// test to prove StaticRateLimiter is behaviorally identical, not a
// reimplementation. Capacity now matches burstCapacity (Spec 004 T131,
// contracts/rate-ceiling.md decision 1) rather than the whole per-minute
// budget -- StaticRateLimiter's own capacity changed by design, so parity
// is against the new intended behavior, not the pre-Spec-004 one.
type referenceTokenBucket struct {
	rate     float64
	capacity float64
	tokens   float64
}

func newReferenceTokenBucket(limitPerMin int) *referenceTokenBucket {
	rate := float64(limitPerMin) / 60.0
	capacity := burstCapacity(limitPerMin)
	return &referenceTokenBucket{rate: rate, capacity: capacity, tokens: capacity}
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

// TestStaticRateLimiterBurstCapacityIsNotTheWholeBudget: Spec 004 T131,
// contracts/rate-ceiling.md decision 1. A burst well under the whole minute's
// budget must still be throttled -- capacity is about a second's worth of
// the budget, not the whole thing.
func TestStaticRateLimiterBurstCapacityIsNotTheWholeBudget(t *testing.T) {
	limiter := NewStaticRateLimiter(600)
	const burst = 25
	admitted := 0
	for i := 0; i < burst; i++ {
		if limiter.Allow("s") {
			admitted++
		}
	}
	if admitted == burst {
		t.Fatalf("all %d requests in an instantaneous burst were admitted against a 600rpm limiter -- capacity must be smaller than the whole minute's budget", burst)
	}
}

// TestStaticRateLimiterNonPositiveLimitRefusesEverything: Spec 004 T132,
// contracts/rate-ceiling.md decision 2.
func TestStaticRateLimiterNonPositiveLimitRefusesEverything(t *testing.T) {
	for _, limit := range []int{0, -1, -100} {
		limiter := NewStaticRateLimiter(limit)
		if limiter.Allow("s") {
			t.Errorf("NewStaticRateLimiter(%d): expected the very first request to be refused, got admitted", limit)
		}
	}
}

// TestStaticRateLimiterSetLimitResizesLiveWithoutFreeBurstOrUnfairDrain: Spec
// 004 T134, contracts/rate-ceiling.md decision 4. A live ceiling change must
// resize an already-running limiter's capacity, rescaling held tokens
// proportionally rather than resetting them (which would grant a free burst
// on a raise, or an unfair drain on a cut).
func TestStaticRateLimiterSetLimitResizesLiveWithoutFreeBurstOrUnfairDrain(t *testing.T) {
	limiter := NewStaticRateLimiter(60) // capacity 1
	if !limiter.Allow("s") {
		t.Fatal("expected first request to be allowed: limiter must start with a full bucket")
	}
	if limiter.Allow("s") {
		t.Fatal("expected second immediate request denied: single-token budget just consumed")
	}

	// Raise the ceiling enough to grow capacity from 1 to 2.
	limiter.SetLimit(120)
	if limiter.tokens != 0 {
		t.Fatalf("tokens after SetLimit raised capacity while empty = %v, want 0 (rescaling 0 tokens must not manufacture a free burst)", limiter.tokens)
	}
	if limiter.Allow("s") {
		t.Fatal("expected immediate request after a capacity raise to still be denied: no tokens were held to rescale up from empty")
	}
}

// TestStaticRateLimiterSetLimitToNonPositiveThenBackRefusesThenRecovers:
// verifies the T132 fail-closed behaviour composes correctly with the T134
// live-resize path: a live drop to a non-positive ceiling must refuse
// everything immediately, and a later live raise must recover cleanly.
func TestStaticRateLimiterSetLimitToNonPositiveThenBackRefusesThenRecovers(t *testing.T) {
	limiter := NewStaticRateLimiter(60)
	limiter.SetLimit(0)
	if limiter.Allow("s") {
		t.Fatal("expected request refused after a live drop to a non-positive ceiling")
	}
	limiter.SetLimit(60)
	if !limiter.Allow("s") {
		t.Fatal("expected request admitted after a live raise back to a positive ceiling")
	}
}
