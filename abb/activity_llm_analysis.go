package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"go.temporal.io/sdk/activity"
)

// CheckContentRequirements checks if the content satisfies the requirements
func (a *Activities) CheckContentRequirements(ctx context.Context, content []byte, requirements []string) (CheckContentRequirementsResult, error) {
	logger := activity.GetLogger(ctx)

	// Get fresh config
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to get configuration in CheckContentRequirements: %w", err)
	}

	logger.Debug("Starting content requirements check",
		"content_length", len(content),
		"requirements_count", len(requirements))

	// Convert content bytes to string for the prompt
	contentStr := string(content)

	// --- Input Size Checks ---
	if len(contentStr) > MaxContentCharsForLLMCheck {
		reason := fmt.Sprintf("Content exceeds maximum character limit (%d > %d)", len(contentStr), MaxContentCharsForLLMCheck)
		logger.Warn(reason)
		return CheckContentRequirementsResult{Satisfies: false, Reason: reason}, nil // Not an error, just failed check
	}

	requirementsStr := strings.Join(requirements, "\n")
	if len(requirementsStr) > MaxRequirementsCharsForLLMCheck {
		reason := fmt.Sprintf("Requirements exceed maximum character limit (%d > %d)", len(requirementsStr), MaxRequirementsCharsForLLMCheck)
		logger.Warn(reason)
		return CheckContentRequirementsResult{Satisfies: false, Reason: reason}, nil // Not an error, just failed check
	}
	// --- End Input Size Checks ---

	// Construct the final prompt. This part of the prompt includes the specification
	// for the LLM to follow when checking the content against the requirements. That
	// is to say, you may influence the LLM's behavior by changing the base prompt
	// on the provider, but this is where the LLM's behavior is specified to be
	// in accordance with the code (i.e., requiring a particular format for the response).
	prompt := fmt.Sprintf(`%s

Requirements:
%s

Content (JSON):
%s

You must respond ONLY with a valid JSON object containing two keys: "satisfies" (a boolean indicating if the content meets the requirements) and "reason" (a string explaining your decision). Example: {"satisfies": true, "reason": "Content meets all criteria."}`, cfg.Prompt, strings.Join(requirements, "\n"), contentStr)

	// Log estimated token count
	estimatedTokens := len(prompt) / 4
	logger.Info("Sending prompt to LLM", "estimated_tokens", estimatedTokens, "prompt_length_chars", len(prompt))

	// Create LLM provider instance from config fetched within the activity
	llmProvider, err := NewLLMProvider(cfg.LLMConfig) // Use cfg.LLMConfig
	if err != nil {
		logger.Error("Failed to create LLM provider from config", "error", err)
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// Call the LLM service
	resp, err := llmProvider.Complete(ctx, prompt)
	if err != nil {
		logger.Error("Failed to get LLM response", "error", err)
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to check content requirements: %w", err)
	}

	// Log the raw response for debugging
	logger.Debug("Raw LLM response",
		"response", resp,
		"response_length", len(resp),
		"response_bytes", []byte(resp))

	// Try to clean the response if needed
	resp = strings.TrimSpace(resp)
	if strings.HasPrefix(resp, "```json") {
		resp = strings.TrimPrefix(resp, "```json")
	}
	if strings.HasSuffix(resp, "```") {
		resp = strings.TrimSuffix(resp, "```")
	}
	resp = strings.TrimSpace(resp)

	logger.Debug("Cleaned LLM response",
		"response", resp,
		"response_length", len(resp))

	// Parse the LLM response
	var result CheckContentRequirementsResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		logger.Error("Failed to parse LLM response",
			"error", err,
			"raw_response", resp,
			"response_bytes", []byte(resp))
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	return result, nil
}

// ValidateWalletResult represents the result of validating a payout wallet against requirements.
type ValidateWalletResult struct {
	Satisfies bool   `json:"satisfies"`
	Reason    string `json:"reason"`
}

// ValidatePayoutWallet checks if the provided payout wallet is permissible based on bounty requirements.
func (a *Activities) ValidatePayoutWallet(ctx context.Context, payoutWallet string, requirements []string) (ValidateWalletResult, error) {
	logger := activity.GetLogger(ctx)

	cfg, err := getConfiguration(ctx)
	if err != nil {
		return ValidateWalletResult{Satisfies: false, Reason: "Configuration error"}, fmt.Errorf("failed to get configuration in ValidatePayoutWallet: %w", err)
	}

	logger.Debug("Starting payout wallet validation", "payoutWallet", payoutWallet, "requirements_count", len(requirements))

	requirementsStr := strings.Join(requirements, "\n")
	// Basic input size check for requirements string to avoid overly long prompts
	if len(requirementsStr) > MaxRequirementsCharsForLLMCheck { // Reusing existing constant
		reason := fmt.Sprintf("Requirements string for wallet validation exceeds maximum character limit (%d > %d)", len(requirementsStr), MaxRequirementsCharsForLLMCheck)
		logger.Warn(reason)
		// Return as not satisfied if requirements are too long to process reliably
		return ValidateWalletResult{Satisfies: false, Reason: reason}, nil
	}

	// Construct the specific prompt for wallet validation.
	// The base prompt (cfg.Prompt) might be generic for content checking, so we create a more targeted one here.
	// We use a default base if cfg.Prompt is empty or too generic.
	promptBase := cfg.Prompt
	if promptBase == "" || strings.Contains(promptBase, "content verification system") { // Check if it's the default content prompt
		promptBase = "You are a Payout Wallet Policy Enforcer. Your task is to determine if the provided Payout Wallet is explicitly allowed or not disallowed based on the given Bounty Requirements."
	}

	prompt := fmt.Sprintf(`%s

Bounty Requirements:
---
%s
---

Candidate Payout Wallet: %s

Based *only* on the requirements pertaining to payout wallet restrictions (ignore all other types of requirements like content quality, topics, etc.):
- If the requirements specify allowed wallet(s) and the Candidate Payout Wallet is one of them, it satisfies.
- If the requirements specify disallowed wallet(s) and the Candidate Payout Wallet is one of them, it does NOT satisfy.
- If the requirements mention a general rule for payout wallets (e.g., "must be a Solana address on devnet") and the candidate wallet adheres to it, it satisfies.
- If no specific wallet restrictions are mentioned, or if the restrictions are too vague to make a definitive judgment about *this specific wallet address*, assume it satisfies.

You MUST respond ONLY with a valid JSON object containing two keys: "satisfies" (a boolean) and "reason" (a string explaining your decision based *only* on wallet restrictions).
Example (Wallet Allowed): {"satisfies": true, "reason": "Payout wallet matches the allowed wallet XYZ specified in requirements."}
Example (Wallet Disallowed): {"satisfies": false, "reason": "Payout wallet ABC is explicitly disallowed by requirement 'Payouts only to XYZ'."}
Example (No Restriction): {"satisfies": true, "reason": "No specific payout wallet restrictions found in the requirements."}
`, promptBase, requirementsStr, payoutWallet)

	// Log estimated token count
	estimatedTokens := len(prompt) / 4 // Simple approximation
	logger.Info("Sending wallet validation prompt to LLM", "estimated_tokens", estimatedTokens, "prompt_length_chars", len(prompt))

	llmProvider, err := NewLLMProvider(cfg.LLMConfig)
	if err != nil {
		logger.Error("Failed to create LLM provider for wallet validation", "error", err)
		return ValidateWalletResult{Satisfies: false, Reason: "LLM provider error"}, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	resp, err := llmProvider.Complete(ctx, prompt)
	if err != nil {
		logger.Error("Failed to get LLM response for wallet validation", "error", err)
		return ValidateWalletResult{Satisfies: false, Reason: "LLM communication error"}, fmt.Errorf("failed to validate wallet: %w", err)
	}

	resp = strings.TrimSpace(resp)
	if strings.HasPrefix(resp, "```json") {
		resp = strings.TrimPrefix(resp, "```json")
	}
	if strings.HasSuffix(resp, "```") {
		resp = strings.TrimSuffix(resp, "```")
	}
	resp = strings.TrimSpace(resp)

	logger.Debug("Cleaned LLM response for wallet validation", "response", resp)

	var result ValidateWalletResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		logger.Error("Failed to parse LLM response for wallet validation", "error", err, "raw_response", resp)
		return ValidateWalletResult{Satisfies: false, Reason: "LLM response parsing error"}, fmt.Errorf("failed to parse LLM response: %w", err)
	}

	return result, nil
}

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
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Failed to download image: %v", err)}, fmt.Errorf("failed to create image request for %s: %w", imageUrl, err)
	}
	// Add a generic User-Agent
	req.Header.Set("User-Agent", "AffiliateBountyBoard-Worker/1.0")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		logger.Error("Failed to download image", "url", imageUrl, "error", err)
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Failed to download image: %v", err)}, fmt.Errorf("failed to download image from %s: %w", imageUrl, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Error("Failed to download image, bad status", "url", imageUrl, "status_code", resp.StatusCode)
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Failed to download image, status: %d", resp.StatusCode)}, fmt.Errorf("failed to download image from %s, status: %d", imageUrl, resp.StatusCode)
	}

	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read image data", "url", imageUrl, "error", err)
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Failed to read image data: %v", err)}, fmt.Errorf("failed to read image data from %s: %w", imageUrl, err)
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
