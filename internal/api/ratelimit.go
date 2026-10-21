package api

import (
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// RateLimiter is a shared token-bucket limiter. The bucket holds one second's
// worth of requests (or one request for rates below 1 RPS) and refills
// continuously, so short bursts are allowed but the long-term rate is capped.
// A nil limiter or a rate <= 0 allows every request.
type RateLimiter struct {
	mu       sync.Mutex
	rate     float64
	burst    float64
	tokens   float64
	last     time.Time
	lastWarn time.Time
	now      func() time.Time
}

// NewRateLimiter returns a limiter that admits rate requests per second.
func NewRateLimiter(rate float64) *RateLimiter {
	burst := math.Max(1, rate)
	return &RateLimiter{
		rate:   rate,
		burst:  burst,
		tokens: burst,
		last:   time.Now(),
	}
}

// Enabled reports whether the limiter will reject any requests.
func (l *RateLimiter) Enabled() bool {
	return l != nil && l.rate > 0
}

// Allow reports whether one request may proceed and consumes a token if so.
func (l *RateLimiter) Allow() bool {
	if !l.Enabled() {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if l.now != nil {
		now = l.now()
	}
	if elapsed := now.Sub(l.last).Seconds(); elapsed > 0 {
		l.tokens = math.Min(l.burst, l.tokens+elapsed*l.rate)
		l.last = now
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

// Middleware rejects requests with 429 once the shared limiter is exhausted.
// Rejected requests are not passed to next, so all endpoints share the limit.
func (l *RateLimiter) Middleware(next http.Handler) http.Handler {
	if !l.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow() {
			l.logRejection(r)
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// logRejection records at most one warning per second so an abusive client
// cannot turn rejected requests into an unbounded log flood.
func (l *RateLimiter) logRejection(r *http.Request) {
	l.mu.Lock()
	now := time.Now()
	if l.now != nil {
		now = l.now()
	}
	if !l.lastWarn.IsZero() && now.Sub(l.lastWarn) < time.Second {
		l.mu.Unlock()
		return
	}
	l.lastWarn = now
	rate := l.rate
	l.mu.Unlock()

	log.Warn().Str("method", r.Method).Str("path", r.URL.Path).Float64("rate_limit_rps", rate).Msg("rate limit exceeded")
}
