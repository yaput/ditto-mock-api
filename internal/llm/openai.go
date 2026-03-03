package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const defaultBaseURL = "https://api.openai.com/v1"

// OpenAIClient implements scanner.LLMClient using the OpenAI Chat Completions API.
type OpenAIClient struct {
	apiKey  string
	model   string
	baseURL string
	temp    float64
	maxTok  int
	client  *http.Client
}

// Option configures the OpenAI client.
type Option func(*OpenAIClient)

// WithBaseURL overrides the API base URL (useful for proxies/testing).
func WithBaseURL(url string) Option {
	return func(c *OpenAIClient) { c.baseURL = url }
}

// WithHTTPClient provides a custom http.Client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *OpenAIClient) { c.client = hc }
}

// NewOpenAIClient creates a new OpenAI API client.
func NewOpenAIClient(apiKey, model string, temperature float64, maxTokens int, opts ...Option) *OpenAIClient {
	c := &OpenAIClient{
		apiKey:  apiKey,
		model:   model,
		baseURL: defaultBaseURL,
		temp:    temperature,
		maxTok:  maxTokens,
		client:  http.DefaultClient,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ChatCompletion sends a chat completion request and returns the response text.
func (c *OpenAIClient) ChatCompletion(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	body := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: c.temp,
		MaxTokens:   c.maxTok,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling openai: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai returned %d: %s", resp.StatusCode, string(respBody))
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("openai error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("openai returned no choices")
	}

	return chatResp.Choices[0].Message.Content, nil
}
