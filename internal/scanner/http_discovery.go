package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/ditto-mock/ditto-mock-api/internal/config"
)

// DiscoverHTTPServices scans Go source files in the service repo for config address
// field patterns (e.g., Config.TalkroomTicketingServiceAddress) and derives HTTP
// service dependency names from them.
//
// The algorithm:
//  1. Walk all .go files in serviceRepo
//  2. Match patterns like `Config.XxxServiceAddress` (suffix is configurable)
//  3. Strip the suffix to get CamelCase service name (e.g., "TalkroomTicketing")
//  4. Convert to kebab-case (e.g., "talkroom-ticketing")
//  5. Resolve to local repo path under workspaceRoot
//  6. Skip services whose repos don't exist locally
func DiscoverHTTPServices(discoveryCfg config.DiscoveryConfig) ([]config.DependencyConfig, error) {
	if !discoveryCfg.HTTPDiscovery.Enabled {
		return nil, nil
	}

	suffix := discoveryCfg.HTTPDiscovery.AddressSuffix
	if suffix == "" {
		suffix = "ServiceAddress"
	}

	// Build regex: match Config.Xxx<suffix> where Xxx is one or more CamelCase words.
	// Captures the part before the suffix.
	pattern := regexp.MustCompile(`\bConfig\.(\w+?)` + regexp.QuoteMeta(suffix) + `\b`)

	serviceNames := make(map[string]bool)

	err := filepath.Walk(discoveryCfg.ServiceRepo, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable paths
		}
		if info.IsDir() {
			base := info.Name()
			if base == "vendor" || base == ".git" || base == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		matches := pattern.FindAllSubmatch(data, -1)
		for _, m := range matches {
			camelName := string(m[1])
			kebab := camelToKebab(camelName)
			serviceNames[kebab] = true
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking service repo for HTTP discovery: %w", err)
	}

	if len(serviceNames) == 0 {
		return nil, nil
	}

	var configs []config.DependencyConfig
	for serviceName := range serviceNames {
		repoPath := resolveHTTPServicePath(serviceName, discoveryCfg)
		if repoPath == "" {
			continue
		}
		configs = append(configs, config.DependencyConfig{
			Name:     serviceName,
			Prefix:   "/",
			RepoPath: repoPath,
		})
	}

	return configs, nil
}

// resolveHTTPServicePath finds the local repo path for an HTTP service.
// It checks repo_map first, then falls back to workspace_root/<service-name>.
func resolveHTTPServicePath(serviceName string, cfg config.DiscoveryConfig) string {
	// Check repo_map for explicit override (by service name).
	if override, ok := cfg.RepoMap[serviceName]; ok {
		if _, err := os.Stat(override); err == nil {
			return override
		}
	}

	// Default: workspace_root/<service-name>
	repoPath := filepath.Join(cfg.WorkspaceRoot, serviceName)
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		return "" // repo not available locally
	}
	return repoPath
}

// camelToKebab converts a CamelCase string to kebab-case.
// e.g., "TalkroomTicketing" -> "talkroom-ticketing"
// e.g., "TalkroomConvDossier" -> "talkroom-conv-dossier"
func camelToKebab(s string) string {
	var result []rune
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			prev := rune(s[i-1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) {
				result = append(result, '-')
			} else if unicode.IsUpper(prev) && i+1 < len(s) && unicode.IsLower(rune(s[i+1])) {
				// Handle runs of uppercase: "HTTPServer" -> "http-server"
				result = append(result, '-')
			}
		}
		result = append(result, unicode.ToLower(r))
	}
	return string(result)
}
