package abb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/http/api"
	"go.temporal.io/sdk/activity"
	temporal_log "go.temporal.io/sdk/log"
)

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
		token, err := getRedditAuthTokenForPull(client, *deps) // Renamed function
		if err != nil {
			return fmt.Errorf("failed to get Reddit token: %w", err)
		}
		deps.RedditAuthToken = token
		deps.RedditAuthTokenExp = time.Now().Add(1 * time.Hour)
	}
	return nil
}

// MarshalJSON implements json.Marshaler for RedditDependencies
func (deps RedditDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		UserAgent          string    `json:"user_agent"`
		Username           string    `json:"username"`
		Password           string    `json:"password"`
		ClientID           string    `json:"client_id"`
		ClientSecret       string    `json:"client_secret"`
		RedditAuthToken    string    `json:"reddit_auth_token"`
		RedditAuthTokenExp time.Time `json:"reddit_auth_token_exp"`
	}

	aux := Aux{
		UserAgent:          deps.UserAgent,
		Username:           deps.Username,
		Password:           deps.Password,
		ClientID:           deps.ClientID,
		ClientSecret:       deps.ClientSecret,
		RedditAuthToken:    deps.RedditAuthToken,
		RedditAuthTokenExp: deps.RedditAuthTokenExp,
	}

	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for RedditDependencies
func (deps *RedditDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		UserAgent          string    `json:"user_agent"`
		Username           string    `json:"username"`
		Password           string    `json:"password"`
		ClientID           string    `json:"client_id"`
		ClientSecret       string    `json:"client_secret"`
		RedditAuthToken    string    `json:"reddit_auth_token"`
		RedditAuthTokenExp time.Time `json:"reddit_auth_token_exp"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	deps.UserAgent = aux.UserAgent
	deps.Username = aux.Username
	deps.Password = aux.Password
	deps.ClientID = aux.ClientID
	deps.ClientSecret = aux.ClientSecret
	deps.RedditAuthToken = aux.RedditAuthToken
	deps.RedditAuthTokenExp = aux.RedditAuthTokenExp

	return nil
}

// RedditContent represents the extracted content from Reddit
type RedditContent struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Selftext    string    `json:"selftext"`
	URL         string    `json:"url"`
	Body        string    `json:"body"`      // For comments
	Author      string    `json:"author"`    // For both posts and comments
	Subreddit   string    `json:"subreddit"` // For both posts and comments
	Score       int       `json:"score"`
	Created     time.Time `json:"created_utc"`
	IsComment   bool      `json:"is_comment"`
	Permalink   string    `json:"permalink"`
	NumComments int       `json:"num_comments"`
	IsStickied  bool      `json:"is_stickied"`
	IsLocked    bool      `json:"is_locked"`
	IsNSFW      bool      `json:"is_nsfw"`
	IsSpoiler   bool      `json:"is_spoiler"`
	Flair       string    `json:"flair"`
	Thumbnail   string    `json:"thumbnail"` // Added Thumbnail field
}

// UnmarshalJSON implements custom unmarshaling for RedditContent
func (r *RedditContent) UnmarshalJSON(data []byte) error {
	// First unmarshal into a map to handle the created_utc field flexibly
	var rawData map[string]interface{}
	if err := json.Unmarshal(data, &rawData); err != nil {
		return err
	}

	// Handle the created_utc field separately
	var createdTime time.Time
	switch v := rawData["created_utc"].(type) {
	case float64:
		createdTime = time.Unix(int64(v), 0)
	case string:
		// Try parsing as ISO 8601 first
		var err error
		createdTime, err = time.Parse(time.RFC3339, v)
		if err != nil {
			// If that fails, try parsing as a Unix timestamp string
			timestamp, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return fmt.Errorf("failed to parse created_utc timestamp: %w", err)
			}
			createdTime = time.Unix(int64(timestamp), 0)
		}
	case json.Number:
		timestamp, err := v.Float64()
		if err != nil {
			return fmt.Errorf("failed to parse created_utc timestamp: %w", err)
		}
		createdTime = time.Unix(int64(timestamp), 0)
	default:
		// Allow for created_utc to be missing or null, default to zero time
		createdTime = time.Time{}
	}

	type Aux struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Selftext    string `json:"selftext"`
		URL         string `json:"url"`
		Body        string `json:"body"`
		Author      string `json:"author"`
		Subreddit   string `json:"subreddit"`
		Score       int    `json:"score"`
		IsComment   bool   `json:"is_comment"`
		Permalink   string `json:"permalink"`
		NumComments int    `json:"num_comments"`
		IsStickied  bool   `json:"stickied"`
		IsLocked    bool   `json:"locked"`
		IsNSFW      bool   `json:"over_18"`
		IsSpoiler   bool   `json:"spoiler"`
		Flair       string `json:"link_flair_text"`
		Thumbnail   string `json:"thumbnail"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("failed to unmarshal reddit aux struct: %w (data: %s)", err, string(data))
	}

	r.ID = aux.ID
	r.Title = aux.Title
	r.Selftext = aux.Selftext
	r.URL = aux.URL
	r.Body = aux.Body
	r.Author = aux.Author
	r.Subreddit = aux.Subreddit
	r.Score = aux.Score
	r.Created = createdTime
	r.IsComment = aux.IsComment
	r.Permalink = aux.Permalink
	r.NumComments = aux.NumComments
	r.IsStickied = aux.IsStickied
	r.IsLocked = aux.IsLocked
	r.IsNSFW = aux.IsNSFW
	r.IsSpoiler = aux.IsSpoiler
	r.Flair = aux.Flair
	r.Thumbnail = aux.Thumbnail

	return nil
}

// getRedditAuthTokenForPull obtains an authentication token from Reddit (used by PullRedditContent)
func getRedditAuthTokenForPull(client *http.Client, deps RedditDependencies) (string, error) {
	form := url.Values{}
	form.Add("grant_type", "password")
	form.Add("username", deps.Username)
	form.Add("password", deps.Password)
	encodedForm := form.Encode()

	req, err := http.NewRequest("POST", "https://www.reddit.com/api/v1/access_token", strings.NewReader(encodedForm))
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}

	req.Header.Set("User-Agent", deps.UserAgent)
	auth := base64.StdEncoding.EncodeToString([]byte(deps.ClientID + ":" + deps.ClientSecret))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Content-Length", strconv.Itoa(len(encodedForm)))

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make token request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read token response body (status %d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Reddit token API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error,omitempty"`
	}

	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w (body: %s)", err, string(bodyBytes))
	}

	if result.AccessToken == "" {
		errMsg := "Reddit token response was successful but contained an empty access token."
		if result.Error != "" {
			errMsg = fmt.Sprintf("%s Reddit API error field: '%s'.", errMsg, result.Error)
		}
		return "", fmt.Errorf("%s Response body: %s", errMsg, string(bodyBytes))
	}
	return result.AccessToken, nil
}

// --- Bounty Publisher Activities (Reddit Specific) ---

// PublishBountiesReddit fetches bounties from the ABB server and posts them to Reddit.
func (a *Activities) PublishBountiesReddit(ctx context.Context) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Running PublishBountiesReddit activity...")

	cfg, err := getConfiguration(ctx)
	if err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	client := a.httpClient

	abbToken, err := a.getABBAuthToken(ctx, logger, cfg, client)
	if err != nil {
		logger.Error("Failed to get ABB auth token", "error", err)
		return err
	}
	logger.Debug("Obtained ABB auth token")

	bounties, err := a.fetchBounties(ctx, logger, cfg, client, abbToken)
	if err != nil {
		logger.Error("Failed to fetch bounties", "error", err)
		return err
	}
	if len(bounties) == 0 {
		logger.Info("No active bounties found to post.")
		return nil
	}
	logger.Info("Fetched bounties", "count", len(bounties))

	postTitle, postBody, err := a.formatBountiesForReddit(bounties, cfg.ABBServerConfig.PublicBaseURL)
	if err != nil {
		logger.Error("Failed to format bounties", "error", err)
		return fmt.Errorf("internal error formatting bounties: %w", err)
	}
	logger.Debug("Formatted bounties for Reddit post")

	redditToken, err := a.getRedditToken(ctx, logger, cfg, client) // This is the method (a *Activities) getRedditToken
	if err != nil {
		logger.Error("Failed to get Reddit auth token", "error", err)
		return err
	}
	logger.Debug("Obtained Reddit auth token")

	err = a.postToReddit(ctx, logger, cfg, client, redditToken, postTitle, postBody, cfg.RedditFlairID)
	if err != nil {
		logger.Error("Failed to post to Reddit", "error", err)
		return err
	}

	logger.Info("Successfully posted bounties to Reddit", "subreddit", cfg.PublishTargetSubreddit)
	return nil
}

// --- Helper methods for PublishBountiesReddit ---

// formatBountiesForReddit formats the list of bounties into a Reddit post title and body (Markdown)
func (a *Activities) formatBountiesForReddit(bounties []api.BountyListItem, publicBaseURL string) (string, string, error) {
	title := fmt.Sprintf("📢 Active Bounties (%s)", time.Now().Format("2006-01-02"))

	var body strings.Builder
	body.WriteString(fmt.Sprintf("Here are the currently active bounties available:\n\n"))
	body.WriteString("| Bounty ID | Platform | Reward/Post | Total Reward | Requirements |\n")
	body.WriteString("|---|---|---|---|---|\n")

	for _, b := range bounties {
		reqSummary := strings.Join(b.Requirements, " ")
		if len(reqSummary) > 2048 {
			reqSummary = reqSummary[:2045] + "..."
		}
		line := fmt.Sprintf("| [%s](%s/bounties/%s) | %s | $%.2f | $%.2f | %s |\n",
			b.WorkflowID,
			publicBaseURL,
			b.WorkflowID,
			b.PlatformKind,
			b.BountyPerPost,
			b.TotalBounty,
			reqSummary,
		)
		body.WriteString(line)
	}
	return title, body.String(), nil
}

// getRedditToken (method for publisher) gets an OAuth2 token from Reddit
func (a *Activities) getRedditToken(ctx context.Context, logger temporal_log.Logger, cfg *Configuration, client *http.Client) (string, error) {
	tokenURL := "https://www.reddit.com/api/v1/access_token"
	form := url.Values{}
	form.Add("grant_type", "password")
	form.Add("username", cfg.RedditDeps.Username)
	form.Add("password", cfg.RedditDeps.Password)

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create reddit token request: %w", err)
	}

	req.SetBasicAuth(cfg.RedditDeps.ClientID, cfg.RedditDeps.ClientSecret)
	req.Header.Set("User-Agent", cfg.RedditDeps.UserAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("reddit token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		logger.Error("Reddit token request failed", "status", resp.StatusCode, "body", string(bodyBytes))
		return "", fmt.Errorf("reddit token request returned status %d", resp.StatusCode)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Scope       string `json:"scope"`
		TokenType   string `json:"token_type"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode reddit token response: %w", err)
	}

	if result.AccessToken == "" {
		return "", fmt.Errorf("reddit token response did not contain an access token")
	}

	logger.Debug("Successfully obtained Reddit access token", "scope", result.Scope, "expires_in", result.ExpiresIn)
	return result.AccessToken, nil
}

// postToReddit posts the formatted content to the specified subreddit
func (a *Activities) postToReddit(ctx context.Context, logger temporal_log.Logger, cfg *Configuration, client *http.Client, token, title, body, flairID string) error {
	submitURL := "https://oauth.reddit.com/api/submit"

	form := url.Values{}
	form.Add("api_type", "json")
	form.Add("kind", "self")
	form.Add("sr", cfg.PublishTargetSubreddit)
	form.Add("title", title)
	form.Add("text", body)
	if flairID != "" { // Only add flair_id if it's provided
		form.Add("flair_id", flairID)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", submitURL, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create reddit submit request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", cfg.RedditDeps.UserAgent)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("reddit submit request failed: %w", err)
	}
	defer resp.Body.Close()

	respBodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		logger.Error("Reddit submit request failed", "status", resp.StatusCode, "body", string(respBodyBytes))
		return fmt.Errorf("reddit submit request returned status %d: %s", resp.StatusCode, string(respBodyBytes))
	}

	var result struct {
		JSON struct {
			Errors [][]interface{} `json:"errors"` // Changed to interface{} for flexibility
			Data   struct {
				URL string `json:"url"`
			} `json:"data"`
		} `json:"json"`
	}

	if err := json.Unmarshal(respBodyBytes, &result); err != nil {
		logger.Warn("Failed to decode reddit submit response JSON, but status was OK", "error", err, "response_body", string(respBodyBytes))
	} else if len(result.JSON.Errors) > 0 {
		logger.Error("Reddit API reported errors after submission", "errors", result.JSON.Errors, "response_body", string(respBodyBytes))
		// Format errors for better display
		var errorMessages []string
		for _, errGroup := range result.JSON.Errors {
			var currentError []string
			for _, e := range errGroup {
				currentError = append(currentError, fmt.Sprintf("%v", e))
			}
			errorMessages = append(errorMessages, strings.Join(currentError, ", "))
		}
		return fmt.Errorf("reddit API returned errors: [%s]", strings.Join(errorMessages, "; "))
	} else {
		logger.Info("Reddit post submitted successfully", "post_url", result.JSON.Data.URL)
	}

	return nil
}
