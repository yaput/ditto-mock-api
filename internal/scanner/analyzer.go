package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/ditto-mock/ditto-mock-api/internal/config"
	"github.com/ditto-mock/ditto-mock-api/internal/llm"
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
// If the prompt exceeds the context window, the scan is automatically chunked
// into smaller pieces that each fit within the token limit.
func (a *LLMAnalyzer) Analyze(scan *models.ScanOutput) ([]models.Endpoint, error) {
	// Reserve tokens for system prompt, response, and overhead.
	maxPromptTokens := a.cfg.MaxContextTokens - a.cfg.MaxTokens - promptOverhead
	if maxPromptTokens < 4000 {
		maxPromptTokens = 4000
	}

	chunks := ChunkScanOutput(scan, maxPromptTokens, a.cfg.MaxTokens)

	a.logger.Info("analysis chunking decision",
		"repo", scan.Repo,
		"total_routes", len(scan.Routes),
		"total_structs", len(scan.Structs),
		"total_handlers", len(scan.Handlers),
		"chunks", len(chunks),
		"max_prompt_tokens", maxPromptTokens,
	)

	var allEndpoints []models.Endpoint
	for i, chunk := range chunks {
		a.logger.Info("analyzing chunk",
			"chunk", fmt.Sprintf("%d/%d", i+1, len(chunks)),
			"routes", len(chunk.Routes),
			"structs", len(chunk.Structs),
			"handlers", len(chunk.Handlers),
		)

		endpoints, err := a.analyzeChunk(chunk)
		if err != nil {
			return nil, fmt.Errorf("analyzing chunk %d/%d: %w", i+1, len(chunks), err)
		}
		allEndpoints = append(allEndpoints, endpoints...)
	}

	if len(allEndpoints) == 0 {
		return nil, fmt.Errorf("LLM returned no endpoints across %d chunks", len(chunks))
	}

	return allEndpoints, nil
}

// analyzeChunk sends a single chunk to the LLM with retries.
// If the response is truncated, it attempts JSON repair to salvage partial results.
// If repair fails and the chunk has multiple routes, it splits and retries recursively.
func (a *LLMAnalyzer) analyzeChunk(scan *models.ScanOutput) ([]models.Endpoint, error) {
	prompt, err := buildAnalysisPrompt(scan)
	if err != nil {
		return nil, fmt.Errorf("building analysis prompt: %w", err)
	}

	a.logger.Debug("chunk prompt stats",
		"repo", scan.Repo,
		"prompt_chars", len(prompt),
		"estimated_tokens", estimateTokens(prompt),
	)

	sysPrompt := "You are an expert Go developer analyzing a microservice codebase. " +
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

		response, callErr := a.client.ChatCompletion(ctx, sysPrompt, prompt)
		cancel()

		truncated := errors.Is(callErr, llm.ErrResponseTruncated)
		if callErr != nil && !truncated {
			lastErr = callErr
			a.logger.Warn("LLM call failed", "attempt", attempt+1, "error", callErr)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		if truncated {
			a.logger.Warn("LLM response truncated (max_tokens reached)",
				"attempt", attempt+1,
				"response_len", len(response),
				"routes", len(scan.Routes),
			)
			// Try to repair the truncated JSON and salvage partial results.
			endpoints, repairErr := repairTruncatedJSON(response)
			if repairErr == nil && len(endpoints) > 0 {
				a.logger.Info("salvaged partial endpoints from truncated response",
					"salvaged", len(endpoints),
					"routes_in_chunk", len(scan.Routes),
				)
				return endpoints, nil
			}

			// Repair failed — if we have multiple routes, split the chunk in half and recurse.
			if len(scan.Routes) > 1 {
				a.logger.Info("splitting chunk due to truncation",
					"routes", len(scan.Routes),
				)
				return a.splitAndRetry(scan)
			}

			// Single route that still overflows — nothing more we can do.
			lastErr = fmt.Errorf("response truncated for single-route chunk and repair failed")
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

// splitAndRetry splits a chunk in half and analyzes each half independently.
func (a *LLMAnalyzer) splitAndRetry(scan *models.ScanOutput) ([]models.Endpoint, error) {
	mid := len(scan.Routes) / 2

	structIndex := buildStructIndex(scan.Structs)
	handlerIndex := make(map[string]models.ExtractedHandler, len(scan.Handlers))
	for _, h := range scan.Handlers {
		handlerIndex[h.Name] = h
	}

	buildHalf := func(routes []models.ExtractedRoute) *models.ScanOutput {
		handlers := collectHandlers(routes, handlerIndex)
		refs := referencedStructNames(handlers, structIndex)
		structs := collectStructs(refs, structIndex)
		return &models.ScanOutput{
			Repo:      scan.Repo,
			Framework: scan.Framework,
			Routes:    routes,
			Handlers:  handlers,
			Structs:   structs,
		}
	}

	firstHalf := buildHalf(scan.Routes[:mid])
	secondHalf := buildHalf(scan.Routes[mid:])

	ep1, err := a.analyzeChunk(firstHalf)
	if err != nil {
		return nil, fmt.Errorf("analyzing first half after split: %w", err)
	}

	ep2, err := a.analyzeChunk(secondHalf)
	if err != nil {
		return nil, fmt.Errorf("analyzing second half after split: %w", err)
	}

	return append(ep1, ep2...), nil
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

// repairTruncatedJSON attempts to extract valid endpoint objects from truncated JSON.
// It finds the last complete object in the array by searching backwards for "},"
// or "}" followed by "]" that forms valid JSON when the array is closed.
func repairTruncatedJSON(response string) ([]models.Endpoint, error) {
	cleaned := cleanJSONResponse(response)

	// Find the start of the array.
	arrStart := strings.Index(cleaned, "[")
	if arrStart < 0 {
		return nil, fmt.Errorf("no JSON array found in truncated response")
	}

	// Try progressively shorter suffixes, looking for a point where
	// closing brackets produces valid JSON.
	content := cleaned[arrStart:]

	// Strategy: find the last complete object by looking for "}," or "}" patterns
	// from the end and try closing the array there.
	for i := len(content) - 1; i > 0; i-- {
		if content[i] != '}' {
			continue
		}
		// Try closing the array right after this '}'.
		candidate := content[:i+1]
		// Strip any trailing comma.
		candidate = strings.TrimRight(candidate, ", \t\n\r")
		if !strings.HasSuffix(candidate, "}") {
			continue
		}
		candidate += "]"

		var endpoints []models.Endpoint
		if err := json.Unmarshal([]byte(candidate), &endpoints); err == nil && len(endpoints) > 0 {
			return endpoints, nil
		}
	}

	return nil, fmt.Errorf("could not repair truncated JSON")
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
