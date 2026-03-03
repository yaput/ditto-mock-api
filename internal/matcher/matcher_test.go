package matcher

import (
	"testing"

	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

func buildRegistries() []models.DependencyRegistry {
	return []models.DependencyRegistry{
		{
			Dependency: "user-service",
			Endpoints: []models.Endpoint{
				{Method: "GET", Path: "/users", StatusCode: 200},
				{Method: "POST", Path: "/users", StatusCode: 201},
				{Method: "GET", Path: "/users/{id}", StatusCode: 200},
				{Method: "DELETE", Path: "/users/{id}", StatusCode: 204},
			},
		},
		{
			Dependency: "order-service",
			Endpoints: []models.Endpoint{
				{Method: "GET", Path: "/orders", StatusCode: 200},
				{Method: "GET", Path: "/orders/{orderId}/items", StatusCode: 200},
			},
		},
	}
}

func buildPrefixes() map[string]string {
	return map[string]string{
		"user-service":  "/api/users-svc",
		"order-service": "/api/orders-svc",
	}
}

func TestMatch_ExactPath(t *testing.T) {
	m := New(buildRegistries(), buildPrefixes())
	res, err := m.Match("GET", "/api/users-svc/users")
	if err != nil {
		t.Fatal(err)
	}
	if res.Dependency != "user-service" {
		t.Errorf("expected user-service, got %s", res.Dependency)
	}
	if res.Endpoint.Method != "GET" {
		t.Errorf("expected GET, got %s", res.Endpoint.Method)
	}
}

func TestMatch_WithPathParam(t *testing.T) {
	m := New(buildRegistries(), buildPrefixes())
	res, err := m.Match("GET", "/api/users-svc/users/abc-123")
	if err != nil {
		t.Fatal(err)
	}
	if res.Endpoint.Path != "/users/{id}" {
		t.Errorf("expected /users/{id}, got %s", res.Endpoint.Path)
	}
	if res.PathParams["id"] != "abc-123" {
		t.Errorf("expected id=abc-123, got %s", res.PathParams["id"])
	}
}

func TestMatch_MethodMismatch(t *testing.T) {
	m := New(buildRegistries(), buildPrefixes())
	_, err := m.Match("PATCH", "/api/users-svc/users")
	if err == nil {
		t.Fatal("expected no match for PATCH /users")
	}
	noMatch, ok := err.(*NoMatchError)
	if !ok {
		t.Fatalf("expected NoMatchError, got %T", err)
	}
	if noMatch.StatusCode() != 501 {
		t.Errorf("expected 501, got %d", noMatch.StatusCode())
	}
}

func TestMatch_NoMatch(t *testing.T) {
	m := New(buildRegistries(), buildPrefixes())
	_, err := m.Match("GET", "/api/unknown/path")
	if err == nil {
		t.Fatal("expected no match")
	}
}

func TestMatch_NestedPath(t *testing.T) {
	m := New(buildRegistries(), buildPrefixes())
	res, err := m.Match("GET", "/api/orders-svc/orders/ord-42/items")
	if err != nil {
		t.Fatal(err)
	}
	if res.Dependency != "order-service" {
		t.Errorf("expected order-service, got %s", res.Dependency)
	}
	if res.PathParams["orderId"] != "ord-42" {
		t.Errorf("expected orderId=ord-42, got %s", res.PathParams["orderId"])
	}
}

func TestMatch_LongestPrefixMatch(t *testing.T) {
	regs := []models.DependencyRegistry{
		{
			Dependency: "short",
			Endpoints:  []models.Endpoint{{Method: "GET", Path: "/data", StatusCode: 200}},
		},
		{
			Dependency: "long",
			Endpoints:  []models.Endpoint{{Method: "GET", Path: "/data", StatusCode: 200}},
		},
	}
	prefixes := map[string]string{
		"short": "/api",
		"long":  "/api/v2",
	}

	m := New(regs, prefixes)
	res, err := m.Match("GET", "/api/v2/data")
	if err != nil {
		t.Fatal(err)
	}
	if res.Dependency != "long" {
		t.Errorf("expected long (longest prefix), got %s", res.Dependency)
	}
}

func TestMatch_ColonParamSyntax(t *testing.T) {
	regs := []models.DependencyRegistry{
		{
			Dependency: "svc",
			Endpoints: []models.Endpoint{
				{Method: "GET", Path: "/items/:itemId", StatusCode: 200},
			},
		},
	}
	prefixes := map[string]string{"svc": "/"}

	m := New(regs, prefixes)
	res, err := m.Match("GET", "/items/xyz")
	if err != nil {
		t.Fatal(err)
	}
	if res.PathParams["itemId"] != "xyz" {
		t.Errorf("expected itemId=xyz, got %s", res.PathParams["itemId"])
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"", "/"},
		{"/", "/"},
		{"/users/", "/users"},
		{"users", "/users"},
		{"/api/v1/", "/api/v1"},
	}
	for _, tc := range tests {
		got := normalizePath(tc.input)
		if got != tc.want {
			t.Errorf("normalizePath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNormalizeParamSyntax(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{":id", "{id}"},
		{"{id}", "{id}"},
		{"users", "users"},
	}
	for _, tc := range tests {
		got := normalizeParamSyntax(tc.input)
		if got != tc.want {
			t.Errorf("normalizeParamSyntax(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseSegments(t *testing.T) {
	segs := parseSegments("/users/{id}/orders")
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(segs))
	}
	if segs[0].value != "users" || segs[0].isParam {
		t.Error("first segment wrong")
	}
	if segs[1].value != "id" || !segs[1].isParam {
		t.Error("second segment should be param 'id'")
	}
	if segs[2].value != "orders" || segs[2].isParam {
		t.Error("third segment wrong")
	}
}

func TestMatchSegments(t *testing.T) {
	pattern := parseSegments("/users/{id}")
	params, ok := matchSegments(pattern, "/users/abc")
	if !ok {
		t.Fatal("expected match")
	}
	if params["id"] != "abc" {
		t.Errorf("expected id=abc, got %s", params["id"])
	}
}

func TestMatchSegments_LengthMismatch(t *testing.T) {
	pattern := parseSegments("/users/{id}")
	_, ok := matchSegments(pattern, "/users/abc/extra")
	if ok {
		t.Fatal("expected no match for length mismatch")
	}
}

func TestMatchSegments_StaticMismatch(t *testing.T) {
	pattern := parseSegments("/users/{id}")
	_, ok := matchSegments(pattern, "/orders/abc")
	if ok {
		t.Fatal("expected no match for static segment mismatch")
	}
}
