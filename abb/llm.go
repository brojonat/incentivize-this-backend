package abb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// LLMProvider represents a generic LLM service provider
type LLMProvider interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// LLMConfig holds configuration for LLM providers
type LLMConfig struct {
	Provider    string // "openai", "anthropic", "ollama"
	APIKey      string
	Model       string
	BaseURL     string // useful for self-hosted models or different endpoints
	MaxTokens   int
	Temperature float64
}

// NewLLMProvider creates a new LLM provider based on the configuration
func NewLLMProvider(cfg LLMConfig) (LLMProvider, error) {
	switch cfg.Provider {
	case "openai":
		return &OpenAIProvider{cfg: cfg}, nil
	case "anthropic":
		return &AnthropicProvider{cfg: cfg}, nil
	case "ollama":
		return &OllamaProvider{cfg: cfg}, nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", cfg.Provider)
	}
}

// OpenAIProvider implements LLMProvider for OpenAI
type OpenAIProvider struct {
	cfg LLMConfig
}

func (p *OpenAIProvider) Complete(ctx context.Context, prompt string) (string, error) {
	// Create request body
	reqBody := struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens   int     `json:"max_tokens"`
		Temperature float64 `json:"temperature"`
	}{
		Model: p.cfg.Model,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{
				Role:    "user",
				Content: prompt,
			},
		},
		MaxTokens:   p.cfg.MaxTokens,
		Temperature: p.cfg.Temperature,
	}

	// Marshal request body
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return result.Choices[0].Message.Content, nil
}

// AnthropicProvider implements LLMProvider for Anthropic
type AnthropicProvider struct {
	cfg LLMConfig
}

func (p *AnthropicProvider) Complete(ctx context.Context, prompt string) (string, error) {
	// Implementation will go here
	return "", nil
}

// OllamaProvider implements LLMProvider for Ollama
type OllamaProvider struct {
	cfg LLMConfig
}

func (p *OllamaProvider) Complete(ctx context.Context, prompt string) (string, error) {
	// Implementation will go here
	return "", nil
}
