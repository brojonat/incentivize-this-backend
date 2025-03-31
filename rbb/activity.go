package rbb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/brojonat/reddit-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/jmespath/go-jmespath"
	"go.temporal.io/sdk/activity"
)

// Activities holds all activity implementations and their dependencies
type Activities struct {
	solanaConfig solana.SolanaConfig
	httpClient   *http.Client
	serverURL    string
	authToken    string
	redditDeps   RedditDependencies
	llmDeps      LLMDependencies
}

// NewActivities creates a new Activities instance
func NewActivities(config solana.SolanaConfig, serverURL, authToken string, redditDeps RedditDependencies, llmDeps LLMDependencies) *Activities {
	return &Activities{
		solanaConfig: config,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		serverURL:    serverURL,
		authToken:    authToken,
		redditDeps:   redditDeps,
		llmDeps:      llmDeps,
	}
}

// PullRedditContent pulls content from Reddit
func (a *Activities) PullRedditContent(ctx context.Context, contentID string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Pulling Reddit content", "content_id", contentID)

	// Set HTTP client
	a.redditDeps.Client = a.httpClient

	isComment := strings.HasPrefix(contentID, "t1_")

	// Build URL based on content type
	url := fmt.Sprintf("https://oauth.reddit.com/api/info?id=%s", contentID)

	// Create request to Reddit's API
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set required headers
	req.Header.Set("User-Agent", a.redditDeps.UserAgent)

	// Add authentication if credentials are provided
	if a.redditDeps.Username != "" && a.redditDeps.Password != "" && a.redditDeps.ClientID != "" && a.redditDeps.ClientSecret != "" {
		// Ensure we have a valid token with at least 5 minutes remaining
		if err := a.redditDeps.ensureValidRedditToken(5 * time.Minute); err != nil {
			return "", fmt.Errorf("failed to ensure valid Reddit token: %w", err)
		}
		req.Header.Set("Authorization", "bearer "+a.redditDeps.RedditAuthToken)
	}

	// Send request
	resp, err := a.redditDeps.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch content: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("reddit API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse JSON
	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("failed to parse JSON response: %w", err)
	}

	// Extract content using JMESPath
	var content string
	if isComment {
		// For comments, extract the body field
		expr := "data.children[0].data.body"
		result, err := jmespath.Search(expr, data)
		if err != nil {
			return "", fmt.Errorf("failed to extract comment body: %w", err)
		}
		content, ok := result.(string)
		if !ok {
			return "", fmt.Errorf("comment body is not a string")
		}
		if content == "" {
			return "", fmt.Errorf("empty comment body")
		}
		return content, nil
	}

	// For posts, extract the selftext field
	expr := "data.children[0].data.selftext"
	result, err := jmespath.Search(expr, data)
	if err != nil {
		return "", fmt.Errorf("failed to extract post text: %w", err)
	}
	content, ok := result.(string)
	if !ok {
		return "", fmt.Errorf("post text is not a string")
	}
	if content == "" {
		return "", fmt.Errorf("empty post text")
	}
	return content, nil
}

// CheckContentRequirements checks if the content satisfies the requirements
func (a *Activities) CheckContentRequirements(ctx context.Context, content, requirements string) (CheckContentRequirementsResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Starting content requirements check",
		"content_length", len(content),
		"requirements_count", strings.Count(requirements, "\n")+1)

	prompt := fmt.Sprintf(`You are a content verification system. Your task is to determine if the given content satisfies the specified requirements.

Requirements:
%s

Content to evaluate:
%s

You must respond with a valid JSON object in exactly this format:
{
  "satisfies": true/false,
  "reason": "your explanation here"
}

Do not include any text before or after the JSON object. The response must be valid JSON that can be parsed by a JSON parser.
Evaluate strictly and conservatively. Only return true if ALL requirements are clearly met.`, requirements, content)

	// Call the LLM service using the provider
	resp, err := a.llmDeps.Provider.Complete(ctx, prompt)
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

	// Log the reason for observability
	logger.Info("Content evaluation result",
		"satisfies", result.Satisfies,
		"reason", result.Reason)

	return result, nil
}

// TransferUSDC transfers USDC from one account to another
func (a *Activities) TransferUSDC(ctx context.Context, from, to solanago.PublicKey, amount *solana.USDCAmount) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Transferring USDC", "from", from.String(), "to", to.String(), "amount", amount.ToUSDC())

	// Create Solana client
	client, err := solana.NewSolanaClient(a.solanaConfig)
	if err != nil {
		return fmt.Errorf("failed to create Solana client: %w", err)
	}

	// Execute transfer
	err = client.TransferUSDC(ctx, from, to, amount)
	if err != nil {
		return fmt.Errorf("failed to transfer USDC: %w", err)
	}

	return nil
}

// ReleaseEscrow releases USDC from escrow to the specified account
func (a *Activities) ReleaseEscrow(ctx context.Context, to solanago.PublicKey, amount *solana.USDCAmount) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Releasing escrow", "to", to.String(), "amount", amount.ToUSDC())

	// Create Solana client
	client, err := solana.NewSolanaClient(a.solanaConfig)
	if err != nil {
		return fmt.Errorf("failed to create Solana client: %w", err)
	}

	// Execute release
	err = client.ReleaseEscrow(ctx, to, amount)
	if err != nil {
		return fmt.Errorf("failed to release escrow: %w", err)
	}

	return nil
}

// LLMDependencies holds the dependencies for LLM-related activities
type LLMDependencies struct {
	Provider LLMProvider
}

// HTTPDependencies holds the dependencies for HTTP-related activities
type HTTPDependencies struct {
	Client    *http.Client
	ServerURL string
	AuthToken string
}

// PayBountyRequest represents the request body for paying a bounty
type PayBountyRequest struct {
	UserID string  `json:"user_id"`
	Amount float64 `json:"amount"`
}

// PayBounty pays a bounty to a user by making an HTTP request to the server
func (a *Activities) PayBounty(ctx context.Context, userID string, amount float64) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Paying bounty", "user_id", userID, "amount", amount)

	// Create request body
	reqBody := PayBountyRequest{
		UserID: userID,
		Amount: amount,
	}

	// Marshal request body
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", a.serverURL+"/bounties/pay", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.authToken)

	// Send request
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			return fmt.Errorf("request failed with status %d", resp.StatusCode)
		}
		return fmt.Errorf("request failed: %s", errResp.Error)
	}

	logger.Info("Successfully paid bounty", "user_id", userID, "amount", amount)
	return nil
}

// ReturnBountyToOwnerRequest represents the request body for returning a bounty to the owner
type ReturnBountyToOwnerRequest struct {
	OwnerID string  `json:"owner_id"`
	Amount  float64 `json:"amount"`
}

// ReturnBountyToOwner returns the remaining bounty amount to the owner by making an HTTP request to the server
func (a *Activities) ReturnBountyToOwner(ctx context.Context, ownerID string, amount float64) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Returning bounty to owner", "owner_id", ownerID, "amount", amount)

	// Create request body
	reqBody := ReturnBountyToOwnerRequest{
		OwnerID: ownerID,
		Amount:  amount,
	}

	// Marshal request body
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", a.serverURL+"/bounties/return", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.authToken)

	// Send request
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
			return fmt.Errorf("request failed with status %d", resp.StatusCode)
		}
		return fmt.Errorf("request failed: %s", errResp.Error)
	}

	logger.Info("Successfully returned bounty to owner", "owner_id", ownerID, "amount", amount)
	return nil
}

// RedditDependencies holds the dependencies for Reddit-related activities
type RedditDependencies struct {
	Client             *http.Client
	UserAgent          string    // Reddit requires a user agent string
	Username           string    // Reddit username for authentication
	Password           string    // Reddit password for authentication
	ClientID           string    // Reddit client ID for authentication
	ClientSecret       string    // Reddit client secret for authentication
	RedditAuthToken    string    // Cached Reddit auth token
	RedditAuthTokenExp time.Time // When the auth token expires
}

// ensureValidRedditToken ensures we have a valid Reddit token that won't expire within minDur
func (deps *RedditDependencies) ensureValidRedditToken(minDur time.Duration) error {
	// short circuit early if the token doesn't need to be refreshed
	if time.Until(deps.RedditAuthTokenExp) > minDur {
		return nil
	}

	// otherwise hit the reddit API for a new token
	formData := url.Values{
		"grant_type": {"password"},
		"username":   {deps.Username},
		"password":   {deps.Password},
	}
	r, err := http.NewRequest(http.MethodPost, "https://www.reddit.com/api/v1/access_token", strings.NewReader(formData.Encode()))
	if err != nil {
		return err
	}
	r.SetBasicAuth(deps.ClientID, deps.ClientSecret)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("User-Agent", deps.UserAgent)
	resp, err := deps.Client.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
		TokenType   string `json:"token_type"`
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("bad response %d code for getting reddit auth token: %s", resp.StatusCode, string(b))
	}
	err = json.Unmarshal(b, &body)
	if err != nil {
		return err
	}

	deps.RedditAuthToken = body.AccessToken
	dur := time.Duration(body.ExpiresIn * int(time.Second))
	deps.RedditAuthTokenExp = time.Now().Add(dur)
	return nil
}
