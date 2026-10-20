package api

import (
	"net/http"
	"path"
	"sync"
	"time"
)

const (
	defaultMaxTrackedPaths = 4096
	pathLimiterIdleTTL     = 10 * time.Minute
)

type pathLimiterEntry struct {
	limiter  *RateLimiter
	lastSeen time.Time
}

// PathRateLimiter keeps one token bucket per URL path. A noisy endpoint (for
// example the VPP graph) therefore cannot consume the budget of unrelated
// endpoints, while each path is still capped at rate requests per second.
// A nil limiter or a rate <= 0 allows every request.
type PathRateLimiter struct {
	mu       sync.Mutex
	rate     float64
	maxPaths int
	paths    map[string]*pathLimiterEntry
}

// NewPathRateLimiter returns a limiter that admits rate requests per second for
// each distinct URL path.
func NewPathRateLimiter(rate float64) *PathRateLimiter {
	return &PathRateLimiter{
		rate:     rate,
		maxPaths: defaultMaxTrackedPaths,
		paths:    make(map[string]*pathLimiterEntry),
	}
}

// Enabled reports whether the limiter will reject any requests.
func (l *PathRateLimiter) Enabled() bool {
	return l != nil && l.rate > 0
}

// Allow reports whether one request for the given path may proceed.
func (l *PathRateLimiter) Allow(urlPath string) bool {
	return l.limiterFor(urlPath).Allow()
}

// Middleware rejects requests with 429 when that path's bucket is exhausted.
func (l *PathRateLimiter) Middleware(next http.Handler) http.Handler {
	if !l.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limiter := l.limiterFor(r.URL.Path)
		if !limiter.Allow() {
			limiter.logRejection(r)
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *PathRateLimiter) limiterFor(urlPath string) *RateLimiter {
	if !l.Enabled() {
		return nil
	}
	key := path.Clean(urlPath)
	if key == "." {
		key = "/"
	}

	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if entry, ok := l.paths[key]; ok {
		entry.lastSeen = now
		return entry.limiter
	}
	if len(l.paths) >= l.maxPaths {
		l.evictLocked(now)
	}
	limiter := NewRateLimiter(l.rate)
	l.paths[key] = &pathLimiterEntry{limiter: limiter, lastSeen: now}
	return limiter
}

// evictLocked drops idle paths first. If every path is active, it removes the
// least recently seen one so a client cannot grow the map without bound.
func (l *PathRateLimiter) evictLocked(now time.Time) {
	cutoff := now.Add(-pathLimiterIdleTTL)
	for key, entry := range l.paths {
		if entry.lastSeen.Before(cutoff) {
			delete(l.paths, key)
		}
	}
	if len(l.paths) < l.maxPaths {
		return
	}

	var oldestKey string
	var oldest time.Time
	for key, entry := range l.paths {
		if oldestKey == "" || entry.lastSeen.Before(oldest) {
			oldestKey = key
			oldest = entry.lastSeen
		}
	}
	if oldestKey != "" {
		delete(l.paths, oldestKey)
	}
}
