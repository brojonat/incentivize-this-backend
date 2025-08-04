package abb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/brojonat/affiliate-bounty-board/http/api"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/log"
)

// Fetches an auth token from the ABB /token endpoint
func (a *Activities) getABBAuthToken(ctx context.Context, logger log.Logger, cfg *Configuration, client *http.Client) (string, error) {
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

func (a *Activities) PostSolanaTransaction(ctx context.Context, tx api.SolanaTransaction) error {
	logger := activity.GetLogger(ctx)
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}
	client := http.DefaultClient
	token, err := a.getABBAuthToken(ctx, logger, cfg, client)
	if err != nil {
		return fmt.Errorf("failed to get auth token: %w", err)
	}

	bountiesURL := fmt.Sprintf("%s/solana/transactions", strings.TrimSuffix(cfg.ABBServerConfig.APIEndpoint, "/"))

	body, err := json.Marshal(tx)
	if err != nil {
		return fmt.Errorf("failed to marshal transaction: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bountiesURL, bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("bounties request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bounties request returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

func (a *Activities) QueryForTransaction(ctx context.Context, bountyID string) ([]api.SolanaTransaction, error) {
	logger := activity.GetLogger(ctx)
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration: %w", err)
	}
	client := http.DefaultClient
	token, err := a.getABBAuthToken(ctx, logger, cfg, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	reqURL := fmt.Sprintf("%s/bounties/%s/transactions", strings.TrimSuffix(cfg.ABBServerConfig.APIEndpoint, "/"), bountyID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
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

	var transactions []api.SolanaTransaction
	if err := json.NewDecoder(resp.Body).Decode(&transactions); err != nil {
		// Log the body for debugging decode errors
		bodyBytes, _ := io.ReadAll(resp.Body) // Reread might be needed if decoder consumed it
		logger.Error("Failed to decode bounties response", "error", err, "response_body", string(bodyBytes))
		return nil, fmt.Errorf("failed to decode bounties response: %w", err)
	}

	return transactions, nil
}

func (a *Activities) GetLatestSolanaTransactionForRecipient(ctx context.Context, recipientWallet string) (*api.SolanaTransaction, error) {
	logger := activity.GetLogger(ctx)
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration: %w", err)
	}
	client := http.DefaultClient
	token, err := a.getABBAuthToken(ctx, logger, cfg, client)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	reqURL := fmt.Sprintf("%s/solana/transactions/latest/%s", strings.TrimSuffix(cfg.ABBServerConfig.APIEndpoint, "/"), recipientWallet)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request to get latest transaction failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get latest transaction request returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read latest transaction response body: %w", err)
	}

	if len(bodyBytes) == 0 || string(bodyBytes) == "null\n" {
		return nil, nil
	}

	var transaction api.SolanaTransaction
	if err := json.Unmarshal(bodyBytes, &transaction); err != nil {
		logger.Error("Failed to decode latest transaction response", "error", err, "response_body", string(bodyBytes))
		return nil, fmt.Errorf("failed to decode latest transaction response: %w", err)
	}

	return &transaction, nil
}
