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
	// Complete sends a prompt to the LLM and returns the completion.
	// If schema is not nil, it will be used to request a structured JSON response.
	Complete(ctx context.Context, prompt string, schema map[string]interface{}) (string, error)
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

// EmbeddingConfig holds configuration for embedding LLM providers
type EmbeddingConfig struct {
	Provider string
	APIKey   string
	Model    string
	BaseURL  string // Optional: for self-hosted or alternative endpoints
}

// LLMEmbeddingProvider defines the interface for generating text embeddings.
type LLMEmbeddingProvider interface {
	GenerateEmbedding(ctx context.Context, text string, modelName string) ([]float32, error)
}

// EmbeddingDimension should match the model you are using (e.g., 768, 1536).
const EmbeddingDimension = 1536

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

// NewLLMEmbeddingProvider creates a new LLM embedding provider based on the configuration
func NewLLMEmbeddingProvider(cfg EmbeddingConfig) (LLMEmbeddingProvider, error) {
	switch cfg.Provider {
	case "openai":
		return &OpenAIEmbeddingProvider{cfg: cfg}, nil
	// Add other providers like "ollama" here if needed
	default:
		return nil, fmt.Errorf("unsupported LLM Embedding provider: %s", cfg.Provider)
	}
}

// OpenAIProvider implements LLMProvider for OpenAI (Text)
type OpenAIProvider struct {
	cfg LLMConfig
}

// Complete sends a prompt to the OpenAI API and returns the completion.
// If a schema is provided, it requests a structured JSON response matching the schema.
func (p *OpenAIProvider) Complete(ctx context.Context, prompt string, schema map[string]interface{}) (string, error) {
	// Define a struct for the response format that can handle json_schema
	type responseFormat struct {
		Type       string      `json:"type"`
		JSONSchema interface{} `json:"json_schema,omitempty"`
	}

	// Create request body
	reqBody := struct {
		Model          string          `json:"model"`
		Messages       []interface{}   `json:"messages"`
		MaxTokens      int             `json:"max_tokens"`
		Temperature    float64         `json:"temperature"`
		ResponseFormat *responseFormat `json:"response_format,omitempty"`
	}{
		Model: p.cfg.Model,
		Messages: []interface{}{
			map[string]string{
				"role":    "user",
				"content": prompt,
			},
		},
		MaxTokens:   p.cfg.MaxTokens,
		Temperature: p.cfg.Temperature,
	}

	// Set the response format only if a schema is provided
	if schema != nil {
		reqBody.ResponseFormat = &responseFormat{
			Type:       "json_schema",
			JSONSchema: schema,
		}
	}

	// Marshal request body
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Determine the URL to use
	url := "https://api.openai.com/v1/chat/completions"
	if p.cfg.BaseURL != "" {
		url = p.cfg.BaseURL
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
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

	// Define the JSON schema for the expected output
	schema := map[string]interface{}{
		"name":        "image_analysis",
		"description": "Analysis of an image based on provided requirements.",
		"strict":      true,
		"schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"satisfies": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether the image satisfies the visual requirements.",
				},
				"reason": map[string]interface{}{
					"type":        "string",
					"description": "The reason why the image does or does not satisfy the requirements.",
				},
			},
			"required":             []string{"satisfies", "reason"},
			"additionalProperties": false,
		},
	}

	// Create request body for GPT-4 Vision API
	reqBody := struct {
		Model          string `json:"model"`
		ResponseFormat struct {
			Type       string      `json:"type"`
			JSONSchema interface{} `json:"json_schema"`
		} `json:"response_format"`
		Messages []struct {
			Role    string        `json:"role"`
			Content []interface{} `json:"content"` // Content is an array for vision
		} `json:"messages"`
		MaxTokens int `json:"max_tokens"`
	}{
		Model: p.cfg.Model,
		ResponseFormat: struct {
			Type       string      `json:"type"`
			JSONSchema interface{} `json:"json_schema"`
		}{
			Type:       "json_schema",
			JSONSchema: schema,
		},
		Messages: []struct {
			Role    string        `json:"role"`
			Content []interface{} `json:"content"`
		}{
			{
				Role: "user",
				Content: []interface{}{
					map[string]string{"type": "text", "text": prompt},
					map[string]interface{}{"type": "image_url", "image_url": map[string]string{"url": imageUrl}},
				},
			},
		},
		MaxTokens: 2048,
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
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(bodyBytes, &apiResponse); err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to decode OpenAI vision API response: %w", err)
	}

	if len(apiResponse.Choices) == 0 || apiResponse.Choices[0].Message.Content == "" {
		return CheckContentRequirementsResult{}, fmt.Errorf("received an empty response from OpenAI vision API")
	}

	// The response content is a JSON string, so we need to unmarshal it
	var analysisResult CheckContentRequirementsResult
	if err := json.Unmarshal([]byte(apiResponse.Choices[0].Message.Content), &analysisResult); err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to unmarshal JSON from vision API response content: %w. Content: %s", err, apiResponse.Choices[0].Message.Content)
	}

	return analysisResult, nil
}

// OpenAIEmbeddingProvider implements LLMEmbeddingProvider for OpenAI
type OpenAIEmbeddingProvider struct {
	cfg EmbeddingConfig
}

// GenerateEmbedding generates text embeddings using the OpenAI API.
// The modelName parameter in the interface is available, but this implementation
// will primarily use the model specified in its own configuration (cfg.ModelName).
func (p *OpenAIEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string, modelName string) ([]float32, error) {
	// Use the model from the provider's configuration if modelName argument is empty
	effectiveModelName := p.cfg.Model
	if modelName != "" {
		effectiveModelName = modelName // Allow override if explicitly passed
	}
	if effectiveModelName == "" {
		return nil, fmt.Errorf("OpenAI embedding model name not configured or provided")
	}

	apiURL := "https://api.openai.com/v1/embeddings"
	if p.cfg.BaseURL != "" {
		apiURL = strings.TrimSuffix(p.cfg.BaseURL, "/") + "/v1/embeddings"
	}

	reqBody := struct {
		Input string `json:"input"`
		Model string `json:"model"`
	}{
		Input: text,
		Model: effectiveModelName,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal OpenAI embedding request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI embedding request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send OpenAI embedding request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI Embeddings API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI embedding response: %w (body: %s)", err, string(bodyBytes))
	}

	if len(result.Data) == 0 || len(result.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("no embedding data in OpenAI response")
	}

	return result.Data[0].Embedding, nil
}

// AnthropicProvider implements LLMProvider for Anthropic (Text)
type AnthropicProvider struct {
	cfg LLMConfig
}

func (p *AnthropicProvider) Complete(ctx context.Context, prompt string, schema map[string]interface{}) (string, error) {
	// Anthropic doesn't support JSON schema response format in the same way.
	// We'll have to rely on prompt engineering for now.
	// This implementation will ignore the schema.
	return p.Complete(ctx, prompt, nil)
}

// OllamaProvider implements LLMProvider for Ollama
type OllamaProvider struct {
	cfg LLMConfig
}

func (p *OllamaProvider) Complete(ctx context.Context, prompt string, schema map[string]interface{}) (string, error) {
	// Ollama's support for JSON schema varies by model.
	// We'll rely on prompt engineering and the json format parameter.
	// This implementation will ignore the schema.
	return p.Complete(ctx, prompt, nil)
}
