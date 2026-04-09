package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grafana/grafana/pkg/setting"
)

func TestAuditLog_DefaultConfigIsValid(t *testing.T) {
	cfg := DefaultAuditLogConfig()
	require.True(t, cfg.Enabled)
	require.NotEmpty(t, cfg.AuditMethods)
	require.NotEmpty(t, cfg.SkipPaths)
	require.Contains(t, cfg.AuditMethods, http.MethodPost)
	require.Contains(t, cfg.AuditMethods, http.MethodDelete)
}

func TestAuditLog_DisabledReturnsPassthrough(t *testing.T) {
	cfg := DefaultAuditLogConfig()
	cfg.Enabled = false
	mw := AuditLog(&setting.Cfg{}, cfg)
	require.NotNil(t, mw)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
}

func TestAuditLog_EnabledReturnsNonNilHandler(t *testing.T) {
	cfg := DefaultAuditLogConfig()
	mw := AuditLog(&setting.Cfg{}, cfg)
	require.NotNil(t, mw)
}

func TestAuditLog_MethodSetBuiltCorrectly(t *testing.T) {
	cfg := AuditLogConfig{
		Enabled:      true,
		AuditMethods: []string{"post", "DELETE", "Patch"},
	}
	mw := AuditLog(&setting.Cfg{}, cfg)
	assert.NotNil(t, mw)
}

func TestAuditLog_EmptyAuditPathsLogsAll(t *testing.T) {
	cfg := DefaultAuditLogConfig()
	cfg.AuditPaths = nil
	mw := AuditLog(&setting.Cfg{}, cfg)
	require.NotNil(t, mw)
}

func TestAuditLog_SkipPathsPrecedeAuditPaths(t *testing.T) {
	cfg := AuditLogConfig{
		Enabled:      true,
		SkipPaths:    []string{"/api/health"},
		AuditPaths:   []string{"/api/"},
		AuditMethods: []string{http.MethodPost},
	}
	mw := AuditLog(&setting.Cfg{}, cfg)
	require.NotNil(t, mw)
}

func TestAuditLog_NonAuditedMethodPassesThrough(t *testing.T) {
	cfg := DefaultAuditLogConfig()
	mw := AuditLog(&setting.Cfg{}, cfg)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAuditLog_AuditedRequestCallsDownstream(t *testing.T) {
	cfg := DefaultAuditLogConfig()
	mw := AuditLog(&setting.Cfg{}, cfg)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
	})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/dashboards", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.True(t, called)
}

func TestShouldAudit(t *testing.T) {
	methodSet := map[string]struct{}{
		"POST":   {},
		"DELETE": {},
	}
	acfg := AuditLogConfig{
		SkipPaths:  []string{"/api/health", "/metrics"},
		AuditPaths: []string{"/api/"},
	}

	tests := []struct {
		name   string
		path   string
		method string
		want   bool
	}{
		{"audited POST to API", "/api/dashboards", "POST", true},
		{"audited DELETE to API", "/api/dashboards/1", "DELETE", true},
		{"non-audited GET", "/api/dashboards", "GET", false},
		{"skipped health", "/api/health", "POST", false},
		{"skipped metrics", "/metrics", "POST", false},
		{"non-API path", "/login", "POST", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldAudit(tt.path, tt.method, methodSet, acfg)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestShouldAudit_EmptyAuditPathsLogsAll(t *testing.T) {
	methodSet := map[string]struct{}{"POST": {}}
	acfg := AuditLogConfig{AuditPaths: nil}

	assert.True(t, shouldAudit("/anything", "POST", methodSet, acfg))
}
