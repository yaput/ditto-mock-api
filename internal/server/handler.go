package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/ditto-mock/ditto-mock-api/internal/cache"
	"github.com/ditto-mock/ditto-mock-api/internal/generator"
	"github.com/ditto-mock/ditto-mock-api/internal/matcher"
	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

// handleMock is the catch-all handler for all non-admin requests.
// Flow: match → cache lookup → generate → cache store → respond.
func (s *Server) handleMock(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Match the request to an endpoint.
	result, err := s.matcher.Match(r.Method, r.URL.Path)
	if err != nil {
		var noMatch *matcher.NoMatchError
		if errors.As(err, &noMatch) {
			writeJSON(w, noMatch.StatusCode(), map[string]string{
				"error":  "no matching endpoint",
				"method": r.Method,
				"path":   r.URL.Path,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// Read request body for cache key + prompt context.
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	bodyHash := hashBody(body)

	// Build cache key.
	query := r.URL.RawQuery
	cacheKey := cache.BuildKey(r.Method, r.URL.Path, query, bodyHash)

	// Cache lookup.
	if s.cache != nil {
		cached, cacheErr := s.cache.Get(cacheKey)
		if cacheErr == nil && cached != nil {
			s.logger.Debug("cache hit",
				"method", r.Method,
				"path", r.URL.Path,
				"dependency", result.Dependency,
			)
			writeCachedResponse(w, cached)
			return
		}
	}

	// Cache miss — generate via LLM.
	s.logger.Debug("cache miss, generating",
		"method", r.Method,
		"path", r.URL.Path,
		"dependency", result.Dependency,
	)

	reqCtx := generator.RequestContext{
		Method:      r.Method,
		Path:        r.URL.Path,
		PathParams:  result.PathParams,
		QueryParams: queryToMap(r.URL.Query()),
		Body:        string(body),
	}

	resp, genErr := s.generator.Generate(ctx, result.Endpoint, reqCtx)
	if genErr != nil {
		s.logger.Error("generation failed", "error", genErr, "path", r.URL.Path)
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "failed to generate mock response",
		})
		return
	}

	// Store in cache.
	if s.cache != nil {
		headersJSON, _ := json.Marshal(resp.Headers)
		entry := &models.CachedResponse{
			KeyHash:         cacheKey,
			Method:          r.Method,
			Path:            r.URL.Path,
			Query:           query,
			RequestBodyHash: bodyHash,
			ResponseStatus:  resp.StatusCode,
			ResponseHeaders: string(headersJSON),
			ResponseBody:    resp.Body,
			Dependency:      result.Dependency,
		}
		if putErr := s.cache.Put(entry); putErr != nil {
			s.logger.Warn("cache put failed", "error", putErr)
		}
	}

	// Write response.
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(resp.StatusCode)
	io.WriteString(w, resp.Body)
}

func writeCachedResponse(w http.ResponseWriter, cached *models.CachedResponse) {
	var headers map[string]string
	if cached.ResponseHeaders != "" {
		json.Unmarshal([]byte(cached.ResponseHeaders), &headers)
	}
	for k, v := range headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(cached.ResponseStatus)
	io.WriteString(w, cached.ResponseBody)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func hashBody(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

func queryToMap(q map[string][]string) map[string]string {
	out := make(map[string]string, len(q))
	for k, v := range q {
		out[k] = strings.Join(v, ",")
	}
	return out
}
