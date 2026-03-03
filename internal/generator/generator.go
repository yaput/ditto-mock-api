package generator

import (
	"context"

	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

// MockResponse is the generated mock HTTP response.
type MockResponse struct {
	StatusCode int
	Headers    map[string]string
	Body       string
}

// RequestContext holds the incoming request details for generation.
type RequestContext struct {
	Method      string
	Path        string
	PathParams  map[string]string
	QueryParams map[string]string
	Body        string
}

// Generator creates mock responses from endpoint definitions.
type Generator interface {
	Generate(ctx context.Context, endpoint models.Endpoint, req RequestContext) (*MockResponse, error)
}
