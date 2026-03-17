package scanner

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/ditto-mock/ditto-mock-api/internal/config"
)

func TestCamelToKebab(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"TalkroomTicketing", "talkroom-ticketing"},
		{"TalkroomConvDossier", "talkroom-conv-dossier"},
		{"TalkroomEndUserDossier", "talkroom-end-user-dossier"},
		{"TalkroomAgent", "talkroom-agent"},
		{"Simple", "simple"},
		{"HTTPServer", "http-server"},
		{"MyAPIService", "my-api-service"},
		{"", ""},
		{"lowercase", "lowercase"},
		{"A", "a"},
	}
	for _, tt := range tests {
		got := camelToKebab(tt.input)
		if got != tt.want {
			t.Errorf("camelToKebab(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDiscoverHTTPServices_Disabled(t *testing.T) {
	cfg := config.DiscoveryConfig{
		HTTPDiscovery: config.HTTPDiscoveryConfig{
			Enabled: false,
		},
	}
	deps, err := DiscoverHTTPServices(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 deps when disabled, got %d", len(deps))
	}
}

func TestDiscoverHTTPServices_FindsServices(t *testing.T) {
	workspace := t.TempDir()
	serviceRepo := filepath.Join(workspace, "my-bff")

	// Create a Go file with HTTP client config address patterns.
	clientsDir := filepath.Join(serviceRepo, "internal", "drivers", "http_clients")
	if err := os.MkdirAll(clientsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	goFile := `package http_clients

import "fmt"

func NewComponent(d *Dependencies) (*Component, error) {
	ticketingURL := fmt.Sprintf("http://%s", d.Config.TalkroomTicketingServiceAddress)
	agentURL := fmt.Sprintf("http://%s", d.Config.TalkroomAgentServiceAddress)
	convURL := fmt.Sprintf("http://%s", d.Config.TalkroomConvDossierServiceAddress)
	eudURL := fmt.Sprintf("http://%s", d.Config.TalkroomEndUserDossierServiceAddress)
	return nil, nil
}
`
	if err := os.WriteFile(filepath.Join(clientsDir, "component.go"), []byte(goFile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create local repos for some services (not all).
	for _, name := range []string{"talkroom-ticketing", "talkroom-agent", "talkroom-conv-dossier"} {
		if err := os.MkdirAll(filepath.Join(workspace, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// talkroom-end-user-dossier intentionally NOT created.

	cfg := config.DiscoveryConfig{
		Enabled:     true,
		ServiceRepo: serviceRepo,
		HTTPDiscovery: config.HTTPDiscoveryConfig{
			Enabled:       true,
			AddressSuffix: "ServiceAddress",
		},
		WorkspaceRoot: workspace,
	}

	deps, err := DiscoverHTTPServices(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should find 3 services (not talkroom-end-user-dossier since its repo doesn't exist).
	if len(deps) != 3 {
		t.Fatalf("expected 3 deps, got %d: %+v", len(deps), deps)
	}

	names := make([]string, len(deps))
	for i, d := range deps {
		names[i] = d.Name
	}
	sort.Strings(names)

	expected := []string{"talkroom-agent", "talkroom-conv-dossier", "talkroom-ticketing"}
	for i, exp := range expected {
		if names[i] != exp {
			t.Errorf("deps[%d].Name = %q, want %q", i, names[i], exp)
		}
	}

	// Verify all have prefix "/" and valid repo paths.
	for _, dep := range deps {
		if dep.Prefix != "/" {
			t.Errorf("dep %s: expected prefix /, got %s", dep.Name, dep.Prefix)
		}
		if dep.RepoPath == "" {
			t.Errorf("dep %s: expected non-empty repo_path", dep.Name)
		}
	}
}

func TestDiscoverHTTPServices_NoMatches(t *testing.T) {
	workspace := t.TempDir()
	serviceRepo := filepath.Join(workspace, "my-bff")
	srcDir := filepath.Join(serviceRepo, "internal")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	goFile := `package internal

func main() {
	x := "no config patterns here"
	_ = x
}
`
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(goFile), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DiscoveryConfig{
		Enabled:     true,
		ServiceRepo: serviceRepo,
		HTTPDiscovery: config.HTTPDiscoveryConfig{
			Enabled:       true,
			AddressSuffix: "ServiceAddress",
		},
		WorkspaceRoot: workspace,
	}

	deps, err := DiscoverHTTPServices(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 deps, got %d", len(deps))
	}
}

func TestDiscoverHTTPServices_SkipsTestFiles(t *testing.T) {
	workspace := t.TempDir()
	serviceRepo := filepath.Join(workspace, "my-bff")
	if err := os.MkdirAll(serviceRepo, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pattern only appears in test file — should not be discovered.
	testFile := `package main

import "testing"

func TestSomething(t *testing.T) {
	_ = d.Config.TalkroomTicketingServiceAddress
}
`
	if err := os.WriteFile(filepath.Join(serviceRepo, "main_test.go"), []byte(testFile), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DiscoveryConfig{
		Enabled:     true,
		ServiceRepo: serviceRepo,
		HTTPDiscovery: config.HTTPDiscoveryConfig{
			Enabled:       true,
			AddressSuffix: "ServiceAddress",
		},
		WorkspaceRoot: workspace,
	}

	deps, err := DiscoverHTTPServices(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 deps from test files, got %d", len(deps))
	}
}

func TestDiscoverHTTPServices_CustomSuffix(t *testing.T) {
	workspace := t.TempDir()
	serviceRepo := filepath.Join(workspace, "my-bff")
	if err := os.MkdirAll(serviceRepo, 0o755); err != nil {
		t.Fatal(err)
	}

	// Config.PaymentGatewayServiceURL → strip "ServiceURL" → "PaymentGateway" → "payment-gateway"
	// Config.NotificationHubServiceURL → strip "ServiceURL" → "NotificationHub" → "notification-hub"
	goFile := `package main

var addr = cfg.Config.PaymentGatewayServiceURL
var addr2 = cfg.Config.NotificationHubServiceURL
`
	if err := os.WriteFile(filepath.Join(serviceRepo, "main.go"), []byte(goFile), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create repos matching the derived kebab-case names.
	for _, name := range []string{"payment-gateway", "notification-hub"} {
		if err := os.MkdirAll(filepath.Join(workspace, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.DiscoveryConfig{
		Enabled:     true,
		ServiceRepo: serviceRepo,
		HTTPDiscovery: config.HTTPDiscoveryConfig{
			Enabled:       true,
			AddressSuffix: "ServiceURL",
		},
		WorkspaceRoot: workspace,
	}

	deps, err := DiscoverHTTPServices(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 2 {
		t.Fatalf("expected 2 deps, got %d: %+v", len(deps), deps)
	}
}

func TestDiscoverHTTPServices_RepoMapOverride(t *testing.T) {
	workspace := t.TempDir()
	serviceRepo := filepath.Join(workspace, "my-bff")
	customPath := filepath.Join(workspace, "custom-ticketing")

	if err := os.MkdirAll(serviceRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(customPath, 0o755); err != nil {
		t.Fatal(err)
	}

	goFile := `package main

import "fmt"

var addr = fmt.Sprintf("http://%s", d.Config.TalkroomTicketingServiceAddress)
`
	if err := os.WriteFile(filepath.Join(serviceRepo, "main.go"), []byte(goFile), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DiscoveryConfig{
		Enabled:     true,
		ServiceRepo: serviceRepo,
		HTTPDiscovery: config.HTTPDiscoveryConfig{
			Enabled:       true,
			AddressSuffix: "ServiceAddress",
		},
		WorkspaceRoot: workspace,
		RepoMap: map[string]string{
			"talkroom-ticketing": customPath,
		},
	}

	deps, err := DiscoverHTTPServices(cfg)
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

func TestDiscoverHTTPServices_Deduplicates(t *testing.T) {
	workspace := t.TempDir()
	serviceRepo := filepath.Join(workspace, "my-bff")

	// Two files referencing the same service.
	for _, dir := range []string{"internal/a", "internal/b"} {
		d := filepath.Join(serviceRepo, dir)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		goFile := `package x
var addr = d.Config.TalkroomTicketingServiceAddress
`
		if err := os.WriteFile(filepath.Join(d, "client.go"), []byte(goFile), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.MkdirAll(filepath.Join(workspace, "talkroom-ticketing"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DiscoveryConfig{
		Enabled:     true,
		ServiceRepo: serviceRepo,
		HTTPDiscovery: config.HTTPDiscoveryConfig{
			Enabled:       true,
			AddressSuffix: "ServiceAddress",
		},
		WorkspaceRoot: workspace,
	}

	deps, err := DiscoverHTTPServices(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 deduplicated dep, got %d: %+v", len(deps), deps)
	}
}

func TestDiscoverDependencies_MergesHTTPServices(t *testing.T) {
	workspace := t.TempDir()
	serviceRepo := filepath.Join(workspace, "my-service")
	goModRepo := filepath.Join(workspace, "zero-api")
	httpRepo := filepath.Join(workspace, "talkroom-ticketing")

	for _, dir := range []string{serviceRepo, goModRepo, httpRepo} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// go.mod references zero-api.
	goModContent := `module github.com/example/my-service

go 1.22.0

require (
	github.com/zeals-co-ltd/zero-api v0.0.1
)
`
	if err := os.WriteFile(filepath.Join(serviceRepo, "go.mod"), []byte(goModContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Source references talkroom-ticketing via HTTP client.
	goFile := `package main
var addr = d.Config.TalkroomTicketingServiceAddress
`
	if err := os.WriteFile(filepath.Join(serviceRepo, "main.go"), []byte(goFile), 0o644); err != nil {
		t.Fatal(err)
	}

	discCfg := config.DiscoveryConfig{
		Enabled:       true,
		ServiceRepo:   serviceRepo,
		OrgPrefixes:   []string{"github.com/zeals-co-ltd/"},
		WorkspaceRoot: workspace,
		HTTPDiscovery: config.HTTPDiscoveryConfig{
			Enabled:       true,
			AddressSuffix: "ServiceAddress",
		},
	}

	deps, err := DiscoverDependencies(discCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should find both: zero-api (from go.mod) and talkroom-ticketing (from HTTP scan).
	if len(deps) != 2 {
		t.Fatalf("expected 2 deps, got %d: %+v", len(deps), deps)
	}

	names := make(map[string]bool)
	for _, d := range deps {
		names[d.Name] = true
	}
	if !names["zero-api"] {
		t.Error("expected zero-api from go.mod discovery")
	}
	if !names["talkroom-ticketing"] {
		t.Error("expected talkroom-ticketing from HTTP discovery")
	}
}

func TestDiscoverDependencies_HTTPServiceDeduplicatedWithGoMod(t *testing.T) {
	workspace := t.TempDir()
	serviceRepo := filepath.Join(workspace, "my-service")
	ticketingRepo := filepath.Join(workspace, "talkroom-ticketing")

	for _, dir := range []string{serviceRepo, ticketingRepo} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// go.mod also references talkroom-ticketing.
	goModContent := `module github.com/example/my-service

go 1.22.0

require (
	github.com/zeals-co-ltd/talkroom-ticketing v0.0.7
)
`
	if err := os.WriteFile(filepath.Join(serviceRepo, "go.mod"), []byte(goModContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Source also references it via HTTP client.
	goFile := `package main
var addr = d.Config.TalkroomTicketingServiceAddress
`
	if err := os.WriteFile(filepath.Join(serviceRepo, "main.go"), []byte(goFile), 0o644); err != nil {
		t.Fatal(err)
	}

	discCfg := config.DiscoveryConfig{
		Enabled:       true,
		ServiceRepo:   serviceRepo,
		OrgPrefixes:   []string{"github.com/zeals-co-ltd/"},
		WorkspaceRoot: workspace,
		HTTPDiscovery: config.HTTPDiscoveryConfig{
			Enabled:       true,
			AddressSuffix: "ServiceAddress",
		},
	}

	deps, err := DiscoverDependencies(discCfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be 1 (deduplicated), with go.mod taking precedence.
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep (deduplicated), got %d: %+v", len(deps), deps)
	}

	if deps[0].Name != "talkroom-ticketing" {
		t.Errorf("expected talkroom-ticketing, got %s", deps[0].Name)
	}
	// go.mod discovery sets prefix to /talkroom-ticketing, HTTP sets /.
	// Since go.mod wins (added first), prefix should be /talkroom-ticketing.
	if deps[0].Prefix != "/talkroom-ticketing" {
		t.Errorf("expected prefix /talkroom-ticketing (go.mod precedence), got %s", deps[0].Prefix)
	}
}

func TestDiscoverHTTPServices_SkipsVendorDir(t *testing.T) {
	workspace := t.TempDir()
	serviceRepo := filepath.Join(workspace, "my-bff")
	vendorDir := filepath.Join(serviceRepo, "vendor", "some-pkg")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pattern only in vendor dir — should be skipped.
	goFile := `package somepkg
var addr = d.Config.TalkroomTicketingServiceAddress
`
	if err := os.WriteFile(filepath.Join(vendorDir, "client.go"), []byte(goFile), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(workspace, "talkroom-ticketing"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.DiscoveryConfig{
		Enabled:     true,
		ServiceRepo: serviceRepo,
		HTTPDiscovery: config.HTTPDiscoveryConfig{
			Enabled:       true,
			AddressSuffix: "ServiceAddress",
		},
		WorkspaceRoot: workspace,
	}

	deps, err := DiscoverHTTPServices(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 deps (vendor should be skipped), got %d", len(deps))
	}
}
