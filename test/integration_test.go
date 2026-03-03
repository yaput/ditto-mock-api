package test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ditto-mock/ditto-mock-api/internal/cache"
	"github.com/ditto-mock/ditto-mock-api/internal/config"
	"github.com/ditto-mock/ditto-mock-api/internal/generator"
	"github.com/ditto-mock/ditto-mock-api/internal/matcher"
	"github.com/ditto-mock/ditto-mock-api/internal/models"
	"github.com/ditto-mock/ditto-mock-api/internal/scanner"
	"github.com/ditto-mock/ditto-mock-api/internal/server"

	_ "modernc.org/sqlite" // SQLite driver registration
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// fakeLLMClient implements scanner.LLMClient for testing.
type fakeLLMClient struct{}

func (f *fakeLLMClient) ChatCompletion(_ context.Context, _, _ string) (string, error) {
	endpoints := []models.Endpoint{
		{
			Method:      "GET",
			Path:        "/users",
			Description: "List all users",
			StatusCode:  200,
			ResponseBody: &models.BodySchema{
				Type: "array",
				Fields: []models.FieldSchema{
					{Name: "ID", Type: "string", JSONKey: "id"},
					{Name: "Name", Type: "string", JSONKey: "name"},
					{Name: "Email", Type: "string", JSONKey: "email"},
				},
			},
		},
		{
			Method:      "GET",
			Path:        "/users/{id}",
			Description: "Get a user by ID",
			StatusCode:  200,
			ResponseBody: &models.BodySchema{
				Type: "object",
				Fields: []models.FieldSchema{
					{Name: "ID", Type: "string", JSONKey: "id"},
					{Name: "Name", Type: "string", JSONKey: "name"},
					{Name: "Email", Type: "string", JSONKey: "email"},
					{Name: "Role", Type: "string", JSONKey: "role"},
					{Name: "Active", Type: "bool", JSONKey: "active"},
				},
			},
		},
		{
			Method:      "POST",
			Path:        "/users",
			Description: "Create a new user",
			StatusCode:  201,
			RequestBody: &models.BodySchema{
				Type: "object",
				Fields: []models.FieldSchema{
					{Name: "Name", Type: "string", JSONKey: "name", Required: true},
					{Name: "Email", Type: "string", JSONKey: "email", Required: true},
					{Name: "Role", Type: "string", JSONKey: "role"},
				},
			},
			ResponseBody: &models.BodySchema{
				Type: "object",
				Fields: []models.FieldSchema{
					{Name: "ID", Type: "string", JSONKey: "id"},
					{Name: "Name", Type: "string", JSONKey: "name"},
					{Name: "Email", Type: "string", JSONKey: "email"},
				},
			},
		},
		{Method: "PUT", Path: "/users/{id}", Description: "Update a user", StatusCode: 200},
		{Method: "DELETE", Path: "/users/{id}", Description: "Delete a user", StatusCode: 204},
		{Method: "GET", Path: "/teams", Description: "List all teams", StatusCode: 200},
		{Method: "GET", Path: "/teams/{id}", Description: "Get a team by ID", StatusCode: 200},
		{Method: "POST", Path: "/teams", Description: "Create a new team", StatusCode: 201},
	}
	data, _ := json.Marshal(endpoints)
	return string(data), nil
}

// fakeGenerator implements generator.Generator for testing.
type fakeGenerator struct{}

func (f *fakeGenerator) Generate(_ context.Context, ep models.Endpoint, req generator.RequestContext) (*generator.MockResponse, error) {
	body := fmt.Sprintf(`{"mock":true,"method":"%s","path":"%s","endpoint_path":"%s"}`,
		req.Method, req.Path, ep.Path)
	return &generator.MockResponse{
		StatusCode: ep.StatusCode,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       body,
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func exampleRepoPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repo := filepath.Join(wd, "..", "testdata", "example-repo")
	abs, err := filepath.Abs(repo)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("example-repo not found at %s: %v", abs, err)
	}
	return abs
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestASTScan_ExampleRepo verifies the AST scanner parses the example repo
// and extracts the expected structs, routes, and handlers.
func TestASTScan_ExampleRepo(t *testing.T) {
	repoPath := exampleRepoPath(t)

	structs, routes, handlers, err := scanner.ExtractFromDir(repoPath)
	if err != nil {
		t.Fatalf("ExtractFromDir: %v", err)
	}

	// Structs: User, CreateUserRequest, UpdateUserRequest, UserListResponse,
	//          Team, CreateTeamRequest, UserHandler, TeamHandler
	if len(structs) < 6 {
		t.Errorf("expected at least 6 structs, got %d", len(structs))
		for _, s := range structs {
			t.Logf("  struct: %s.%s", s.Package, s.Name)
		}
	}

	structNames := make(map[string]bool)
	for _, s := range structs {
		structNames[s.Name] = true
	}
	for _, want := range []string{"User", "Team", "CreateUserRequest", "CreateTeamRequest", "UserListResponse"} {
		if !structNames[want] {
			t.Errorf("missing expected struct: %s", want)
		}
	}

	// Verify User struct fields have JSON tags.
	for _, s := range structs {
		if s.Name == "User" {
			if len(s.Fields) < 5 {
				t.Errorf("User struct should have >= 5 fields, got %d", len(s.Fields))
			}
			for _, f := range s.Fields {
				if f.JSONTag == "" || f.JSONTag == "-" {
					t.Errorf("User field %s missing json tag", f.Name)
				}
			}
			break
		}
	}

	// Routes: 8 chi route registrations.
	if len(routes) < 8 {
		t.Errorf("expected at least 8 routes, got %d", len(routes))
		for _, r := range routes {
			t.Logf("  route: %s %s -> %s", r.Method, r.Path, r.Handler)
		}
	}

	routeSet := make(map[string]bool)
	for _, r := range routes {
		routeSet[r.Method+" "+r.Path] = true
	}
	for _, want := range []string{
		"GET /users", "GET /users/{id}", "POST /users",
		"PUT /users/{id}", "DELETE /users/{id}",
		"GET /teams", "GET /teams/{id}", "POST /teams",
	} {
		if !routeSet[want] {
			t.Errorf("missing expected route: %s", want)
		}
	}

	// Handlers.
	if len(handlers) < 8 {
		t.Errorf("expected at least 8 handlers, got %d", len(handlers))
		for _, h := range handlers {
			t.Logf("  handler: %s (decodes: %s, encodes: %s)", h.Name, h.Decodes, h.Encodes)
		}
	}

	// Framework detection.
	fw := scanner.DetectFramework(repoPath, []string{"."})
	if fw != "chi" {
		t.Errorf("expected framework 'chi', got %q", fw)
	}
}

// TestFullScanPipeline verifies: AST extract -> LLM analyze -> registry output.
func TestFullScanPipeline(t *testing.T) {
	repoPath := exampleRepoPath(t)
	logger := testLogger()

	cfg := &config.Config{
		LLM: config.LLMConfig{APIKey: "sk-test"},
		Scanner: config.ScannerConfig{
			RegistryPath: filepath.Join(t.TempDir(), "registry.json"),
		},
		Dependencies: []config.DependencyConfig{
			{Name: "user-service", Prefix: "/api/users", RepoPath: repoPath, ScanPaths: []string{"."}},
		},
	}

	analyzer := scanner.NewLLMAnalyzer(&fakeLLMClient{}, cfg.LLM, logger)
	sc := scanner.New(cfg, analyzer, logger)

	registries, err := sc.ScanAll()
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	if len(registries) != 1 {
		t.Fatalf("expected 1 registry, got %d", len(registries))
	}

	reg := registries[0]
	if reg.Dependency != "user-service" {
		t.Errorf("expected dependency 'user-service', got %q", reg.Dependency)
	}
	if reg.FrameworkDetected != "chi" {
		t.Errorf("expected framework 'chi', got %q", reg.FrameworkDetected)
	}
	if len(reg.Endpoints) != 8 {
		t.Errorf("expected 8 endpoints, got %d", len(reg.Endpoints))
	}

	// Verify persistence round-trip.
	registryPath := cfg.Scanner.RegistryPath
	if err := scanner.SaveRegistries(registryPath, registries); err != nil {
		t.Fatalf("SaveRegistries: %v", err)
	}
	loaded, err := scanner.LoadRegistries(registryPath)
	if err != nil {
		t.Fatalf("LoadRegistries: %v", err)
	}
	if len(loaded) != 1 || len(loaded[0].Endpoints) != 8 {
		t.Errorf("loaded registry mismatch: %d registries", len(loaded))
	}
}

// TestEndToEnd_ServerFlow exercises the complete flow:
// scan -> build matcher -> start server -> HTTP requests -> verify responses.
func TestEndToEnd_ServerFlow(t *testing.T) {
	repoPath := exampleRepoPath(t)
	logger := testLogger()

	cfg := &config.Config{
		Server: config.ServerConfig{Host: "127.0.0.1", Port: 0},
		LLM:    config.LLMConfig{APIKey: "sk-test"},
		Cache:  config.CacheConfig{Enabled: true, DBPath: filepath.Join(t.TempDir(), "test.db"), TTL: time.Hour},
		Scanner: config.ScannerConfig{
			RegistryPath: filepath.Join(t.TempDir(), "registry.json"),
		},
		Dependencies: []config.DependencyConfig{
			{Name: "user-service", Prefix: "/api/users", RepoPath: repoPath, ScanPaths: []string{"."}},
		},
	}

	// Scan.
	analyzer := scanner.NewLLMAnalyzer(&fakeLLMClient{}, cfg.LLM, logger)
	sc := scanner.New(cfg, analyzer, logger)
	registries, err := sc.ScanAll()
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	// Build matcher.
	prefixes := make(map[string]string)
	for _, dep := range cfg.Dependencies {
		prefixes[dep.Name] = dep.Prefix
	}
	m := matcher.New(registries, prefixes)

	// Cache.
	store, err := cache.NewSQLiteStore(cfg.Cache.DBPath, cfg.Cache.TTL)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	gen := &fakeGenerator{}

	scanFunc := func(ctx context.Context) ([]models.DependencyRegistry, error) {
		return sc.ScanAll()
	}

	// Start server.
	srv := server.New(cfg, m, store, gen, registries, logger, server.WithScanFunc(scanFunc))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())

	baseURL := fmt.Sprintf("http://%s", ln.Addr().String())
	client := &http.Client{Timeout: 5 * time.Second}

	// --- Health Check ---
	t.Run("HealthCheck", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/_ditto/health")
		if err != nil {
			t.Fatalf("health: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		var body map[string]any
		json.NewDecoder(resp.Body).Decode(&body)
		if body["status"] != "ok" {
			t.Errorf("expected status ok, got %v", body["status"])
		}
	})

	// --- Registry List ---
	t.Run("RegistryList", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/_ditto/registry")
		if err != nil {
			t.Fatalf("registry: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		var regs []models.DependencyRegistry
		json.NewDecoder(resp.Body).Decode(&regs)
		if len(regs) != 1 {
			t.Errorf("expected 1 registry, got %d", len(regs))
		}
	})

	// --- Mock GET (cache miss -> generate) ---
	t.Run("MockGET_CacheMiss", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/api/users/users/abc123")
		if err != nil {
			t.Fatalf("mock GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
		var body map[string]any
		json.NewDecoder(resp.Body).Decode(&body)
		if body["mock"] != true {
			t.Errorf("expected mock=true, got %v", body["mock"])
		}
		if body["endpoint_path"] != "/users/{id}" {
			t.Errorf("expected endpoint_path=/users/{id}, got %v", body["endpoint_path"])
		}
	})

	// --- Mock GET (cache hit) ---
	t.Run("MockGET_CacheHit", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/api/users/users/abc123")
		if err != nil {
			t.Fatalf("mock GET cached: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		var body map[string]any
		json.NewDecoder(resp.Body).Decode(&body)
		if body["mock"] != true {
			t.Errorf("expected mock=true from cache")
		}
	})

	// --- Mock POST ---
	t.Run("MockPOST", func(t *testing.T) {
		payload := `{"name":"Alice","email":"alice@example.com"}`
		resp, err := client.Post(baseURL+"/api/users/users", "application/json", strings.NewReader(payload))
		if err != nil {
			t.Fatalf("mock POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 201, got %d: %s", resp.StatusCode, body)
		}
	})

	// --- Mock DELETE ---
	t.Run("MockDELETE", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", baseURL+"/api/users/users/abc123", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("mock DELETE: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 204, got %d: %s", resp.StatusCode, body)
		}
	})

	// --- No Match ---
	t.Run("NoMatch", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/nonexistent/path")
		if err != nil {
			t.Fatalf("no match: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotImplemented {
			t.Errorf("expected 501, got %d", resp.StatusCode)
		}
	})

	// --- Cache Stats ---
	t.Run("CacheStats", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/_ditto/cache/stats")
		if err != nil {
			t.Fatalf("cache stats: %v", err)
		}
		defer resp.Body.Close()
		var stats models.CacheStats
		json.NewDecoder(resp.Body).Decode(&stats)
		if stats.TotalEntries < 3 {
			t.Errorf("expected >= 3 cache entries, got %d", stats.TotalEntries)
		}
	})

	// --- Cache Purge ---
	t.Run("CachePurge", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", baseURL+"/_ditto/cache", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("cache purge: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}
		statsResp, statsErr := client.Get(baseURL + "/_ditto/cache/stats")
		if statsErr != nil {
			t.Fatalf("cache stats after purge: %v", statsErr)
		}
		defer statsResp.Body.Close()
		var stats models.CacheStats
		json.NewDecoder(statsResp.Body).Decode(&stats)
		if stats.TotalEntries != 0 {
			t.Errorf("expected 0 entries after purge, got %d", stats.TotalEntries)
		}
	})

	// --- Re-scan ---
	t.Run("ReScan", func(t *testing.T) {
		req, _ := http.NewRequest("POST", baseURL+"/_ditto/scan", nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("re-scan: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}
	})

	// --- Config Redacted ---
	t.Run("ConfigRedacted", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/_ditto/config")
		if err != nil {
			t.Fatalf("config: %v", err)
		}
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)
		bodyStr := string(bodyBytes)
		if strings.Contains(bodyStr, "sk-test") {
			t.Error("config should not contain raw API key")
		}
		if !strings.Contains(bodyStr, "REDACTED") {
			t.Error("config should contain REDACTED")
		}
	})
}

// TestMatcherIntegration verifies the matcher routes requests to the correct endpoints.
func TestMatcherIntegration(t *testing.T) {
	repoPath := exampleRepoPath(t)
	logger := testLogger()

	cfg := &config.Config{
		LLM: config.LLMConfig{APIKey: "sk-test"},
		Scanner: config.ScannerConfig{
			RegistryPath: filepath.Join(t.TempDir(), "registry.json"),
		},
		Dependencies: []config.DependencyConfig{
			{Name: "user-service", Prefix: "/api/v1", RepoPath: repoPath, ScanPaths: []string{"."}},
		},
	}

	analyzer := scanner.NewLLMAnalyzer(&fakeLLMClient{}, cfg.LLM, logger)
	sc := scanner.New(cfg, analyzer, logger)
	registries, err := sc.ScanAll()
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}

	m := matcher.New(registries, map[string]string{"user-service": "/api/v1"})

	tests := []struct {
		method     string
		path       string
		wantMatch  bool
		wantEPPath string
		wantParams map[string]string
	}{
		{"GET", "/api/v1/users", true, "/users", nil},
		{"GET", "/api/v1/users/42", true, "/users/{id}", map[string]string{"id": "42"}},
		{"POST", "/api/v1/users", true, "/users", nil},
		{"PUT", "/api/v1/users/99", true, "/users/{id}", map[string]string{"id": "99"}},
		{"DELETE", "/api/v1/users/99", true, "/users/{id}", map[string]string{"id": "99"}},
		{"GET", "/api/v1/teams", true, "/teams", nil},
		{"GET", "/api/v1/teams/eng", true, "/teams/{id}", map[string]string{"id": "eng"}},
		{"POST", "/api/v1/teams", true, "/teams", nil},
		{"GET", "/unknown/path", false, "", nil},
		{"PATCH", "/api/v1/users/1", false, "", nil},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s_%s", tc.method, tc.path), func(t *testing.T) {
			result, err := m.Match(tc.method, tc.path)
			if tc.wantMatch {
				if err != nil {
					t.Fatalf("expected match, got error: %v", err)
				}
				if result.Endpoint.Path != tc.wantEPPath {
					t.Errorf("endpoint path: want %q, got %q", tc.wantEPPath, result.Endpoint.Path)
				}
				for k, v := range tc.wantParams {
					if result.PathParams[k] != v {
						t.Errorf("param %s: want %q, got %q", k, v, result.PathParams[k])
					}
				}
			} else {
				if err == nil {
					t.Errorf("expected no match, got: %+v", result)
				}
			}
		})
	}
}
