package matcher

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

// MatchResult holds the result of matching an incoming request to an endpoint.
type MatchResult struct {
	Dependency string
	Endpoint   models.Endpoint
	PathParams map[string]string
}

// Matcher resolves incoming HTTP requests to the appropriate endpoint definition.
type Matcher struct {
	entries []registryEntry
}

type registryEntry struct {
	prefix   string
	depName  string
	endpoint models.Endpoint
	segments []segment
}

type segment struct {
	value   string
	isParam bool
}

// New builds a Matcher from the scanned dependency registries.
func New(registries []models.DependencyRegistry, prefixes map[string]string) *Matcher {
	var entries []registryEntry

	for _, reg := range registries {
		prefix, ok := prefixes[reg.Dependency]
		if !ok {
			prefix = "/"
		}
		prefix = normalizePath(prefix)

		for _, ep := range reg.Endpoints {
			normalizedPath := normalizePath(ep.Path)
			segs := parseSegments(normalizedPath)
			entries = append(entries, registryEntry{
				prefix:   prefix,
				depName:  reg.Dependency,
				endpoint: ep,
				segments: segs,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return len(entries[i].prefix) > len(entries[j].prefix)
	})

	return &Matcher{entries: entries}
}

// Match finds the best matching endpoint for the given HTTP request.
func (m *Matcher) Match(method, path string) (*MatchResult, error) {
	path = normalizePath(path)

	for _, entry := range m.entries {
		if !strings.HasPrefix(path, entry.prefix) {
			continue
		}

		stripped := path[len(entry.prefix):]
		if stripped == "" {
			stripped = "/"
		} else if !strings.HasPrefix(stripped, "/") {
			stripped = "/" + stripped
		}

		if entry.endpoint.Method != method && entry.endpoint.Method != "ANY" {
			continue
		}

		params, ok := matchSegments(entry.segments, stripped)
		if !ok {
			continue
		}

		return &MatchResult{
			Dependency: entry.depName,
			Endpoint:   entry.endpoint,
			PathParams: params,
		}, nil
	}

	return nil, &NoMatchError{Method: method, Path: path}
}

// NoMatchError is returned when no endpoint matches the request.
type NoMatchError struct {
	Method string
	Path   string
}

func (e *NoMatchError) Error() string {
	return fmt.Sprintf("no matching endpoint for %s %s", e.Method, e.Path)
}

func (e *NoMatchError) StatusCode() int {
	return http.StatusNotImplemented
}

func normalizePath(p string) string {
	if p == "" || p == "/" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.TrimRight(p, "/")
	return p
}

func normalizeParamSyntax(s string) string {
	if strings.HasPrefix(s, ":") {
		return "{" + s[1:] + "}"
	}
	return s
}

func parseSegments(path string) []segment {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	segs := make([]segment, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		p = normalizeParamSyntax(p)
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			segs = append(segs, segment{value: p[1 : len(p)-1], isParam: true})
		} else {
			segs = append(segs, segment{value: p})
		}
	}
	return segs
}

func matchSegments(pattern []segment, path string) (map[string]string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	filtered := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			filtered = append(filtered, p)
		}
	}

	if len(filtered) != len(pattern) {
		return nil, false
	}

	params := make(map[string]string)
	for i, seg := range pattern {
		if seg.isParam {
			params[seg.value] = filtered[i]
		} else if seg.value != filtered[i] {
			return nil, false
		}
	}

	return params, true
}
