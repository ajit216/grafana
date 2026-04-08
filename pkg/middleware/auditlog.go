package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/grafana/grafana/pkg/infra/log"
	contextmodel "github.com/grafana/grafana/pkg/services/contexthandler/model"
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

var auditLogger = log.New("audit")

// AuditLog returns a web.Handler that writes a structured audit log entry for
// each request that matches the configured methods and paths.
//
// The handler must be placed after the context handler so that user identity
// is available on the request context.
//
// Example:
//
//	router.Use(middleware.AuditLog(cfg, middleware.DefaultAuditLogConfig()))
func AuditLog(cfg *setting.Cfg, acfg AuditLogConfig) web.Handler {
	if !acfg.Enabled {
		return func(_ *contextmodel.ReqContext) {}
	}

	methodSet := make(map[string]struct{}, len(acfg.AuditMethods))
	for _, m := range acfg.AuditMethods {
		methodSet[strings.ToUpper(m)] = struct{}{}
	}

	return func(c *contextmodel.ReqContext) {
		path := c.Req.URL.Path
		method := strings.ToUpper(c.Req.Method)

		// Skip paths take priority over audit paths
		for _, skip := range acfg.SkipPaths {
			if strings.HasPrefix(path, skip) {
				return
			}
		}

		// Check if method is auditable
		if _, ok := methodSet[method]; !ok {
			return
		}

		// Check if path falls under an audited prefix
		audited := len(acfg.AuditPaths) == 0
		for _, ap := range acfg.AuditPaths {
			if strings.HasPrefix(path, ap) {
				audited = true
				break
			}
		}
		if !audited {
			return
		}

		start := time.Now()
		// Response is written by the next handler in chain; we log after.
		// Note: there is no explicit hook here — duration only includes
		// time up to when the audit log entry is written, not full response time.
		duration := time.Since(start)

		userID := int64(0)
		orgID := int64(0)
		userLogin := ""
		if c.SignedInUser != nil {
			userID = c.SignedInUser.UserID
			orgID = c.SignedInUser.OrgID
			userLogin = c.SignedInUser.Login
		}

		status := c.Resp.Status()
		fields := []any{
			"method", method,
			"path", path,
			"status", status,
			"duration_ms", duration.Milliseconds(),
			"remote_addr", c.RemoteAddr(),
			"user_id", userID,
			"org_id", orgID,
			"user_login", userLogin,
		}

		if c.Req.URL.RawQuery != "" {
			fields = append(fields, "query", c.Req.URL.RawQuery)
		}

		// Log at warn level for non-2xx responses so they stand out
		if status >= http.StatusBadRequest {
			auditLogger.Warn("audit", fields...)
		} else {
			auditLogger.Info("audit", fields...)
		}
	}
}
