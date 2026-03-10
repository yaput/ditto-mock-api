package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/ditto-mock/ditto-mock-api/internal/config"
	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

// LLMClient is the interface for making LLM API calls.
type LLMClient interface {
	ChatCompletion(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// LLMAnalyzer uses an LLM to resolve ambiguity in raw AST scan output.
type LLMAnalyzer struct {
	client LLMClient
	cfg    config.LLMConfig
	logger *slog.Logger
}

// NewLLMAnalyzer creates a new LLM-based analyzer.
func NewLLMAnalyzer(client LLMClient, cfg config.LLMConfig, logger *slog.Logger) *LLMAnalyzer {
	return &LLMAnalyzer{
		client: client,
		cfg:    cfg,
		logger: logger,
	}
}

// Analyze sends the raw scan output to the LLM and returns structured endpoints.
func (a *LLMAnalyzer) Analyze(scan *models.ScanOutput) ([]models.Endpoint, error) {
	prompt, err := buildAnalysisPrompt(scan)
	if err != nil {
		return nil, fmt.Errorf("building analysis prompt: %w", err)
	}

	systemPrompt := "You are an expert Go developer analyzing a microservice codebase. " +
		"Your job is to produce a structured endpoint registry from extracted code artifacts. " +
		"Return ONLY valid JSON — no markdown, no explanation, no wrapping."

	var lastErr error
	maxRetries := a.cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Timeout)

		a.logger.Debug("sending analysis request to LLM",
			"attempt", attempt+1,
			"repo", scan.Repo,
		)

		response, callErr := a.client.ChatCompletion(ctx, systemPrompt, prompt)
		cancel()

		if callErr != nil {
			lastErr = callErr
			a.logger.Warn("LLM call failed", "attempt", attempt+1, "error", callErr)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		a.logger.Debug("received LLM analysis response", "length", len(response))

		endpoints, parseErr := parseEndpointsResponse(response)
		if parseErr != nil {
			lastErr = parseErr
			a.logger.Warn("failed to parse LLM response", "attempt", attempt+1, "error", parseErr)
			continue
		}

		return endpoints, nil
	}

	return nil, fmt.Errorf("LLM analysis failed after %d attempts: %w", maxRetries+1, lastErr)
}

func buildAnalysisPrompt(scan *models.ScanOutput) (string, error) {
	structsJSON, err := json.MarshalIndent(scan.Structs, "", "  ")
	if err != nil {
		return "", err
	}
	routesJSON, err := json.MarshalIndent(scan.Routes, "", "  ")
	if err != nil {
		return "", err
	}
	handlersJSON, err := json.MarshalIndent(scan.Handlers, "", "  ")
	if err != nil {
		return "", err
	}

	prompt := fmt.Sprintf(`I will provide you with extracted code artifacts from a Go HTTP service (%s framework).

## Extracted Structs
%s

## Extracted Routes
%s

## Handler Function Summaries
%s

## Task
For each route, determine:
1. The HTTP method and path pattern (normalize path params to {param} format)
2. The request body struct (if any for POST/PUT/PATCH)
3. The response body struct for the success case
4. The expected success HTTP status code
5. A brief description of what the endpoint does

Match each route's handler to its handler summary via the "handler" and "name" fields.
Use the "decodes" field to identify request body struct, and "encodes" field to identify response body struct.
Then look up those struct names in the Extracted Structs and fully expand them.

## Output Format
Return a JSON array:
[
  {
    "method": "GET",
    "path": "/users/{id}",
    "description": "Get user by ID",
    "request_body": null,
    "response_body": {"type": "object", "fields": [{"name": "id", "type": "string", "json_key": "id", "required": true}]},
    "status_code": 200
  }
]

## CRITICAL Rules
- You MUST fully resolve and inline ALL nested struct types recursively
- Each field of type struct MUST have its "fields" array populated with the actual struct fields from Extracted Structs
- Each field of type []StructType MUST have an "items" object with the struct's fields fully expanded
- Do NOT return generic fields like {"name": "response", "type": "object"} without expanding the nested struct fields
- Use json tag names as "json_key", not Go field names
- Mark fields without "omitempty" as required: true
- Infer formats from Go types: time.Time -> "date-time", uuid.UUID -> "uuid"
- Normalize path params: :id -> {id}
- Return ONLY valid JSON`, scan.Framework, structsJSON, routesJSON, handlersJSON)

	return prompt, nil
}

func parseEndpointsResponse(response string) ([]models.Endpoint, error) {
	cleaned := cleanJSONResponse(response)

	var endpoints []models.Endpoint
	if err := json.Unmarshal([]byte(cleaned), &endpoints); err != nil {
		return nil, fmt.Errorf("parsing endpoints JSON: %w", err)
	}

	if len(endpoints) == 0 {
		return nil, fmt.Errorf("LLM returned empty endpoints array")
	}

	return endpoints, nil
}

// trailingCommaRe matches a comma followed by optional whitespace then ] or }.
var trailingCommaRe = regexp.MustCompile(`,\s*([}\]])`)

func cleanJSONResponse(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```json"); i >= 0 {
		s = s[i+7:]
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
	} else if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
	}
	s = strings.TrimSpace(s)
	// Remove trailing commas before } or ] — a common LLM output defect.
	s = trailingCommaRe.ReplaceAllString(s, "$1")
	return s
}
