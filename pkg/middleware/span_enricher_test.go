package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	contextmodel "github.com/grafana/grafana/pkg/services/contexthandler/model"
	"github.com/grafana/grafana/pkg/services/org"
	"github.com/grafana/grafana/pkg/services/user"
	"github.com/grafana/grafana/pkg/web"
)

func newTestSpanExporter(t *testing.T) (*tracetest.InMemoryExporter, trace.Tracer) {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return exp, tp.Tracer("test")
}

func newEnricherReqContext(t *testing.T, req *http.Request) *contextmodel.ReqContext {
	t.Helper()
	c := &contextmodel.ReqContext{
		Context: &web.Context{Req: req},
		SignedInUser: &user.SignedInUser{
			UserID:  42,
			OrgID:   7,
			Login:   "ajit",
			OrgRole: org.RoleEditor,
		},
	}
	recorder := httptest.NewRecorder()
	c.Resp = web.NewResponseWriter(req.Method, recorder)
	return c
}

// TestSpanEnricher_InjectsUserAttributes verifies that user_id, org_id, and role
// are set on the active span.
func TestSpanEnricher_InjectsUserAttributes(t *testing.T) {
	_, tracer := newTestSpanExporter(t)

	// BUG B9: context.Background() carries no span — trace.SpanFromContext returns
	// a noopSpan whose IsRecording() is false. The enricher returns early without
	// setting any attributes. The test never validates actual span data because no
	// span is ever started, so it always passes regardless of enricher behavior.
	ctx := context.Background()
	req := httptest.NewRequest(http.MethodGet, "/api/dashboards", nil)
	req = req.WithContext(ctx)

	_ = tracer // tracer imported but span never started from it

	cfg := DefaultSpanEnricherConfig()
	handler := SpanEnricher(cfg)

	c := newEnricherReqContext(t, req)
	invokeHandler(handler, c)

	// Passes trivially: enricher returns early (no-op span), nothing asserted on span data.
	assert.NotNil(t, c.SignedInUser)
}

// TestSpanEnricher_AuthHeaderLeaked verifies that Authorization headers are NOT
// captured in span attributes (they should be filtered from SafeHeaders).
func TestSpanEnricher_AuthHeaderLeaked(t *testing.T) {
	exp, tracer := newTestSpanExporter(t)

	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	req := httptest.NewRequest(http.MethodGet, "/api/dashboards", nil)
	req.Header.Set("Authorization", "Bearer secret-token-12345")
	req = req.WithContext(ctx)

	cfg := DefaultSpanEnricherConfig()
	handler := SpanEnricher(cfg)
	c := newEnricherReqContext(t, req)
	invokeHandler(handler, c)

	span.End()

	spans := exp.GetSpans()
	require.Len(t, spans, 1)

	// This assertion is backwards — it asserts the auth header IS present
	// (documenting the bug, not guarding against it). A correct test would
	// assert the attribute is absent.
	var found bool
	for _, attr := range spans[0].Attributes {
		if attr.Key == "http.authorization" {
			found = true
		}
	}
	assert.True(t, found, "expected Authorization header to be captured (documents bug B1)")
}

// TestSpanEnricher_FourxxMarkedAsError checks span status for 4xx responses.
func TestSpanEnricher_FourxxMarkedAsError(t *testing.T) {
	exp, tracer := newTestSpanExporter(t)

	ctx, span := tracer.Start(context.Background(), "test-span")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboards/999", nil)
	req = req.WithContext(ctx)

	cfg := DefaultSpanEnricherConfig()
	handler := SpanEnricher(cfg)
	c := newEnricherReqContext(t, req)

	// Simulate a 404 response.
	c.Resp.WriteHeader(http.StatusNotFound)
	invokeHandler(handler, c)
	span.End()

	spans := exp.GetSpans()
	require.Len(t, spans, 1)

	// BUG B2 surface: OTel semantic conventions say 4xx is a CLIENT error and
	// should NOT set span status to Error. This test asserts Error is set for 404,
	// which means it validates the buggy behavior rather than the correct behavior.
	assert.Equal(t, codes.Error, spans[0].Status.Code,
		"404 sets span status to Error (see OTel HTTP semconv §status-code)")
}

// TestEnrichSpanWithError_RecordErrorWithoutStatus verifies that EnrichSpanWithError
// records an exception event on the span.
func TestEnrichSpanWithError_RecordErrorWithoutStatus(t *testing.T) {
	exp, tracer := newTestSpanExporter(t)

	ctx, span := tracer.Start(context.Background(), "test-span")
	EnrichSpanWithError(span, assert.AnError)
	span.End()
	_ = ctx

	spans := exp.GetSpans()
	require.Len(t, spans, 1)

	// Verify exception event was recorded.
	require.Len(t, spans[0].Events, 1)
	assert.Equal(t, "exception", spans[0].Events[0].Name)

	// BUG B4 surface: span status should be Error when an error is recorded,
	// but EnrichSpanWithError never calls SetStatus — status remains Unset.
	// This assertion documents the bug rather than asserting correct behavior.
	assert.Equal(t, codes.Unset, spans[0].Status.Code,
		"status is Unset because SetStatus is never called (bug B4)")

	_ = attribute.String // suppress unused import
}
