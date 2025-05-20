package abb

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// LLMProvider represents a generic LLM service provider for text completion
type LLMProvider interface {
	Complete(ctx context.Context, prompt string) (string, error)
}

// LLMConfig holds configuration for text LLM providers
type LLMConfig struct {
	Provider    string // "openai", "anthropic", "ollama"
	APIKey      string
	Model       string
	BaseURL     string // useful for self-hosted models or different endpoints
	MaxTokens   int
	Temperature float64
	BasePrompt  string // Added BasePrompt
}

// ImageLLMProvider represents a generic LLM service provider for image analysis
type ImageLLMProvider interface {
	// AnalyzeImage analyzes the image data based on the prompt and returns a structured result.
	AnalyzeImage(ctx context.Context, imageData []byte, prompt string) (CheckContentRequirementsResult, error)
}

// ImageLLMConfig holds configuration for image LLM providers
type ImageLLMConfig struct {
	Provider   string // "openai" (currently only openai vision supported)
	APIKey     string
	Model      string
	BasePrompt string // Added BasePrompt
	// Potentially add BaseURL if needed for self-hosted vision models
}

// LLMEmbeddingProvider defines the interface for generating text embeddings.
type LLMEmbeddingProvider interface {
	GenerateEmbedding(ctx context.Context, text string, modelName string) ([]float32, error)
}

// EmbeddingDimension is a placeholder for the actual dimension of your embeddings.
// This should match the model you are using (e.g., 768, 1536).
const EmbeddingDimension = 768 // Example dimension, configure as needed

// NewLLMProvider creates a new text LLM provider based on the configuration
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

// NewImageLLMProvider creates a new image LLM provider based on the configuration
func NewImageLLMProvider(cfg ImageLLMConfig) (ImageLLMProvider, error) {
	switch cfg.Provider {
	case "openai":
		// Currently only supports OpenAI vision models
		return &OpenAIImageProvider{cfg: cfg}, nil
	default:
		return nil, fmt.Errorf("unsupported Image LLM provider: %s", cfg.Provider)
	}
}

// OpenAIProvider implements LLMProvider for OpenAI (Text)
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

// OpenAIImageProvider implements ImageLLMProvider for OpenAI (Vision)
type OpenAIImageProvider struct {
	cfg ImageLLMConfig // Uses ImageLLMConfig
}

func (p *OpenAIImageProvider) AnalyzeImage(ctx context.Context, imageData []byte, prompt string) (CheckContentRequirementsResult, error) {
	// Encode image to base64
	base64Image := base64.StdEncoding.EncodeToString(imageData)
	imageUrl := fmt.Sprintf("data:image/jpeg;base64,%s", base64Image) // Assuming JPEG, adjust if needed or detect mime type

	// Construct the structured prompt asking for JSON output
	structuredPrompt := fmt.Sprintf(`%s

You must analyze the image based ONLY on the requirements related to visual content. Ignore requirements about text, links, or other non-visual aspects.
Respond ONLY with a valid JSON object containing two keys: "satisfies" (a boolean indicating if the image meets the relevant requirements) and "reason" (a string explaining your decision). Example: {"satisfies": true, "reason": "Image meets all visual criteria."}
If there are no specific visual requirements mentioned, respond with {"satisfies": true, "reason": "No specific visual requirements specified."}`, prompt)

	// Create request body for GPT-4 Vision API
	// Reference: https://platform.openai.com/docs/guides/vision
	reqBody := struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string        `json:"role"`
			Content []interface{} `json:"content"` // Content is an array for vision
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"` // Max tokens for the *text* response
	}{
		Model: p.cfg.Model,
		Messages: []struct {
			Role    string        `json:"role"`
			Content []interface{} `json:"content"`
		}{
			{
				Role: "user",
				Content: []interface{}{
					map[string]string{"type": "text", "text": structuredPrompt},
					map[string]interface{}{"type": "image_url", "image_url": map[string]string{"url": imageUrl}},
				},
			},
		},
		MaxTokens: 1000, // Adjust max response tokens as needed
	}

	// Marshal request body
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to marshal OpenAI vision request body: %w", err)
	}

	// Create request (Use the standard chat completions endpoint)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to create OpenAI vision request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	// Send request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to send OpenAI vision request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	bodyBytes, _ := io.ReadAll(resp.Body) // Read body for potential error messages
	if resp.StatusCode != http.StatusOK {
		return CheckContentRequirementsResult{}, fmt.Errorf("OpenAI Vision API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Parse standard chat completion response to get the raw text content
	var completionResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(bodyBytes, &completionResponse); err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to parse OpenAI vision completion response: %w (body: %s)", err, string(bodyBytes))
	}

	if len(completionResponse.Choices) == 0 || completionResponse.Choices[0].Message.Content == "" {
		return CheckContentRequirementsResult{}, fmt.Errorf("no analysis content in OpenAI vision response")
	}

	// Now, parse the message content as JSON into CheckContentRequirementsResult
	llmResponseContent := strings.TrimSpace(completionResponse.Choices[0].Message.Content)
	if strings.HasPrefix(llmResponseContent, "```json") {
		llmResponseContent = strings.TrimPrefix(llmResponseContent, "```json")
		llmResponseContent = strings.TrimSuffix(llmResponseContent, "```")
	}
	llmResponseContent = strings.TrimSpace(llmResponseContent)

	var result CheckContentRequirementsResult
	if err := json.Unmarshal([]byte(llmResponseContent), &result); err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to parse LLM JSON response for image analysis: %w (raw_content: %s)", err, llmResponseContent)
	}

	return result, nil
}

// AnthropicProvider implements LLMProvider for Anthropic (Text)
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
