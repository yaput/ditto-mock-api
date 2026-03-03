// Package models defines shared domain types used across the application.
package models

import "time"

// FieldSchema describes a single field in a request or response body.
type FieldSchema struct {
	Name      string        `json:"name"`
	Type      string        `json:"type"`
	JSONKey   string        `json:"json_key"`
	Format    string        `json:"format,omitempty"`
	Required  bool          `json:"required"`
	Fields    []FieldSchema `json:"fields,omitempty"`
	Items     *FieldSchema  `json:"items,omitempty"`
	Enum      []string      `json:"enum,omitempty"`
	Omitempty bool          `json:"omitempty,omitempty"`
}

// BodySchema describes the shape of a request or response body.
type BodySchema struct {
	Type   string        `json:"type"`
	Fields []FieldSchema `json:"fields,omitempty"`
}

// Endpoint represents a single discovered API endpoint.
type Endpoint struct {
	Method       string      `json:"method"`
	Path         string      `json:"path"`
	Description  string      `json:"description"`
	RequestBody  *BodySchema `json:"request_body"`
	ResponseBody *BodySchema `json:"response_body"`
	StatusCode   int         `json:"status_code"`
}

// DependencyRegistry holds scan results for one dependency.
type DependencyRegistry struct {
	ScannedAt         time.Time  `json:"scanned_at"`
	Dependency        string     `json:"dependency"`
	RepoPath          string     `json:"repo_path"`
	FrameworkDetected string     `json:"framework_detected"`
	Endpoints         []Endpoint `json:"endpoints"`
}

// ---- Scanner intermediate types ----

// StructField is a field extracted from a Go struct via AST.
type StructField struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	JSONTag   string `json:"json_tag"`
	Omitempty bool   `json:"omitempty"`
}

// ExtractedStruct is a Go struct extracted via AST.
type ExtractedStruct struct {
	Name    string        `json:"name"`
	Package string        `json:"package"`
	File    string        `json:"file"`
	Fields  []StructField `json:"fields"`
}

// ExtractedRoute is a route registration extracted via AST.
type ExtractedRoute struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Handler string `json:"handler"`
	File    string `json:"file"`
	Line    int    `json:"line"`
}

// ExtractedHandler is a handler function summary extracted via AST.
type ExtractedHandler struct {
	Name        string `json:"name"`
	Receiver    string `json:"receiver"`
	File        string `json:"file"`
	Decodes     string `json:"decodes"`
	Encodes     string `json:"encodes"`
	StatusCodes []int  `json:"status_codes"`
}

// ScanOutput is the complete AST scan output for one dependency.
type ScanOutput struct {
	Repo      string             `json:"repo"`
	Framework string             `json:"framework"`
	Structs   []ExtractedStruct  `json:"structs"`
	Routes    []ExtractedRoute   `json:"routes"`
	Handlers  []ExtractedHandler `json:"handlers"`
}

// ---- Cache types ----

// CachedResponse stores a cached mock response.
type CachedResponse struct {
	KeyHash         string    `json:"key_hash"`
	Method          string    `json:"method"`
	Path            string    `json:"path"`
	Query           string    `json:"query"`
	RequestBodyHash string    `json:"request_body_hash"`
	ResponseStatus  int       `json:"response_status"`
	ResponseHeaders string    `json:"response_headers"`
	ResponseBody    string    `json:"response_body"`
	Dependency      string    `json:"dependency"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// CacheStats holds cache statistics.
type CacheStats struct {
	TotalEntries int   `json:"total_entries"`
	TotalSize    int64 `json:"total_size_bytes"`
	HitCount     int64 `json:"hit_count"`
	MissCount    int64 `json:"miss_count"`
}
