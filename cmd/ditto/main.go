package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ditto-mock/ditto-mock-api/internal/cache"
	"github.com/ditto-mock/ditto-mock-api/internal/config"
	"github.com/ditto-mock/ditto-mock-api/internal/generator"
	"github.com/ditto-mock/ditto-mock-api/internal/llm"
	"github.com/ditto-mock/ditto-mock-api/internal/matcher"
	"github.com/ditto-mock/ditto-mock-api/internal/models"
	"github.com/ditto-mock/ditto-mock-api/internal/scanner"
	"github.com/ditto-mock/ditto-mock-api/internal/server"
)

func main() {
	configPath := flag.String("config", "configs/ditto.yaml", "path to configuration file")
	flag.Parse()

	// Load configuration.
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	// Set up logger.
	logger := setupLogger(cfg.Logging)
	logger.Info("ditto starting", "config", *configPath)

	// Create LLM client.
	llmClient := llm.NewOpenAIClient(
		cfg.LLM.APIKey,
		cfg.LLM.Model,
		cfg.LLM.Temperature,
		cfg.LLM.MaxTokens,
	)

	// Create scanner.
	analyzer := scanner.NewLLMAnalyzer(llmClient, cfg.LLM, logger)
	sc := scanner.New(cfg, analyzer, logger)

	// Run scan phase.
	registries, err := sc.LoadOrScan()
	if err != nil {
		logger.Error("scan failed", "error", err)
		os.Exit(1)
	}
	logger.Info("scan phase complete", "registries", len(registries))

	// Build prefix map from config.
	prefixes := make(map[string]string, len(cfg.Dependencies))
	for _, dep := range cfg.Dependencies {
		prefixes[dep.Name] = dep.Prefix
	}

	// Create matcher.
	m := matcher.New(registries, prefixes)

	// Create cache.
	var cacheStore cache.Store
	if cfg.Cache.Enabled {
		cacheStore, err = cache.NewSQLiteStore(cfg.Cache.DBPath, cfg.Cache.TTL)
		if err != nil {
			logger.Error("failed to open cache", "error", err)
			os.Exit(1)
		}
		defer cacheStore.Close()
		logger.Info("cache enabled", "path", cfg.Cache.DBPath)
	}

	// Create generator.
	gen := generator.NewOpenAI(llmClient, cfg.LLM, logger)

	// Create scan callback for admin re-scan.
	scanFunc := func(ctx context.Context) ([]models.DependencyRegistry, error) {
		regs, scanErr := sc.ScanAll()
		if scanErr != nil {
			return nil, scanErr
		}
		m = matcher.New(regs, prefixes)
		return regs, nil
	}

	// Create and start server.
	srv := server.New(cfg, m, cacheStore, gen, registries, logger, server.WithScanFunc(scanFunc))

	// Graceful shutdown.
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		if listenErr := srv.ListenAndServe(); listenErr != nil {
			logger.Error("server error", "error", listenErr)
			done <- syscall.SIGTERM
		}
	}()

	logStartupSummary(logger, cfg, registries)

	<-done
	logger.Info("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if shutdownErr := srv.Shutdown(ctx); shutdownErr != nil {
		logger.Error("shutdown error", "error", shutdownErr)
	}

	logger.Info("ditto stopped")
}

func setupLogger(cfg config.LoggingConfig) *slog.Logger {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

func logStartupSummary(logger *slog.Logger, cfg *config.Config, registries []models.DependencyRegistry) {
	totalEndpoints := 0
	for _, reg := range registries {
		totalEndpoints += len(reg.Endpoints)
	}
	logger.Info("ditto ready",
		"host", cfg.Server.Host,
		"port", cfg.Server.Port,
		"dependencies", len(registries),
		"endpoints", totalEndpoints,
		"cache_enabled", cfg.Cache.Enabled,
	)
}
