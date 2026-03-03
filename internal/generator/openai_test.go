package generator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ditto-mock/ditto-mock-api/internal/config"
	"github.com/ditto-mock/ditto-mock-api/internal/models"
)

type mockLLMClient struct {
	response string
	err      error
	calls    int
}

func (m *mockLLMClient) ChatCompletion(_ context.Context, _, _ string) (string, error) {
	m.calls++
	return m.response, m.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestOpenAIGenerator_Generate_Success(t *testing.T) {
	client := &mockLLMClient{response: `{"id":"123","name":"Alice"}`}
	cfg := config.LLMConfig{Timeout: 10 * time.Second, MaxRetries: 0}
	gen := NewOpenAI(client, cfg, testLogger())

	endpoint := models.Endpoint{
		Method:     "GET",
		Path:       "/users/{id}",
		StatusCode: 200,
	}
	req := RequestContext{
		Method:     "GET",
		Path:       "/users/123",
		PathParams: map[string]string{"id": "123"},
	}

	resp, err := gen.Generate(context.Background(), endpoint, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if resp.Headers["Content-Type"] != "application/json" {
		t.Error("expected Content-Type: application/json")
	}
	if resp.Body != `{"id":"123","name":"Alice"}` {
		t.Errorf("unexpected body: %s", resp.Body)
	}
	if client.calls != 1 {
		t.Errorf("expected 1 call, got %d", client.calls)
	}
}

func TestOpenAIGenerator_Generate_MarkdownCleaning(t *testing.T) {
	client := &mockLLMClient{response: "```json\n{\"ok\":true}\n```"}
	cfg := config.LLMConfig{Timeout: 10 * time.Second, MaxRetries: 0}
	gen := NewOpenAI(client, cfg, testLogger())

	resp, err := gen.Generate(context.Background(), models.Endpoint{StatusCode: 200}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != `{"ok":true}` {
		t.Errorf("expected cleaned JSON, got: %s", resp.Body)
	}
}

func TestOpenAIGenerator_Generate_RetryAndFail(t *testing.T) {
	client := &mockLLMClient{err: fmt.Errorf("timeout")}
	cfg := config.LLMConfig{Timeout: 2 * time.Second, MaxRetries: 1}
	gen := NewOpenAI(client, cfg, testLogger())

	_, err := gen.Generate(context.Background(), models.Endpoint{}, RequestContext{})
	if err == nil {
		t.Fatal("expected error")
	}
	if client.calls != 2 {
		t.Errorf("expected 2 calls (1 + 1 retry), got %d", client.calls)
	}
}

func TestBuildResponsePrompt(t *testing.T) {
	endpoint := models.Endpoint{
		Method:      "POST",
		Path:        "/users",
		Description: "Create a user",
		StatusCode:  201,
		ResponseBody: &models.BodySchema{
			Type: "object",
			Fields: []models.FieldSchema{
				{Name: "id", Type: "string", JSONKey: "id", Required: true},
			},
		},
	}
	req := RequestContext{
		Method: "POST",
		Path:   "/users",
		Body:   `{"name":"Bob"}`,
		QueryParams: map[string]string{
			"notify": "true",
		},
	}

	prompt := buildResponsePrompt(endpoint, req)

	checks := []string{
		"POST /users",
		"Create a user",
		"201",
		`{"name":"Bob"}`,
		"notify = true",
		"Response Schema",
		"ONLY valid JSON",
	}
	for _, c := range checks {
		if !strings.Contains(prompt, c) {
			t.Errorf("prompt missing: %q", c)
		}
	}
}

func TestSystemPrompt(t *testing.T) {
	p := systemPrompt()
	if !strings.Contains(p, "mock API") {
		t.Error("system prompt should mention mock API")
	}
}

func TestCleanResponse(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"plain", `{"a":1}`, `{"a":1}`},
		{"markdown", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"whitespace", "  \n{\"a\":1}\n  ", `{"a":1}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanResponse(tc.input)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
