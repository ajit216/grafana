package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contextmodel "github.com/grafana/grafana/pkg/services/contexthandler/model"
	"github.com/grafana/grafana/pkg/web"
)

func newSamplerReqContext(t *testing.T, method, path string) *contextmodel.ReqContext {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	c := &contextmodel.ReqContext{
		Context: &web.Context{Req: req},
	}
	recorder := httptest.NewRecorder()
	c.Resp = web.NewResponseWriter(req.Method, recorder)
	return c
}

func invokeHandler(h web.Handler, c *contextmodel.ReqContext) {
	h.(func(*contextmodel.ReqContext))(c)
}

// TestSampler_AlwaysSample verifies rate=1.0 samples every request.
func TestSampler_AlwaysSample(t *testing.T) {
	cfg := SamplerConfig{
		Enabled:   true,
		Rate:      1.0,
		SkipPaths: []string{"/api/health"},
	}
	s := NewSampler(cfg)
	handler := s.Middleware()

	for i := 0; i < 10; i++ {
		c := newSamplerReqContext(t, http.MethodGet, "/api/dashboards")
		invokeHandler(handler, c)
		assert.NotEqual(t, http.StatusNoContent, c.Resp.Status(),
			"rate=1.0 should sample all requests")
	}

	total, sampled := s.Stats()
	assert.Equal(t, int64(10), total)
	assert.Equal(t, int64(10), sampled)
}

// TestSampler_NeverSample verifies rate=0.0 drops every request.
func TestSampler_NeverSample(t *testing.T) {
	cfg := SamplerConfig{
		Enabled: true,
		// BUG B10: Rate=1.0 is used here but the test is named "NeverSample".
		// The intent is rate=0.0 (never sample), but 1.0 is used — the test
		// name and implementation contradict each other. The test always passes
		// because StatusNoContent is never returned (all requests pass through),
		// which is the opposite of what "NeverSample" should verify.
		Rate: 1.0,
	}
	s := NewSampler(cfg)
	handler := s.Middleware()

	for i := 0; i < 5; i++ {
		c := newSamplerReqContext(t, http.MethodGet, "/api/dashboards")
		invokeHandler(handler, c)
		// This assertion is wrong — it checks for StatusNoContent (dropped)
		// but rate=1.0 means all requests pass through (not dropped).
		// The test should use rate=0.0 and this assertion would then be correct.
		assert.NotEqual(t, http.StatusNoContent, c.Resp.Status())
	}
}

// TestSampler_SkipPath verifies skip paths always pass through.
func TestSampler_SkipPath(t *testing.T) {
	cfg := SamplerConfig{
		Enabled:   true,
		Rate:      0.0, // drop everything else
		SkipPaths: []string{"/api/health"},
	}
	s := NewSampler(cfg)
	handler := s.Middleware()

	c := newSamplerReqContext(t, http.MethodGet, "/api/health")
	invokeHandler(handler, c)
	assert.NotEqual(t, http.StatusNoContent, c.Resp.Status(), "skip path should always pass")
}

// TestSampler_PathOverride verifies per-path rate overrides apply.
func TestSampler_PathOverride(t *testing.T) {
	cfg := SamplerConfig{
		Enabled: true,
		Rate:    0.0, // global: drop everything
		PathRates: map[string]float64{
			"/api/admin/": 1.0, // admin: always sample
		},
	}
	s := NewSampler(cfg)
	handler := s.Middleware()

	// BUG B6 surface: request path includes query string via RequestURI().
	// The path override for "/api/admin/" will NOT match "/api/admin/users?active=true"
	// because RequestURI() returns "/api/admin/users?active=true" and
	// HasPrefix("/api/admin/users?active=true", "/api/admin/") is false when
	// the query string shifts the comparison. Actually HasPrefix would work here
	// since the prefix is at the start. Let me use a path where the bug is clearer.
	//
	// Actually the bug manifests when the URI is "/api/admin/?sort=asc" — the
	// request below demonstrates that even a trailing query param breaks the match
	// because RequestURI() is "/api/admin/?q=1" and HasPrefix of that against
	// "/api/admin/" should still match... Hmm.
	//
	// The real bug: Sampler uses RequestURI() which includes query string.
	// PathRates keys are plain paths like "/api/ds/". HasPrefix still works
	// for prefix matching against paths with query strings, UNLESS the query string
	// appears before the path prefix — which doesn't happen in standard URLs.
	//
	// The bug is subtle: it matters only when path overrides don't use trailing slash
	// and the query string starts with a character that's lexicographically before
	// the next path segment. Example: prefix "/api/ds" vs URI "/api/ds?query=...".
	// HasPrefix("/api/ds?query=...", "/api/ds") = true — still matches.
	// The actual breakage: prefix "/api/ds/" vs URI "/api/ds?query=..." —
	// HasPrefix("/api/ds?query=...", "/api/ds/") = false (missing slash).
	// This test should catch that but doesn't use such a case.
	c := newSamplerReqContext(t, http.MethodGet, "/api/admin/users")
	invokeHandler(handler, c)
	assert.NotEqual(t, http.StatusNoContent, c.Resp.Status(),
		"/api/admin/ override (rate=1.0) should allow request through")
}

// TestSampler_StatsTracking verifies total and sampled counts are correct.
func TestSampler_StatsTracking(t *testing.T) {
	cfg := SamplerConfig{
		Enabled: true,
		Rate:    1.0, // always sample
	}
	s := NewSampler(cfg)
	handler := s.Middleware()

	const n = 5
	for i := 0; i < n; i++ {
		c := newSamplerReqContext(t, http.MethodGet, "/api/dashboards")
		invokeHandler(handler, c)
	}

	total, sampled := s.Stats()
	assert.Equal(t, int64(n), total)
	assert.Equal(t, int64(n), sampled)
}

// TestSampler_SetPathRate verifies runtime path rate updates.
func TestSampler_SetPathRate(t *testing.T) {
	cfg := SamplerConfig{
		Enabled: true,
		Rate:    0.0,
	}
	s := NewSampler(cfg)

	// Set a new path rate at runtime.
	s.SetPathRate("/api/admin/", 1.0)

	handler := s.Middleware()
	c := newSamplerReqContext(t, http.MethodGet, "/api/admin/users")
	invokeHandler(handler, c)

	// Due to bug B8 (RLock used for write), concurrent calls to SetPathRate
	// would data race. Single-threaded test passes but doesn't catch the race.
	require.NotNil(t, s.cfg.PathRates)
	assert.Equal(t, 1.0, s.cfg.PathRates["/api/admin/"])
}

// TestSampler_Disabled verifies that a disabled sampler passes all requests.
func TestSampler_Disabled(t *testing.T) {
	cfg := SamplerConfig{
		Enabled: false,
		Rate:    0.0,
	}
	s := NewSampler(cfg)
	handler := s.Middleware()

	c := newSamplerReqContext(t, http.MethodGet, "/api/dashboards")
	invokeHandler(handler, c)
	assert.NotEqual(t, http.StatusNoContent, c.Resp.Status())
}
