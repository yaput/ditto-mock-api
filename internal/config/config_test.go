package config

import (
	"os"
	"testing"
	"time"
)

func TestParse_ValidConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test-key-123")

	data := []byte(`
server:
  port: 9090
  host: "127.0.0.1"
llm:
  provider: "openai"
  model: "gpt-4o-mini"
  api_key: "${OPENAI_API_KEY}"
  temperature: 0.5
  max_tokens: 2048
  timeout: 15s
  max_retries: 3
cache:
  enabled: true
  db_path: "./test-cache.db"
  ttl: "12h"
scanner:
  registry_path: "./.ditto/test-registry.json"
  scan_on_startup: true
dependencies:
  - name: "user-service"
    prefix: "/api/users"
    repo_path: "../user-service"
    scan_paths:
      - "./internal/handler"
logging:
  level: "debug"
  format: "json"
`)

	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", cfg.Server.Host)
	}
	if cfg.LLM.APIKey != "sk-test-key-123" {
		t.Errorf("expected API key sk-test-key-123, got %s", cfg.LLM.APIKey)
	}
	if cfg.LLM.Temperature != 0.5 {
		t.Errorf("expected temperature 0.5, got %f", cfg.LLM.Temperature)
	}
	if cfg.LLM.Timeout != 15*time.Second {
		t.Errorf("expected timeout 15s, got %v", cfg.LLM.Timeout)
	}
	if cfg.LLM.MaxRetries != 3 {
		t.Errorf("expected max_retries 3, got %d", cfg.LLM.MaxRetries)
	}
	if cfg.Cache.DBPath != "./test-cache.db" {
		t.Errorf("expected db_path ./test-cache.db, got %s", cfg.Cache.DBPath)
	}
	if cfg.Cache.TTL != 12*time.Hour {
		t.Errorf("expected ttl 12h, got %v", cfg.Cache.TTL)
	}
	if len(cfg.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d", len(cfg.Dependencies))
	}
	dep := cfg.Dependencies[0]
	if dep.Name != "user-service" {
		t.Errorf("expected name user-service, got %s", dep.Name)
	}
	if dep.Prefix != "/api/users" {
		t.Errorf("expected prefix /api/users, got %s", dep.Prefix)
	}
	if dep.RepoPath != "../user-service" {
		t.Errorf("expected repo_path ../user-service, got %s", dep.RepoPath)
	}
	if len(dep.ScanPaths) != 1 || dep.ScanPaths[0] != "./internal/handler" {
		t.Errorf("expected scan_paths [./internal/handler], got %v", dep.ScanPaths)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("expected level debug, got %s", cfg.Logging.Level)
	}
}

func TestParse_Defaults(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")

	data := []byte(`
llm:
  api_key: "${OPENAI_API_KEY}"
dependencies:
  - name: "svc"
    prefix: "/api"
    repo_path: "../svc"
`)

	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("expected default host 0.0.0.0, got %s", cfg.Server.Host)
	}
	if cfg.LLM.Model != "gpt-4o-mini" {
		t.Errorf("expected default model gpt-4o-mini, got %s", cfg.LLM.Model)
	}
	if cfg.Cache.DBPath != "./ditto-cache.db" {
		t.Errorf("expected default db_path, got %s", cfg.Cache.DBPath)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("expected default level info, got %s", cfg.Logging.Level)
	}
}

func TestParse_MissingAPIKey(t *testing.T) {
	os.Unsetenv("OPENAI_API_KEY")

	data := []byte(`
llm:
  api_key: ""
dependencies:
  - name: "svc"
    prefix: "/api"
    repo_path: "../svc"
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for missing API key")
	}
}

func TestParse_MissingDependencies(t *testing.T) {
	data := []byte(`
llm:
  api_key: "sk-test"
dependencies: []
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for empty dependencies")
	}
}

func TestParse_DuplicateDependencyName(t *testing.T) {
	data := []byte(`
llm:
  api_key: "sk-test"
dependencies:
  - name: "svc"
    prefix: "/api/a"
    repo_path: "../a"
  - name: "svc"
    prefix: "/api/b"
    repo_path: "../b"
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for duplicate dependency name")
	}
}

func TestParse_MissingPrefix(t *testing.T) {
	data := []byte(`
llm:
  api_key: "sk-test"
dependencies:
  - name: "svc"
    prefix: ""
    repo_path: "../svc"
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for missing prefix")
	}
}

func TestParse_PrefixMustStartWithSlash(t *testing.T) {
	data := []byte(`
llm:
  api_key: "sk-test"
dependencies:
  - name: "svc"
    prefix: "api/svc"
    repo_path: "../svc"
`)
	_, err := Parse(data)
	if err == nil {
		t.Fatal("expected error for prefix not starting with /")
	}
}

func TestSubstituteEnvVars(t *testing.T) {
	t.Setenv("MY_VAR", "hello")
	result := substituteEnvVars("key: ${MY_VAR}")
	if result != "key: hello" {
		t.Errorf("expected 'key: hello', got '%s'", result)
	}
}

func TestSubstituteEnvVars_Unset(t *testing.T) {
	os.Unsetenv("UNSET_VAR")
	result := substituteEnvVars("key: ${UNSET_VAR}")
	if result != "key: ${UNSET_VAR}" {
		t.Errorf("expected unresolved var to remain, got '%s'", result)
	}
}
