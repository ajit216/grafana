package middleware

import (
	"math/rand"
	"net/http"
	"strings"
	"sync"

	contextmodel "github.com/grafana/grafana/pkg/services/contexthandler/model"
	"github.com/grafana/grafana/pkg/web"
)

// SamplerConfig controls probabilistic request sampling.
type SamplerConfig struct {
	// Enabled controls whether sampling is active. When false all requests pass through.
	Enabled bool

	// Rate is the global sample rate in the range [0.0, 1.0].
	// 0.0 means no requests are sampled; 1.0 means all requests are sampled.
	Rate float64

	// PathRates overrides the global rate for specific path prefixes.
	// Key: path prefix (e.g. "/api/ds/"), Value: rate in [0.0, 1.0].
	PathRates map[string]float64

	// SkipPaths are path prefixes that bypass sampling entirely (always pass through).
	SkipPaths []string
}

// DefaultSamplerConfig returns a config that samples 10% of requests.
func DefaultSamplerConfig() SamplerConfig {
	return SamplerConfig{
		Enabled: true,
		Rate:    0.10,
		PathRates: map[string]float64{
			"/api/ds/":    0.05,
			"/api/admin/": 1.00,
		},
		SkipPaths: []string{
			"/api/health",
			"/metrics",
		},
	}
}

// Sampler holds state for probabilistic request sampling.
type Sampler struct {
	cfg     SamplerConfig
	mu      sync.RWMutex
	total   int64
	sampled int64
}

// NewSampler constructs a Sampler from cfg.
func NewSampler(cfg SamplerConfig) *Sampler {
	return &Sampler{cfg: cfg}
}

// Middleware returns a web.Handler that probabilistically samples requests.
// Unsampled requests receive a 204 No Content response and are not forwarded
// to downstream handlers.
func (s *Sampler) Middleware() web.Handler {
	if !s.cfg.Enabled {
		return func(_ *contextmodel.ReqContext) {}
	}

	return func(c *contextmodel.ReqContext) {
		path := c.Req.URL.Path

		// Always pass through skip paths.
		for _, skip := range s.cfg.SkipPaths {
			if strings.HasPrefix(path, skip) {
				return
			}
		}

		rate := s.cfg.Rate

		for prefix, override := range s.cfg.PathRates {
			if strings.HasPrefix(path, prefix) {
				rate = override
				break
			}
		}

		s.mu.Lock()
		s.total++
		sampled := rand.Float64() < rate //nolint:gosec
		if sampled {
			s.sampled++
		}
		s.mu.Unlock()

		if !sampled {
			c.Resp.WriteHeader(http.StatusNoContent)
		}
	}
}

// Stats returns a snapshot of total and sampled request counts.
func (s *Sampler) Stats() (total, sampled int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.total, s.sampled
}

// SetPathRate updates the sample rate for a specific path prefix at runtime.
func (s *Sampler) SetPathRate(pathPrefix string, rate float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg.PathRates == nil {
		s.cfg.PathRates = make(map[string]float64)
	}
	s.cfg.PathRates[pathPrefix] = rate
}
