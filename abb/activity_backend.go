package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/brojonat/affiliate-bounty-board/http/api"
	temporal_log "go.temporal.io/sdk/log"
)

// Fetches an auth token from the ABB /token endpoint
func (a *Activities) getABBAuthToken(ctx context.Context, logger temporal_log.Logger, cfg *Configuration, client *http.Client) (string, error) {
	form := url.Values{}
	form.Add("username", "temporal-bounty-poster") // Use a specific username
	form.Add("password", cfg.ABBServerConfig.SecretKey)

	tokenURL := fmt.Sprintf("%s/token", strings.TrimSuffix(cfg.ABBServerConfig.APIEndpoint, "/"))
	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create ABB token request: %w", err)
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ABB token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ABB token request returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp api.TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode ABB token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("ABB access_token is empty")
	}

	return tokenResp.AccessToken, nil
}

// Fetches the list of bounties from the ABB /bounties endpoint
func (a *Activities) fetchBounties(ctx context.Context, logger temporal_log.Logger, cfg *Configuration, client *http.Client, token string) ([]api.BountyListItem, error) {
	bountiesURL := fmt.Sprintf("%s/bounties", strings.TrimSuffix(cfg.ABBServerConfig.APIEndpoint, "/"))
	req, err := http.NewRequestWithContext(ctx, "GET", bountiesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create bounties request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bounties request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bounties request returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var bounties []api.BountyListItem
	if err := json.NewDecoder(resp.Body).Decode(&bounties); err != nil {
		// Log the body for debugging decode errors
		bodyBytes, _ := io.ReadAll(resp.Body) // Reread might be needed if decoder consumed it
		logger.Error("Failed to decode bounties response", "error", err, "response_body", string(bodyBytes))
		return nil, fmt.Errorf("failed to decode bounties response: %w", err)
	}

	return bounties, nil
}
