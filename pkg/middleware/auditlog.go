package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/grafana/grafana/pkg/infra/log"
	"github.com/grafana/grafana/pkg/services/contexthandler"
	"github.com/grafana/grafana/pkg/setting"
	"github.com/grafana/grafana/pkg/web"
)

// AuditLogConfig holds configuration for the audit log middleware.
type AuditLogConfig struct {
	// Enabled controls whether audit logging is active.
	Enabled bool

	// SkipPaths is a list of URL path prefixes that should not be audit-logged.
	// Checked before AuditPaths — if a path matches here, it is skipped
	// regardless of AuditPaths.
	SkipPaths []string

	// AuditPaths is a list of URL path prefixes that must be audit-logged.
	// If empty, all non-skipped write methods are logged.
	AuditPaths []string

	// AuditMethods is the set of HTTP methods considered auditable.
	// Defaults to POST, PUT, PATCH, DELETE.
	AuditMethods []string
}

// DefaultAuditLogConfig returns a sensible default configuration that logs
// all mutating API operations except internal health/metrics paths.
func DefaultAuditLogConfig() AuditLogConfig {
	return AuditLogConfig{
		Enabled: true,
		SkipPaths: []string{
			"/api/health",
			"/metrics",
			"/api/live",
		},
		AuditPaths: []string{
			"/api/",
		},
		AuditMethods: []string{
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
		},
	}
}

// auditLogWriter is the logging interface used by AuditLog. It is a package-level
// var so tests can replace it with a recording implementation.
type auditLogWriter interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

var auditLogger auditLogWriter = log.New("audit")

// AuditLog returns a web.Middleware that writes a structured audit log entry for
// each request that matches the configured methods and paths.
//
// The middleware must be placed after the context handler so that user identity
// is available on the request context.
//
// Example:
//
//	router.Use(middleware.AuditLog(cfg, middleware.DefaultAuditLogConfig()))
func AuditLog(_ *setting.Cfg, acfg AuditLogConfig) web.Middleware {
	if !acfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}

	methodSet := make(map[string]struct{}, len(acfg.AuditMethods))
	for _, m := range acfg.AuditMethods {
		methodSet[strings.ToUpper(m)] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			method := strings.ToUpper(r.Method)

			// Skip paths take priority over audit paths.
			for _, skip := range acfg.SkipPaths {
				if strings.HasPrefix(path, skip) {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Pass through non-auditable methods without logging.
			if _, ok := methodSet[method]; !ok {
				next.ServeHTTP(w, r)
				return
			}

			// Pass through paths outside audited prefixes.
			audited := len(acfg.AuditPaths) == 0
			for _, ap := range acfg.AuditPaths {
				if strings.HasPrefix(path, ap) {
					audited = true
					break
				}
			}
			if !audited {
				next.ServeHTTP(w, r)
				return
			}

			// Wrap the writer so we can read status after downstream runs.
			rw := web.Rw(w, r)
			start := time.Now()
			next.ServeHTTP(rw, r)
			duration := time.Since(start)

			status := rw.Status()
			if status == 0 {
				status = http.StatusOK
			}

			var userID, orgID int64
			var userLogin string
			if reqCtx := contexthandler.FromContext(r.Context()); reqCtx != nil && reqCtx.SignedInUser != nil {
				userID = reqCtx.SignedInUser.UserID
				orgID = reqCtx.SignedInUser.OrgID
				userLogin = reqCtx.SignedInUser.Login
			}

			fields := []any{
				"method", method,
				"path", path,
				"status", status,
				"duration_ms", duration.Milliseconds(),
				"remote_addr", r.RemoteAddr,
				"user_id", userID,
				"org_id", orgID,
				"user_login", userLogin,
			}

			if r.URL.RawQuery != "" {
				fields = append(fields, "query", r.URL.RawQuery)
			}

			// Log at warn level for non-2xx responses so they stand out.
			if status >= http.StatusBadRequest {
				auditLogger.Warn("audit", fields...)
			} else {
				auditLogger.Info("audit", fields...)
			}
		})
	}
}
