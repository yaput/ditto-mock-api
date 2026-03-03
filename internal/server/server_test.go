package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ditto-mock/ditto-mock-api/internal/config"
	"github.com/ditto-mock/ditto-mock-api/internal/generator"
	"github.com/ditto-mock/ditto-mock-api/internal/matcher"
	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

// ---- Mocks ----

type mockCache struct {
	store  map[string]*models.CachedResponse
	putErr error
	purged int64
	stats  *models.CacheStats
}

func newMockCache() *mockCache {
	return &mockCache{store: make(map[string]*models.CachedResponse)}
}

func (c *mockCache) Get(key string) (*models.CachedResponse, error) {
	entry, ok := c.store[key]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return entry, nil
}

func (c *mockCache) Put(entry *models.CachedResponse) error {
	if c.putErr != nil {
		return c.putErr
	}
	c.store[entry.KeyHash] = entry
	return nil
}

func (c *mockCache) Delete(key string) error {
	delete(c.store, key)
	return nil
}

func (c *mockCache) Purge(_ string) (int64, error) { return c.purged, nil }

func (c *mockCache) Stats() (*models.CacheStats, error) {
	if c.stats != nil {
		return c.stats, nil
	}
	return &models.CacheStats{TotalEntries: len(c.store)}, nil
}

func (c *mockCache) Close() error { return nil }

type mockGenerator struct {
	response *generator.MockResponse
	err      error
}

func (g *mockGenerator) Generate(_ context.Context, _ models.Endpoint, _ generator.RequestContext) (*generator.MockResponse, error) {
	return g.response, g.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{Host: "127.0.0.1", Port: 0},
		LLM:    config.LLMConfig{APIKey: "sk-test-secret-key"},
		Dependencies: []config.DependencyConfig{
			{Name: "user-service", Prefix: "/users"},
		},
	}
}

func testRegistries() []models.DependencyRegistry {
	return []models.DependencyRegistry{
		{
			Dependency:        "user-service",
			FrameworkDetected: "chi",
			Endpoints: []models.Endpoint{
				{Method: "GET", Path: "/users/{id}", StatusCode: 200, Description: "Get user"},
				{Method: "POST", Path: "/users", StatusCode: 201, Description: "Create user"},
			},
		},
	}
}

func testServer(gen *mockGenerator, c *mockCache) *Server {
	cfg := testConfig()
	registries := testRegistries()
	prefixes := map[string]string{"user-service": "/api/users"}
	m := matcher.New(registries, prefixes)
	return New(cfg, m, c, gen, registries, testLogger())
}

// ---- Health ----

func TestHealth(t *testing.T) {
	srv := testServer(&mockGenerator{}, newMockCache())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/_ditto/health", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body map[string]string
	json.NewDecoder(rec.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("expected ok, got %s", body["status"])
	}
}

// ---- Registry ----

func TestRegistryList(t *testing.T) {
	srv := testServer(&mockGenerator{}, newMockCache())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/_ditto/registry", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var list []map[string]any
	json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 1 {
		t.Fatalf("expected 1 registry, got %d", len(list))
	}
	if list[0]["name"] != "user-service" {
		t.Errorf("expected user-service, got %v", list[0]["name"])
	}
}

func TestRegistryDetail(t *testing.T) {
	srv := testServer(&mockGenerator{}, newMockCache())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/_ditto/registry/user-service", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRegistryDetail_NotFound(t *testing.T) {
	srv := testServer(&mockGenerator{}, newMockCache())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/_ditto/registry/unknown", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ---- Cache Admin ----

func TestCachePurgeAll(t *testing.T) {
	mc := newMockCache()
	mc.purged = 5
	srv := testServer(&mockGenerator{}, mc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/_ditto/cache", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "purged") {
		t.Errorf("expected purged in body: %s", body)
	}
}

func TestCacheStats(t *testing.T) {
	mc := newMockCache()
	mc.stats = &models.CacheStats{TotalEntries: 42, HitCount: 100, MissCount: 10}
	srv := testServer(&mockGenerator{}, mc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/_ditto/cache/stats", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var stats models.CacheStats
	json.NewDecoder(rec.Body).Decode(&stats)
	if stats.TotalEntries != 42 {
		t.Errorf("expected 42 entries, got %d", stats.TotalEntries)
	}
}

// ---- Config ----

func TestConfigRedacted(t *testing.T) {
	srv := testServer(&mockGenerator{}, newMockCache())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/_ditto/config", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "sk-test-secret-key") {
		t.Error("API key should be redacted")
	}
	if !strings.Contains(body, "REDACTED") {
		t.Error("expected REDACTED placeholder")
	}
}

// ---- Mock Handler ----

func TestMockHandler_Success(t *testing.T) {
	gen := &mockGenerator{
		response: &generator.MockResponse{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       `{"id":"123","name":"Alice"}`,
		},
	}
	srv := testServer(gen, newMockCache())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/users/users/123", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != `{"id":"123","name":"Alice"}` {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestMockHandler_CacheHit(t *testing.T) {
	gen := &mockGenerator{
		response: &generator.MockResponse{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       `{"fresh":true}`,
		},
	}
	mc := newMockCache()
	srv := testServer(gen, mc)

	// First request: cache miss, generate and store.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/api/users/users/123", nil)
	srv.httpServer.Handler.ServeHTTP(rec1, req1)
	if rec1.Code != 200 {
		t.Fatalf("first request: expected 200, got %d", rec1.Code)
	}

	// Second request: should hit cache.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/users/users/123", nil)
	srv.httpServer.Handler.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("second request: expected 200, got %d", rec2.Code)
	}
	if rec2.Body.String() != `{"fresh":true}` {
		t.Errorf("expected cached body, got: %s", rec2.Body.String())
	}
}

func TestMockHandler_NoMatch(t *testing.T) {
	srv := testServer(&mockGenerator{}, newMockCache())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/unknown/path", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 501 {
		t.Fatalf("expected 501, got %d", rec.Code)
	}
}

func TestMockHandler_GenerationError(t *testing.T) {
	gen := &mockGenerator{err: fmt.Errorf("LLM timeout")}
	srv := testServer(gen, newMockCache())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/users/users/123", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 502 {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}

// ---- CORS ----

func TestCORSPreflight(t *testing.T) {
	srv := testServer(&mockGenerator{}, newMockCache())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/api/users/users/123", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 204 {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS Allow-Origin header")
	}
}

func TestCORSHeaders(t *testing.T) {
	gen := &mockGenerator{
		response: &generator.MockResponse{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       `{}`,
		},
	}
	srv := testServer(gen, newMockCache())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/users/users/123", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS Allow-Origin on regular requests")
	}
}

// ---- Scan Admin ----

func TestScanAll_NotConfigured(t *testing.T) {
	srv := testServer(&mockGenerator{}, newMockCache())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/_ditto/scan", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 501 {
		t.Fatalf("expected 501, got %d", rec.Code)
	}
}

func TestScanAll_Success(t *testing.T) {
	scanCalled := false
	scanFn := func(_ context.Context) ([]models.DependencyRegistry, error) {
		scanCalled = true
		return testRegistries(), nil
	}
	cfg := testConfig()
	registries := testRegistries()
	prefixes := map[string]string{"user-service": "/api/users"}
	m := matcher.New(registries, prefixes)
	srv := New(cfg, m, newMockCache(), &mockGenerator{}, registries, testLogger(), WithScanFunc(scanFn))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/_ditto/scan", nil)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !scanCalled {
		t.Error("scan function was not called")
	}
}

// ---- Middleware ----

func TestStatusRecorder(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
	sr.WriteHeader(http.StatusCreated)
	if sr.status != http.StatusCreated {
		t.Errorf("expected 201, got %d", sr.status)
	}
}

// ---- Helpers ----

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, 201, map[string]string{"ok": "yes"})
	if rec.Code != 201 {
		t.Errorf("expected 201, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Error("expected Content-Type: application/json")
	}
}

func TestHashBody(t *testing.T) {
	if hashBody(nil) != "" {
		t.Error("nil body should return empty hash")
	}
	if hashBody([]byte("test")) == "" {
		t.Error("non-empty body should return hash")
	}
}

func TestQueryToMap(t *testing.T) {
	q := map[string][]string{
		"a": {"1"},
		"b": {"2", "3"},
	}
	m := queryToMap(q)
	if m["a"] != "1" {
		t.Errorf("expected 1, got %s", m["a"])
	}
	if m["b"] != "2,3" {
		t.Errorf("expected 2,3, got %s", m["b"])
	}
}

func TestMockHandler_WithBody(t *testing.T) {
	gen := &mockGenerator{
		response: &generator.MockResponse{
			StatusCode: 201,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       `{"id":"new"}`,
		},
	}
	srv := testServer(gen, newMockCache())
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"Bob"}`)
	req := httptest.NewRequest("POST", "/api/users/users", body)
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	respBody, _ := io.ReadAll(rec.Body)
	if string(respBody) != `{"id":"new"}` {
		t.Errorf("unexpected body: %s", string(respBody))
	}
}
