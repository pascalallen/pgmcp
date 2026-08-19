package middleware

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

const (
	// anonymousPrincipal is the bucket every unauthenticated caller shares.
	anonymousPrincipal = "anonymous"
	// idleTTL is how long a principal's bucket survives without a call.
	idleTTL = 10 * time.Minute
	// evictEvery is how many calls pass between sweeps for idle buckets.
	evictEvery = 1000
	// secondsPerMinute converts a per-minute allowance into a per-second rate.
	secondsPerMinute = 60
)

// rateEntry is one principal's token bucket and the time it was last used.
type rateEntry struct {
	lim  *rate.Limiter
	last time.Time
}

// rateLimiter holds one token bucket per principal. Buckets are created on
// first use and swept when they fall idle, so a churn of principals cannot
// grow the map without bound.
type rateLimiter struct {
	perMinute int
	burst     int
	now       func() time.Time

	mu      sync.Mutex
	entries map[string]*rateEntry
	calls   int
}

// RateLimit throttles tools/call to perMinute calls with the given burst, per
// principal — the authenticated user id, or a shared anonymous bucket. A call
// over the limit comes back as a tool-level error result rather than a
// protocol error, so the model can read it and back off. A non-positive
// perMinute disables throttling entirely.
func RateLimit(perMinute int, burst int) mcp.Middleware {
	return newRateLimiter(perMinute, burst, time.Now).middleware()
}

// newRateLimiter builds a rate limiter reading the clock through now, which
// tests replace to drive the bucket and the eviction sweep deterministically.
func newRateLimiter(perMinute int, burst int, now func() time.Time) *rateLimiter {
	return &rateLimiter{
		perMinute: perMinute,
		burst:     burst,
		now:       now,
		entries:   map[string]*rateEntry{},
	}
}

// middleware adapts the limiter to an mcp.Middleware.
func (l *rateLimiter) middleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		if l.perMinute <= 0 {
			return next
		}

		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodCallTool {
				return next(ctx, method, req)
			}

			key := principalID(req)
			if key == "" {
				key = anonymousPrincipal
			}

			if allowed, retryAfter := l.allow(key); !allowed {
				return toolError(fmt.Sprintf("rate limit exceeded, retry in %ds", retrySeconds(retryAfter))), nil
			}

			return next(ctx, method, req)
		}
	}
}

// allow consumes a token for the principal, reporting whether the call may
// proceed and, when it may not, how long until a token frees up.
func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls++
	if l.calls%evictEvery == 0 {
		l.evictLocked(now)
	}

	entry, ok := l.entries[key]
	if !ok {
		limit := rate.Limit(float64(l.perMinute) / secondsPerMinute)
		entry = &rateEntry{lim: rate.NewLimiter(limit, l.burst)}
		l.entries[key] = entry
	}

	entry.last = now

	if entry.lim.AllowN(now, 1) {
		return true, 0
	}

	reservation := entry.lim.ReserveN(now, 1)
	retryAfter := reservation.DelayFrom(now)
	reservation.CancelAt(now)

	return false, retryAfter
}

// evictLocked drops buckets that have gone idle. The caller holds l.mu.
func (l *rateLimiter) evictLocked(now time.Time) {
	for key, entry := range l.entries {
		if now.Sub(entry.last) > idleTTL {
			delete(l.entries, key)
		}
	}
}

// retrySeconds rounds a retry delay up to whole seconds, never below one, so
// the advice a client reads is always actionable.
func retrySeconds(d time.Duration) int {
	seconds := int(math.Ceil(d.Seconds()))
	if seconds < 1 {
		return 1
	}

	return seconds
}
