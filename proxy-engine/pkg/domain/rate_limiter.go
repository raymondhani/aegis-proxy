package domain

// RateLimiter is the tier-neutral extension point for per-session request
// throttling. It carries no ML, tier, plan, or entitlement vocabulary: the
// OSS engine ships a static implementation, and higher tiers may inject an
// adaptive one by composition (see contracts/rate-limiter-port.md).
type RateLimiter interface {
	// Allow reports whether the next request for sessionID may proceed.
	Allow(sessionID string) bool
}
