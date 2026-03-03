package scanner

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/ditto-mock/ditto-mock-api/internal/config"
	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

// Analyzer resolves raw AST scan output into a structured endpoint list using LLM.
type Analyzer interface {
	Analyze(scan *models.ScanOutput) ([]models.Endpoint, error)
}

// Scanner orchestrates AST extraction and LLM analysis for dependency repos.
type Scanner struct {
	cfg      *config.Config
	analyzer Analyzer
	logger   *slog.Logger
}

// New creates a new Scanner.
func New(cfg *config.Config, analyzer Analyzer, logger *slog.Logger) *Scanner {
	return &Scanner{
		cfg:      cfg,
		analyzer: analyzer,
		logger:   logger,
	}
}

// ScanAll scans all configured dependencies and returns their registries.
func (s *Scanner) ScanAll() ([]models.DependencyRegistry, error) {
	var registries []models.DependencyRegistry

	for _, dep := range s.cfg.Dependencies {
		reg, err := s.ScanDependency(dep)
		if err != nil {
			s.logger.Error("scan failed for dependency", "name", dep.Name, "error", err)
			continue
		}
		registries = append(registries, *reg)
		s.logger.Info("scanned dependency",
			"name", dep.Name,
			"endpoints", len(reg.Endpoints),
			"framework", reg.FrameworkDetected,
		)
	}

	return registries, nil
}

// ScanDependency scans a single dependency Go source code.
func (s *Scanner) ScanDependency(dep config.DependencyConfig) (*models.DependencyRegistry, error) {
	absRepo, err := filepath.Abs(dep.RepoPath)
	if err != nil {
		return nil, fmt.Errorf("resolving repo path %s: %w", dep.RepoPath, err)
	}

	dirs := dep.ScanPaths
	if len(dirs) == 0 {
		dirs = []string{"."}
	}

	var allStructs []models.ExtractedStruct
	var allRoutes []models.ExtractedRoute
	var allHandlers []models.ExtractedHandler

	for _, dir := range dirs {
		scanDir := filepath.Join(absRepo, dir)
		s.logger.Debug("scanning directory", "path", scanDir)

		structs, routes, handlers, scanErr := ExtractFromDir(scanDir)
		if scanErr != nil {
			s.logger.Warn("error scanning directory", "path", scanDir, "error", scanErr)
			continue
		}
		allStructs = append(allStructs, structs...)
		allRoutes = append(allRoutes, routes...)
		allHandlers = append(allHandlers, handlers...)
	}

	framework := DetectFramework(absRepo, dirs)

	scanOutput := &models.ScanOutput{
		Repo:      dep.Name,
		Framework: framework,
		Structs:   allStructs,
		Routes:    allRoutes,
		Handlers:  allHandlers,
	}

	endpoints, err := s.analyzer.Analyze(scanOutput)
	if err != nil {
		return nil, fmt.Errorf("analyzing scan output: %w", err)
	}

	reg := &models.DependencyRegistry{
		ScannedAt:         time.Now().UTC(),
		Dependency:        dep.Name,
		RepoPath:          dep.RepoPath,
		FrameworkDetected: framework,
		Endpoints:         endpoints,
	}

	return reg, nil
}

// LoadOrScan loads the registry from disk if available, otherwise scans.
func (s *Scanner) LoadOrScan() ([]models.DependencyRegistry, error) {
	registryPath := s.cfg.Scanner.RegistryPath

	if !s.cfg.Scanner.ScanOnStartup {
		regs, err := LoadRegistries(registryPath)
		if err == nil && len(regs) > 0 {
			s.logger.Info("loaded cached registries", "path", registryPath, "count", len(regs))
			return regs, nil
		}
		s.logger.Info("no cached registry found, performing scan")
	}

	regs, err := s.ScanAll()
	if err != nil {
		return nil, err
	}

	if err := SaveRegistries(registryPath, regs); err != nil {
		s.logger.Warn("failed to persist registry", "path", registryPath, "error", err)
	}

	return regs, nil
}

// SaveRegistries persists registries to disk as JSON.
func SaveRegistries(path string, regs []models.DependencyRegistry) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating registry dir: %w", err)
	}
	data, err := json.MarshalIndent(regs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling registries: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadRegistries loads registries from a JSON file on disk.
func LoadRegistries(path string) ([]models.DependencyRegistry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var regs []models.DependencyRegistry
	if err := json.Unmarshal(data, &regs); err != nil {
		return nil, fmt.Errorf("unmarshaling registries: %w", err)
	}
	return regs, nil
}
