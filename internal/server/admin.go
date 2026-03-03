package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleHealth returns a simple health check response.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleRegistryList returns a summary of all dependency registries.
func (s *Server) handleRegistryList(w http.ResponseWriter, _ *http.Request) {
	type depSummary struct {
		Name      string `json:"name"`
		Endpoints int    `json:"endpoints"`
		Framework string `json:"framework"`
	}
	summaries := make([]depSummary, 0, len(s.registries))
	for _, reg := range s.registries {
		summaries = append(summaries, depSummary{
			Name:      reg.Dependency,
			Endpoints: len(reg.Endpoints),
			Framework: reg.FrameworkDetected,
		})
	}
	writeJSON(w, http.StatusOK, summaries)
}

// handleRegistryDetail returns the full registry for a specific dependency.
func (s *Server) handleRegistryDetail(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/_ditto/registry/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing dependency name"})
		return
	}
	for _, reg := range s.registries {
		if reg.Dependency == name {
			writeJSON(w, http.StatusOK, reg)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "dependency not found", "name": name})
}

// handleScanAll triggers a re-scan of all dependencies.
func (s *Server) handleScanAll(w http.ResponseWriter, r *http.Request) {
	if s.scanFunc == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "scan not configured"})
		return
	}
	registries, err := s.scanFunc(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.registries = registries
	writeJSON(w, http.StatusOK, map[string]string{"status": "scan complete"})
}

// handleScanDependency triggers a re-scan of a single dependency.
func (s *Server) handleScanDependency(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/_ditto/scan/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing dependency name"})
		return
	}
	if s.scanFunc == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "scan not configured"})
		return
	}
	// For MVP, re-scan all and return. Can optimize later.
	registries, err := s.scanFunc(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.registries = registries
	writeJSON(w, http.StatusOK, map[string]string{"status": "scan complete", "dependency": name})
}

// handleCachePurgeAll clears the entire cache.
func (s *Server) handleCachePurgeAll(w http.ResponseWriter, _ *http.Request) {
	if s.cache == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "cache disabled"})
		return
	}
	deleted, err := s.cache.Purge("")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "purged", "deleted": deleted})
}

// handleCachePurgeDep clears cache for a specific dependency.
func (s *Server) handleCachePurgeDep(w http.ResponseWriter, r *http.Request) {
	dep := strings.TrimPrefix(r.URL.Path, "/_ditto/cache/")
	if dep == "" || dep == "stats" {
		// Route to stats handler if path is /_ditto/cache/stats
		if dep == "stats" {
			s.handleCacheStats(w, r)
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing dependency name"})
		return
	}
	if s.cache == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "cache disabled"})
		return
	}
	deleted, err := s.cache.Purge(dep)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "purged", "dependency": dep, "deleted": deleted})
}

// handleCacheStats returns cache statistics.
func (s *Server) handleCacheStats(w http.ResponseWriter, _ *http.Request) {
	if s.cache == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "cache disabled"})
		return
	}
	stats, err := s.cache.Stats()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// handleConfig returns the current running config with API key redacted.
func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	// Deep copy via JSON round-trip to avoid mutating original.
	data, _ := json.Marshal(s.cfg)
	var redacted map[string]any
	json.Unmarshal(data, &redacted)

	// Redact API key. Config uses yaml tags, so json.Marshal uses Go field names.
	if llm, ok := redacted["LLM"].(map[string]any); ok {
		if _, hasKey := llm["APIKey"]; hasKey {
			llm["APIKey"] = "***REDACTED***"
		}
	}

	writeJSON(w, http.StatusOK, redacted)
}
