package abb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLLMProvider(t *testing.T) {
	tests := []struct {
		name          string
		cfg           LLMConfig
		expectedType  string
		expectedError bool
	}{
		{
			name: "openai provider",
			cfg: LLMConfig{
				Provider:    "openai",
				APIKey:      "test-api-key",
				Model:       "gpt-4",
				MaxTokens:   1000,
				Temperature: 0.7,
			},
			expectedType:  "*abb.OpenAIProvider",
			expectedError: false,
		},
		{
			name: "anthropic provider",
			cfg: LLMConfig{
				Provider:    "anthropic",
				APIKey:      "test-api-key",
				Model:       "claude-3",
				MaxTokens:   1000,
				Temperature: 0.7,
			},
			expectedType:  "*abb.AnthropicProvider",
			expectedError: false,
		},
		{
			name: "ollama provider",
			cfg: LLMConfig{
				Provider:    "ollama",
				APIKey:      "",
				Model:       "llama2",
				BaseURL:     "http://localhost:11434",
				MaxTokens:   1000,
				Temperature: 0.7,
			},
			expectedType:  "*abb.OllamaProvider",
			expectedError: false,
		},
		{
			name: "unsupported provider",
			cfg: LLMConfig{
				Provider:    "invalid",
				APIKey:      "test-key",
				Model:       "model",
				MaxTokens:   1000,
				Temperature: 0.7,
			},
			expectedType:  "",
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewLLMProvider(tt.cfg)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, provider)
				assert.Contains(t, err.Error(), "unsupported LLM provider")
			} else {
				require.NoError(t, err)
				require.NotNil(t, provider)

				// Verify interface conformance
				var _ LLMProvider = provider

				// Check type
				typeName := getTypeName(provider)
				assert.Equal(t, tt.expectedType, typeName)
			}
		})
	}
}

func TestNewLLMEmbeddingProvider(t *testing.T) {
	tests := []struct {
		name          string
		cfg           EmbeddingConfig
		expectedType  string
		expectedError bool
	}{
		{
			name: "openai embedding provider",
			cfg: EmbeddingConfig{
				Provider: "openai",
				APIKey:   "test-api-key",
				Model:    "text-embedding-3-small",
			},
			expectedType:  "*abb.OpenAIEmbeddingProvider",
			expectedError: false,
		},
		{
			name: "openai with custom base URL",
			cfg: EmbeddingConfig{
				Provider: "openai",
				APIKey:   "test-api-key",
				Model:    "text-embedding-ada-002",
				BaseURL:  "https://custom.openai.com",
			},
			expectedType:  "*abb.OpenAIEmbeddingProvider",
			expectedError: false,
		},
		{
			name: "unsupported embedding provider",
			cfg: EmbeddingConfig{
				Provider: "unsupported",
				APIKey:   "test-key",
				Model:    "model",
			},
			expectedType:  "",
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewLLMEmbeddingProvider(tt.cfg)

			if tt.expectedError {
				assert.Error(t, err)
				assert.Nil(t, provider)
				assert.Contains(t, err.Error(), "unsupported LLM Embedding provider")
			} else {
				require.NoError(t, err)
				require.NotNil(t, provider)

				// Verify interface conformance
				var _ LLMEmbeddingProvider = provider

				// Check type
				typeName := getTypeName(provider)
				assert.Equal(t, tt.expectedType, typeName)
			}
		})
	}
}

func TestLLMConfig_Validation(t *testing.T) {
	// Test that valid configs can be created
	validConfig := LLMConfig{
		Provider:    "openai",
		APIKey:      "sk-test123",
		Model:       "gpt-4",
		BaseURL:     "https://api.openai.com",
		MaxTokens:   2000,
		Temperature: 0.5,
	}

	provider, err := NewLLMProvider(validConfig)
	require.NoError(t, err)
	assert.NotNil(t, provider)

	// Verify config is stored correctly
	openAIProvider, ok := provider.(*OpenAIProvider)
	require.True(t, ok)
	assert.Equal(t, "openai", openAIProvider.cfg.Provider)
	assert.Equal(t, "sk-test123", openAIProvider.cfg.APIKey)
	assert.Equal(t, "gpt-4", openAIProvider.cfg.Model)
	assert.Equal(t, "https://api.openai.com", openAIProvider.cfg.BaseURL)
	assert.Equal(t, 2000, openAIProvider.cfg.MaxTokens)
	assert.Equal(t, 0.5, openAIProvider.cfg.Temperature)
}

func TestEmbeddingConfig_Validation(t *testing.T) {
	// Test that valid embedding configs can be created
	validConfig := EmbeddingConfig{
		Provider: "openai",
		APIKey:   "sk-test456",
		Model:    "text-embedding-3-small",
		BaseURL:  "https://api.openai.com",
	}

	provider, err := NewLLMEmbeddingProvider(validConfig)
	require.NoError(t, err)
	assert.NotNil(t, provider)

	// Verify config is stored correctly
	openAIProvider, ok := provider.(*OpenAIEmbeddingProvider)
	require.True(t, ok)
	assert.Equal(t, "openai", openAIProvider.cfg.Provider)
	assert.Equal(t, "sk-test456", openAIProvider.cfg.APIKey)
	assert.Equal(t, "text-embedding-3-small", openAIProvider.cfg.Model)
	assert.Equal(t, "https://api.openai.com", openAIProvider.cfg.BaseURL)
}

func TestToolStructure(t *testing.T) {
	// Test that Tool struct can be properly constructed
	tool := Tool{
		Name:        "test_function",
		Description: "A test function for testing",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"param1": map[string]interface{}{
					"type":        "string",
					"description": "First parameter",
				},
			},
			"required": []string{"param1"},
		},
	}

	assert.Equal(t, "test_function", tool.Name)
	assert.Equal(t, "A test function for testing", tool.Description)
	assert.NotNil(t, tool.Parameters)
	assert.Equal(t, "object", tool.Parameters["type"])
}

func TestToolCallStructure(t *testing.T) {
	// Test that ToolCall struct can be properly constructed
	toolCall := ToolCall{
		ID:        "call_abc123",
		Name:      "pull_content",
		Arguments: `{"platform":"reddit","content_id":"123"}`,
	}

	assert.Equal(t, "call_abc123", toolCall.ID)
	assert.Equal(t, "pull_content", toolCall.Name)
	assert.Contains(t, toolCall.Arguments, "reddit")
}

func TestCheckContentRequirementsResult(t *testing.T) {
	// Test success case
	successResult := CheckContentRequirementsResult{
		Satisfies: true,
		Reason:    "Content meets all requirements",
	}
	assert.True(t, successResult.Satisfies)
	assert.Equal(t, "Content meets all requirements", successResult.Reason)

	// Test failure case
	failureResult := CheckContentRequirementsResult{
		Satisfies: false,
		Reason:    "Missing required elements",
	}
	assert.False(t, failureResult.Satisfies)
	assert.Equal(t, "Missing required elements", failureResult.Reason)
}

func TestMessage_Structure(t *testing.T) {
	// Test user message
	userMsg := Message{
		Role:    "user",
		Content: "Hello, assistant!",
	}
	assert.Equal(t, "user", userMsg.Role)
	assert.Equal(t, "Hello, assistant!", userMsg.Content)
	assert.Empty(t, userMsg.ToolCalls)

	// Test assistant message with tool calls
	assistantMsg := Message{
		Role:    "assistant",
		Content: "",
		ToolCalls: []ToolCall{
			{
				ID:        "call_1",
				Name:      "function1",
				Arguments: `{"arg":"value"}`,
			},
		},
	}
	assert.Equal(t, "assistant", assistantMsg.Role)
	assert.Len(t, assistantMsg.ToolCalls, 1)
	assert.Equal(t, "call_1", assistantMsg.ToolCalls[0].ID)

	// Test tool response message
	toolMsg := Message{
		Role:       "tool",
		Content:    "Function result",
		ToolCallID: "call_1",
	}
	assert.Equal(t, "tool", toolMsg.Role)
	assert.Equal(t, "call_1", toolMsg.ToolCallID)
}

func TestLLMResponse_Structure(t *testing.T) {
	// Test response with content only
	textResponse := LLMResponse{
		Content:   "Here is my response",
		ToolCalls: nil,
	}
	assert.Equal(t, "Here is my response", textResponse.Content)
	assert.Empty(t, textResponse.ToolCalls)

	// Test response with tool calls
	toolResponse := LLMResponse{
		Content: "",
		ToolCalls: []ToolCall{
			{ID: "call_1", Name: "tool1", Arguments: "{}"},
			{ID: "call_2", Name: "tool2", Arguments: "{}"},
		},
	}
	assert.Empty(t, toolResponse.Content)
	assert.Len(t, toolResponse.ToolCalls, 2)
}

func TestEmbeddingDimension_Constant(t *testing.T) {
	// Verify the embedding dimension constant is set correctly
	assert.Equal(t, 1536, EmbeddingDimension)
}

// Helper function to get type name for assertions
func getTypeName(v interface{}) string {
	if v == nil {
		return "<nil>"
	}
	return getType(v)
}

func getType(v interface{}) string {
	switch v.(type) {
	case *OpenAIProvider:
		return "*abb.OpenAIProvider"
	case *AnthropicProvider:
		return "*abb.AnthropicProvider"
	case *OllamaProvider:
		return "*abb.OllamaProvider"
	case *OpenAIEmbeddingProvider:
		return "*abb.OpenAIEmbeddingProvider"
	default:
		return "unknown"
	}
}
