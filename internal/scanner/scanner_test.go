package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ditto-mock/ditto-mock-api/internal/config"
	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

// --- Mock LLM Client ---

type mockLLMClient struct {
	response string
	err      error
	calls    int
}

func (m *mockLLMClient) ChatCompletion(_ context.Context, _, _ string) (string, error) {
	m.calls++
	return m.response, m.err
}

// --- Mock Analyzer ---

type mockAnalyzer struct {
	endpoints []models.Endpoint
	err       error
	calls     int
	lastScan  *models.ScanOutput
}

func (m *mockAnalyzer) Analyze(scan *models.ScanOutput) ([]models.Endpoint, error) {
	m.calls++
	m.lastScan = scan
	return m.endpoints, m.err
}

// --- Helper to create Go source files for testing ---

func writeGoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// =============================================================================
// AST Extractor Tests
// =============================================================================

func TestExtractFromDir_Structs(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "user.go", `package api

type User struct {
	ID   string `+"`"+`json:"id"`+"`"+`
	Name string `+"`"+`json:"name,omitempty"`+"`"+`
	Age  int    `+"`"+`json:"age"`+"`"+`
}

type Address struct {
	Street string `+"`"+`json:"street"`+"`"+`
	City   string `+"`"+`json:"city"`+"`"+`
}
`)

	structs, _, _, err := ExtractFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(structs) != 2 {
		t.Fatalf("expected 2 structs, got %d", len(structs))
	}

	// Check User struct
	user := structs[0]
	if user.Name != "User" {
		t.Errorf("expected User, got %s", user.Name)
	}
	if user.Package != "api" {
		t.Errorf("expected package api, got %s", user.Package)
	}
	if len(user.Fields) != 3 {
		t.Errorf("expected 3 fields, got %d", len(user.Fields))
	}

	// Check JSON tag
	idField := user.Fields[0]
	if idField.JSONTag != "id" {
		t.Errorf("expected json tag 'id', got '%s'", idField.JSONTag)
	}
	if idField.Omitempty {
		t.Error("expected omitempty false for id")
	}

	nameField := user.Fields[1]
	if nameField.JSONTag != "name" {
		t.Errorf("expected json tag 'name', got '%s'", nameField.JSONTag)
	}
	if !nameField.Omitempty {
		t.Error("expected omitempty true for name")
	}
}

func TestExtractFromDir_SkipsTestFiles(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "model.go", `package api
type Model struct {
	ID string
}
`)
	writeGoFile(t, dir, "model_test.go", `package api
type TestHelper struct {
	Foo string
}
`)

	structs, _, _, err := ExtractFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(structs) != 1 {
		t.Fatalf("expected 1 struct (should skip _test.go), got %d", len(structs))
	}
	if structs[0].Name != "Model" {
		t.Errorf("expected Model, got %s", structs[0].Name)
	}
}

func TestExtractFromDir_SkipsVendor(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "main.go", `package main
type App struct{}
`)
	vendorDir := filepath.Join(dir, "vendor", "lib")
	writeGoFile(t, vendorDir, "lib.go", `package lib
type Lib struct{}
`)

	structs, _, _, err := ExtractFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(structs) != 1 {
		t.Fatalf("expected 1 struct (should skip vendor), got %d", len(structs))
	}
}

func TestExtractFromDir_Routes_Chi(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "routes.go", `package api

import "github.com/go-chi/chi/v5"

func SetupRoutes(r chi.Router) {
	r.Get("/users", ListUsers)
	r.Post("/users", CreateUser)
	r.Get("/users/{id}", GetUser)
	r.Delete("/users/{id}", DeleteUser)
}
`)

	_, routes, _, err := ExtractFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 4 {
		t.Fatalf("expected 4 routes, got %d", len(routes))
	}

	expected := []struct {
		method  string
		path    string
		handler string
	}{
		{"GET", "/users", "ListUsers"},
		{"POST", "/users", "CreateUser"},
		{"GET", "/users/{id}", "GetUser"},
		{"DELETE", "/users/{id}", "DeleteUser"},
	}

	for i, e := range expected {
		if routes[i].Method != e.method {
			t.Errorf("route %d: expected method %s, got %s", i, e.method, routes[i].Method)
		}
		if routes[i].Path != e.path {
			t.Errorf("route %d: expected path %s, got %s", i, e.path, routes[i].Path)
		}
		if routes[i].Handler != e.handler {
			t.Errorf("route %d: expected handler %s, got %s", i, e.handler, routes[i].Handler)
		}
	}
}
func TestExtractFromDir_Routes_Stdlib122(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "routes.go", `package api

import "net/http"

func SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", HealthCheck)
	mux.HandleFunc("POST /messages/{user_id}/{channel_id}", SendMessage)
	mux.HandleFunc("GET /conversation/{user_id}/{channel_id}", GetConversation)
	mux.HandleFunc("/legacy", LegacyHandler)
}
`)

	_, routes, _, err := ExtractFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 4 {
		t.Fatalf("expected 4 routes, got %d", len(routes))
	}

	expected := []struct {
		method  string
		path    string
		handler string
	}{
		{"GET", "/health", "HealthCheck"},
		{"POST", "/messages/{user_id}/{channel_id}", "SendMessage"},
		{"GET", "/conversation/{user_id}/{channel_id}", "GetConversation"},
		{"ANY", "/legacy", "LegacyHandler"},
	}

	for i, e := range expected {
		if routes[i].Method != e.method {
			t.Errorf("route %d: expected method %s, got %s", i, e.method, routes[i].Method)
		}
		if routes[i].Path != e.path {
			t.Errorf("route %d: expected path %s, got %s", i, e.path, routes[i].Path)
		}
		if routes[i].Handler != e.handler {
			t.Errorf("route %d: expected handler %s, got %s", i, e.handler, routes[i].Handler)
		}
	}
}

func TestExtractFromDir_HandlerBodyAnalysis(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "handler.go", `package api

import (
	"encoding/json"
	"net/http"
)

type CreateRequest struct {
	Name string
}

type CreateResponse struct {
	ID string
}

func CreateHandler(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	json.NewDecoder(r.Body).Decode(&req)
	resp := CreateResponse{ID: "123"}
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(resp)
}
`)

	_, _, handlers, err := ExtractFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(handlers) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(handlers))
	}
	h := handlers[0]
	if h.Decodes != "CreateRequest" {
		t.Errorf("expected decodes CreateRequest, got %s", h.Decodes)
	}
	if h.Encodes != "CreateResponse" {
		t.Errorf("expected encodes CreateResponse, got %s", h.Encodes)
	}
	if len(h.StatusCodes) != 1 || h.StatusCodes[0] != 201 {
		t.Errorf("expected status codes [201], got %v", h.StatusCodes)
	}
}
func TestExtractFromDir_Handlers(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "handler.go", `package api

import "net/http"

func ListUsers(w http.ResponseWriter, r *http.Request) {
	// some logic
}

func privateHelper() {
	// should be excluded - not exported
}
`)

	_, _, handlers, err := ExtractFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(handlers) != 1 {
		t.Fatalf("expected 1 handler (exported + HTTP signature), got %d", len(handlers))
	}
	if handlers[0].Name != "ListUsers" {
		t.Errorf("expected ListUsers, got %s", handlers[0].Name)
	}
}

func TestExtractFromDir_UnexportedFieldsSkipped(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "model.go", `package api

type Item struct {
	ID       string `+"`"+`json:"id"`+"`"+`
	internal string
}
`)

	structs, _, _, err := ExtractFromDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(structs) != 1 {
		t.Fatal("expected 1 struct")
	}
	if len(structs[0].Fields) != 1 {
		t.Fatalf("expected 1 exported field, got %d", len(structs[0].Fields))
	}
	if structs[0].Fields[0].Name != "ID" {
		t.Errorf("expected ID, got %s", structs[0].Fields[0].Name)
	}
}

// =============================================================================
// parseJSONTag Tests
// =============================================================================

func TestParseJSONTag(t *testing.T) {
	tests := []struct {
		name     string
		tag      string
		wantKey  string
		wantOmit bool
	}{
		{"simple", "`" + `json:"id"` + "`", "id", false},
		{"omitempty", "`" + `json:"name,omitempty"` + "`", "name", true},
		{"dash", "`" + `json:"-"` + "`", "-", false},
		{"no json tag", "`" + `xml:"data"` + "`", "", false},
		{"empty", "", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var tag *ast.BasicLit
			if tc.tag != "" {
				tag = &ast.BasicLit{Kind: token.STRING, Value: tc.tag}
			}
			key, omit := parseJSONTag(tag)
			if key != tc.wantKey {
				t.Errorf("key: got %q, want %q", key, tc.wantKey)
			}
			if omit != tc.wantOmit {
				t.Errorf("omitempty: got %v, want %v", omit, tc.wantOmit)
			}
		})
	}
}

// =============================================================================
// Framework Detection Tests
// =============================================================================

func TestDetectFrameworkFromImports(t *testing.T) {
	tests := []struct {
		name    string
		imports []string
		want    string
	}{
		{"chi v5", []string{"github.com/go-chi/chi/v5"}, FrameworkChi},
		{"gin", []string{"github.com/gin-gonic/gin"}, FrameworkGin},
		{"echo v4", []string{"github.com/labstack/echo/v4"}, FrameworkEcho},
		{"gorilla", []string{"github.com/gorilla/mux"}, FrameworkGorilla},
		{"stdlib", []string{"net/http"}, FrameworkStdlib},
		{"unknown", []string{"encoding/json"}, FrameworkUnknown},
		{"prefers chi over stdlib", []string{"net/http", "github.com/go-chi/chi/v5"}, FrameworkChi},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectFrameworkFromImports(tc.imports)
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDetectFramework_FromFiles(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "main.go", `package main

import (
	"net/http"
	"github.com/go-chi/chi/v5"
)

func main() {
	r := chi.NewRouter()
	http.ListenAndServe(":8080", r)
}
`)

	got := DetectFramework(dir, []string{"."})
	if got != FrameworkChi {
		t.Errorf("expected chi, got %s", got)
	}
}

func TestDetectFramework_StdlibOnly(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "main.go", `package main

import "net/http"

func main() {
	http.ListenAndServe(":8080", nil)
}
`)

	got := DetectFramework(dir, []string{"."})
	if got != FrameworkStdlib {
		t.Errorf("expected stdlib, got %s", got)
	}
}

func TestDetectFramework_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	got := DetectFramework(dir, []string{"."})
	if got != FrameworkUnknown {
		t.Errorf("expected unknown, got %s", got)
	}
}

// =============================================================================
// LLM Analyzer Tests
// =============================================================================

func TestLLMAnalyzer_Analyze_Success(t *testing.T) {
	response := `[{"method":"GET","path":"/users","description":"List users","request_body":null,"response_body":{"type":"array"},"status_code":200}]`
	client := &mockLLMClient{response: response}
	cfg := config.LLMConfig{
		Timeout:    10 * time.Second,
		MaxRetries: 0,
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	analyzer := NewLLMAnalyzer(client, cfg, logger)

	scan := &models.ScanOutput{
		Repo:      "test-service",
		Framework: "chi",
		Structs:   []models.ExtractedStruct{{Name: "User", Package: "api"}},
		Routes:    []models.ExtractedRoute{{Method: "GET", Path: "/users", Handler: "ListUsers"}},
	}

	endpoints, err := analyzer.Analyze(scan)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
	if endpoints[0].Method != "GET" {
		t.Errorf("expected GET, got %s", endpoints[0].Method)
	}
	if endpoints[0].Path != "/users" {
		t.Errorf("expected /users, got %s", endpoints[0].Path)
	}
	if client.calls != 1 {
		t.Errorf("expected 1 LLM call, got %d", client.calls)
	}
}

func TestLLMAnalyzer_Analyze_RetryOnError(t *testing.T) {
	// First call returns error, mock always returns error
	client := &mockLLMClient{err: fmt.Errorf("connection refused")}
	cfg := config.LLMConfig{
		Timeout:    2 * time.Second,
		MaxRetries: 1,
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	analyzer := NewLLMAnalyzer(client, cfg, logger)

	scan := &models.ScanOutput{Repo: "test"}

	_, err := analyzer.Analyze(scan)
	if err == nil {
		t.Fatal("expected error")
	}
	// Should have attempted 1 + 1 retry = 2 calls
	if client.calls != 2 {
		t.Errorf("expected 2 LLM calls (1 + 1 retry), got %d", client.calls)
	}
}

func TestLLMAnalyzer_Analyze_MarkdownWrapped(t *testing.T) {
	response := "```json\n" + `[{"method":"POST","path":"/items","description":"Create item","request_body":null,"response_body":null,"status_code":201}]` + "\n```"
	client := &mockLLMClient{response: response}
	cfg := config.LLMConfig{
		Timeout:    10 * time.Second,
		MaxRetries: 0,
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	analyzer := NewLLMAnalyzer(client, cfg, logger)

	endpoints, err := analyzer.Analyze(&models.ScanOutput{Repo: "test", Framework: "chi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
	if endpoints[0].StatusCode != 201 {
		t.Errorf("expected 201, got %d", endpoints[0].StatusCode)
	}
}

func TestLLMAnalyzer_Analyze_EmptyArrayError(t *testing.T) {
	client := &mockLLMClient{response: "[]"}
	cfg := config.LLMConfig{
		Timeout:    5 * time.Second,
		MaxRetries: 0,
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	analyzer := NewLLMAnalyzer(client, cfg, logger)

	_, err := analyzer.Analyze(&models.ScanOutput{Repo: "test"})
	if err == nil {
		t.Fatal("expected error for empty array")
	}
}

// =============================================================================
// Prompt Building Tests
// =============================================================================

func TestBuildAnalysisPrompt(t *testing.T) {
	scan := &models.ScanOutput{
		Repo:      "user-service",
		Framework: "chi",
		Structs: []models.ExtractedStruct{
			{Name: "User", Package: "api", Fields: []models.StructField{
				{Name: "ID", Type: "string", JSONTag: "id"},
			}},
		},
		Routes: []models.ExtractedRoute{
			{Method: "GET", Path: "/users", Handler: "ListUsers"},
		},
		Handlers: []models.ExtractedHandler{
			{Name: "ListUsers", Encodes: "User"},
		},
	}

	prompt, err := buildAnalysisPrompt(scan)
	if err != nil {
		t.Fatal(err)
	}

	// Check that prompt contains key elements
	checks := []string{
		"chi",
		"User", "ListUsers", "/users",
		"Extracted Structs", "Extracted Routes", "Handler Function",
	}
	for _, check := range checks {
		if !contains(prompt, check) {
			t.Errorf("prompt missing expected string: %q", check)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// =============================================================================
// JSON Response Cleaning Tests
// =============================================================================

func TestCleanJSONResponse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain JSON", `[{"a":1}]`, `[{"a":1}]`},
		{"markdown json block", "```json\n[{\"a\":1}]\n```", `[{"a":1}]`},
		{"markdown plain block", "```\n[{\"a\":1}]\n```", `[{"a":1}]`},
		{"with whitespace", "  \n[{\"a\":1}]\n  ", `[{"a":1}]`},
		{"markdown with text before", "Here is the result:\n```json\n[{\"a\":1}]\n```", `[{"a":1}]`},
		{"trailing comma object", `{"a":1,}`, `{"a":1}`},
		{"trailing comma array", `[1,2,3,]`, `[1,2,3]`},
		{"trailing comma nested", `{"items":[{"a":1,},]}`, `{"items":[{"a":1}]}`},
		{"trailing comma with whitespace", "{\"a\":1 , \n}", `{"a":1 }`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanJSONResponse(tc.input)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseEndpointsResponse(t *testing.T) {
	raw := `[{"method":"GET","path":"/items","description":"List","request_body":null,"response_body":null,"status_code":200}]`
	eps, err := parseEndpointsResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 {
		t.Fatalf("expected 1, got %d", len(eps))
	}
}

func TestParseEndpointsResponse_TrailingComma(t *testing.T) {
	raw := `[{"method":"GET","path":"/items","description":"List","request_body":null,"response_body":null,"status_code":200,}]`
	eps, err := parseEndpointsResponse(raw)
	if err != nil {
		t.Fatalf("trailing comma should be tolerated, got: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("expected 1, got %d", len(eps))
	}
}

func TestParseEndpointsResponse_InvalidJSON(t *testing.T) {
	_, err := parseEndpointsResponse("not json at all")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// =============================================================================
// Scanner Orchestrator Tests
// =============================================================================

func TestScanner_ScanDependency(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "main.go", `package main

import (
	"net/http"
	"github.com/go-chi/chi/v5"
)

type Item struct {
	ID string `+"`"+`json:"id"`+"`"+`
}

func SetupRoutes(r chi.Router) {
	r.Get("/items", ListItems)
}

func ListItems(w http.ResponseWriter, r *http.Request) {
}
`)

	analyzer := &mockAnalyzer{
		endpoints: []models.Endpoint{
			{Method: "GET", Path: "/items", Description: "List items", StatusCode: 200},
		},
	}

	cfg := &config.Config{
		Dependencies: []config.DependencyConfig{
			{
				Name:     "test-service",
				RepoPath: dir,
			},
		},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	s := New(cfg, analyzer, logger)

	reg, err := s.ScanDependency(cfg.Dependencies[0])
	if err != nil {
		t.Fatal(err)
	}

	if reg.Dependency != "test-service" {
		t.Errorf("expected test-service, got %s", reg.Dependency)
	}
	if reg.FrameworkDetected != FrameworkChi {
		t.Errorf("expected chi, got %s", reg.FrameworkDetected)
	}
	if len(reg.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(reg.Endpoints))
	}

	// Verify the analyzer received the right scan data
	if analyzer.calls != 1 {
		t.Errorf("expected 1 analyzer call, got %d", analyzer.calls)
	}
	if analyzer.lastScan.Repo != "test-service" {
		t.Errorf("expected repo test-service, got %s", analyzer.lastScan.Repo)
	}
	if len(analyzer.lastScan.Structs) != 1 {
		t.Errorf("expected 1 struct in scan, got %d", len(analyzer.lastScan.Structs))
	}
	if len(analyzer.lastScan.Routes) != 1 {
		t.Errorf("expected 1 route in scan, got %d", len(analyzer.lastScan.Routes))
	}
}

// failingAnalyzer returns an error for a specific dependency.
type failingAnalyzer struct {
	failFor string
}

func (a *failingAnalyzer) Analyze(scan *models.ScanOutput) ([]models.Endpoint, error) {
	if scan.Repo == a.failFor {
		return nil, fmt.Errorf("analysis failed for %s", scan.Repo)
	}
	return []models.Endpoint{
		{Method: "GET", Path: "/ok", StatusCode: 200},
	}, nil
}

func TestScanner_ScanAll_PartialFailure(t *testing.T) {
	goodDir := t.TempDir()
	writeGoFile(t, goodDir, "main.go", `package main
type OK struct{}
`)

	badDir := t.TempDir()
	writeGoFile(t, badDir, "main.go", `package main
type Bad struct{}
`)

	analyzer := &failingAnalyzer{failFor: "bad"}

	cfg := &config.Config{
		Dependencies: []config.DependencyConfig{
			{Name: "good", RepoPath: goodDir},
			{Name: "bad", RepoPath: badDir},
		},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	s := New(cfg, analyzer, logger)
	regs, err := s.ScanAll()
	if err != nil {
		t.Fatal(err)
	}

	// Should still get 1 result for the "good" dep even though "bad" failed
	if len(regs) != 1 {
		t.Fatalf("expected 1 registry (partial failure), got %d", len(regs))
	}
	if regs[0].Dependency != "good" {
		t.Errorf("expected good, got %s", regs[0].Dependency)
	}
}

// =============================================================================
// Registry Persistence Tests
// =============================================================================

func TestSaveAndLoadRegistries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registries.json")

	regs := []models.DependencyRegistry{
		{
			ScannedAt:         time.Now().UTC().Truncate(time.Second),
			Dependency:        "svc-a",
			RepoPath:          "/repos/svc-a",
			FrameworkDetected: "chi",
			Endpoints: []models.Endpoint{
				{Method: "GET", Path: "/health", Description: "Health check", StatusCode: 200},
			},
		},
	}

	if err := SaveRegistries(path, regs); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadRegistries(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded) != 1 {
		t.Fatalf("expected 1, got %d", len(loaded))
	}
	if loaded[0].Dependency != "svc-a" {
		t.Errorf("expected svc-a, got %s", loaded[0].Dependency)
	}
	if len(loaded[0].Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(loaded[0].Endpoints))
	}

	// Verify JSON roundtrip
	orig, _ := json.Marshal(regs[0].Endpoints)
	roundtrip, _ := json.Marshal(loaded[0].Endpoints)
	if string(orig) != string(roundtrip) {
		t.Errorf("endpoint roundtrip mismatch:\norig:      %s\nroundtrip: %s", orig, roundtrip)
	}
}

func TestLoadRegistries_NotFound(t *testing.T) {
	_, err := LoadRegistries("/nonexistent/path/registries.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// =============================================================================
// typeToString Tests
// =============================================================================

func TestTypeToString(t *testing.T) {
	tests := []struct {
		name string
		expr ast.Expr
		want string
	}{
		{"ident", &ast.Ident{Name: "string"}, "string"},
		{"star", &ast.StarExpr{X: &ast.Ident{Name: "User"}}, "*User"},
		{"slice", &ast.ArrayType{Elt: &ast.Ident{Name: "int"}}, "[]int"},
		{"map", &ast.MapType{
			Key:   &ast.Ident{Name: "string"},
			Value: &ast.Ident{Name: "int"},
		}, "map[string]int"},
		{"selector", &ast.SelectorExpr{
			X:   &ast.Ident{Name: "time"},
			Sel: &ast.Ident{Name: "Time"},
		}, "time.Time"},
		{"interface", &ast.InterfaceType{}, "interface{}"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := typeToString(tc.expr)
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestParseStdlibPattern(t *testing.T) {
	tests := []struct {
		pattern    string
		wantMethod string
		wantPath   string
		wantOk     bool
	}{
		{"GET /health", "GET", "/health", true},
		{"POST /messages/{user_id}", "POST", "/messages/{user_id}", true},
		{"PUT /users/{id}", "PUT", "/users/{id}", true},
		{"DELETE /items/{id}", "DELETE", "/items/{id}", true},
		{"PATCH /update", "PATCH", "/update", true},
		{"/legacy", "", "", false},                           // no method prefix
		{"GET", "", "", false},                               // no path
		{"INVALID /foo", "", "", false},                      // invalid method
		{"get /lowercase", "GET", "/lowercase", true},        // normalized to uppercase
		{"", "", "", false},                                  // empty
		{"GET  /double-space", "GET", "/double-space", true}, // SplitN tolerates extra space
	}
	for _, tc := range tests {
		t.Run(tc.pattern, func(t *testing.T) {
			method, path, ok := parseStdlibPattern(tc.pattern)
			if ok != tc.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOk)
			}
			if !tc.wantOk {
				return
			}
			if method != tc.wantMethod {
				t.Errorf("method = %s, want %s", method, tc.wantMethod)
			}
			if path != tc.wantPath {
				t.Errorf("path = %s, want %s", path, tc.wantPath)
			}
		})
	}
}
