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

// LLMProvider represents a generic LLM service provider for text completion
type LLMProvider interface {
	// GenerateResponse executes a simple one-shot call: instructions + message -> assistant text (no tool calls)
	// If schemaJSON is non-nil, it must be a JSON object with fields compatible with Responses API JSON schema config
	// e.g. {"name":"MySchema","strict":true,"schema":{...}} and will be passed under response_format.json_schema.
	GenerateResponse(ctx context.Context, instructions string, message string, schemaJSON []byte) (string, error)

	// GenerateResponsesTurn executes a single turn of a multi-turn agent using the Responses API.
	//  - previousResponseID empty => start turn with userInput and tools
	//  - previousResponseID set   => continue turn with functionOutputs only
	GenerateResponsesTurn(
		ctx context.Context,
		previousResponseID string,
		userInput string,
		tools []Tool,
		functionOutputs map[string]string,
	) (assistantText string, toolCalls []ToolCall, responseID string, err error)

	// AnalyzeImage analyzes the image from a URL based on the prompt and returns a structured result.
	AnalyzeImage(ctx context.Context, imageURL string, prompt string) (CheckContentRequirementsResult, error)
}

// Tool defines a function the LLM can invoke.
type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
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
func (p *OpenAIProvider) GenerateResponse(ctx context.Context, instructions string, message string, schemaJSON []byte) (string, error) {
	reqBody := map[string]interface{}{
		"model":             p.cfg.Model,
		"instructions":      instructions,
		"input":             message,
		"store":             true,
		"max_output_tokens": p.cfg.MaxTokens,
	}

	if len(schemaJSON) > 0 {
		// Provide structured output via JSON schema under text.format per Responses API
		var schemaWrapper struct {
			Name   string          `json:"name"`
			Strict bool            `json:"strict"`
			Schema json.RawMessage `json:"schema"`
		}
		if err := json.Unmarshal(schemaJSON, &schemaWrapper); err != nil {
			return "", fmt.Errorf("failed to unmarshal schemaJSON: %w", err)
		}
		if schemaWrapper.Name == "" {
			return "", fmt.Errorf("schema name is required for structured outputs")
		}
		if len(schemaWrapper.Schema) == 0 {
			return "", fmt.Errorf("schema is empty for structured outputs")
		}

		reqBody["text"] = map[string]interface{}{
			"format": map[string]interface{}{
				"type":   "json_schema",
				"name":   schemaWrapper.Name,
				"schema": schemaWrapper.Schema,
				"strict": schemaWrapper.Strict,
			},
		}
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal responses request body: %w", err)
	}

	apiURL := "https://api.openai.com/v1/responses"
	if p.cfg.BaseURL != "" {
		apiURL = strings.TrimSuffix(p.cfg.BaseURL, "/") + "/responses"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create responses request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send responses request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("responses api returned status %d: %s", resp.StatusCode, string(body))
	}

	assistantText, _, _, err := parseResponsesOutput(body)
	if err != nil {
		return "", err
	}
	return assistantText, nil
}

func (p *OpenAIProvider) GenerateResponsesTurn(ctx context.Context, previousResponseID string, userInput string, tools []Tool, functionOutputs map[string]string) (string, []ToolCall, string, error) {
	req := map[string]interface{}{
		"model":             p.cfg.Model,
		"store":             true,
		"max_output_tokens": p.cfg.MaxTokens,
	}

	if previousResponseID != "" {
		req["previous_response_id"] = previousResponseID
		inputs := make([]map[string]interface{}, 0, len(functionOutputs))
		for callID, output := range functionOutputs {
			inputs = append(inputs, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  output,
			})
		}
		req["input"] = inputs
	} else {
		req["input"] = userInput
	}

	if len(tools) > 0 {
		toolList := make([]map[string]interface{}, 0, len(tools))
		for _, t := range tools {
			toolList = append(toolList, map[string]interface{}{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Parameters,
				"strict":      true,
			})
		}
		req["tools"] = toolList
	}

	jsonData, err := json.Marshal(req)
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to marshal responses request: %w", err)
	}
	apiURL := "https://api.openai.com/v1/responses"
	if p.cfg.BaseURL != "" {
		apiURL = strings.TrimSuffix(p.cfg.BaseURL, "/") + "/responses"
	}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to create responses request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	client := &http.Client{}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return "", nil, "", fmt.Errorf("failed to send responses request: %w", err)
	}
	defer httpResp.Body.Close()
	body, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode != http.StatusOK {
		return "", nil, "", fmt.Errorf("responses api returned status %d: %s", httpResp.StatusCode, string(body))
	}

	assistantText, toolCalls, responseID, err := parseResponsesOutput(body)
	if err != nil {
		return "", nil, "", err
	}
	return assistantText, toolCalls, responseID, nil
}

func parseResponsesOutput(body []byte) (assistantText string, toolCalls []ToolCall, responseID string, err error) {
	var root struct {
		ID     string          `json:"id"`
		Output json.RawMessage `json:"output"`
	}
	if e := json.Unmarshal(body, &root); e != nil {
		return "", nil, "", fmt.Errorf("failed to decode responses body: %w", e)
	}
	responseID = root.ID

	var items []map[string]any
	if e := json.Unmarshal(root.Output, &items); e != nil {
		var alt struct {
			Output []map[string]any `json:"output"`
		}
		if e2 := json.Unmarshal(body, &alt); e2 == nil && len(alt.Output) > 0 {
			items = alt.Output
		} else {
			return "", nil, responseID, fmt.Errorf("unexpected responses output format")
		}
	}

	var textBuilder []string
	var calls []ToolCall
	for _, it := range items {
		t, _ := it["type"].(string)
		switch t {
		case "message":
			if content, ok := it["content"].([]any); ok {
				for _, c := range content {
					if cm, ok := c.(map[string]any); ok {
						if cm["type"] == "output_text" {
							if txt, _ := cm["text"].(string); txt != "" {
								textBuilder = append(textBuilder, txt)
							}
						}
					}
				}
			}
			if mtc, ok := it["tool_calls"].([]any); ok {
				for _, raw := range mtc {
					if m, ok := raw.(map[string]any); ok {
						id, _ := m["id"].(string)
						if fn, ok := m["function"].(map[string]any); ok {
							name, _ := fn["name"].(string)
							args, _ := fn["arguments"].(string)
							if id != "" && name != "" {
								calls = append(calls, ToolCall{ID: id, Name: name, Arguments: args})
							}
						}
					}
				}
			}
		case "function_call":
			id, _ := it["call_id"].(string)
			name, _ := it["name"].(string)
			var argsStr string
			if s, ok := it["arguments"].(string); ok {
				argsStr = s
			} else if obj, ok := it["arguments"].(map[string]any); ok {
				if b, e := json.Marshal(obj); e == nil {
					argsStr = string(b)
				}
			}
			if id != "" && name != "" {
				calls = append(calls, ToolCall{ID: id, Name: name, Arguments: argsStr})
			}
		}
	}

	return strings.TrimSpace(strings.Join(textBuilder, "\n")), calls, responseID, nil
}

// AnalyzeImage analyzes an image using the OpenAI Responses API with vision capabilities.
func (p *OpenAIProvider) AnalyzeImage(ctx context.Context, imageURL string, prompt string) (CheckContentRequirementsResult, error) {
	// --- Download Image ---
	req, err := http.NewRequestWithContext(ctx, "GET", imageURL, nil)
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to create image download request: %w", err)
	}
	req.Header.Set("User-Agent", "AffiliateBountyBoard-Worker/1.0")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to download image, status: %d", resp.StatusCode)
	}

	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to read image data: %w", err)
	}

	// Encode image to base64
	base64Image := base64.StdEncoding.EncodeToString(imageData)
	imageUrl := fmt.Sprintf("data:image/jpeg;base64,%s", base64Image)

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
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to marshal schema: %w", err)
	}

	// Construct a structured input for the Responses API, similar to chat completions
	inputContent := []map[string]interface{}{
		{
			"type": "message",
			"role": "user",
			"content": []map[string]interface{}{
				{"type": "input_text", "text": prompt},
				{"type": "input_image", "image_url": imageUrl},
			},
		},
	}

	reqBody := map[string]interface{}{
		"model":             p.cfg.Model,
		"input":             inputContent,
		"store":             true,
		"max_output_tokens": 4096,
	}

	var schemaWrapper struct {
		Name   string          `json:"name"`
		Strict bool            `json:"strict"`
		Schema json.RawMessage `json:"schema"`
	}
	if err := json.Unmarshal(schemaJSON, &schemaWrapper); err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to unmarshal schemaJSON: %w", err)
	}

	reqBody["text"] = map[string]interface{}{
		"format": map[string]interface{}{
			"type":   "json_schema",
			"name":   schemaWrapper.Name,
			"schema": schemaWrapper.Schema,
			"strict": schemaWrapper.Strict,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to marshal OpenAI vision request body: %w", err)
	}

	apiURL := "https://api.openai.com/v1/responses"
	req, err = http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to create OpenAI vision request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)

	resp, err = client.Do(req)
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to send OpenAI vision request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return CheckContentRequirementsResult{}, fmt.Errorf("OpenAI Vision API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	assistantText, _, _, err := parseResponsesOutput(bodyBytes)
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to parse OpenAI vision API response: %w", err)
	}

	if assistantText == "" {
		return CheckContentRequirementsResult{}, fmt.Errorf("received an empty response from OpenAI vision API")
	}

	var analysisResult CheckContentRequirementsResult
	if err := json.Unmarshal([]byte(assistantText), &analysisResult); err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to unmarshal JSON from vision API response content: %w. Content: %s", err, assistantText)
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

func (p *AnthropicProvider) GenerateResponse(ctx context.Context, instructions string, message string, schemaJSON []byte) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (p *AnthropicProvider) GenerateResponsesTurn(ctx context.Context, previousResponseID string, userInput string, tools []Tool, functionOutputs map[string]string) (string, []ToolCall, string, error) {
	return "", nil, "", fmt.Errorf("not implemented")
}

func (p *AnthropicProvider) AnalyzeImage(ctx context.Context, imageURL string, prompt string) (CheckContentRequirementsResult, error) {
	return CheckContentRequirementsResult{}, fmt.Errorf("not implemented")
}

// AnthropicProvider implements LLMProvider for Anthropic (Text)
type GeminiProvider struct {
	cfg LLMConfig
}

func (p *GeminiProvider) GenerateResponse(ctx context.Context, instructions string, message string, schemaJSON []byte) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (p *GeminiProvider) GenerateResponsesTurn(ctx context.Context, previousResponseID string, userInput string, tools []Tool, functionOutputs map[string]string) (string, []ToolCall, string, error) {
	return "", nil, "", fmt.Errorf("not implemented")
}

func (p *GeminiProvider) AnalyzeImage(ctx context.Context, imageURL string, prompt string) (CheckContentRequirementsResult, error) {
	return CheckContentRequirementsResult{}, fmt.Errorf("not implemented")
}

// OllamaProvider implements LLMProvider for Ollama
type OllamaProvider struct {
	cfg LLMConfig
}

func (p *OllamaProvider) GenerateResponse(ctx context.Context, instructions string, message string, schemaJSON []byte) (string, error) {
	return "", fmt.Errorf("not implemented")
}

func (p *OllamaProvider) GenerateResponsesTurn(ctx context.Context, previousResponseID string, userInput string, tools []Tool, functionOutputs map[string]string) (string, []ToolCall, string, error) {
	return "", nil, "", fmt.Errorf("not implemented")
}

func (p *OllamaProvider) AnalyzeImage(ctx context.Context, imageURL string, prompt string) (CheckContentRequirementsResult, error) {
	return CheckContentRequirementsResult{}, fmt.Errorf("not implemented")
}
