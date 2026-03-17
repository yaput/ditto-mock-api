package scanner

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ditto-mock/ditto-mock-api/internal/config"
)

// GoModDependency represents a single require entry from go.mod.
type GoModDependency struct {
	Module  string // e.g., "github.com/zeals-co-ltd/talkroom-ticketing"
	Version string // e.g., "v0.0.7-..."
}

// ParseGoMod reads a go.mod file and extracts all require dependencies.
func ParseGoMod(path string) ([]GoModDependency, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening go.mod at %s: %w", path, err)
	}
	defer f.Close()

	var deps []GoModDependency
	inRequireBlock := false
	sc := bufio.NewScanner(f)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())

		// Skip comments and empty lines.
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}

		// Detect require block boundaries.
		if strings.HasPrefix(line, "require (") || line == "require (" {
			inRequireBlock = true
			continue
		}
		if inRequireBlock && line == ")" {
			inRequireBlock = false
			continue
		}

		// Single-line require: require github.com/foo/bar v1.0.0
		if strings.HasPrefix(line, "require ") && !strings.Contains(line, "(") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				deps = append(deps, GoModDependency{Module: parts[1], Version: parts[2]})
			}
			continue
		}

		// Inside require block: github.com/foo/bar v1.0.0
		if inRequireBlock {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				deps = append(deps, GoModDependency{Module: parts[0], Version: parts[1]})
			}
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scanning go.mod: %w", err)
	}

	return deps, nil
}

// FilterByOrgPrefix returns only dependencies whose module path starts with one of the given prefixes.
func FilterByOrgPrefix(deps []GoModDependency, prefixes []string) []GoModDependency {
	var filtered []GoModDependency
	for _, dep := range deps {
		for _, prefix := range prefixes {
			if strings.HasPrefix(dep.Module, prefix) {
				filtered = append(filtered, dep)
				break
			}
		}
	}
	return filtered
}

// ResolveRepoPaths maps discovered go.mod modules to local filesystem paths.
// It uses the last segment of the module path as the directory name under workspaceRoot,
// with optional explicit overrides from repoMap.
func ResolveRepoPaths(deps []GoModDependency, workspaceRoot string, repoMap map[string]string) map[string]string {
	result := make(map[string]string, len(deps))
	for _, dep := range deps {
		// Check explicit repo_map override first.
		if override, ok := repoMap[dep.Module]; ok {
			result[dep.Module] = override
			continue
		}
		// Default: last path segment of module under workspace root.
		parts := strings.Split(dep.Module, "/")
		repoName := parts[len(parts)-1]
		result[dep.Module] = filepath.Join(workspaceRoot, repoName)
	}
	return result
}

// ModuleToServiceName derives a service name from a Go module path.
// e.g., "github.com/zeals-co-ltd/talkroom-ticketing" -> "talkroom-ticketing"
func ModuleToServiceName(module string) string {
	parts := strings.Split(module, "/")
	return parts[len(parts)-1]
}

// ModuleToPrefix derives an API prefix from a service name.
// e.g., "talkroom-ticketing" -> "/talkroom-ticketing"
func ModuleToPrefix(serviceName string) string {
	return "/" + serviceName
}

// DiscoverDependencies runs the full discovery pipeline: parse go.mod, filter by org,
// resolve local paths, discover HTTP service dependencies, and return DependencyConfig
// entries ready for scanning.
func DiscoverDependencies(discoveryCfg config.DiscoveryConfig) ([]config.DependencyConfig, error) {
	goModPath := filepath.Join(discoveryCfg.ServiceRepo, "go.mod")

	allDeps, err := ParseGoMod(goModPath)
	if err != nil {
		return nil, fmt.Errorf("parsing go.mod for discovery: %w", err)
	}

	orgDeps := FilterByOrgPrefix(allDeps, discoveryCfg.OrgPrefixes)

	repoPaths := ResolveRepoPaths(orgDeps, discoveryCfg.WorkspaceRoot, discoveryCfg.RepoMap)

	seen := make(map[string]bool)
	var configs []config.DependencyConfig
	for _, dep := range orgDeps {
		repoPath := repoPaths[dep.Module]

		// Verify the repo directory exists.
		if _, err := os.Stat(repoPath); os.IsNotExist(err) {
			continue // skip deps whose repos aren't available locally
		}

		serviceName := ModuleToServiceName(dep.Module)
		seen[serviceName] = true
		configs = append(configs, config.DependencyConfig{
			Name:     serviceName,
			Prefix:   ModuleToPrefix(serviceName),
			RepoPath: repoPath,
		})
	}

	// Discover HTTP service dependencies (services called via HTTP clients).
	httpDeps, err := DiscoverHTTPServices(discoveryCfg)
	if err != nil {
		return configs, nil // non-fatal: return go.mod results even if HTTP discovery fails
	}
	for _, dep := range httpDeps {
		if seen[dep.Name] {
			continue // already discovered via go.mod
		}
		seen[dep.Name] = true
		configs = append(configs, dep)
	}

	return configs, nil
}
