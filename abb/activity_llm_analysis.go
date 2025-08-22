package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"go.temporal.io/sdk/activity"
)

// AnalyzeImageURL downloads an image from a URL and uses a configured image LLM
// to analyze it based on a provided text prompt, returning a structured result.
func (a *Activities) AnalyzeImageURL(ctx context.Context, imageUrl string, prompt string) (CheckContentRequirementsResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Starting image analysis", "imageUrl", imageUrl)

	// --- Load Image LLM Configuration --- (Directly using os.Getenv for simplicity here)
	providerName := os.Getenv(EnvLLMImageProvider)
	apiKey := os.Getenv(EnvLLMImageAPIKey)
	modelName := os.Getenv(EnvLLMImageModel)

	if providerName == "" || apiKey == "" || modelName == "" {
		logger.Error("Image LLM environment variables not fully configured",
			"provider_env", EnvLLMImageProvider, "key_env", EnvLLMImageAPIKey, "model_env", EnvLLMImageModel)
		// Fail the activity if essential config is missing
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Image LLM provider configuration incomplete (check env vars %s, %s, %s)",
				EnvLLMImageProvider, EnvLLMImageAPIKey, EnvLLMImageModel)}, fmt.Errorf("image LLM provider configuration incomplete (check env vars %s, %s, %s)",
				EnvLLMImageProvider, EnvLLMImageAPIKey, EnvLLMImageModel)
	}

	// --- Create Image LLM Provider --- (Using a hypothetical NewImageLLMProvider)
	// We'll need to implement this provider logic in llm.go
	// For now, assume it takes a simple config struct
	imageLLMConfig := ImageLLMConfig{
		Provider: providerName,
		APIKey:   apiKey,
		Model:    modelName,
		// Add other potential image-specific config here (e.g., detail level for vision models)
	}
	imageProvider, err := NewImageLLMProvider(imageLLMConfig) // Needs implementation in llm.go
	if err != nil {
		logger.Error("Failed to create image LLM provider", "error", err)
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Image LLM provider failed: %v", err)}, fmt.Errorf("failed to create image LLM provider: %w", err)
	}

	// --- Download Image ---
	logger.Debug("Downloading image", "url", imageUrl)
	// Use the shared httpClient, but create a new request specific to this activity context
	req, err := http.NewRequestWithContext(ctx, "GET", imageUrl, nil)
	if err != nil {
		logger.Error("Failed to create image download request", "url", imageUrl, "error", err)
		return CheckContentRequirementsResult{
			Satisfies: false,
			Reason:    fmt.Sprintf("Failed to create image download request for %s: %v. Visual requirements could not be assessed.", imageUrl, err),
		}, nil
	}
	// Add a generic User-Agent
	req.Header.Set("User-Agent", "AffiliateBountyBoard-Worker/1.0")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		logger.Error("Failed to download image", "url", imageUrl, "error", err)
		return CheckContentRequirementsResult{
			Satisfies: false,
			Reason:    fmt.Sprintf("Failed to download image from %s: %v. Visual requirements could not be assessed.", imageUrl, err),
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body) // Try to read body for context
		logger.Error("Failed to download image, bad status", "url", imageUrl, "status_code", resp.StatusCode, "body", string(bodyBytes))
		return CheckContentRequirementsResult{
			Satisfies: false,
			Reason:    fmt.Sprintf("Failed to download image from %s, status: %d. Body: %s. Visual requirements could not be assessed.", imageUrl, resp.StatusCode, string(bodyBytes)),
		}, nil
	}

	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read image data", "url", imageUrl, "error", err)
		return CheckContentRequirementsResult{
			Satisfies: false,
			Reason:    fmt.Sprintf("Failed to read image data from %s: %v. Visual requirements could not be assessed.", imageUrl, err),
		}, nil
	}
	logger.Debug("Image downloaded successfully", "url", imageUrl, "size_bytes", len(imageData))

	// --- Analyze Image using Image LLM Provider ---
	logger.Info("Sending image data to image LLM for analysis", "provider", providerName, "model", modelName)
	analysisResult, err := imageProvider.AnalyzeImage(ctx, imageData, prompt)
	if err != nil {
		logger.Error("Image LLM analysis failed", "error", err)
		// Return a default 'false' result along with the error
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Image LLM provider failed: %v", err)}, fmt.Errorf("image LLM analysis failed: %w", err)
	}

	logger.Info("Image analysis successful", "satisfies", analysisResult.Satisfies, "reason", analysisResult.Reason)
	return analysisResult, nil // Return the structured result and nil error
}

// DetectMaliciousContentResult is the structured response from the malicious content detection LLM call.
type DetectMaliciousContentResult struct {
	IsMalicious bool   `json:"is_malicious"`
	Reason      string `json:"reason"`
}

// DetectMaliciousContent uses an LLM to determine if the provided content contains
// prompt injection or other attempts to manipulate a downstream AI.
func (a *Activities) DetectMaliciousContent(ctx context.Context, content string) (DetectMaliciousContentResult, error) {
	logger := activity.GetLogger(ctx)
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return DetectMaliciousContentResult{IsMalicious: true, Reason: "Configuration error"}, fmt.Errorf("failed to get configuration: %w", err)
	}

	if len(content) > MaxContentCharsForLLMCheck {
		logger.Warn("Content exceeds maximum character limit, truncating for malicious content check", "original_length", len(content), "max_length", MaxContentCharsForLLMCheck)
		content = content[:MaxContentCharsForLLMCheck]
	}

	provider, err := NewLLMProvider(cfg.LLMConfig)
	if err != nil {
		logger.Error("Failed to create LLM provider for malicious content detection", "error", err)
		return DetectMaliciousContentResult{IsMalicious: true, Reason: "LLM provider error"}, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// Structured output schema for malicious content detection
	schema := map[string]interface{}{
		"name":   "malicious_content_detection",
		"strict": true,
		"schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"is_malicious": map[string]interface{}{
					"type":        "boolean",
					"description": "True if the content contains malicious instructions or a jailbreak attempt.",
				},
				"reason": map[string]interface{}{
					"type":        "string",
					"description": "A brief explanation for the decision.",
				},
			},
			"required":             []string{"is_malicious", "reason"},
			"additionalProperties": false,
		},
	}
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		logger.Error("Failed to marshal malicious content schema", "error", err)
		return DetectMaliciousContentResult{IsMalicious: true, Reason: "Schema marshal error"}, fmt.Errorf("failed to marshal malicious content schema: %w", err)
	}

	response, err := provider.GenerateResponse(ctx, cfg.MaliciousContentPrompt, content, schemaJSON)
	if err != nil {
		logger.Error("Failed to get LLM response for malicious content detection", "error", err)
		return DetectMaliciousContentResult{IsMalicious: true, Reason: "LLM communication error"}, fmt.Errorf("failed to get LLM response: %w", err)
	}

	var result DetectMaliciousContentResult
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		logger.Error("Failed to parse LLM tool call arguments for malicious content detection", "error", err, "raw_response", response)
		return DetectMaliciousContentResult{IsMalicious: true, Reason: "LLM response parsing error"}, fmt.Errorf("failed to parse LLM response arguments: %w", err)
	}

	logger.Info("Malicious content detection result", "is_malicious", result.IsMalicious, "reason", result.Reason)
	return result, nil
}
