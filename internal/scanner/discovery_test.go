package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ditto-mock/ditto-mock-api/internal/config"
)

func TestParseGoMod_RequireBlock(t *testing.T) {
	content := `module github.com/example/my-service

go 1.22.0

require (
	github.com/zeals-co-ltd/talkroom-ticketing v0.0.7-0.20240301
	github.com/zeals-co-ltd/talkroom-bff v1.2.3
	github.com/gin-gonic/gin v1.9.1
	gopkg.in/yaml.v3 v3.0.1
)
`
	dir := t.TempDir()
	goModPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goModPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	deps, err := ParseGoMod(goModPath)
	if err != nil {
		t.Fatalf("ParseGoMod error: %v", err)
	}

	if len(deps) != 4 {
		t.Fatalf("expected 4 deps, got %d", len(deps))
	}

	expected := map[string]string{
		"github.com/zeals-co-ltd/talkroom-ticketing": "v0.0.7-0.20240301",
		"github.com/zeals-co-ltd/talkroom-bff":       "v1.2.3",
		"github.com/gin-gonic/gin":                   "v1.9.1",
		"gopkg.in/yaml.v3":                           "v3.0.1",
	}
	for _, dep := range deps {
		if v, ok := expected[dep.Module]; !ok {
			t.Errorf("unexpected module: %s", dep.Module)
		} else if dep.Version != v {
			t.Errorf("module %s: expected version %s, got %s", dep.Module, v, dep.Version)
		}
	}
}

func TestParseGoMod_SingleLineRequire(t *testing.T) {
	content := `module github.com/example/svc

go 1.22.0

require github.com/zeals-co-ltd/talkroom-ticketing v0.0.7
`
	dir := t.TempDir()
	goModPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goModPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	deps, err := ParseGoMod(goModPath)
	if err != nil {
		t.Fatalf("ParseGoMod error: %v", err)
	}

	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].Module != "github.com/zeals-co-ltd/talkroom-ticketing" {
		t.Errorf("unexpected module: %s", deps[0].Module)
	}
}

func TestParseGoMod_MultipleRequireBlocks(t *testing.T) {
	content := `module github.com/example/svc

go 1.22.0

require (
	github.com/zeals-co-ltd/svc-a v1.0.0
)

require (
	github.com/zeals-co-ltd/svc-b v2.0.0
	github.com/third-party/lib v0.1.0
)
`
	dir := t.TempDir()
	goModPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goModPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	deps, err := ParseGoMod(goModPath)
	if err != nil {
		t.Fatalf("ParseGoMod error: %v", err)
	}

	if len(deps) != 3 {
		t.Fatalf("expected 3 deps, got %d", len(deps))
	}
}

func TestParseGoMod_CommentsAndBlanks(t *testing.T) {
	content := `module github.com/example/svc

go 1.22.0

require (
	// This is a comment
	github.com/zeals-co-ltd/svc-a v1.0.0

	github.com/zeals-co-ltd/svc-b v2.0.0
)
`
	dir := t.TempDir()
	goModPath := filepath.Join(dir, "go.mod")
	if err := os.WriteFile(goModPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	deps, err := ParseGoMod(goModPath)
	if err != nil {
		t.Fatalf("ParseGoMod error: %v", err)
	}

	if len(deps) != 2 {
		t.Fatalf("expected 2 deps, got %d", len(deps))
	}
}

func TestParseGoMod_FileNotFound(t *testing.T) {
	_, err := ParseGoMod("/nonexistent/go.mod")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestFilterByOrgPrefix(t *testing.T) {
	deps := []GoModDependency{
		{Module: "github.com/zeals-co-ltd/talkroom-ticketing", Version: "v1.0.0"},
		{Module: "github.com/zeals-co-ltd/talkroom-bff", Version: "v2.0.0"},
		{Module: "github.com/gin-gonic/gin", Version: "v1.9.1"},
		{Module: "gopkg.in/yaml.v3", Version: "v3.0.1"},
		{Module: "github.com/myorg/internal-svc", Version: "v0.1.0"},
	}

	filtered := FilterByOrgPrefix(deps, []string{"github.com/zeals-co-ltd/"})
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered deps, got %d", len(filtered))
	}

	// Multiple prefixes.
	filtered = FilterByOrgPrefix(deps, []string{"github.com/zeals-co-ltd/", "github.com/myorg/"})
	if len(filtered) != 3 {
		t.Fatalf("expected 3 filtered deps, got %d", len(filtered))
	}
}

func TestFilterByOrgPrefix_Empty(t *testing.T) {
	deps := []GoModDependency{
		{Module: "github.com/gin-gonic/gin", Version: "v1.9.1"},
	}
	filtered := FilterByOrgPrefix(deps, []string{"github.com/zeals-co-ltd/"})
	if len(filtered) != 0 {
		t.Fatalf("expected 0 filtered deps, got %d", len(filtered))
	}
}

func TestResolveRepoPaths_Default(t *testing.T) {
	deps := []GoModDependency{
		{Module: "github.com/zeals-co-ltd/talkroom-ticketing"},
		{Module: "github.com/zeals-co-ltd/talkroom-bff"},
	}

	result := ResolveRepoPaths(deps, "../", nil)

	if result["github.com/zeals-co-ltd/talkroom-ticketing"] != filepath.Join("../", "talkroom-ticketing") {
		t.Errorf("unexpected path: %s", result["github.com/zeals-co-ltd/talkroom-ticketing"])
	}
	if result["github.com/zeals-co-ltd/talkroom-bff"] != filepath.Join("../", "talkroom-bff") {
		t.Errorf("unexpected path: %s", result["github.com/zeals-co-ltd/talkroom-bff"])
	}
}

func TestResolveRepoPaths_WithOverride(t *testing.T) {
	deps := []GoModDependency{
		{Module: "github.com/zeals-co-ltd/talkroom-ticketing"},
	}
	repoMap := map[string]string{
		"github.com/zeals-co-ltd/talkroom-ticketing": "/custom/path/ticketing",
	}

	result := ResolveRepoPaths(deps, "../", repoMap)

	if result["github.com/zeals-co-ltd/talkroom-ticketing"] != "/custom/path/ticketing" {
		t.Errorf("expected override path, got: %s", result["github.com/zeals-co-ltd/talkroom-ticketing"])
	}
}

func TestModuleToServiceName(t *testing.T) {
	tests := []struct {
		module string
		want   string
	}{
		{"github.com/zeals-co-ltd/talkroom-ticketing", "talkroom-ticketing"},
		{"github.com/org/my-service", "my-service"},
		{"simple", "simple"},
	}
	for _, tt := range tests {
		got := ModuleToServiceName(tt.module)
		if got != tt.want {
			t.Errorf("ModuleToServiceName(%q) = %q, want %q", tt.module, got, tt.want)
		}
	}
}

func TestModuleToPrefix(t *testing.T) {
	got := ModuleToPrefix("talkroom-ticketing")
	if got != "/talkroom-ticketing" {
		t.Errorf("expected /talkroom-ticketing, got %s", got)
	}
}

func TestDiscoverDependencies_Integration(t *testing.T) {
	// Set up a fake workspace with a service repo containing go.mod
	// and a target repo directory.
	workspace := t.TempDir()
	serviceRepo := filepath.Join(workspace, "my-service")
	targetRepo := filepath.Join(workspace, "talkroom-ticketing")

	if err := os.MkdirAll(serviceRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetRepo, 0o755); err != nil {
		t.Fatal(err)
	}

	goModContent := `module github.com/example/my-service

go 1.22.0

require (
	github.com/zeals-co-ltd/talkroom-ticketing v0.0.7
	github.com/zeals-co-ltd/missing-service v1.0.0
	github.com/gin-gonic/gin v1.9.1
)
`
	if err := os.WriteFile(filepath.Join(serviceRepo, "go.mod"), []byte(goModContent), 0o644); err != nil {
		t.Fatal(err)
	}

	discCfg := config.DiscoveryConfig{
		Enabled:       true,
		ServiceRepo:   serviceRepo,
		OrgPrefixes:   []string{"github.com/zeals-co-ltd/"},
		WorkspaceRoot: workspace,
	}

	deps, err := DiscoverDependencies(discCfg)
	if err != nil {
		t.Fatalf("DiscoverDependencies error: %v", err)
	}

	// Should find talkroom-ticketing but not missing-service (no directory) or gin (not org).
	if len(deps) != 1 {
		t.Fatalf("expected 1 discovered dep, got %d: %+v", len(deps), deps)
	}

	dep := deps[0]
	if dep.Name != "talkroom-ticketing" {
		t.Errorf("expected name talkroom-ticketing, got %s", dep.Name)
	}
	if dep.Prefix != "/talkroom-ticketing" {
		t.Errorf("expected prefix /talkroom-ticketing, got %s", dep.Prefix)
	}
	if dep.RepoPath != filepath.Join(workspace, "talkroom-ticketing") {
		t.Errorf("expected repo_path %s, got %s", filepath.Join(workspace, "talkroom-ticketing"), dep.RepoPath)
	}
}

func TestDiscoverDependencies_Disabled(t *testing.T) {
	discCfg := config.DiscoveryConfig{
		Enabled: false,
	}

	// DiscoverDependencies doesn't check Enabled (caller does), but let's test the path
	// where no go.mod exists and it errors.
	_, err := DiscoverDependencies(discCfg)
	if err == nil {
		t.Fatal("expected error when service_repo is empty")
	}
}

func TestDiscoverDependencies_NoOrgMatches(t *testing.T) {
	workspace := t.TempDir()
	serviceRepo := filepath.Join(workspace, "my-service")
	if err := os.MkdirAll(serviceRepo, 0o755); err != nil {
		t.Fatal(err)
	}

	goModContent := `module github.com/example/my-service

go 1.22.0

require (
	github.com/gin-gonic/gin v1.9.1
)
`
	if err := os.WriteFile(filepath.Join(serviceRepo, "go.mod"), []byte(goModContent), 0o644); err != nil {
		t.Fatal(err)
	}

	discCfg := config.DiscoveryConfig{
		Enabled:       true,
		ServiceRepo:   serviceRepo,
		OrgPrefixes:   []string{"github.com/zeals-co-ltd/"},
		WorkspaceRoot: workspace,
	}

	deps, err := DiscoverDependencies(discCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 deps, got %d", len(deps))
	}
}

func TestDiscoverDependencies_WithRepoMapOverride(t *testing.T) {
	workspace := t.TempDir()
	serviceRepo := filepath.Join(workspace, "my-service")
	customPath := filepath.Join(workspace, "custom-ticketing")

	if err := os.MkdirAll(serviceRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(customPath, 0o755); err != nil {
		t.Fatal(err)
	}

	goModContent := `module github.com/example/my-service

go 1.22.0

require (
	github.com/zeals-co-ltd/talkroom-ticketing v0.0.7
)
`
	if err := os.WriteFile(filepath.Join(serviceRepo, "go.mod"), []byte(goModContent), 0o644); err != nil {
		t.Fatal(err)
	}

	discCfg := config.DiscoveryConfig{
		Enabled:       true,
		ServiceRepo:   serviceRepo,
		OrgPrefixes:   []string{"github.com/zeals-co-ltd/"},
		WorkspaceRoot: workspace,
		RepoMap: map[string]string{
			"github.com/zeals-co-ltd/talkroom-ticketing": customPath,
		},
	}

	deps, err := DiscoverDependencies(discCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].RepoPath != customPath {
		t.Errorf("expected custom path %s, got %s", customPath, deps[0].RepoPath)
	}
}
