package abb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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

	// Add timeout to prevent workflow from hanging indefinitely on embedding generation
	timeoutCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

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
	textPreview := embeddingText
	if len(embeddingText) > 100 {
		textPreview = embeddingText[:100]
	}
	logger.Info("Constructed text for embedding", "bounty_id", input.BountyID, "text_preview", textPreview)

	// Use the EmbeddingConfig from the global Configuration
	if cfg.EmbeddingConfig.Provider == "" || cfg.EmbeddingConfig.Model == "" {
		return errors.New("LLM Embedding Provider or Model not configured.")
	}

	llmProvider, err := NewLLMEmbeddingProvider(cfg.EmbeddingConfig)
	if err != nil {
		return fmt.Errorf("failed to init LLM embedding provider: %w", err)
	}

	embeddingSlice, err := llmProvider.GenerateEmbedding(timeoutCtx, embeddingText, cfg.EmbeddingConfig.Model)
	if err != nil {
		// Check if it was a timeout
		if timeoutCtx.Err() == context.DeadlineExceeded {
			logger.Error("Embedding generation timed out after 60 seconds", "bounty_id", input.BountyID)
			return fmt.Errorf("embedding generation timed out after 60 seconds: %w", err)
		}
		return fmt.Errorf("failed to generate embedding: %w", err)
	}

	embeddingVector := pgvector.NewVector(embeddingSlice)

	// Get ABB Auth Token
	abbToken, err := a.getABBAuthToken(ctx, logger, cfg, a.httpClient)
	if err != nil {
		return fmt.Errorf("failed to get ABB auth token for embedding storage: %w", err)
	}

	// Make HTTP POST request to /bounties/embeddings
	storeEmbeddingReqPayload := struct {
		BountyID    string          `json:"bounty_id"`
		Embedding   pgvector.Vector `json:"embedding"`
		Environment string          `json:"environment"`
	}{
		BountyID:    input.BountyID,
		Embedding:   embeddingVector,
		Environment: cfg.Environment,
	}

	payloadBytes, err := json.Marshal(storeEmbeddingReqPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal store embedding request: %w", err)
	}

	storeURL := strings.TrimSuffix(cfg.ABBServerConfig.APIEndpoint, "/") + "/bounties/embeddings"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, storeURL, bytes.NewReader(payloadBytes))
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
