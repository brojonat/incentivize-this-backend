package abb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pgvector/pgvector-go"
	"go.temporal.io/sdk/activity"
)

// GenerateAndStoreBountyEmbeddingActivityInput defines the input for the activity.
// We pass the full workflow input to have all necessary fields for embedding text construction.
type GenerateAndStoreBountyEmbeddingActivityInput struct {
	BountyID      string
	WorkflowInput BountyAssessmentWorkflowInput
}

// GenerateAndStoreBountyEmbeddingActivity constructs text from bounty details,
// generates an embedding, and stores it via an HTTP call to the server.
func (a *Activities) GenerateAndStoreBountyEmbeddingActivity(ctx context.Context, input GenerateAndStoreBountyEmbeddingActivityInput) error {
	logger := activity.GetLogger(ctx)
	cfg, err := getConfiguration(ctx) // Fetch fresh configuration
	if err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	embeddingTextPayload := struct {
		ContentPlatform string   `json:"content_platform"`
		ContentKind     string   `json:"content_kind"`
		Requirements    []string `json:"requirements"`
		// Add more fields if needed, e.g., bounty owner, total amount, etc.
	}{
		ContentPlatform: string(input.WorkflowInput.Platform),
		ContentKind:     string(input.WorkflowInput.ContentKind),
		Requirements:    input.WorkflowInput.Requirements,
	}
	embeddingTextBytes, err := json.Marshal(embeddingTextPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal text for embedding: %w", err)
	}
	embeddingText := string(embeddingTextBytes)
	logger.Info("Constructed text for embedding", "bounty_id", input.BountyID, "text_preview", embeddingText[:100])

	// The actual LLMEmbeddingProvider would be instantiated based on LLMConfig in Configuration.
	// For this activity, we assume an LLM client/provider is accessible or created here.
	// This part requires a concrete LLM client or provider setup.
	// For now, this is a placeholder for the actual embedding generation call.

	// Placeholder for llmProvider initialization and usage:
	// llmProvider, err := NewLLMEmbeddingProviderFromConfig(cfg.LLMConfig, cfg.ABBServerConfig.LLMEmbeddingModel) // Hypothetical constructor
	// if err != nil {
	// 	 return fmt.Errorf("failed to init LLM embedding provider: %w", err)
	// }
	// embeddingSlice, err := llmProvider.GenerateEmbedding(ctx, embeddingText, cfg.ABBServerConfig.LLMEmbeddingModel)
	// if err != nil {
	// 	 return fmt.Errorf("failed to generate embedding: %w", err)
	// }

	// !!! SIMULATED EMBEDDING - REPLACE WITH ACTUAL LLM CALL !!!
	if cfg.ABBServerConfig.LLMEmbeddingModel == "" {
		logger.Warn("LLMEmbeddingModel not configured, skipping embedding generation for activity.")
		// Depending on strictness, you might return an error or allow workflow to proceed without embedding
		return nil // For now, allow to proceed if model not set
	}
	logger.Info("Simulating embedding generation as actual LLM call is not implemented here.", "model", cfg.ABBServerConfig.LLMEmbeddingModel)
	embeddingSlice := make([]float32, EmbeddingDimension) // Using EmbeddingDimension from abb/llm.go
	// Populate with some dummy data for testing if needed, e.g. embeddingSlice[0] = 0.123
	// In a real scenario, `embeddingSlice` would come from the LLM.
	// !!! END SIMULATED EMBEDDING !!!

	embeddingVector := pgvector.NewVector(embeddingSlice)

	// Get ABB Auth Token
	abbToken, err := a.getABBAuthToken(ctx, logger, cfg, a.httpClient)
	if err != nil {
		return fmt.Errorf("failed to get ABB auth token for embedding storage: %w", err)
	}

	// 4. Make HTTP POST request to /bounties/embeddings
	storeEmbeddingReqPayload := struct {
		BountyID  string          `json:"bounty_id"`
		Embedding pgvector.Vector `json:"embedding"`
	}{
		BountyID:  input.BountyID,
		Embedding: embeddingVector,
	}

	payloadBytes, err := json.Marshal(storeEmbeddingReqPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal store embedding request: %w", err)
	}

	storeURL := strings.TrimSuffix(cfg.ABBServerConfig.APIEndpoint, "/") + "/bounties/embeddings"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", storeURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create store embedding HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+abbToken)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("store embedding HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Consider reading resp.Body for more detailed error from server
		bodyBytes, _ := io.ReadAll(resp.Body)
		logger.Error("Failed to store bounty embedding", "bounty_id", input.BountyID, "status_code", resp.StatusCode, "response_body", string(bodyBytes))
		return fmt.Errorf("store embedding request returned status %d. Body: %s", resp.StatusCode, string(bodyBytes))
	}

	logger.Info("Successfully generated and stored bounty embedding", "bounty_id", input.BountyID)
	return nil
}
