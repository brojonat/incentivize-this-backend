package abb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"encoding/base64"

	"github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/jmespath/go-jmespath"
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
	// PlatformAmazon represents the Amazon platform
	PlatformAmazon PlatformType = "amazon"
)

// Activities holds all activity implementations and their dependencies
type Activities struct {
	solanaConfig solana.SolanaConfig
	solanaClient solana.SolanaClient
	httpClient   *http.Client
	serverURL    string
	authToken    string
	redditDeps   RedditDependencies
	youtubeDeps  YouTubeDependencies
	yelpDeps     YelpDependencies
	googleDeps   GoogleDependencies
	amazonDeps   AmazonDependencies
	llmDeps      LLMDependencies
}

// NewActivities creates a new Activities instance
func NewActivities(config solana.SolanaConfig, serverURL, authToken string,
	redditDeps RedditDependencies,
	youtubeDeps YouTubeDependencies,
	yelpDeps YelpDependencies,
	googleDeps GoogleDependencies,
	amazonDeps AmazonDependencies,
	llmDeps LLMDependencies) (*Activities, error) {

	// Create Solana client
	solanaClient, err := solana.NewSolanaClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Solana client: %w", err)
	}

	return &Activities{
		solanaConfig: config,
		solanaClient: solanaClient,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		serverURL:    serverURL,
		authToken:    authToken,
		redditDeps:   redditDeps,
		youtubeDeps:  youtubeDeps,
		yelpDeps:     yelpDeps,
		googleDeps:   googleDeps,
		amazonDeps:   amazonDeps,
		llmDeps:      llmDeps,
	}, nil
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
		return fmt.Errorf("unexpected type for created_utc: %T", v)
	}

	// Create an auxiliary struct for the rest of the fields
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
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Copy all fields from aux to r
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

	return nil
}

// PullRedditContent pulls content from Reddit
func (a *Activities) PullRedditContent(contentID string) (*RedditContent, error) {
	// Create HTTP client for this activity
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Get auth token if needed
	if a.redditDeps.RedditAuthToken == "" || time.Now().After(a.redditDeps.RedditAuthTokenExp) {
		token, err := getRedditToken(client, a.redditDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to get Reddit token: %w", err)
		}
		a.redditDeps.RedditAuthToken = token
		a.redditDeps.RedditAuthTokenExp = time.Now().Add(1 * time.Hour)
	}

	// Create request to Reddit API
	url := fmt.Sprintf("https://oauth.reddit.com/api/info.json?id=%s", contentID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("User-Agent", a.redditDeps.UserAgent)
	req.Header.Set("Authorization", "Bearer "+a.redditDeps.RedditAuthToken)

	// Make request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Log the raw response for debugging
	activity.GetLogger(context.Background()).Debug("Reddit API Response", "response", string(body))

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Reddit API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response as JSON
	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w (body: %s)", err, string(body))
	}

	// Use JMESPath to extract the first child's data
	expression := "data.children[0].data"
	result, err := jmespath.Search(expression, data)
	if err != nil {
		return nil, fmt.Errorf("failed to extract data with JMESPath: %w", err)
	}

	if result == nil {
		return nil, fmt.Errorf("no content found for ID %s", contentID)
	}

	// Convert the result to JSON and then to our struct
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JMESPath result: %w", err)
	}

	// Log the extracted data for debugging
	activity.GetLogger(context.Background()).Debug("Extracted Reddit Data", "data", string(resultJSON))

	var content RedditContent
	if err := json.Unmarshal(resultJSON, &content); err != nil {
		return nil, fmt.Errorf("failed to unmarshal content: %w", err)
	}

	// Set additional fields
	content.ID = contentID
	content.IsComment = strings.HasPrefix(contentID, "t1_")

	return &content, nil
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

	// Execute transfer
	err := a.solanaClient.TransferUSDC(ctx, from, to, amount)
	if err != nil {
		return fmt.Errorf("failed to transfer USDC: %w", err)
	}

	return nil
}

// ReleaseEscrow releases USDC from escrow to the specified account
func (a *Activities) ReleaseEscrow(ctx context.Context, to solanago.PublicKey, amount *solana.USDCAmount) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Releasing escrow", "to", to.String(), "amount", amount.ToUSDC())

	// Execute release
	err := a.solanaClient.ReleaseEscrow(ctx, to, amount)
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

// YouTubeDependencies holds the dependencies for YouTube-related activities
type YouTubeDependencies struct {
	Client          *http.Client
	APIKey          string
	OAuthToken      string // Added for caption access
	ApplicationName string
	MaxResults      int64
}

// Type returns the platform type for YouTubeDependencies
func (deps YouTubeDependencies) Type() PlatformType {
	return PlatformYouTube
}

// YouTubeCaption represents a caption track for a YouTube video
type YouTubeCaption struct {
	ID              string    `json:"id"`
	Language        string    `json:"language"`
	Name            string    `json:"name"`
	TrackKind       string    `json:"trackKind"`
	LastUpdated     time.Time `json:"lastUpdated"`
	Content         string    `json:"content"`
	IsAutoGenerated bool      `json:"isAutoGenerated"`
}

// YouTubeContent represents the extracted content from YouTube
type YouTubeContent struct {
	ID                   string           `json:"id"`
	Title                string           `json:"title"`
	Description          string           `json:"description"`
	ChannelID            string           `json:"channel_id"`
	ChannelTitle         string           `json:"channel_title"`
	PublishedAt          time.Time        `json:"published_at"`
	ViewCount            string           `json:"view_count"`
	LikeCount            string           `json:"like_count"`
	CommentCount         string           `json:"comment_count"`
	Duration             string           `json:"duration"`
	ThumbnailURL         string           `json:"thumbnail_url"`
	Tags                 []string         `json:"tags"`
	CategoryID           string           `json:"category_id"`
	LiveBroadcastContent string           `json:"live_broadcast_content"`
	Captions             []YouTubeCaption `json:"captions"`
}

// fetchYouTubeCaptions fetches available caption tracks for a video
func (a *Activities) fetchYouTubeCaptions(ctx context.Context, videoID string) ([]YouTubeCaption, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Fetching YouTube captions", "video_id", videoID)

	// First, list available caption tracks
	url := fmt.Sprintf("https://www.googleapis.com/youtube/v3/captions?part=snippet&videoId=%s", videoID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create captions list request: %w", err)
	}

	// Set authorization header for captions access
	if a.youtubeDeps.OAuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.youtubeDeps.OAuthToken)
	}

	// Make request
	resp, err := a.youtubeDeps.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list captions: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read captions list response: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YouTube captions API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Language       string    `json:"language"`
				Name           string    `json:"name"`
				TrackKind      string    `json:"trackKind"`
				LastUpdated    time.Time `json:"lastUpdated"`
				AudioTrackType string    `json:"audioTrackType"`
				IsCC           bool      `json:"isCC"`
				IsAutoSynced   bool      `json:"isAutoSynced"`
				IsASR          bool      `json:"isASR"`
			} `json:"snippet"`
		} `json:"items"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode captions list: %w", err)
	}

	// Download each caption track
	var captions []YouTubeCaption
	for _, item := range result.Items {
		caption := YouTubeCaption{
			ID:              item.ID,
			Language:        item.Snippet.Language,
			Name:            item.Snippet.Name,
			TrackKind:       item.Snippet.TrackKind,
			LastUpdated:     item.Snippet.LastUpdated,
			IsAutoGenerated: item.Snippet.IsASR,
		}

		// Download caption content
		content, err := a.downloadYouTubeCaption(ctx, item.ID)
		if err != nil {
			logger.Warn("Failed to download caption track",
				"caption_id", item.ID,
				"language", item.Snippet.Language,
				"error", err)
			continue
		}
		caption.Content = content
		captions = append(captions, caption)
	}

	return captions, nil
}

// downloadYouTubeCaption downloads a specific caption track
func (a *Activities) downloadYouTubeCaption(ctx context.Context, captionID string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Downloading YouTube caption", "caption_id", captionID)

	// Create request to download caption
	url := fmt.Sprintf("https://www.googleapis.com/youtube/v3/captions/%s?tfmt=srt", captionID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create caption download request: %w", err)
	}

	// Set authorization header
	if a.youtubeDeps.OAuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.youtubeDeps.OAuthToken)
	}

	// Make request
	resp, err := a.youtubeDeps.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download caption: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read caption content: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("YouTube caption download API returned status %d: %s", resp.StatusCode, string(body))
	}

	return string(body), nil
}

// PullYouTubeContent pulls content from YouTube
func (a *Activities) PullYouTubeContent(ctx context.Context, contentID string) (*YouTubeContent, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Pulling YouTube content", "content_id", contentID)

	// Set HTTP client
	a.youtubeDeps.Client = a.httpClient

	// Extract video ID from contentID (format: yt_video_id)
	videoID := contentID
	if strings.HasPrefix(contentID, "yt_") {
		videoID = strings.TrimPrefix(contentID, "yt_")
	}

	// Fetch video metadata using YouTube Data API
	videoData, err := a.fetchYouTubeVideoMetadata(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch YouTube video metadata: %w", err)
	}

	// Create YouTubeContent struct
	content := &YouTubeContent{
		ID:                   videoID,
		Title:                videoData.Snippet.Title,
		Description:          videoData.Snippet.Description,
		ChannelID:            videoData.Snippet.ChannelID,
		ChannelTitle:         videoData.Snippet.ChannelTitle,
		PublishedAt:          videoData.Snippet.PublishedAt,
		ViewCount:            videoData.Statistics.ViewCount,
		LikeCount:            videoData.Statistics.LikeCount,
		CommentCount:         videoData.Statistics.CommentCount,
		Duration:             videoData.ContentDetails.Duration,
		ThumbnailURL:         videoData.Snippet.Thumbnails.Default.URL,
		Tags:                 videoData.Snippet.Tags,
		CategoryID:           videoData.Snippet.CategoryID,
		LiveBroadcastContent: videoData.Snippet.LiveBroadcastContent,
	}

	// Fetch video captions if OAuth token is available
	if a.youtubeDeps.OAuthToken != "" {
		captions, err := a.fetchYouTubeCaptions(ctx, videoID)
		if err != nil {
			logger.Warn("Failed to fetch YouTube captions", "error", err)
			// Continue without captions
		} else {
			content.Captions = captions
		}
	} else {
		logger.Info("Skipping caption fetch - no OAuth token provided")
	}

	return content, nil
}

// YouTubeVideoData represents the response from the YouTube Data API
type YouTubeVideoData struct {
	ID      string `json:"id"`
	Snippet struct {
		PublishedAt time.Time `json:"publishedAt"`
		ChannelID   string    `json:"channelId"`
		Title       string    `json:"title"`
		Description string    `json:"description"`
		Thumbnails  struct {
			Default struct {
				URL string `json:"url"`
			} `json:"default"`
		} `json:"thumbnails"`
		ChannelTitle         string   `json:"channelTitle"`
		Tags                 []string `json:"tags"`
		CategoryID           string   `json:"categoryId"`
		LiveBroadcastContent string   `json:"liveBroadcastContent"`
	} `json:"snippet"`
	Statistics struct {
		ViewCount    string `json:"viewCount"`
		LikeCount    string `json:"likeCount"`
		CommentCount string `json:"commentCount"`
	} `json:"statistics"`
	ContentDetails struct {
		Duration string `json:"duration"`
	} `json:"contentDetails"`
}

// UnmarshalJSON implements custom unmarshaling for YouTubeVideoData
func (v *YouTubeVideoData) UnmarshalJSON(data []byte) error {
	// First unmarshal into a map to handle the statistics fields flexibly
	var rawData map[string]interface{}
	if err := json.Unmarshal(data, &rawData); err != nil {
		return err
	}

	// Create an auxiliary struct for the rest of the fields
	type Aux struct {
		ID      string `json:"id"`
		Snippet struct {
			PublishedAt time.Time `json:"publishedAt"`
			ChannelID   string    `json:"channelId"`
			Title       string    `json:"title"`
			Description string    `json:"description"`
			Thumbnails  struct {
				Default struct {
					URL string `json:"url"`
				} `json:"default"`
			} `json:"thumbnails"`
			ChannelTitle         string   `json:"channelTitle"`
			Tags                 []string `json:"tags"`
			CategoryID           string   `json:"categoryId"`
			LiveBroadcastContent string   `json:"liveBroadcastContent"`
		} `json:"snippet"`
		Statistics struct {
			ViewCount    string `json:"viewCount"`
			LikeCount    string `json:"likeCount"`
			CommentCount string `json:"commentCount"`
		} `json:"statistics"`
		ContentDetails struct {
			Duration string `json:"duration"`
		} `json:"contentDetails"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Copy all fields from aux to v
	v.ID = aux.ID
	v.Snippet = aux.Snippet
	v.Statistics = aux.Statistics
	v.ContentDetails = aux.ContentDetails

	return nil
}

// fetchYouTubeVideoMetadata fetches video metadata from the YouTube Data API
func (a *Activities) fetchYouTubeVideoMetadata(ctx context.Context, videoID string) (*YouTubeVideoData, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Fetching YouTube video metadata", "video_id", videoID)

	// Create request to YouTube Data API
	url := fmt.Sprintf("https://www.googleapis.com/youtube/v3/videos?part=snippet,statistics,contentDetails&id=%s&key=%s",
		videoID, a.youtubeDeps.APIKey)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set application name in the request headers
	if a.youtubeDeps.ApplicationName != "" {
		req.Header.Set("X-Goog-Api-Key", a.youtubeDeps.APIKey)
		req.Header.Set("X-Goog-Api-Client", a.youtubeDeps.ApplicationName)
	}

	// Make request
	resp, err := a.youtubeDeps.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YouTube API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result struct {
		Items []YouTubeVideoData `json:"items"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w (body: %s)", err, string(body))
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("no video found for ID %s", videoID)
	}

	return &result.Items[0], nil
}

// fetchYouTubeTranscript fetches the transcript/captions for a YouTube video
func (a *Activities) fetchYouTubeTranscript(ctx context.Context, videoID string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Fetching YouTube transcript", "video_id", videoID)

	// Note: YouTube doesn't provide direct API access to transcripts/captions
	// We would need to use a third-party library or service to extract transcripts
	// For now, we'll return a placeholder message

	// TODO: Implement actual transcript fetching using a library like youtube-transcript-api
	// or a similar service that can extract transcripts from YouTube videos

	return "Transcript fetching not yet implemented. This would require a third-party library or service.", nil
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

// MarshalJSON implements json.Marshaler for YelpDependencies
func (deps YelpDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		APIKey   string `json:"api_key"`
		ClientID string `json:"client_id"`
	}

	aux := Aux{
		APIKey:   deps.APIKey,
		ClientID: deps.ClientID,
	}

	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for YelpDependencies
func (deps *YelpDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		APIKey   string `json:"api_key"`
		ClientID string `json:"client_id"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	deps.APIKey = aux.APIKey
	deps.ClientID = aux.ClientID

	return nil
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

// MarshalJSON implements json.Marshaler for GoogleDependencies
func (deps GoogleDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		APIKey         string `json:"api_key"`
		SearchEngineID string `json:"search_engine_id"`
	}

	aux := Aux{
		APIKey:         deps.APIKey,
		SearchEngineID: deps.SearchEngineID,
	}

	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for GoogleDependencies
func (deps *GoogleDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		APIKey         string `json:"api_key"`
		SearchEngineID string `json:"search_engine_id"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	deps.APIKey = aux.APIKey
	deps.SearchEngineID = aux.SearchEngineID

	return nil
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

// AmazonDependencies holds the dependencies for Amazon-related activities
type AmazonDependencies struct {
	Client        *http.Client
	APIKey        string
	APISecret     string
	AssociateTag  string
	MarketplaceID string
}

// Type returns the platform type for AmazonDependencies
func (deps AmazonDependencies) Type() PlatformType {
	return PlatformAmazon
}

// MarshalJSON implements json.Marshaler for AmazonDependencies
func (deps AmazonDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		APIKey        string `json:"api_key"`
		APISecret     string `json:"api_secret"`
		AssociateTag  string `json:"associate_tag"`
		MarketplaceID string `json:"marketplace_id"`
	}

	aux := Aux{
		APIKey:        deps.APIKey,
		APISecret:     deps.APISecret,
		AssociateTag:  deps.AssociateTag,
		MarketplaceID: deps.MarketplaceID,
	}

	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for AmazonDependencies
func (deps *AmazonDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		APIKey        string `json:"api_key"`
		APISecret     string `json:"api_secret"`
		AssociateTag  string `json:"associate_tag"`
		MarketplaceID string `json:"marketplace_id"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	deps.APIKey = aux.APIKey
	deps.APISecret = aux.APISecret
	deps.AssociateTag = aux.AssociateTag
	deps.MarketplaceID = aux.MarketplaceID

	return nil
}

// PullAmazonContent pulls content from Amazon
func (a *Activities) PullAmazonContent(ctx context.Context, contentID string) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Pulling Amazon content", "content_id", contentID)

	// Set HTTP client
	a.amazonDeps.Client = a.httpClient

	// TODO: implement Amazon content fetching
	// This would involve using the Amazon Product Advertising API to fetch product reviews, etc.
	// Example implementation would authenticate with the Amazon API and fetch content

	return "", fmt.Errorf("Amazon content fetching not yet implemented")
}

// VerifyPaymentResult represents the result of verifying a payment
type VerifyPaymentResult struct {
	Verified bool
	Amount   *solana.USDCAmount
	Error    string
}

// VerifyPayment verifies that payment has been received in the escrow account
func (a *Activities) VerifyPayment(ctx context.Context, from solanago.PublicKey, expectedAmount *solana.USDCAmount, timeout time.Duration) (*VerifyPaymentResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Verifying payment", "from", from.String(), "expected_amount", expectedAmount.ToUSDC(), "timeout", timeout)

	// Create a ticker to check balance periodically
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Create a timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		select {
		case <-timeoutCtx.Done():
			return &VerifyPaymentResult{
				Verified: false,
				Error:    "payment verification timed out",
			}, nil
		case <-ticker.C:
			// Get escrow account balance
			balance, err := a.solanaClient.GetUSDCBalance(ctx, a.solanaConfig.EscrowTokenAccount)
			if err != nil {
				logger.Error("Failed to get escrow balance", "error", err)
				continue
			}

			// Check if we have received the expected amount
			if balance.Cmp(expectedAmount) >= 0 {
				return &VerifyPaymentResult{
					Verified: true,
					Amount:   balance,
				}, nil
			}

			logger.Info("Waiting for payment",
				"expected", expectedAmount.ToUSDC(),
				"current", balance.ToUSDC())
		}
	}
}

// MarshalJSON implements json.Marshaler for YouTubeDependencies
func (deps YouTubeDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		APIKey          string `json:"api_key"`
		OAuthToken      string `json:"oauth_token"`
		ApplicationName string `json:"application_name"`
		MaxResults      int64  `json:"max_results"`
	}

	aux := Aux{
		APIKey:          deps.APIKey,
		OAuthToken:      deps.OAuthToken,
		ApplicationName: deps.ApplicationName,
		MaxResults:      deps.MaxResults,
	}

	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for YouTubeDependencies
func (deps *YouTubeDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		APIKey          string `json:"api_key"`
		OAuthToken      string `json:"oauth_token"`
		ApplicationName string `json:"application_name"`
		MaxResults      int64  `json:"max_results"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	deps.APIKey = aux.APIKey
	deps.OAuthToken = aux.OAuthToken
	deps.ApplicationName = aux.ApplicationName
	deps.MaxResults = aux.MaxResults

	return nil
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
