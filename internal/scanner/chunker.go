package scanner

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

// estimateTokens provides a rough token count for a string.
// GPT models average ~4 characters per token for English/code.
// We use 3.5 as a conservative estimate to avoid undercount.
func estimateTokens(s string) int {
	return (len(s)*10 + 34) / 35 // equivalent to ceil(len/3.5)
}

// promptOverhead is the approximate token count of the system prompt
// plus the static parts of the analysis prompt template.
const promptOverhead = 800

// tokensPerEndpoint is a conservative estimate of response tokens per
// endpoint object (method, path, description, expanded request/response bodies).
const tokensPerEndpoint = 400

// referencedStructNames collects struct names that appear in the handler
// Decodes/Encodes fields or as field types in the given structs.
func referencedStructNames(handlers []models.ExtractedHandler, structIndex map[string]models.ExtractedStruct) map[string]bool {
	names := make(map[string]bool)
	for _, h := range handlers {
		if h.Decodes != "" {
			names[h.Decodes] = true
		}
		if h.Encodes != "" {
			names[h.Encodes] = true
		}
	}

	// Expand transitively: if struct A has a field of type B, include B.
	changed := true
	for changed {
		changed = false
		for name := range names {
			s, ok := structIndex[name]
			if !ok {
				continue
			}
			for _, f := range s.Fields {
				typeName := stripSlicePrefix(f.Type)
				if _, exists := structIndex[typeName]; exists && !names[typeName] {
					names[typeName] = true
					changed = true
				}
			}
		}
	}
	return names
}

// stripSlicePrefix removes [] prefix from type names like "[]User".
func stripSlicePrefix(t string) string {
	for len(t) > 0 && t[0] == '[' {
		if len(t) > 1 && t[1] == ']' {
			t = t[2:]
		} else {
			break
		}
	}
	return t
}

// buildStructIndex creates a lookup map from struct name to ExtractedStruct.
func buildStructIndex(structs []models.ExtractedStruct) map[string]models.ExtractedStruct {
	idx := make(map[string]models.ExtractedStruct, len(structs))
	for _, s := range structs {
		idx[s.Name] = s
	}
	return idx
}

// routeChunk groups routes with their matched handlers and required structs.
type routeChunk struct {
	routes   []models.ExtractedRoute
	handlers []models.ExtractedHandler
	structs  []models.ExtractedStruct
}

// estimateChunkTokens estimates the token count for a chunk when serialized as a prompt.
func estimateChunkTokens(chunk routeChunk, framework string) int {
	// Build a minimal ScanOutput and serialize to estimate size.
	scan := models.ScanOutput{
		Framework: framework,
		Structs:   chunk.structs,
		Routes:    chunk.routes,
		Handlers:  chunk.handlers,
	}
	prompt, err := buildAnalysisPrompt(&scan)
	if err != nil {
		// Fallback: rough estimate from JSON sizes.
		sj, _ := json.Marshal(chunk.structs)
		rj, _ := json.Marshal(chunk.routes)
		hj, _ := json.Marshal(chunk.handlers)
		return estimateTokens(string(sj) + string(rj) + string(hj))
	}
	return estimateTokens(prompt)
}

// ChunkScanOutput splits a large ScanOutput into multiple smaller chunks
// that each fit within maxPromptTokens when rendered as an analysis prompt.
// maxResponseTokens caps how many routes go into a single chunk so the LLM
// response (endpoint JSON array) can fit within the output token limit.
// Routes are grouped, and only the structs referenced by each group's handlers are included.
func ChunkScanOutput(scan *models.ScanOutput, maxPromptTokens, maxResponseTokens int) []*models.ScanOutput {
	// Derive the max routes per chunk from the response token budget.
	maxRoutes := maxResponseTokens / tokensPerEndpoint
	if maxRoutes < 1 {
		maxRoutes = 1
	}

	// Fast path: if the prompt fits AND route count is within response budget,
	// send everything in one chunk.
	if len(scan.Routes) <= maxRoutes {
		fullPrompt, err := buildAnalysisPrompt(scan)
		if err == nil && estimateTokens(fullPrompt)+promptOverhead <= maxPromptTokens {
			return []*models.ScanOutput{scan}
		}
	}

	structIndex := buildStructIndex(scan.Structs)

	// Build a handler lookup: handler name → ExtractedHandler.
	handlerIndex := make(map[string]models.ExtractedHandler, len(scan.Handlers))
	for _, h := range scan.Handlers {
		handlerIndex[h.Name] = h
	}

	// Budget for user prompt content (subtract overhead for system prompt + response tokens).
	budgetTokens := maxPromptTokens - promptOverhead
	if budgetTokens < 2000 {
		budgetTokens = 2000
	}

	var chunks []*models.ScanOutput
	var currentRoutes []models.ExtractedRoute

	for _, route := range scan.Routes {
		currentRoutes = append(currentRoutes, route)

		// Check response budget: too many routes means the response won't fit.
		if len(currentRoutes) > maxRoutes && len(currentRoutes) > 1 {
			emitRoutes := currentRoutes[:len(currentRoutes)-1]
			emitHandlers := collectHandlers(emitRoutes, handlerIndex)
			emitRefs := referencedStructNames(emitHandlers, structIndex)
			emitStructs := collectStructs(emitRefs, structIndex)

			chunks = append(chunks, &models.ScanOutput{
				Repo:      scan.Repo,
				Framework: scan.Framework,
				Structs:   emitStructs,
				Routes:    emitRoutes,
				Handlers:  emitHandlers,
			})

			currentRoutes = []models.ExtractedRoute{route}
			continue
		}

		// Collect handlers for current routes.
		handlers := collectHandlers(currentRoutes, handlerIndex)
		refs := referencedStructNames(handlers, structIndex)
		structs := collectStructs(refs, structIndex)

		chunk := routeChunk{
			routes:   currentRoutes,
			handlers: handlers,
			structs:  structs,
		}

		tokens := estimateChunkTokens(chunk, scan.Framework)
		if tokens > budgetTokens && len(currentRoutes) > 1 {
			// Current batch exceeds input budget. Emit everything except the last route.
			emitRoutes := currentRoutes[:len(currentRoutes)-1]
			emitHandlers := collectHandlers(emitRoutes, handlerIndex)
			emitRefs := referencedStructNames(emitHandlers, structIndex)
			emitStructs := collectStructs(emitRefs, structIndex)

			chunks = append(chunks, &models.ScanOutput{
				Repo:      scan.Repo,
				Framework: scan.Framework,
				Structs:   emitStructs,
				Routes:    emitRoutes,
				Handlers:  emitHandlers,
			})

			// Start a new batch with the last route.
			currentRoutes = []models.ExtractedRoute{route}
		}
	}

	// Emit remaining routes.
	if len(currentRoutes) > 0 {
		handlers := collectHandlers(currentRoutes, handlerIndex)
		refs := referencedStructNames(handlers, structIndex)
		structs := collectStructs(refs, structIndex)

		chunks = append(chunks, &models.ScanOutput{
			Repo:      scan.Repo,
			Framework: scan.Framework,
			Structs:   structs,
			Routes:    currentRoutes,
			Handlers:  handlers,
		})
	}

	// Safety: if a single route exceeds the budget, we still send it
	// (the LLM will need to handle it, or the caller should increase the limit).
	if len(chunks) == 0 {
		chunks = append(chunks, scan)
	}

	return chunks
}

// collectHandlers returns handlers referenced by the given routes.
// It tries exact match first, then falls back to matching the unqualified
// function name (after the last ".") to handle cases like "ctrl.GetUser"
// matching handler name "GetUser".
func collectHandlers(routes []models.ExtractedRoute, handlerIndex map[string]models.ExtractedHandler) []models.ExtractedHandler {
	seen := make(map[string]bool, len(routes))
	var result []models.ExtractedHandler
	for _, r := range routes {
		if seen[r.Handler] {
			continue
		}
		seen[r.Handler] = true
		if h, ok := handlerIndex[r.Handler]; ok {
			result = append(result, h)
			continue
		}
		// Fallback: strip package/receiver prefix (e.g., "ctrl.GetUser" → "GetUser").
		if i := strings.LastIndex(r.Handler, "."); i >= 0 {
			shortName := r.Handler[i+1:]
			if h, ok := handlerIndex[shortName]; ok {
				result = append(result, h)
			}
		}
	}
	return result
}

// collectStructs returns structs whose names are in the refs set.
func collectStructs(refs map[string]bool, structIndex map[string]models.ExtractedStruct) []models.ExtractedStruct {
	var result []models.ExtractedStruct
	for name := range refs {
		if s, ok := structIndex[name]; ok {
			result = append(result, s)
		}
	}
	return result
}

// FormatChunkSummary returns a human-readable summary of chunking decisions.
func FormatChunkSummary(scan *models.ScanOutput, chunks []*models.ScanOutput) string {
	if len(chunks) <= 1 {
		return fmt.Sprintf("scan fits in single prompt (routes=%d, structs=%d, handlers=%d)",
			len(scan.Routes), len(scan.Structs), len(scan.Handlers))
	}
	summary := fmt.Sprintf("scan split into %d chunks (total: routes=%d, structs=%d, handlers=%d):\n",
		len(chunks), len(scan.Routes), len(scan.Structs), len(scan.Handlers))
	for i, c := range chunks {
		summary += fmt.Sprintf("  chunk %d: routes=%d, structs=%d, handlers=%d\n",
			i+1, len(c.Routes), len(c.Structs), len(c.Handlers))
	}
	return summary
}
