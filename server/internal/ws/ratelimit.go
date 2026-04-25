package ws

import (
	"sync"
	"time"
)

// RateLimiter is a per-connection token-bucket. Used mostly as an
// anti-bug measure (designers and well-behaved clients are way under the
// budget; a runaway script-spam scenario gets rate-capped without falling
// over the gateway).
//
// Defaults: 50 messages/second sustained, burst of 100. Tunable per
// connection if a future surface needs higher (e.g. designer mass-spawn).
type RateLimiter struct {
	mu         sync.Mutex
	tokens     float64
	max        float64
	refillRate float64 // tokens per second
	last       time.Time
}

// NewRateLimiter constructs a token bucket.
func NewRateLimiter(burst, perSecond int) *RateLimiter {
	if burst <= 0 {
		burst = 100
	}
	if perSecond <= 0 {
		perSecond = 50
	}
	return &RateLimiter{
		tokens:     float64(burst),
		max:        float64(burst),
		refillRate: float64(perSecond),
		last:       time.Now(),
	}
}

// Allow consumes one token. Returns true on success.
// Caller logs the abuse if Allow returns false repeatedly.
func (r *RateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(r.last).Seconds()
	r.last = now
	r.tokens += elapsed * r.refillRate
	if r.tokens > r.max {
		r.tokens = r.max
	}
	if r.tokens < 1 {
		return false
	}
	r.tokens--
	return true
}

// Tokens returns the current bucket level for telemetry / tests.
func (r *RateLimiter) Tokens() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tokens
}
