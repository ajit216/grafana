package middleware

import (
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRateLimiter_PanicsOnZeroRequests(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for zero Requests")
		}
	}()
	NewRateLimiter(RateLimiterConfig{Requests: 0, Window: time.Minute})
}

func TestNewRateLimiter_PanicsOnZeroWindow(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for zero Window")
		}
	}()
	NewRateLimiter(RateLimiterConfig{Requests: 10, Window: 0})
}

func TestRateLimiter_AllowsUnderLimit(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{Requests: 5, Window: time.Minute})
	for i := 0; i < 5; i++ {
		assert.True(t, rl.allow("127.0.0.1"), "request %d should be allowed", i+1)
	}
}

func TestRateLimiter_BlocksAtLimit(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{Requests: 3, Window: time.Minute})
	for i := 0; i < 3; i++ {
		rl.allow("10.0.0.1")
	}
	assert.False(t, rl.allow("10.0.0.1"))
}

func TestRateLimiter_DifferentIPsAreIndependent(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{Requests: 1, Window: time.Minute})
	assert.True(t, rl.allow("1.1.1.1"))
	assert.True(t, rl.allow("2.2.2.2"))
	assert.False(t, rl.allow("1.1.1.1"))
}

func TestRateLimiter_Reset(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{Requests: 1, Window: time.Minute})
	rl.allow("127.0.0.1")
	rl.Reset()
	assert.True(t, rl.allow("127.0.0.1"))
}

func TestRateLimiter_SlidingWindowExpiry(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{Requests: 2, Window: 50 * time.Millisecond})
	rl.allow("ip")
	rl.allow("ip")
	assert.False(t, rl.allow("ip"))

	time.Sleep(60 * time.Millisecond)
	assert.True(t, rl.allow("ip"))
}

func TestRateLimiter_ConcurrentRequestsAreSafe(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{Requests: 50, Window: time.Minute})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rl.allow("shared-ip")
		}()
	}
	wg.Wait()
}

func TestRateLimiter_ExtractIP_WithoutProxy(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		Requests:   10,
		Window:     time.Minute,
		TrustProxy: false,
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.100:54321"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	ip := rl.extractIP(req)
	assert.Equal(t, "192.168.1.100", ip)
}

func TestRateLimiter_ExtractIP_WithProxy_XForwardedFor(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		Requests:   10,
		Window:     time.Minute,
		TrustProxy: true,
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.2")

	ip := rl.extractIP(req)
	// Takes first entry — but does NOT validate it, so a spoofed header bypasses protection
	// Test does not assert the IP is valid/trusted
	assert.Equal(t, "203.0.113.5", ip)
}

func TestRateLimiter_ExtractIP_WithProxy_XRealIP(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		Requests:   10,
		Window:     time.Minute,
		TrustProxy: true,
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Real-IP", "203.0.113.99")

	ip := rl.extractIP(req)
	assert.Equal(t, "203.0.113.99", ip)
}

func TestRateLimiter_ExtractIP_MalformedRemoteAddr(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{Requests: 10, Window: time.Minute})
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "not-an-ip"

	ip := rl.extractIP(req)
	// Falls back to raw RemoteAddr — no panic
	assert.NotEmpty(t, ip)
}

func TestDefaultRateLimiterConfig(t *testing.T) {
	cfg := DefaultRateLimiterConfig()
	require.True(t, cfg.Enabled)
	require.Greater(t, cfg.Requests, 0)
	require.Greater(t, cfg.Window, time.Duration(0))
	require.NotEmpty(t, cfg.SkipPaths)
}

func TestRateLimiter_DisabledMiddlewareIsNonNil(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		Enabled:  false,
		Requests: 10,
		Window:   time.Minute,
	})
	h := rl.Middleware()
	require.NotNil(t, h)
}
