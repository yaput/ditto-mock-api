package generator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ditto-mock/ditto-mock-api/internal/config"
	"github.com/ditto-mock/ditto-mock-api/internal/models"
	"github.com/ditto-mock/ditto-mock-api/internal/scanner"
)

// OpenAIGenerator generates mock responses using the OpenAI API.
type OpenAIGenerator struct {
	client scanner.LLMClient
	cfg    config.LLMConfig
	logger *slog.Logger
}

// NewOpenAI creates a new OpenAI-based generator.
func NewOpenAI(client scanner.LLMClient, cfg config.LLMConfig, logger *slog.Logger) *OpenAIGenerator {
	return &OpenAIGenerator{
		client: client,
		cfg:    cfg,
		logger: logger,
	}
}

// Generate produces a mock response for the given endpoint and request context.
func (g *OpenAIGenerator) Generate(ctx context.Context, endpoint models.Endpoint, req RequestContext) (*MockResponse, error) {
	prompt := buildResponsePrompt(endpoint, req)
	sys := systemPrompt()

	maxRetries := g.cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, g.cfg.Timeout)

		g.logger.Debug("generating mock response",
			"attempt", attempt+1,
			"method", endpoint.Method,
			"path", endpoint.Path,
		)

		response, err := g.client.ChatCompletion(callCtx, sys, prompt)
		cancel()

		if err != nil {
			lastErr = err
			g.logger.Warn("LLM generation failed", "attempt", attempt+1, "error", err)
			time.Sleep(time.Duration(attempt+1) * time.Second)
			continue
		}

		body := cleanResponse(response)
		return &MockResponse{
			StatusCode: endpoint.StatusCode,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
			Body: body,
		}, nil
	}

	return nil, fmt.Errorf("generation failed after %d attempts: %w", maxRetries+1, lastErr)
}

func cleanResponse(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```json"); i >= 0 {
		s = s[i+7:]
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
	} else if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = s[:j]
		}
	}
	return strings.TrimSpace(s)
}
