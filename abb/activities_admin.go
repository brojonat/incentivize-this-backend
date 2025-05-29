package abb

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// PruneStaleEmbeddingsActivityInput defines the input for PruneStaleEmbeddingsActivity.
// It currently has no fields as the necessary info (API endpoint, token) will be read from env.
type PruneStaleEmbeddingsActivityInput struct{}

// PruneStaleEmbeddingsActivity makes an HTTP POST request to the /embeddings/prune endpoint.
func (a *Activities) PruneStaleEmbeddingsActivity(ctx context.Context, input PruneStaleEmbeddingsActivityInput) (string, error) {
	// Using slog.Default() as a generic logger for the activity context.
	// In a real setup, a logger might be injected or obtained from the activity context differently.
	logger := slog.Default().With("activity", "PruneStaleEmbeddingsActivity")

	apiEndpoint := os.Getenv(EnvABBAPIEndpoint)
	authToken := os.Getenv(EnvABBAuthToken)

	if apiEndpoint == "" {
		logger.Error("ABB_API_ENDPOINT environment variable not set")
		return "", fmt.Errorf("ABB_API_ENDPOINT not set")
	}
	if authToken == "" {
		logger.Error("ABB_AUTH_TOKEN environment variable not set for sudo operation")
		return "", fmt.Errorf("ABB_AUTH_TOKEN not set")
	}

	pruneURL := fmt.Sprintf("%s/embeddings/prune", apiEndpoint)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, pruneURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request to %s: %w", pruneURL, err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+authToken)
	httpReq.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: 2 * time.Minute}

	res, err := httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request to %s failed: %w", pruneURL, err)
	}
	defer res.Body.Close()
	bodyBytes, ioErr := io.ReadAll(res.Body)
	if ioErr != nil {
		return "", fmt.Errorf("failed to read response body from %s (status %d): %w", pruneURL, res.StatusCode, ioErr)
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("prune endpoint %s returned status %d: %s", pruneURL, res.StatusCode, string(bodyBytes))
	}
	return string(bodyBytes), nil
}
