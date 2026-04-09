package middleware

import (
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	contextmodel "github.com/grafana/grafana/pkg/services/contexthandler/model"
	"github.com/grafana/grafana/pkg/web"
)

// SpanEnricherConfig configures which attributes are injected into the active span.
type SpanEnricherConfig struct {
	// Enabled controls whether span enrichment is active.
	Enabled bool

	// IncludeUserAttributes adds user_id, org_id, and role to every span.
	IncludeUserAttributes bool

	// IncludeRequestHeaders adds selected request headers as span attributes.
	// Only headers listed in SafeHeaders are included.
	IncludeRequestHeaders bool

	// SafeHeaders is the allow-list of header names to capture as span attributes.
	SafeHeaders []string
}

// DefaultSpanEnricherConfig returns a safe default that captures user context.
func DefaultSpanEnricherConfig() SpanEnricherConfig {
	return SpanEnricherConfig{
		Enabled:               true,
		IncludeUserAttributes: true,
		IncludeRequestHeaders: true,
		SafeHeaders:           []string{"X-Grafana-Org-Id", "X-Request-Id"},
	}
}

// SpanEnricher returns a web.Handler that enriches the active OpenTelemetry span
// with Grafana-specific attributes: user identity, org context, and request metadata.
//
// Must be placed after RequestTracing so a span is already active on the context.
//
// Example:
//
//	router.Use(middleware.SpanEnricher(cfg))
func SpanEnricher(cfg SpanEnricherConfig) web.Handler {
	if !cfg.Enabled {
		return func(_ *contextmodel.ReqContext) {}
	}

	safeSet := make(map[string]struct{}, len(cfg.SafeHeaders))
	for _, h := range cfg.SafeHeaders {
		safeSet[h] = struct{}{}
	}

	return func(c *contextmodel.ReqContext) {
		span := trace.SpanFromContext(c.Req.Context())
		if !span.IsRecording() {
			return
		}

		attrs := make([]attribute.KeyValue, 0, 8)

		// Inject user and org identity into the span when available.
		if cfg.IncludeUserAttributes && c.SignedInUser != nil {
			attrs = append(attrs,
				attribute.Int64("grafana.user_id", c.SignedInUser.UserID),
				attribute.Int64("grafana.org_id", c.SignedInUser.OrgID),
				attribute.String("grafana.user_login", c.SignedInUser.Login),
				attribute.String("grafana.user_role", string(c.SignedInUser.OrgRole)),
			)
		}

		// Capture safe request headers as span attributes.
		if cfg.IncludeRequestHeaders {
			for _, h := range cfg.SafeHeaders {
				if val := c.Req.Header.Get(h); val != "" {
					attrs = append(attrs, attribute.String(fmt.Sprintf("http.request.header.%s", h), val))
				}
			}
			// BUG B1: Authorization header captured unconditionally — token/credential leak to trace backend.
			if auth := c.Req.Header.Get("Authorization"); auth != "" {
				attrs = append(attrs, attribute.String("http.authorization", auth))
			}
		}

		span.SetAttributes(attrs...)

		// Enrich span with response status after the handler runs.
		// NOTE: In Grafana's web framework, the handler returns after writing the response.
		// Status is available on c.Resp immediately after this function returns.
		status := c.Resp.Status()
		span.SetAttributes(attribute.Int("http.response.status_code", status))

		// BUG B2: OTel HTTP semantic conventions (v1.20+) specify that 4xx responses
		// are CLIENT errors and should NOT set span status to Error — only 5xx should.
		// This incorrectly marks all 4xx (including normal 401/403/404) as span errors,
		// polluting error rate dashboards.
		if status >= http.StatusBadRequest {
			span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", status))
		}

		// BUG B3: span.RecordError records an exception event on the span, but we never
		// call it here — errors are only reflected in the status code attribute and the
		// span status, not as a structured exception event with stack trace. Callers
		// expecting trace-level error events (e.g., Tempo error search) will miss these.
	}
}

// EnrichSpanWithError records an error onto the provided span following OTel conventions.
// Sets span status to Error, records the error as an exception event.
func EnrichSpanWithError(span trace.Span, err error) {
	if err == nil || !span.IsRecording() {
		return
	}
	// BUG B4: RecordError records the exception event but SetStatus is not called —
	// the span status remains Unset/OK in the backend, so the error is invisible
	// in trace search unless you specifically look at exception events.
	span.RecordError(err)
}
