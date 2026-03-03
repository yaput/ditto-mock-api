package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatCompletion_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing auth header")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing content-type")
		}

		var req chatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "gpt-4o-mini" {
			t.Errorf("expected gpt-4o-mini, got %s", req.Model)
		}
		if len(req.Messages) != 2 {
			t.Errorf("expected 2 messages, got %d", len(req.Messages))
		}

		resp := chatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{Content: `{"id":"123"}`}},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewOpenAIClient("test-key", "gpt-4o-mini", 0.7, 1000, WithBaseURL(srv.URL))
	result, err := client.ChatCompletion(context.Background(), "system", "user prompt")
	if err != nil {
		t.Fatal(err)
	}
	if result != `{"id":"123"}` {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestChatCompletion_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer srv.Close()

	client := NewOpenAIClient("test-key", "gpt-4o-mini", 0.7, 1000, WithBaseURL(srv.URL))
	_, err := client.ChatCompletion(context.Background(), "system", "user")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestChatCompletion_EmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(chatResponse{})
	}))
	defer srv.Close()

	client := NewOpenAIClient("test-key", "gpt-4o-mini", 0.7, 1000, WithBaseURL(srv.URL))
	_, err := client.ChatCompletion(context.Background(), "system", "user")
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestNewOpenAIClient_Defaults(t *testing.T) {
	c := NewOpenAIClient("key", "model", 0.5, 500)
	if c.baseURL != defaultBaseURL {
		t.Errorf("expected default base URL, got %s", c.baseURL)
	}
	if c.client != http.DefaultClient {
		t.Error("expected default http client")
	}
}
