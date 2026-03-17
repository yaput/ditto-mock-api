package scanner

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},            // empty string = 0 tokens
		{"hello", 2},       // ceil(5/3.5) ≈ 2
		{"hello world", 4}, // ceil(11/3.5) ≈ 4
	}

	for _, tt := range tests {
		got := estimateTokens(tt.input)
		if got != tt.want {
			t.Errorf("estimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}

	// Large text should be roughly len/3.5.
	largeText := strings.Repeat("x", 10000)
	est := estimateTokens(largeText)
	if est < 2800 || est > 2900 {
		t.Errorf("estimateTokens(10000 chars) = %d, expected ~2857", est)
	}
}

func TestStripSlicePrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"User", "User"},
		{"[]User", "User"},
		{"[][]int", "int"},
		{"[5]byte", "[5]byte"}, // not a slice prefix
		{"", ""},
	}
	for _, tt := range tests {
		got := stripSlicePrefix(tt.input)
		if got != tt.want {
			t.Errorf("stripSlicePrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestReferencedStructNames(t *testing.T) {
	handlers := []models.ExtractedHandler{
		{Name: "GetUser", Decodes: "", Encodes: "User"},
		{Name: "CreateUser", Decodes: "CreateUserRequest", Encodes: "User"},
	}

	structIndex := map[string]models.ExtractedStruct{
		"User": {
			Name: "User",
			Fields: []models.StructField{
				{Name: "ID", Type: "string"},
				{Name: "Address", Type: "Address"},
			},
		},
		"Address": {
			Name: "Address",
			Fields: []models.StructField{
				{Name: "Street", Type: "string"},
			},
		},
		"CreateUserRequest": {
			Name: "CreateUserRequest",
			Fields: []models.StructField{
				{Name: "Name", Type: "string"},
				{Name: "Tags", Type: "[]Tag"},
			},
		},
		"Tag": {
			Name: "Tag",
			Fields: []models.StructField{
				{Name: "Key", Type: "string"},
				{Name: "Value", Type: "string"},
			},
		},
		"Unrelated": {
			Name: "Unrelated",
			Fields: []models.StructField{
				{Name: "Foo", Type: "string"},
			},
		},
	}

	refs := referencedStructNames(handlers, structIndex)

	// Should include: User, CreateUserRequest, Address (nested in User), Tag (nested in CreateUserRequest via []Tag)
	// Should NOT include: Unrelated
	expected := map[string]bool{
		"User":              true,
		"CreateUserRequest": true,
		"Address":           true,
		"Tag":               true,
	}

	for name := range expected {
		if !refs[name] {
			t.Errorf("expected struct %q to be referenced", name)
		}
	}
	if refs["Unrelated"] {
		t.Error("Unrelated struct should not be referenced")
	}
}

func TestChunkScanOutput_FitsInOneChunk(t *testing.T) {
	scan := &models.ScanOutput{
		Repo:      "test-svc",
		Framework: "chi",
		Structs: []models.ExtractedStruct{
			{Name: "User", Fields: []models.StructField{{Name: "ID", Type: "string"}}},
		},
		Routes: []models.ExtractedRoute{
			{Method: "GET", Path: "/users", Handler: "GetUsers"},
		},
		Handlers: []models.ExtractedHandler{
			{Name: "GetUsers", Encodes: "User"},
		},
	}

	// Very large budget — should fit in one chunk.
	chunks := ChunkScanOutput(scan, 100000, 16384)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != scan {
		t.Error("expected original scan to be returned for single chunk")
	}
}

func TestChunkScanOutput_SplitsWhenTooLarge(t *testing.T) {
	// Create a scan with many routes to force splitting.
	var routes []models.ExtractedRoute
	var handlers []models.ExtractedHandler
	var structs []models.ExtractedStruct

	for i := 0; i < 50; i++ {
		name := strings.Repeat("x", 100) // Pad names to increase token count.
		routeName := name + string(rune('A'+i%26))
		handlerName := "Handle" + routeName

		routes = append(routes, models.ExtractedRoute{
			Method:  "GET",
			Path:    "/api/" + routeName,
			Handler: handlerName,
		})
		handlers = append(handlers, models.ExtractedHandler{
			Name:    handlerName,
			Encodes: "Response" + routeName,
		})
		structs = append(structs, models.ExtractedStruct{
			Name: "Response" + routeName,
			Fields: []models.StructField{
				{Name: "ID", Type: "string", JSONTag: "id"},
				{Name: "Data", Type: "string", JSONTag: "data"},
			},
		})
	}

	scan := &models.ScanOutput{
		Repo:      "test-svc",
		Framework: "chi",
		Structs:   structs,
		Routes:    routes,
		Handlers:  handlers,
	}

	// Use a small budget to force chunking.
	chunks := ChunkScanOutput(scan, 3000, 100000)

	if len(chunks) <= 1 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	// Verify all routes are covered.
	totalRoutes := 0
	for _, c := range chunks {
		totalRoutes += len(c.Routes)
		if c.Repo != "test-svc" {
			t.Errorf("chunk repo should be test-svc, got %s", c.Repo)
		}
		if c.Framework != "chi" {
			t.Errorf("chunk framework should be chi, got %s", c.Framework)
		}
	}
	if totalRoutes != 50 {
		t.Errorf("total routes across chunks = %d, want 50", totalRoutes)
	}
}

func TestChunkScanOutput_OnlyIncludesReferencedStructs(t *testing.T) {
	// Create enough data to force multi-chunk splitting.
	var routes []models.ExtractedRoute
	var handlers []models.ExtractedHandler
	var structs []models.ExtractedStruct

	// Two groups of routes, each referencing distinct structs.
	for i := 0; i < 20; i++ {
		padded := strings.Repeat("u", 80)
		rName := padded + fmt.Sprintf("UserRoute%d", i)
		hName := "HandleUser" + rName
		sName := "UserResp" + rName
		routes = append(routes, models.ExtractedRoute{Method: "GET", Path: "/users/" + rName, Handler: hName})
		handlers = append(handlers, models.ExtractedHandler{Name: hName, Encodes: sName})
		structs = append(structs, models.ExtractedStruct{Name: sName, Fields: []models.StructField{{Name: "ID", Type: "string", JSONTag: "id"}}})
	}
	for i := 0; i < 20; i++ {
		padded := strings.Repeat("o", 80)
		rName := padded + fmt.Sprintf("OrderRoute%d", i)
		hName := "HandleOrder" + rName
		sName := "OrderResp" + rName
		routes = append(routes, models.ExtractedRoute{Method: "GET", Path: "/orders/" + rName, Handler: hName})
		handlers = append(handlers, models.ExtractedHandler{Name: hName, Encodes: sName})
		structs = append(structs, models.ExtractedStruct{Name: sName, Fields: []models.StructField{{Name: "ID", Type: "string", JSONTag: "id"}}})
	}

	// Also add an unreferenced struct.
	structs = append(structs, models.ExtractedStruct{Name: "NeverUsed", Fields: []models.StructField{{Name: "X", Type: "int"}}})

	scan := &models.ScanOutput{
		Repo:      "test-svc",
		Framework: "chi",
		Structs:   structs,
		Routes:    routes,
		Handlers:  handlers,
	}

	// Small budget to force multiple chunks.
	chunks := ChunkScanOutput(scan, 3000, 100000)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	// No chunk should include "NeverUsed".
	for i, c := range chunks {
		for _, s := range c.Structs {
			if s.Name == "NeverUsed" {
				t.Errorf("chunk %d includes unreferenced struct NeverUsed", i)
			}
		}
	}
}

func TestCollectHandlers(t *testing.T) {
	handlerIndex := map[string]models.ExtractedHandler{
		"GetUsers":  {Name: "GetUsers", Encodes: "User"},
		"GetOrders": {Name: "GetOrders", Encodes: "Order"},
	}

	routes := []models.ExtractedRoute{
		{Handler: "GetUsers"},
		{Handler: "GetUsers"}, // duplicate
		{Handler: "GetOrders"},
		{Handler: "Missing"}, // not in index
	}

	handlers := collectHandlers(routes, handlerIndex)
	if len(handlers) != 2 {
		t.Fatalf("expected 2 handlers, got %d", len(handlers))
	}
}

func TestFormatChunkSummary_Single(t *testing.T) {
	scan := &models.ScanOutput{
		Routes:   make([]models.ExtractedRoute, 5),
		Structs:  make([]models.ExtractedStruct, 3),
		Handlers: make([]models.ExtractedHandler, 5),
	}
	summary := FormatChunkSummary(scan, []*models.ScanOutput{scan})
	if !strings.Contains(summary, "single prompt") {
		t.Errorf("expected single prompt message, got: %s", summary)
	}
}

func TestFormatChunkSummary_Multiple(t *testing.T) {
	scan := &models.ScanOutput{
		Routes:   make([]models.ExtractedRoute, 10),
		Structs:  make([]models.ExtractedStruct, 5),
		Handlers: make([]models.ExtractedHandler, 10),
	}
	chunks := []*models.ScanOutput{
		{Routes: make([]models.ExtractedRoute, 5)},
		{Routes: make([]models.ExtractedRoute, 5)},
	}
	summary := FormatChunkSummary(scan, chunks)
	if !strings.Contains(summary, "2 chunks") {
		t.Errorf("expected 2 chunks message, got: %s", summary)
	}
}
func TestChunkScanOutput_ResponseBudgetCapsRoutes(t *testing.T) {
	// Create 100 routes — even though the prompt fits, the response budget
	// should force splitting so each chunk has at most maxResponseTokens/tokensPerEndpoint routes.
	var routes []models.ExtractedRoute
	for i := 0; i < 100; i++ {
		routes = append(routes, models.ExtractedRoute{
			Method:  "GET",
			Path:    fmt.Sprintf("/items/%d", i),
			Handler: fmt.Sprintf("GetItem%d", i),
		})
	}

	scan := &models.ScanOutput{
		Repo:      "test-svc",
		Framework: "chi",
		Routes:    routes,
	}

	// maxResponseTokens = 4000 → at 400 tokens/endpoint → max 10 routes per chunk.
	chunks := ChunkScanOutput(scan, 500000, 4000)

	if len(chunks) < 10 {
		t.Fatalf("expected at least 10 chunks for 100 routes with max 10/chunk, got %d", len(chunks))
	}

	totalRoutes := 0
	for _, c := range chunks {
		if len(c.Routes) > 10 {
			t.Errorf("chunk has %d routes, expected at most 10", len(c.Routes))
		}
		totalRoutes += len(c.Routes)
	}
	if totalRoutes != 100 {
		t.Errorf("total routes = %d, want 100", totalRoutes)
	}
}

func TestCollectHandlers_QualifiedName(t *testing.T) {
	handlerIndex := map[string]models.ExtractedHandler{
		"GetUsers":  {Name: "GetUsers", Encodes: "User"},
		"GetOrders": {Name: "GetOrders", Encodes: "Order"},
	}

	// Routes use qualified names like "ctrl.GetUsers".
	routes := []models.ExtractedRoute{
		{Handler: "ctrl.GetUsers"},
		{Handler: "handler.GetOrders"},
		{Handler: "Missing"},
	}

	handlers := collectHandlers(routes, handlerIndex)
	if len(handlers) != 2 {
		t.Fatalf("expected 2 handlers via qualified name fallback, got %d", len(handlers))
	}
}
