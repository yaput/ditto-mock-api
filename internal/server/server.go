package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ditto-mock/ditto-mock-api/internal/cache"
	"github.com/ditto-mock/ditto-mock-api/internal/config"
	"github.com/ditto-mock/ditto-mock-api/internal/generator"
	"github.com/ditto-mock/ditto-mock-api/internal/matcher"
	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

// ScanFunc is a callback the server can invoke to trigger a re-scan.
type ScanFunc func(ctx context.Context) ([]models.DependencyRegistry, error)

// Server is the Ditto HTTP server.
type Server struct {
	cfg        *config.Config
	httpServer *http.Server
	matcher    *matcher.Matcher
	cache      cache.Store
	generator  generator.Generator
	registries []models.DependencyRegistry
	scanFunc   ScanFunc
	logger     *slog.Logger
}

// Option configures the Server.
type Option func(*Server)

// WithScanFunc provides a re-scan callback.
func WithScanFunc(fn ScanFunc) Option {
	return func(s *Server) { s.scanFunc = fn }
}

// New creates a new Ditto server.
func New(
	cfg *config.Config,
	m *matcher.Matcher,
	c cache.Store,
	gen generator.Generator,
	registries []models.DependencyRegistry,
	logger *slog.Logger,
	opts ...Option,
) *Server {
	s := &Server{
		cfg:        cfg,
		matcher:    m,
		cache:      c,
		generator:  gen,
		registries: registries,
		logger:     logger,
	}
	for _, o := range opts {
		o(s)
	}

	mux := http.NewServeMux()

	// Admin routes under /_ditto
	mux.HandleFunc("GET /_ditto/health", s.handleHealth)
	mux.HandleFunc("GET /_ditto/registry", s.handleRegistryList)
	mux.HandleFunc("GET /_ditto/registry/", s.handleRegistryDetail)
	mux.HandleFunc("POST /_ditto/scan", s.handleScanAll)
	mux.HandleFunc("POST /_ditto/scan/", s.handleScanDependency)
	mux.HandleFunc("DELETE /_ditto/cache", s.handleCachePurgeAll)
	mux.HandleFunc("DELETE /_ditto/cache/", s.handleCachePurgeDep)
	mux.HandleFunc("GET /_ditto/cache/stats", s.handleCacheStats)
	mux.HandleFunc("GET /_ditto/config", s.handleConfig)

	// Catch-all for mock requests (SRV-2)
	mux.HandleFunc("/", s.handleMock)

	handler := corsMiddleware(requestLogging(logger, mux))

	s.httpServer = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s
}

// ListenAndServe starts the server.
func (s *Server) ListenAndServe() error {
	s.logger.Info("starting ditto server",
		"addr", s.httpServer.Addr,
		"dependencies", len(s.registries),
	)
	return s.httpServer.ListenAndServe()
}

// Serve starts the server on the given listener (useful for tests).
func (s *Server) Serve(ln net.Listener) error {
	s.logger.Info("starting ditto server",
		"addr", ln.Addr().String(),
		"dependencies", len(s.registries),
	)
	return s.httpServer.Serve(ln)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down ditto server")
	return s.httpServer.Shutdown(ctx)
}

// UpdateMatcher replaces the matcher after a re-scan.
func (s *Server) UpdateMatcher(m *matcher.Matcher, registries []models.DependencyRegistry) {
	s.matcher = m
	s.registries = registries
}
