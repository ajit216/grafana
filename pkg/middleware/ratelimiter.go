package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	contextmodel "github.com/grafana/grafana/pkg/services/contexthandler/model"
	"github.com/grafana/grafana/pkg/web"
)

// RateLimiterConfig configures the per-IP sliding window rate limiter.
type RateLimiterConfig struct {
	// Enabled controls whether rate limiting is active.
	Enabled bool

	// Requests is the maximum number of requests allowed per IP within Window.
	Requests int

	// Window is the duration of the sliding window.
	Window time.Duration

	// SkipPaths is a list of URL path prefixes that bypass rate limiting.
	SkipPaths []string

	// TrustProxy controls whether X-Forwarded-For / X-Real-IP headers are
	// trusted for IP extraction. Enable only when behind a known proxy.
	TrustProxy bool
}

// DefaultRateLimiterConfig returns a permissive default suitable for development.
func DefaultRateLimiterConfig() RateLimiterConfig {
	return RateLimiterConfig{
		Enabled:  true,
		Requests: 100,
		Window:   time.Minute,
		SkipPaths: []string{
			"/api/health",
			"/metrics",
		},
	}
}

type ipBucket struct {
	mu         sync.Mutex
	timestamps []time.Time
}

// RateLimiter holds per-IP state for the sliding window rate limiter.
type RateLimiter struct {
	cfg  RateLimiterConfig
	mu   sync.Mutex
	ips  map[string]*ipBucket
}

// NewRateLimiter constructs a RateLimiter from cfg. Panics if Requests or
// Window are non-positive.
func NewRateLimiter(cfg RateLimiterConfig) *RateLimiter {
	if cfg.Requests <= 0 {
		panic("ratelimiter: Requests must be positive")
	}
	if cfg.Window <= 0 {
		panic("ratelimiter: Window must be positive")
	}
	return &RateLimiter{
		cfg: cfg,
		ips: make(map[string]*ipBucket),
	}
}

// Middleware returns a web.Handler that enforces the rate limit.
//
// Example:
//
//	rl := middleware.NewRateLimiter(middleware.DefaultRateLimiterConfig())
//	router.Use(rl.Middleware())
func (rl *RateLimiter) Middleware() web.Handler {
	if !rl.cfg.Enabled {
		return func(_ *contextmodel.ReqContext) {}
	}

	return func(c *contextmodel.ReqContext) {
		path := c.Req.URL.Path
		for _, skip := range rl.cfg.SkipPaths {
			if strings.HasPrefix(path, skip) {
				return
			}
		}

		ip := rl.extractIP(c.Req)
		if !rl.allow(ip) {
			c.JsonApiErr(http.StatusTooManyRequests, "Rate limit exceeded", nil)
		}
	}
}

// allow returns true if the IP is within the configured request budget.
func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	bucket, ok := rl.ips[ip]
	if !ok {
		bucket = &ipBucket{}
		rl.ips[ip] = bucket
	}
	rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.cfg.Window)

	bucket.mu.Lock()
	defer bucket.mu.Unlock()

	// Evict timestamps outside the window
	valid := bucket.timestamps[:0]
	for _, ts := range bucket.timestamps {
		if ts.After(cutoff) {
			valid = append(valid, ts)
		}
	}
	bucket.timestamps = valid

	if len(bucket.timestamps) >= rl.cfg.Requests {
		return false
	}

	bucket.timestamps = append(bucket.timestamps, now)
	return true
}

// extractIP returns the client IP from the request. When TrustProxy is true
// it checks X-Forwarded-For and X-Real-IP headers first.
func (rl *RateLimiter) extractIP(r *http.Request) string {
	if rl.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// X-Forwarded-For may contain a comma-separated chain; take first
			parts := strings.SplitN(xff, ",", 2)
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr may not have a port (unlikely but guard it)
		return r.RemoteAddr
	}
	return host
}

// Reset clears all per-IP state. Useful in tests.
func (rl *RateLimiter) Reset() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.ips = make(map[string]*ipBucket)
}
