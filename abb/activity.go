package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"encoding/base64"

	solanautil "github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/memo"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/jmespath/go-jmespath"
	"go.temporal.io/sdk/activity"
	temporal_log "go.temporal.io/sdk/log"
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

// SolanaConfig holds the necessary configuration for Solana interactions.
type SolanaConfig struct {
	RPCEndpoint      string               `json:"rpc_endpoint"`
	WSEndpoint       string               `json:"ws_endpoint"`
	EscrowPrivateKey *solanago.PrivateKey `json:"escrow_private_key"`
	EscrowWallet     solanago.PublicKey   `json:"escrow_token_account"`
	TreasuryWallet   string               `json:"treasury_wallet"`
	USDCMintAddress  string               `json:"usdc_mint_address"`
}

// Activities holds all activity implementations and their dependencies
type Activities struct {
	solanaConfig SolanaConfig
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
func NewActivities(config SolanaConfig, serverURL, authToken string,
	redditDeps RedditDependencies,
	youtubeDeps YouTubeDependencies,
	yelpDeps YelpDependencies,
	googleDeps GoogleDependencies,
	amazonDeps AmazonDependencies,
	llmDeps LLMDependencies) (*Activities, error) {

	return &Activities{
		solanaConfig: config,
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
func (a *Activities) PullRedditContent(ctx context.Context, contentID string) (*RedditContent, error) {
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
	activity.GetLogger(ctx).Debug("Reddit API Response", "response", string(body))

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
	activity.GetLogger(ctx).Debug("Extracted Reddit Data", "data", string(resultJSON))

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
func (a *Activities) CheckContentRequirements(ctx context.Context, content []byte, requirements []string) (CheckContentRequirementsResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("Starting content requirements check",
		"content_length", len(content),
		"requirements_count", len(requirements))

	// Convert content bytes to string for the prompt
	contentStr := string(content)

	// Updated prompt to handle JSON input
	prompt := fmt.Sprintf(`You are a content verification system. Your task is to determine if the given content satisfies the specified requirements.

The content to evaluate is provided as a JSON object below.

Requirements:
%s

Content (JSON):
%s

You must respond with a valid JSON object in exactly this format:
{
  "satisfies": true/false,
  "reason": "your explanation here"
}

Do not include any text before or after the JSON object. The response must be valid JSON that can be parsed by a JSON parser.
Evaluate strictly and conservatively. Only return true if ALL requirements are clearly met by the data within the provided JSON content.`, strings.Join(requirements, "\n"), contentStr)

	// Log estimated token count (approx 4 chars/token)
	estimatedTokens := len(prompt) / 4
	logger.Info("Sending prompt to LLM", "estimated_tokens", estimatedTokens, "prompt_length_chars", len(prompt))

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

	return result, nil
}

// TransferUSDC pays a bounty to a user (represented by userID as a public key string)
// by directly transferring USDC from the configured fee payer wallet.
// Logic from the internal transferUSDC helper has been inlined.
func (a *Activities) TransferUSDC(ctx context.Context, userID string, amount float64) error {
	logger := activity.GetLogger(ctx)
	logger.Info("TransferUSDC activity started", "userID", userID, "amount", amount)

	// Convert amount to USDCAmount
	usdcAmount, err := solanautil.NewUSDCAmount(amount)
	if err != nil {
		logger.Error("Failed to create USDCAmount from input", "amount", amount, "error", err)
		return fmt.Errorf("failed to convert amount %.6f to USDCAmount: %w", amount, err)
	}

	// Convert user ID to a Solana wallet address
	// Note: In a real system, you would look up the user's wallet address using the user ID
	// For this example, we'll assume the userID is already a Solana public key string
	recipientPublicKey, err := solanago.PublicKeyFromBase58(userID)
	if err != nil {
		logger.Error("Invalid user ID format, expected Solana public key string", "userID", userID, "error", err)
		return fmt.Errorf("invalid user wallet address format '%s': %w", userID, err)
	}

	// 1. Initialize RPC Client
	client := solanautil.NewRPCClient(a.solanaConfig.RPCEndpoint)
	if err := solanautil.CheckRPCHealth(ctx, client); err != nil {
		logger.Error("RPC health check failed", "error", err)
		return fmt.Errorf("RPC health check failed: %w", err)
	}

	// 2. Load Keys and Addresses
	if a.solanaConfig.EscrowPrivateKey == nil {
		logger.Error("Escrow private key is missing in Solana configuration")
		return fmt.Errorf("escrow private key missing in config")
	}
	senderPrivateKey := *a.solanaConfig.EscrowPrivateKey

	// Get the Owner wallet address (the escrow wallet)
	escrowWalletOwner := a.solanaConfig.EscrowWallet

	// Parse the USDC mint address
	usdcMint, err := solanago.PublicKeyFromBase58(a.solanaConfig.USDCMintAddress)
	if err != nil {
		// This is a config error, likely fatal for this activity
		logger.Error("Failed to parse USDC mint address from config", "mint", a.solanaConfig.USDCMintAddress, "error", err)
		return fmt.Errorf("invalid USDC mint address in config: %w", err)
	}

	// Find the Associated Token Account (ATA) for the escrow wallet and USDC mint
	escrowAtaAddress, _, err := solanago.FindAssociatedTokenAddress(escrowWalletOwner, usdcMint)
	if err != nil {
		// ATA derivation should not fail unless inputs are wrong, treat as fatal
		logger.Error("Failed to derive ATA for escrow wallet", "owner", escrowWalletOwner, "mint", usdcMint, "error", err)
		return fmt.Errorf("failed to find ATA for escrow wallet: %w", err)
	}
	logger.Info("Checking balance for derived Escrow USDC ATA", "ata_address", escrowAtaAddress.String())

	// 3. Convert amount to uint64 base units
	amountBaseUnits := usdcAmount.ToSmallestUnit().Uint64()
	if amountBaseUnits == 0 {
		logger.Warn("Transfer amount is zero, skipping transfer.")
		// Consider if zero amount should be an error depending on requirements
		return nil
	}

	// 4. Send USDC using the solana package function
	logger.Info("Sending USDC transaction...", "sender", senderPrivateKey.PublicKey(), "recipient", recipientPublicKey, "amount_base_units", amountBaseUnits)
	sig, err := solanautil.SendUSDC(
		ctx,
		client,
		usdcMint,
		senderPrivateKey,
		recipientPublicKey, // Use the already parsed recipient key
		amountBaseUnits,
	)
	if err != nil {
		logger.Error("Failed to send USDC transaction via solana package", "error", err)
		// TODO: Check if the error indicates recipient ATA doesn't exist and provide a better error message or handle creation.
		return fmt.Errorf("failed to send USDC: %w", err)
	}
	logger.Info("Transaction sent", "signature", sig.String())

	// 5. Confirm the transaction
	// The overall activity timeout should handle excessive confirmation waits.
	logger.Info("Waiting for transaction confirmation...", "signature", sig.String())
	err = solanautil.ConfirmTransaction(ctx, client, sig, rpc.CommitmentFinalized)
	if err != nil {
		logger.Error("Transaction confirmation failed", "signature", sig.String(), "error", err)
		// Decide if confirmation failure should be a hard error for the activity
		return fmt.Errorf("failed to confirm transaction %s: %w", sig, err)
	}

	logger.Info("TransferUSDC activity completed successfully", "userID", userID, "amount", amount, "signature", sig.String())
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
	ApplicationName string
	MaxResults      int64
}

// Type returns the platform type for YouTubeDependencies
func (deps YouTubeDependencies) Type() PlatformType {
	return PlatformYouTube
}

// MarshalJSON implements json.Marshaler for YouTubeDependencies
// Simplified: Removed OAuth fields
func (deps YouTubeDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		APIKey          string `json:"api_key"`
		ApplicationName string `json:"application_name"`
		MaxResults      int64  `json:"max_results"`
	}

	aux := Aux{
		APIKey:          deps.APIKey,
		ApplicationName: deps.ApplicationName,
		MaxResults:      deps.MaxResults,
	}

	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for YouTubeDependencies
// Simplified: Removed OAuth fields
func (deps *YouTubeDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		APIKey          string `json:"api_key"`
		ApplicationName string `json:"application_name"`
		MaxResults      int64  `json:"max_results"`
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	deps.APIKey = aux.APIKey
	deps.ApplicationName = aux.ApplicationName
	deps.MaxResults = aux.MaxResults

	return nil
}

// YouTubeContent represents the extracted content from YouTube
// Simplified to hold basic metadata and the scraped transcript.
type YouTubeContent struct {
	ID                   string    `json:"id"`
	Title                string    `json:"title"`
	Description          string    `json:"description"`
	ChannelID            string    `json:"channel_id"`
	ChannelTitle         string    `json:"channel_title"`
	PublishedAt          time.Time `json:"published_at"`
	ViewCount            string    `json:"view_count"`    // Kept as string as API returns it this way
	LikeCount            string    `json:"like_count"`    // Kept as string
	CommentCount         string    `json:"comment_count"` // Kept as string
	Duration             string    `json:"duration"`
	ThumbnailURL         string    `json:"thumbnail_url"`
	Tags                 []string  `json:"tags"`
	CategoryID           string    `json:"category_id"`
	LiveBroadcastContent string    `json:"live_broadcast_content"`
	Transcript           string    `json:"transcript,omitempty"` // Store the scraped transcript here
}

// PullYouTubeContent pulls metadata from YouTube Data API and transcript via scraping.
func (a *Activities) PullYouTubeContent(ctx context.Context, contentID string) (*YouTubeContent, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Pulling YouTube content", "content_id", contentID)

	// Set HTTP client (needed for both metadata and transcript fetching)
	a.youtubeDeps.Client = a.httpClient

	// Extract video ID from contentID (assuming format: yt_video_id or just video_id)
	videoID := strings.TrimPrefix(contentID, "yt_")

	// Fetch video metadata using YouTube Data API (still useful)
	videoData, err := a.fetchYouTubeVideoMetadata(ctx, videoID)
	if err != nil {
		// Don't fail outright if metadata fetch fails, maybe transcript is enough?
		logger.Warn("Failed to fetch YouTube video metadata, proceeding to transcript fetch", "video_id", videoID, "error", err)
		// Create a minimal content struct if metadata fails
		content := &YouTubeContent{
			ID: videoID,
		}
		// Attempt to fetch transcript even if metadata failed
		transcript, transcriptErr := a.FetchYouTubeTranscriptDirectly(ctx, videoID, "en") // Default to English
		if transcriptErr != nil {
			logger.Warn("Failed to fetch YouTube transcript directly after metadata failure", "video_id", videoID, "error", transcriptErr)
			// If both fail, return the original metadata error or a combined error
			return nil, fmt.Errorf("failed to fetch metadata (%v) and transcript (%v)", err, transcriptErr)
		} else {
			content.Transcript = transcript
			return content, nil // Return with ID and transcript
		}
	}

	// Create YouTubeContent struct from metadata
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

	// Fetch transcript using the direct scraping method
	transcript, err := a.FetchYouTubeTranscriptDirectly(ctx, videoID, "en") // Default to English
	if err != nil {
		logger.Warn("Failed to fetch YouTube transcript directly", "video_id", videoID, "error", err)
		// Don't fail the whole activity if transcript fails, return metadata only
	} else {
		content.Transcript = transcript
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

// FetchYouTubeTranscriptDirectly attempts to fetch a YouTube transcript by scraping the watch page.
// It looks for embedded JSON data containing caption track URLs using brace counting.
// Defaults to English ("en") if no specific language is requested and found.
func (a *Activities) FetchYouTubeTranscriptDirectly(ctx context.Context, videoID string, preferredLanguage string) (string, error) {
	logger := activity.GetLogger(ctx)
	if preferredLanguage == "" {
		preferredLanguage = "en" // Default to English
	}
	logger.Info("Fetching YouTube transcript directly (scraping)", "video_id", videoID, "language", preferredLanguage)

	watchURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	req, err := http.NewRequestWithContext(ctx, "GET", watchURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request for watch page: %w", err)
	}
	// Set headers to mimic a browser request
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := a.httpClient
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch watch page %s: %w", watchURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch watch page %s: status code %d", watchURL, resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read watch page body: %w", err)
	}
	bodyString := string(bodyBytes)

	// Find the start of the ytInitialPlayerResponse JSON
	startMarker := "ytInitialPlayerResponse = "
	startIndex := strings.Index(bodyString, startMarker)
	if startIndex == -1 {
		return "", fmt.Errorf("could not find '%s' marker in watch page HTML", startMarker)
	}
	startIndex += len(startMarker)

	// Find the end of the JSON object using brace counting
	braceLevel := 0
	endIndex := -1
	inString := false
	for i := startIndex; i < len(bodyString); i++ {
		switch bodyString[i] {
		case '{':
			if !inString {
				braceLevel++
			}
		case '}':
			if !inString {
				braceLevel--
				if braceLevel == 0 {
					// Found the end of the top-level object
					endIndex = i + 1
					goto endLoop // Exit the loop once the end is found
				}
			}
		case '"': // Compare byte value directly
			// Basic handling for strings to avoid counting braces inside them.
			// Check if the previous character was an escape character ('\').
			if !(i > 0 && bodyString[i-1] == '\\') { // Check if the previous char was a backslash
				inString = !inString // Toggle inString state only if it's not an escaped quote
			}
		}
	}
endLoop:

	if endIndex == -1 {
		return "", fmt.Errorf("could not find the end of the ytInitialPlayerResponse JSON object using brace counting")
	}

	jsonString := bodyString[startIndex:endIndex]

	// Debug: Log the extracted JSON string
	// logger.Debug("Extracted JSON string", "json_string", jsonString)

	var playerResponse ytInitialPlayerResponse
	if err := json.Unmarshal([]byte(jsonString), &playerResponse); err != nil {
		// Log more context on unmarshal failure
		logSnippet := jsonString
		if len(logSnippet) > 1000 {
			logSnippet = logSnippet[:500] + "..." + logSnippet[len(logSnippet)-500:]
		}
		logger.Error("Failed to unmarshal ytInitialPlayerResponse JSON", "error", err, "extracted_json_snippet", logSnippet)
		return "", fmt.Errorf("failed to unmarshal player response JSON: %w", err)
	}

	if playerResponse.Captions == nil ||
		playerResponse.Captions.PlayerCaptionsTracklistRenderer == nil ||
		playerResponse.Captions.PlayerCaptionsTracklistRenderer.CaptionTracks == nil {
		logger.Warn("No caption tracks found in player response JSON", "video_id", videoID)
		return "", fmt.Errorf("no caption tracks found for video %s", videoID)
	}

	var captionURL string
	foundPreferred := false
	captionTracks := *playerResponse.Captions.PlayerCaptionsTracklistRenderer.CaptionTracks

	// Try to find the preferred language
	for _, track := range captionTracks {
		if track.LanguageCode == preferredLanguage {
			captionURL = track.BaseUrl
			foundPreferred = true
			logger.Info("Found preferred language caption track", "language", preferredLanguage, "url", captionURL)
			break
		}
	}

	// If preferred language not found, try default English if different
	if !foundPreferred && preferredLanguage != "en" {
		for _, track := range captionTracks {
			if track.LanguageCode == "en" {
				captionURL = track.BaseUrl
				logger.Info("Preferred language not found, using default English caption track", "url", captionURL)
				break
			}
		}
	}

	// If still not found, take the first available track as a last resort
	if captionURL == "" && len(captionTracks) > 0 {
		captionURL = captionTracks[0].BaseUrl
		logger.Warn("Preferred/default language not found, using first available caption track", "language", captionTracks[0].LanguageCode, "url", captionURL)
	}

	if captionURL == "" {
		return "", fmt.Errorf("no suitable caption track URL found for video %s", videoID)
	}

	// Fetch the actual caption content
	captionReq, err := http.NewRequestWithContext(ctx, "GET", captionURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request for caption URL %s: %w", captionURL, err)
	}
	captionResp, err := client.Do(captionReq)
	if err != nil {
		return "", fmt.Errorf("failed to fetch caption content from %s: %w", captionURL, err)
	}
	defer captionResp.Body.Close()

	if captionResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch caption content from %s: status code %d", captionURL, captionResp.StatusCode)
	}

	captionBytes, err := io.ReadAll(captionResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read caption content body: %w", err)
	}

	logger.Info("Successfully fetched transcript directly", "video_id", videoID, "language", preferredLanguage, "bytes", len(captionBytes))
	return string(captionBytes), nil
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
	Amount   *solanautil.USDCAmount
	Error    string
}

// VerifyPayment verifies that a specific payment transaction has been received in the escrow account
func (a *Activities) VerifyPayment(ctx context.Context, from solanago.PublicKey, expectedAmount *solanautil.USDCAmount, timeout time.Duration) (*VerifyPaymentResult, error) {
	logger := activity.GetLogger(ctx)
	expectedAmountUint64 := expectedAmount.ToSmallestUnit().Uint64()
	activityInfo := activity.GetInfo(ctx)
	expectedWorkflowID := activityInfo.WorkflowExecution.ID
	logger.Info("VerifyPayment started",
		"workflow_id", expectedWorkflowID,
		"from_owner", from.String(),
		"expected_amount_lamports", expectedAmountUint64,
		"timeout", timeout,
		"rpc_endpoint", a.solanaConfig.RPCEndpoint,
		"usdc_mint", a.solanaConfig.USDCMintAddress,
		"escrow_owner", a.solanaConfig.EscrowWallet.String())

	// Create a ticker to check transactions periodically
	ticker := time.NewTicker(10 * time.Second) // Check more frequently for debugging
	defer ticker.Stop()

	// Create a timeout context for the entire verification process
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rpcClient := solanautil.NewRPCClient(a.solanaConfig.RPCEndpoint)

	// Get the escrow wallet that should receive the funds
	escrowWallet := a.solanaConfig.EscrowWallet

	// Parse the USDC mint address
	usdcMint, err := solanago.PublicKeyFromBase58(a.solanaConfig.USDCMintAddress)
	if err != nil {
		logger.Error("Failed to parse USDC mint address from config", "mint", a.solanaConfig.USDCMintAddress, "error", err)
		return nil, fmt.Errorf("invalid USDC mint address in config: %w", err)
	}

	// Find the Associated Token Account (ATA) for the escrow wallet and USDC mint
	escrowAtaAddress, _, err := solanago.FindAssociatedTokenAddress(escrowWallet, usdcMint)
	if err != nil {
		logger.Error("Failed to derive ATA for escrow wallet", "owner", escrowWallet, "mint", usdcMint, "error", err)
		return nil, fmt.Errorf("failed to find ATA for escrow wallet: %w", err)
	}
	logger.Info("Derived Escrow USDC ATA to check for incoming transaction", "ata_address", escrowAtaAddress.String())

	// Keep track of signatures we've already checked to avoid redundant lookups
	checkedSignatures := make(map[solanago.Signature]bool)

	for {
		select {
		case <-timeoutCtx.Done():
			logger.Warn("Payment verification timed out", "from_owner", from.String(), "escrow_ata", escrowAtaAddress.String())
			return &VerifyPaymentResult{
				Verified: false,
				Error:    "payment verification timed out",
			}, nil // Timeout is not a processing error, just verification failure

		case <-ticker.C:
			logger.Debug("Checking for recent transactions...", "escrow_ata", escrowAtaAddress.String())
			// Get recent signatures for the escrow ATA
			limitSignatures := 15 // Fetch a few more signatures
			signatures, err := rpcClient.GetSignaturesForAddressWithOpts(
				ctx,
				escrowAtaAddress,
				&rpc.GetSignaturesForAddressOpts{
					Limit:      &limitSignatures, // Pointer to int
					Commitment: rpc.CommitmentFinalized,
				},
			)
			if err != nil {
				logger.Error("Failed to get signatures for escrow ATA", "account", escrowAtaAddress.String(), "error", err)
				// Don't fail immediately, could be transient RPC issue, wait for next tick or timeout
				continue
			}

			logger.Debug("Fetched signatures", "count", len(signatures))

			// Process signatures from oldest in batch to newest to find the first matching one
			for i := len(signatures) - 1; i >= 0; i-- {
				sigResult := signatures[i]
				// Defensive nil check
				if sigResult == nil {
					logger.Warn("Received nil signature result in batch", "index", i)
					continue
				}
				sig := sigResult.Signature
				logger.Debug("Checking signature", "index", i, "sig", sig.String())

				if checkedSignatures[sig] {
					logger.Debug("Signature already checked, skipping", "sig", sig.String())
					continue // Already processed this one
				}
				checkedSignatures[sig] = true // Mark as checked

				// Check if transaction actually succeeded before fetching details
				if sigResult.Err != nil {
					logger.Debug("Skipping failed transaction", "signature", sig.String(), "error", sigResult.Err)
					continue
				}

				logger.Debug("Fetching transaction details", "signature", sig.String())
				// Ensure MaxSupportedTransactionVersion is set to 0 to handle versioned transactions
				maxSupportedTxVersion := uint64(0)
				txResult, err := rpcClient.GetTransaction(
					ctx,
					sig,
					&rpc.GetTransactionOpts{
						Encoding:                       solanago.EncodingBase64, // Fetch as Base64 to avoid parsing issues
						Commitment:                     rpc.CommitmentFinalized,
						MaxSupportedTransactionVersion: &maxSupportedTxVersion,
					},
				)
				if err != nil {
					logger.Warn("Failed to get transaction details", "signature", sig.String(), "error", err)
					continue
				}
				if txResult == nil || txResult.Meta == nil {
					logger.Warn("Received nil transaction or meta", "signature", sig.String())
					continue
				}
				// Double-check transaction success via meta error
				if txResult.Meta.Err != nil {
					logger.Debug("Skipping transaction with meta error", "signature", sig.String(), "error", txResult.Meta.Err)
					continue
				}

				// --- Transaction Parsing Logic ---
				// Look for the specific transfer: from 'from' owner, to 'escrowAtaAddress', correct USDC amount.
				// Using token balances is often the most reliable way across instruction types.
				if txResult.Meta != nil && txResult.Meta.PreTokenBalances != nil && txResult.Meta.PostTokenBalances != nil && txResult.Transaction != nil {

					// Get account keys from the meta - this might be more reliable than parsing the tx itself
					// Need to reconstruct the PublicKey list from meta if possible, or find another way
					// For now, let's attempt to get it from the (potentially unparsed) transaction envelope first.
					// If GetTransaction fails due to parsing later, this check might need refinement.
					var accountKeys []solanago.PublicKey
					rawTx, err := txResult.Transaction.GetTransaction()
					if err != nil {
						logger.Warn("Could not decode raw transaction from envelope", "signature", sig.String(), "error", err)
						// Even if we can't check memo, maybe balances are enough? Let checkTokenBalances run.
						// Setting accountKeys to nil might break checkTokenBalances, needs review.
						// For now, let's assume we need the decoded tx for reliable keys.
						logger.Error("Cannot verify transaction without decoded account keys", "signature", sig.String())
						continue
					}
					if rawTx == nil || len(rawTx.Message.AccountKeys) == 0 {
						logger.Warn("Decoded transaction or account keys are nil/empty, cannot verify transaction", "signature", sig.String())
						continue
					}
					accountKeys = rawTx.Message.AccountKeys

					// --- Check 1: Verify Memo ---
					memoMatches := false
					memoContent := ""
					for _, ix := range rawTx.Message.Instructions {
						progKey, err := rawTx.Message.Program(ix.ProgramIDIndex)
						if err != nil {
							logger.Warn("Could not get program key for instruction", "index", ix.ProgramIDIndex, "error", err)
							continue
						}
						if progKey.Equals(memo.ProgramID) {
							memoContent = string(ix.Data)
							if memoContent == expectedWorkflowID {
								memoMatches = true
								break // Found matching memo
							}
						}
					}
					logger.Debug("Memo check result", "signature", sig.String(), "found_memo", memoContent, "expected_memo", expectedWorkflowID, "match", memoMatches)

					// --- Check 2: Verify Token Balances ---
					balancesMatch, err := checkTokenBalancesForTransfer(
						logger, // Pass the temporal logger
						txResult.Meta.PreTokenBalances,
						txResult.Meta.PostTokenBalances,
						accountKeys,          // Pass account keys from message
						from,                 // Expected source *owner*
						escrowAtaAddress,     // Expected destination *ATA*
						usdcMint,             // Expected mint
						expectedAmountUint64, // Expected amount in lamports
					)
					if err != nil {
						logger.Error("Error checking token balances for tx", "signature", sig.String(), "error", err)
						continue // Move to next signature
					}

					// --- Final Verification ---
					if balancesMatch && memoMatches {
						logger.Info("Matching payment transaction found (balances and memo match)",
							"signature", sig.String(),
							"from_owner", from.String(),
							"to_ata", escrowAtaAddress.String(),
							"amount_lamports", expectedAmountUint64)
						// We found the specific transaction we were looking for.
						return &VerifyPaymentResult{
							Verified: true,
							Amount:   expectedAmount, // Return the amount we were looking for
						}, nil
					}
				} else {
					logger.Warn("Transaction missing pre/post token balances, cannot verify via balance diff", "signature", sig.String())
					// TODO: Optionally add parsing of instructions here as a fallback
				}
				// --- End Transaction Parsing ---
			}
			logger.Debug("Finished checking batch of signatures")
		} // end select
	} // end for
}

// checkTokenBalancesForTransfer parses token balances to find a specific transfer.
// Returns true if the specified transfer is found, false otherwise.
func checkTokenBalancesForTransfer(
	logger temporal_log.Logger,
	preBalances []rpc.TokenBalance,
	postBalances []rpc.TokenBalance,
	accountKeys []solanago.PublicKey, // Added: List of accounts from the transaction message
	expectedSourceOwner solanago.PublicKey,
	expectedDestATA solanago.PublicKey,
	expectedMint solanago.PublicKey,
	expectedAmountLamports uint64,
) (bool, error) {

	logger.Debug("checkTokenBalances: Input",
		"expectedSourceOwner", expectedSourceOwner.String(),
		"expectedDestATA", expectedDestATA.String(),
		"expectedMint", expectedMint.String(),
		"expectedAmountLamports", expectedAmountLamports)

	// Create maps for easy lookup PREFERABLY from ATA address -> balance info
	preBalanceMap := make(map[solanago.PublicKey]rpc.TokenBalance) // Correct type pointer usage
	for _, b := range preBalances {
		if int(b.AccountIndex) >= len(accountKeys) {
			logger.Error("PreBalance AccountIndex out of bounds", "index", b.AccountIndex, "accountKeysLen", len(accountKeys))
			continue // Skip invalid index
		}
		accountAddr := accountKeys[b.AccountIndex]
		preBalanceMap[accountAddr] = b
	}
	postBalanceMap := make(map[solanago.PublicKey]rpc.TokenBalance) // Correct type pointer usage
	for _, b := range postBalances {
		if int(b.AccountIndex) >= len(accountKeys) {
			logger.Error("PostBalance AccountIndex out of bounds", "index", b.AccountIndex, "accountKeysLen", len(accountKeys))
			continue // Skip invalid index
		}
		accountAddr := accountKeys[b.AccountIndex]
		postBalanceMap[accountAddr] = b
		logger.Debug("checkTokenBalances: Post balance entry", "accountIndex", b.AccountIndex, "accountAddr", accountAddr.String(), "owner", b.Owner.String(), "mint", b.Mint.String(), "amount", b.UiTokenAmount.Amount)
	}

	// Check Destination ATA
	postDestBal, destExists := postBalanceMap[expectedDestATA]
	if !destExists {
		logger.Debug("checkTokenBalances: Expected destination ATA not found in post balances", "dest_ata", expectedDestATA.String())
		return false, nil
	}
	if !postDestBal.Mint.Equals(expectedMint) {
		logger.Debug("checkTokenBalances: Destination ATA mint mismatch", "dest_ata", expectedDestATA.String(), "found_mint", postDestBal.Mint.String(), "expected_mint", expectedMint.String())
		return false, nil
	}
	preDestBal, ok := preBalanceMap[expectedDestATA] // Okay if it didn't exist before

	preDestAmountLamports := uint64(0)
	// Only parse if pre-balance existed and amount is valid
	if ok && preDestBal.UiTokenAmount.Amount != "" {
		var err error
		preDestAmountLamports, err = strconv.ParseUint(preDestBal.UiTokenAmount.Amount, 10, 64)
		if err != nil {
			logger.Warn("checkTokenBalances: Failed to parse preDestAmountLamports", "value", preDestBal.UiTokenAmount.Amount, "error", err)
			// Treat unparseable balance as 0 or handle error appropriately
			preDestAmountLamports = 0
		}
	}
	postDestAmountLamports, err := strconv.ParseUint(postDestBal.UiTokenAmount.Amount, 10, 64)
	if err != nil {
		logger.Error("checkTokenBalances: Failed to parse postDestAmountLamports", "value", postDestBal.UiTokenAmount.Amount, "error", err)
		return false, fmt.Errorf("failed to parse post-destination balance: %w", err) // Critical parsing failure
	}

	destIncrease := postDestAmountLamports - preDestAmountLamports
	logger.Debug("checkTokenBalances: Destination balance check", "dest_ata", expectedDestATA.String(), "pre", preDestAmountLamports, "post", postDestAmountLamports, "increase", destIncrease, "expected", expectedAmountLamports)
	if destIncrease != expectedAmountLamports {
		logger.Debug("checkTokenBalances: Destination ATA balance did not increase by expected amount")
		return false, nil // Didn't receive the right amount
	}
	logger.Debug("Destination ATA balance increase matches expected amount", "dest_ata", expectedDestATA.String(), "increase", expectedAmountLamports)

	// Check Source Account
	foundSourceMatch := false
	for sourceAta, preSourceBal := range preBalanceMap {
		// Check mint and owner match
		logger.Debug("checkTokenBalances: Checking potential source account", "source_ata", sourceAta.String(), "owner", preSourceBal.Owner.String(), "expected_owner", expectedSourceOwner.String(), "mint", preSourceBal.Mint.String(), "expected_mint", expectedMint.String())
		if preSourceBal.Mint.Equals(expectedMint) && preSourceBal.Owner.Equals(expectedSourceOwner) {
			postSourceBal, sourcePostExists := postBalanceMap[sourceAta]
			if !sourcePostExists { // Source account might have been closed, check balance went to 0
				logger.Warn("checkTokenBalances: Source ATA not found in post balances, potentially closed?", "ata", sourceAta.String())
				continue // Cannot verify decrease if post balance is missing
			}

			preSourceAmountLamports, err := strconv.ParseUint(preSourceBal.UiTokenAmount.Amount, 10, 64)
			if err != nil {
				logger.Warn("checkTokenBalances: Failed to parse preSourceAmountLamports", "value", preSourceBal.UiTokenAmount.Amount, "error", err)
				continue // Skip if pre-balance is unparseable
			}
			postSourceAmountLamports, err := strconv.ParseUint(postSourceBal.UiTokenAmount.Amount, 10, 64)
			if err != nil {
				logger.Warn("checkTokenBalances: Failed to parse postSourceAmountLamports", "value", postSourceBal.UiTokenAmount.Amount, "error", err)
				continue // Skip if post-balance is unparseable
			}

			sourceDecrease := preSourceAmountLamports - postSourceAmountLamports
			logger.Debug("checkTokenBalances: Source balance check", "source_ata", sourceAta.String(), "pre", preSourceAmountLamports, "post", postSourceAmountLamports, "decrease", sourceDecrease, "expected", expectedAmountLamports)

			// Check if balance decreased by the expected amount
			if sourceDecrease == expectedAmountLamports {
				logger.Debug("Source account balance decrease matches expected amount and owner",
					"source_ata", sourceAta.String(),
					"source_owner", expectedSourceOwner.String(),
					"decrease", expectedAmountLamports)
				foundSourceMatch = true
				break // Found the matching source decrease
			}
		}
	}

	if !foundSourceMatch {
		logger.Debug("checkTokenBalances: Did not find any source ATA owned by expected owner with matching balance decrease")
		return false, nil
	}

	// If we passed the destination check AND found a matching source owned by the correct wallet
	return true, nil
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

// Helper function to create pointer for literals (if solanago.Ptr is unavailable/problematic)
func ptr[T any](v T) *T {
	return &v
}

// ytInitialPlayerResponse is used to parse the JSON data embedded in the YouTube watch page HTML
// containing caption track information.
type ytInitialPlayerResponse struct {
	Captions *struct {
		PlayerCaptionsTracklistRenderer *struct {
			CaptionTracks *[]struct {
				BaseUrl        string `json:"baseUrl"`
				LanguageCode   string `json:"languageCode"`
				IsTranslatable bool   `json:"isTranslatable"`
			} `json:"captionTracks"`
		} `json:"playerCaptionsTracklistRenderer"`
	} `json:"captions"`
}
