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

// TestSpanEnricher_InjectsUserAttributes verifies that user_id, org_id, login, and role
// are set on the active span.
func TestSpanEnricher_InjectsUserAttributes(t *testing.T) {
	exp, tracer := newTestSpanExporter(t)

	ctx, span := tracer.Start(context.Background(), "test-span")
	req := httptest.NewRequest(http.MethodGet, "/api/dashboards", nil)
	req = req.WithContext(ctx)

	cfg := DefaultSpanEnricherConfig()
	handler := SpanEnricher(cfg)

	c := newEnricherReqContext(t, req)
	invokeHandler(handler, c)
	span.End()

	spans := exp.GetSpans()
	require.Len(t, spans, 1)

	attrMap := make(map[attribute.Key]attribute.Value)
	for _, a := range spans[0].Attributes {
		attrMap[a.Key] = a.Value
	}

	assert.Equal(t, int64(42), attrMap["grafana.user_id"].AsInt64())
	assert.Equal(t, int64(7), attrMap["grafana.org_id"].AsInt64())
	assert.Equal(t, "ajit", attrMap["grafana.user_login"].AsString())
	assert.Equal(t, "Editor", attrMap["grafana.user_role"].AsString())
}

// TestSpanEnricher_AuthHeaderNotLeaked verifies that the Authorization header is
// never captured as a span attribute (credential leak prevention).
func TestSpanEnricher_AuthHeaderNotLeaked(t *testing.T) {
	exp, tracer := newTestSpanExporter(t)

	ctx, span := tracer.Start(context.Background(), "test-span")

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

	for _, attr := range spans[0].Attributes {
		assert.NotEqual(t, attribute.Key("http.authorization"), attr.Key,
			"Authorization header must never be captured in spans")
	}
}

// TestSpanEnricher_FourxxNotMarkedAsError verifies 4xx responses do NOT set span
// status to Error per OTel HTTP semconv v1.20+ (only 5xx are server errors).
func TestSpanEnricher_FourxxNotMarkedAsError(t *testing.T) {
	exp, tracer := newTestSpanExporter(t)

	ctx, span := tracer.Start(context.Background(), "test-span")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboards/999", nil)
	req = req.WithContext(ctx)

	cfg := DefaultSpanEnricherConfig()
	handler := SpanEnricher(cfg)
	c := newEnricherReqContext(t, req)

	c.Resp.WriteHeader(http.StatusNotFound)
	invokeHandler(handler, c)
	span.End()

	spans := exp.GetSpans()
	require.Len(t, spans, 1)

	assert.NotEqual(t, codes.Error, spans[0].Status.Code,
		"404 is a client error and must not set span status to Error")
}

// TestSpanEnricher_FivexxMarkedAsError verifies 5xx responses set span status to Error.
func TestSpanEnricher_FivexxMarkedAsError(t *testing.T) {
	exp, tracer := newTestSpanExporter(t)

	ctx, span := tracer.Start(context.Background(), "test-span")

	req := httptest.NewRequest(http.MethodGet, "/api/dashboards", nil)
	req = req.WithContext(ctx)

	cfg := DefaultSpanEnricherConfig()
	handler := SpanEnricher(cfg)
	c := newEnricherReqContext(t, req)

	c.Resp.WriteHeader(http.StatusInternalServerError)
	invokeHandler(handler, c)
	span.End()

	spans := exp.GetSpans()
	require.Len(t, spans, 1)

	assert.Equal(t, codes.Error, spans[0].Status.Code,
		"500 is a server error and must set span status to Error")
}

// TestEnrichSpanWithError records an exception event and sets span status to Error.
func TestEnrichSpanWithError(t *testing.T) {
	exp, tracer := newTestSpanExporter(t)

	ctx, span := tracer.Start(context.Background(), "test-span")
	EnrichSpanWithError(span, assert.AnError)
	span.End()
	_ = ctx

	spans := exp.GetSpans()
	require.Len(t, spans, 1)

	require.Len(t, spans[0].Events, 1)
	assert.Equal(t, "exception", spans[0].Events[0].Name)
	assert.Equal(t, codes.Error, spans[0].Status.Code)
}
