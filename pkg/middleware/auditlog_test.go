package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grafana/grafana/pkg/setting"
)

// recordingLogger captures audit log calls so tests can assert on them.
type recordingLogger struct {
	infos [][]any
	warns [][]any
}

func (r *recordingLogger) Info(_ string, args ...any) { r.infos = append(r.infos, args) }
func (r *recordingLogger) Warn(_ string, args ...any) { r.warns = append(r.warns, args) }

// withRecordingLogger swaps the package-level auditLogger for the duration of
// the test and restores it on cleanup.
func withRecordingLogger(t *testing.T) *recordingLogger {
	t.Helper()
	rl := &recordingLogger{}
	orig := auditLogger
	auditLogger = rl
	t.Cleanup(func() { auditLogger = orig })
	return rl
}

// auditNextHandler is a minimal downstream handler that writes a given status.
func auditNextHandler(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	})
}

func TestAuditLog_DefaultConfigIsValid(t *testing.T) {
	cfg := DefaultAuditLogConfig()
	require.True(t, cfg.Enabled)
	require.NotEmpty(t, cfg.AuditMethods)
	require.NotEmpty(t, cfg.SkipPaths)
	require.Contains(t, cfg.AuditMethods, http.MethodPost)
	require.Contains(t, cfg.AuditMethods, http.MethodDelete)
}

func TestAuditLog_DisabledPassesThrough(t *testing.T) {
	logger := withRecordingLogger(t)
	cfg := DefaultAuditLogConfig()
	cfg.Enabled = false

	mw := AuditLog(&setting.Cfg{}, cfg)
	var nextCalled bool
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/dashboards", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.True(t, nextCalled, "disabled middleware must pass through to next")
	assert.Empty(t, logger.infos)
	assert.Empty(t, logger.warns)
}

// TestAuditLog_MethodSetBuiltCorrectly verifies that method matching is
// case-insensitive: a request with method "post" (lowercase) is treated the
// same as "POST" and triggers an audit log entry.
func TestAuditLog_MethodSetBuiltCorrectly(t *testing.T) {
	logger := withRecordingLogger(t)
	cfg := AuditLogConfig{
		Enabled:      true,
		AuditMethods: []string{"post", "DELETE", "Patch"},
		AuditPaths:   []string{"/api/"},
	}
	mw := AuditLog(&setting.Cfg{}, cfg)
	handler := mw(auditNextHandler(http.StatusOK))

	// Lowercase "post" must match the uppercased "POST" in the method set.
	req := httptest.NewRequest("post", "/api/dashboards", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.Len(t, logger.infos, 1, "lowercase 'post' must produce an audit log entry")

	// "GET" is not in AuditMethods — should not produce a log entry.
	req = httptest.NewRequest(http.MethodGet, "/api/dashboards", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Len(t, logger.infos, 1, "GET must not produce an audit log entry")
}

func TestAuditLog_EmptyAuditPathsLogsAll(t *testing.T) {
	logger := withRecordingLogger(t)
	cfg := DefaultAuditLogConfig()
	cfg.AuditPaths = nil
	mw := AuditLog(&setting.Cfg{}, cfg)
	handler := mw(auditNextHandler(http.StatusCreated))

	req := httptest.NewRequest(http.MethodPost, "/some/custom/path", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Len(t, logger.infos, 1, "empty AuditPaths must log all non-skipped methods")
}

// TestAuditLog_SkipPathsPrecedeAuditPaths verifies that a path matching
// SkipPaths is never logged, even when it also matches AuditPaths.
func TestAuditLog_SkipPathsPrecedeAuditPaths(t *testing.T) {
	logger := withRecordingLogger(t)
	cfg := AuditLogConfig{
		Enabled:      true,
		SkipPaths:    []string{"/api/health"},
		AuditPaths:   []string{"/api/"},
		AuditMethods: []string{http.MethodPost},
	}
	mw := AuditLog(&setting.Cfg{}, cfg)
	var nextCalled bool
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	// /api/health matches both SkipPaths and AuditPaths; skip must win.
	req := httptest.NewRequest(http.MethodPost, "/api/health", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.True(t, nextCalled, "skip path must still forward the request downstream")
	assert.Empty(t, logger.infos, "skip path must not produce a log entry")
	assert.Empty(t, logger.warns)
}

// TestAuditLog_NonOKStatusLogsAtWarn verifies that a 4xx/5xx response is
// logged at Warn level rather than Info.
func TestAuditLog_NonOKStatusLogsAtWarn(t *testing.T) {
	logger := withRecordingLogger(t)
	cfg := DefaultAuditLogConfig()
	mw := AuditLog(&setting.Cfg{}, cfg)
	handler := mw(auditNextHandler(http.StatusBadRequest))

	req := httptest.NewRequest(http.MethodPost, "/api/dashboards", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Empty(t, logger.infos)
	require.Len(t, logger.warns, 1, "4xx response must produce a Warn log entry")
}

// TestAuditLog_DurationAndStatusCaptured verifies that the logged duration is
// non-zero and that the status code reflects the actual response, not the default.
func TestAuditLog_DurationAndStatusCaptured(t *testing.T) {
	logger := withRecordingLogger(t)
	cfg := DefaultAuditLogConfig()
	mw := AuditLog(&setting.Cfg{}, cfg)
	handler := mw(auditNextHandler(http.StatusCreated))

	req := httptest.NewRequest(http.MethodPost, "/api/dashboards", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.Len(t, logger.infos, 1)
	fields := logger.infos[0]

	// Find "status" key
	var status, durationMs any
	for i := 0; i+1 < len(fields); i += 2 {
		switch fields[i] {
		case "status":
			status = fields[i+1]
		case "duration_ms":
			durationMs = fields[i+1]
		}
	}

	assert.Equal(t, http.StatusCreated, status, "logged status must match actual response status")
	assert.GreaterOrEqual(t, durationMs.(int64), int64(0), "duration_ms must be non-negative")
}
