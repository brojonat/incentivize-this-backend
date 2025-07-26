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

// CheckContentRequirementsResult represents the result of checking content requirements.
// It's used for both text and image analysis.
type CheckContentRequirementsResult struct {
	Satisfies bool   `json:"satisfies"`
	Reason    string `json:"reason"`
}

// ValidatePayoutWalletResult is the structured response from the payout wallet validation LLM call.
type ValidatePayoutWalletResult struct {
	IsValid bool   `json:"is_valid"`
	Reason  string `json:"reason"`
}

// LLMProvider represents a generic LLM service provider for text completion
type LLMProvider interface {
	// GenerateResponse sends a conversational history to the LLM and gets a response.
	GenerateResponse(ctx context.Context, messages []Message, tools []Tool) (*LLMResponse, error)
	// ValidateWalletWithPrompt uses the LLM to validate a wallet based on a given prompt.
	ValidateWalletWithPrompt(ctx context.Context, payoutWallet string, validationPrompt string) (ValidatePayoutWalletResult, error)
}

// Tool defines a function the LLM can invoke.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Parameters defined as a JSON Schema object.
	Parameters map[string]interface{} `json:"parameters"`
}

// ToolCall represents the LLM's request to call a specific tool.
type ToolCall struct {
	ID        string `json:"id"`        // A unique identifier for the tool call instance.
	Name      string `json:"name"`      // The name of the tool to be called.
	Arguments string `json:"arguments"` // A JSON string containing the arguments.
}

// Message represents a single message in the conversation history.
type Message struct {
	Role       string     `json:"role"`                   // "user", "assistant", or "tool"
	Content    string     `json:"content"`                // Text content of the message.
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // For assistant messages requesting tool calls.
	ToolCallID string     `json:"tool_call_id,omitempty"` // For "tool" role messages, linking to a ToolCall.
}

// LLMResponse is the object returned by the LLM provider.
// It can either be a final answer or a request to call tools.
type LLMResponse struct {
	// The final text content, if available.
	Content string
	// A list of tool calls requested by the LLM. If this is not empty,
	// the application should execute the tools and send the results back.
	ToolCalls []ToolCall
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

// GenerateResponse sends a conversational history to the LLM and gets a response.
func (p *OpenAIProvider) GenerateResponse(ctx context.Context, messages []Message, tools []Tool) (*LLMResponse, error) {
	// 1. Map our abstract aPI to the OpenAI specific API
	// API specific-structs
	type openAIMessage struct {
		Role       string      `json:"role"`
		Content    string      `json:"content"`
		ToolCalls  []*ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string      `json:"tool_call_id,omitempty"`
	}
	type openAITool struct {
		Type     string `json:"type"`
		Function Tool   `json:"function"`
	}

	// map messages
	apiMessages := make([]openAIMessage, len(messages))
	for i, msg := range messages {
		apiMessages[i] = openAIMessage{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
		}
		if msg.ToolCalls != nil {
			apiMessages[i].ToolCalls = make([]*ToolCall, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				apiMessages[i].ToolCalls[j] = &tc
			}
		}
	}

	// map tools
	apiTools := make([]openAITool, len(tools))
	for i, t := range tools {
		apiTools[i] = openAITool{
			Type:     "function",
			Function: t,
		}
	}

	// 2. Create the request body
	reqBody := struct {
		Model       string          `json:"model"`
		Messages    []openAIMessage `json:"messages"`
		Tools       []openAITool    `json:"tools,omitempty"`
		MaxTokens   int             `json:"max_tokens"`
		Temperature float64         `json:"temperature"`
	}{
		Model:       p.cfg.Model,
		Messages:    apiMessages,
		Tools:       apiTools,
		MaxTokens:   p.cfg.MaxTokens,
		Temperature: p.cfg.Temperature,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal openai request body: %w", err)
	}

	url := "https://api.openai.com/v1/chat/completions"
	if p.cfg.BaseURL != "" {
		url = p.cfg.BaseURL
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create openai request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	// 3. Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send openai request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai api returned status %d: %s", resp.StatusCode, string(body))
	}

	// 4. Parse the response
	var result struct {
		Choices []struct {
			Message struct {
				Content   string      `json:"content"`
				ToolCalls []*ToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to parse openai response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no choices in openai response")
	}

	choice := result.Choices[0].Message
	response := &LLMResponse{
		Content: choice.Content,
	}

	if choice.ToolCalls != nil {
		response.ToolCalls = make([]ToolCall, len(choice.ToolCalls))
		for i, tc := range choice.ToolCalls {
			response.ToolCalls[i] = *tc
		}
	}

	return response, nil
}

// ValidateWalletWithPrompt uses the LLM to validate a wallet based on a given prompt.
func (p *OpenAIProvider) ValidateWalletWithPrompt(ctx context.Context, payoutWallet string, validationPrompt string) (ValidatePayoutWalletResult, error) {
	prompt := fmt.Sprintf("Please validate the payout wallet `%s` based on the following prompt: %s", payoutWallet, validationPrompt)

	messages := []Message{
		{
			Role:    "user",
			Content: prompt,
		},
	}

	tools := []Tool{
		{
			Name:        "submit_wallet_validation",
			Description: "Determine if a payout wallet is valid and provide a reason.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"is_valid": map[string]interface{}{
						"type":        "boolean",
						"description": "True if the payout wallet is valid.",
					},
					"reason": map[string]interface{}{
						"type":        "string",
						"description": "A brief explanation for the decision.",
					},
				},
				"required":             []string{"is_valid", "reason"},
				"additionalProperties": false,
			},
		},
	}

	resp, err := p.GenerateResponse(ctx, messages, tools)
	if err != nil {
		return ValidatePayoutWalletResult{IsValid: false, Reason: "LLM communication error"}, fmt.Errorf("failed to get LLM response: %w", err)
	}

	if len(resp.ToolCalls) != 1 {
		return ValidatePayoutWalletResult{IsValid: false, Reason: "LLM response format error"}, fmt.Errorf("unexpected number of tool calls: %d", len(resp.ToolCalls))
	}

	var result ValidatePayoutWalletResult
	if err := json.Unmarshal([]byte(resp.ToolCalls[0].Arguments), &result); err != nil {
		return ValidatePayoutWalletResult{IsValid: false, Reason: "LLM response parsing error"}, fmt.Errorf("failed to parse LLM response arguments: %w", err)
	}

	return result, nil
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

func (p *AnthropicProvider) GenerateResponse(ctx context.Context, messages []Message, tools []Tool) (*LLMResponse, error) {
	// Anthropic's tool calling is also in beta and requires a specific header.
	// For now, we will return a "not implemented" error.
	// We will also remove the old `Complete` method.
	return nil, fmt.Errorf("not implemented")
}

func (p *AnthropicProvider) ValidateWalletWithPrompt(ctx context.Context, payoutWallet string, validationPrompt string) (ValidatePayoutWalletResult, error) {
	return ValidatePayoutWalletResult{IsValid: false, Reason: "not implemented"}, fmt.Errorf("not implemented")
}

// OllamaProvider implements LLMProvider for Ollama
type OllamaProvider struct {
	cfg LLMConfig
}

func (p *OllamaProvider) GenerateResponse(ctx context.Context, messages []Message, tools []Tool) (*LLMResponse, error) {
	// Ollama's support for JSON schema varies by model.
	// We'll rely on prompt engineering and the json format parameter.
	// This implementation will ignore the schema.
	return nil, fmt.Errorf("not implemented")
}

func (p *OllamaProvider) ValidateWalletWithPrompt(ctx context.Context, payoutWallet string, validationPrompt string) (ValidatePayoutWalletResult, error) {
	return ValidatePayoutWalletResult{IsValid: false, Reason: "not implemented"}, fmt.Errorf("not implemented")
}
