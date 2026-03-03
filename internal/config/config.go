// Package config handles loading and validating the ditto.yaml configuration.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure.
type Config struct {
	Server       ServerConfig       `yaml:"server"`
	LLM          LLMConfig          `yaml:"llm"`
	Cache        CacheConfig        `yaml:"cache"`
	Scanner      ScannerConfig      `yaml:"scanner"`
	Dependencies []DependencyConfig `yaml:"dependencies"`
	Logging      LoggingConfig      `yaml:"logging"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

// LLMConfig holds LLM provider settings.
type LLMConfig struct {
	Provider    string        `yaml:"provider"`
	Model       string        `yaml:"model"`
	APIKey      string        `yaml:"api_key"`
	Temperature float64       `yaml:"temperature"`
	MaxTokens   int           `yaml:"max_tokens"`
	Timeout     time.Duration `yaml:"timeout"`
	MaxRetries  int           `yaml:"max_retries"`
}

// CacheConfig holds cache settings.
type CacheConfig struct {
	Enabled bool          `yaml:"enabled"`
	DBPath  string        `yaml:"db_path"`
	TTL     time.Duration `yaml:"ttl"`
}

// ScannerConfig holds scanner settings.
type ScannerConfig struct {
	RegistryPath  string   `yaml:"registry_path"`
	ScanOnStartup bool     `yaml:"scan_on_startup"`
	GoFrameworks  []string `yaml:"go_frameworks"`
}

// DependencyConfig defines one dependency to mock.
type DependencyConfig struct {
	Name      string   `yaml:"name"`
	Prefix    string   `yaml:"prefix"`
	RepoPath  string   `yaml:"repo_path"`
	ScanPaths []string `yaml:"scan_paths"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// envVarPattern matches ${VAR_NAME} in config values.
var envVarPattern = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// Load reads and parses the config file at the given path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}
	return Parse(data)
}

// Parse parses YAML config bytes with env var substitution.
func Parse(data []byte) (*Config, error) {
	expanded := substituteEnvVars(string(data))
	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parsing config YAML: %w", err)
	}
	applyDefaults(cfg)
	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}
	return cfg, nil
}

// substituteEnvVars replaces ${VAR} with environment variable values.
func substituteEnvVars(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := envVarPattern.FindStringSubmatch(match)[1]
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		return match // leave unresolved vars as-is
	})
}

// applyDefaults sets sensible defaults for unset fields.
func applyDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	if cfg.LLM.Provider == "" {
		cfg.LLM.Provider = "openai"
	}
	if cfg.LLM.Model == "" {
		cfg.LLM.Model = "gpt-4o-mini"
	}
	if cfg.LLM.Temperature == 0 {
		cfg.LLM.Temperature = 0.7
	}
	if cfg.LLM.MaxTokens == 0 {
		cfg.LLM.MaxTokens = 4096
	}
	if cfg.LLM.Timeout == 0 {
		cfg.LLM.Timeout = 30 * time.Second
	}
	if cfg.LLM.MaxRetries == 0 {
		cfg.LLM.MaxRetries = 2
	}
	if !cfg.Cache.Enabled && cfg.Cache.DBPath == "" {
		cfg.Cache.Enabled = true
	}
	if cfg.Cache.DBPath == "" {
		cfg.Cache.DBPath = "./ditto-cache.db"
	}
	if cfg.Cache.TTL == 0 {
		cfg.Cache.TTL = 24 * time.Hour
	}
	if cfg.Scanner.RegistryPath == "" {
		cfg.Scanner.RegistryPath = "./.ditto/registry.json"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "text"
	}
}

// validate checks required fields.
func validate(cfg *Config) error {
	if cfg.LLM.APIKey == "" {
		return fmt.Errorf("llm.api_key is required (set OPENAI_API_KEY env var)")
	}
	if len(cfg.Dependencies) == 0 {
		return fmt.Errorf("at least one dependency must be configured")
	}
	seen := make(map[string]bool)
	for i, dep := range cfg.Dependencies {
		if dep.Name == "" {
			return fmt.Errorf("dependencies[%d].name is required", i)
		}
		if dep.Prefix == "" {
			return fmt.Errorf("dependencies[%d].prefix is required", i)
		}
		if dep.RepoPath == "" {
			return fmt.Errorf("dependencies[%d].repo_path is required", i)
		}
		if !strings.HasPrefix(dep.Prefix, "/") {
			return fmt.Errorf("dependencies[%d].prefix must start with /", i)
		}
		if seen[dep.Name] {
			return fmt.Errorf("duplicate dependency name: %s", dep.Name)
		}
		seen[dep.Name] = true
	}
	return nil
}
