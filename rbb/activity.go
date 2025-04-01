package rbb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"encoding/base64"

	"github.com/brojonat/reddit-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"go.temporal.io/sdk/activity"
)

// PlatformType represents the type of platform
type PlatformType string

const (
	// PlatformReddit represents the Reddit platform
	PlatformReddit PlatformType = "reddit"
	// PlatformYouTube represents the YouTube platform
	PlatformYouTube PlatformType = "youtube"
	// PlatformYelp represents the Yelp platform
	PlatformYelp PlatformType = "yelp"
	// PlatformGoogle represents the Google platform
	PlatformGoogle PlatformType = "google"
)

// Activities holds all activity implementations and their dependencies
type Activities struct {
	solanaConfig solana.SolanaConfig
	httpClient   *http.Client
	serverURL    string
	authToken    string
	redditDeps   RedditDependencies
	youtubeDeps  YouTubeDependencies
	yelpDeps     YelpDependencies
	googleDeps   GoogleDependencies
	llmDeps      LLMDependencies
}

// NewActivities creates a new Activities instance
func NewActivities(config solana.SolanaConfig, serverURL, authToken string,
	redditDeps RedditDependencies,
	youtubeDeps YouTubeDependencies,
	yelpDeps YelpDependencies,
	googleDeps GoogleDependencies,
	llmDeps LLMDependencies) *Activities {
	return &Activities{
		solanaConfig: config,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		serverURL:    serverURL,
		authToken:    authToken,
		redditDeps:   redditDeps,
		youtubeDeps:  youtubeDeps,
		yelpDeps:     yelpDeps,
		googleDeps:   googleDeps,
		llmDeps:      llmDeps,
	}
}

// PullRedditContent pulls content from Reddit
func (a *Activities) PullRedditContent(contentID string) (string, error) {
	// Create HTTP client for this activity
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Get auth token if needed
	if a.redditDeps.RedditAuthToken == "" || time.Now().After(a.redditDeps.RedditAuthTokenExp) {
		token, err := getRedditToken(client, a.redditDeps)
		if err != nil {
			return "", fmt.Errorf("failed to get Reddit token: %w", err)
		}
		a.redditDeps.RedditAuthToken = token
		a.redditDeps.RedditAuthTokenExp = time.Now().Add(1 * time.Hour)
	}

	// Create request to Reddit API
	url := fmt.Sprintf("https://oauth.reddit.com/api/info.json?id=%s", contentID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("User-Agent", a.redditDeps.UserAgent)
	req.Header.Set("Authorization", "Bearer "+a.redditDeps.RedditAuthToken)

	// Make request
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Reddit API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result struct {
		Data struct {
			Children []struct {
				Data struct {
					Title     string `json:"title"`
					Selftext  string `json:"selftext"`
					URL       string `json:"url"`
					Body      string `json:"body"`      // For comments
					Author    string `json:"author"`    // For both posts and comments
					Subreddit string `json:"subreddit"` // For both posts and comments
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w (body: %s)", err, string(body))
	}

	if len(result.Data.Children) == 0 {
		return "", fmt.Errorf("no content found for ID %s", contentID)
	}

	content := result.Data.Children[0].Data
	isComment := strings.HasPrefix(contentID, "t1_")

	if isComment {
		return fmt.Sprintf("Comment by u/%s in r/%s:\n%s", content.Author, content.Subreddit, content.Body), nil
	}

	// For posts
	if content.Selftext != "" {
		return fmt.Sprintf("Post by u/%s in r/%s:\nTitle: %s\n\n%s",
			content.Author, content.Subreddit, content.Title, content.Selftext), nil
	}

	return fmt.Sprintf("Post by u/%s in r/%s:\nTitle: %s\nURL: %s",
		content.Author, content.Subreddit, content.Title, content.URL), nil
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
	UserAgent          string    // Reddit requires a user agent string
	Username           string    // Reddit username for authentication
	Password           string    // Reddit password for authentication
	ClientID           string    // Reddit client ID for authentication
	ClientSecret       string    // Reddit client secret for authentication
	RedditAuthToken    string    // Cached Reddit auth token
	RedditAuthTokenExp time.Time // When the auth token expires
}

// ensureValidRedditToken ensures the Reddit auth token is valid for at least the specified duration
func (deps *RedditDependencies) ensureValidRedditToken(minRemaining time.Duration) error {
	if deps.RedditAuthToken == "" || time.Now().Add(minRemaining).After(deps.RedditAuthTokenExp) {
		client := &http.Client{
			Timeout: 10 * time.Second,
		}
		token, err := getRedditToken(client, *deps)
		if err != nil {
			return fmt.Errorf("failed to get Reddit token: %w", err)
		}
		deps.RedditAuthToken = token
		deps.RedditAuthTokenExp = time.Now().Add(1 * time.Hour)
	}
	return nil
}

// YouTubeDependencies holds the dependencies for YouTube-related activities
type YouTubeDependencies struct {
	Client          *http.Client
	APIKey          string
	ApplicationName string
	MaxResults      int64
}

// Type returns the platform type for YouTubeDependencies
func (deps YouTubeDependencies) Type() PlatformType {
	return PlatformYouTube
}

// PullYouTubeContent pulls content from YouTube
func (a *Activities) PullYouTubeContent(ctx context.Context, contentID string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Pulling YouTube content", "content_id", contentID)

	// Set HTTP client
	a.youtubeDeps.Client = a.httpClient

	// TODO: implement YouTube content fetching
	// This would involve using the YouTube API to fetch video descriptions, comments, etc.
	// Example implementation would authenticate with the YouTube API and fetch content

	return "", fmt.Errorf("YouTube content fetching not yet implemented")
}

// YelpDependencies holds the dependencies for Yelp-related activities
type YelpDependencies struct {
	Client   *http.Client
	APIKey   string
	ClientID string
}

// Type returns the platform type for YelpDependencies
func (deps YelpDependencies) Type() PlatformType {
	return PlatformYelp
}

// PullYelpContent pulls content from Yelp
func (a *Activities) PullYelpContent(ctx context.Context, contentID string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Pulling Yelp content", "content_id", contentID)

	// Set HTTP client
	a.yelpDeps.Client = a.httpClient

	// TODO: implement Yelp content fetching
	// This would involve using the Yelp Fusion API to fetch business reviews, etc.
	// Example implementation would authenticate with the Yelp API and fetch content

	return "", fmt.Errorf("Yelp content fetching not yet implemented")
}

// GoogleDependencies holds the dependencies for Google-related activities
type GoogleDependencies struct {
	Client         *http.Client
	APIKey         string
	SearchEngineID string
}

// Type returns the platform type for GoogleDependencies
func (deps GoogleDependencies) Type() PlatformType {
	return PlatformGoogle
}

// PullGoogleContent pulls content from Google
func (a *Activities) PullGoogleContent(ctx context.Context, contentID string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Pulling Google content", "content_id", contentID)

	// Set HTTP client
	a.googleDeps.Client = a.httpClient

	// TODO: implement Google content fetching
	// This would involve using the Google Custom Search API or other Google APIs
	// Example implementation would authenticate with the Google API and fetch content

	return "", fmt.Errorf("Google content fetching not yet implemented")
}

// getRedditToken obtains an authentication token from Reddit
func getRedditToken(client *http.Client, deps RedditDependencies) (string, error) {
	// Create request to Reddit's token endpoint
	req, err := http.NewRequest("POST", "https://www.reddit.com/api/v1/access_token", strings.NewReader("grant_type=password"))
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}

	// Set headers
	req.Header.Set("User-Agent", deps.UserAgent)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(deps.ClientID+":"+deps.ClientSecret)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Add username and password to request body
	req.PostForm = map[string][]string{
		"username": {deps.Username},
		"password": {deps.Password},
	}

	// Make request
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make token request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Reddit token API returned status %d", resp.StatusCode)
	}

	// Parse response
	var result struct {
		AccessToken string `json:"access_token"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err)
	}

	return result.AccessToken, nil
}
