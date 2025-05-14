package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os" // Added for potential string conversions
	"strings"
	"time"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/jmespath/go-jmespath" // Added for Reddit JMESPath
	"go.temporal.io/sdk/activity"
)

// Environment Variable Keys for Configuration
const (
	EnvOpenAIAPIKey         = "OPENAI_API_KEY"
	EnvGeminiAPIKey         = "GEMINI_API_KEY"
	EnvAnthropicAPIKey      = "ANTHROPIC_API_KEY"
	EnvLLMProvider          = "LLM_PROVIDER"         // e.g., "openai", "gemini", "anthropic"
	EnvLLMModel             = "LLM_MODEL"            // e.g., "gpt-4o", "gemini-1.5-pro"
	EnvLLMBasePromptFile    = "LLM_BASE_PROMPT_FILE" // Path to file containing base prompt
	EnvSolanaRPCURL         = "SOLANA_RPC_URL"
	EnvSolanaPrivateKey     = "SOLANA_PRIVATE_KEY"       // Base58 encoded private key for escrow/treasury
	EnvSolanaTreasuryWallet = "SOLANA_TREASURY_WALLET"   // Base58 public key
	EnvSolanaEscrowWallet   = "SOLANA_ESCROW_WALLET"     // Base58 public key (can be same as Treasury)
	EnvSolanaUSDCMint       = "SOLANA_USDC_MINT_ADDRESS" // Base58 public key of USDC mint
	EnvEmailSMTP            = "EMAIL_SMTP"
	EnvEmailSMTPPort        = "EMAIL_SMTP_PORT"
	EnvEmailPassword        = "EMAIL_PASSWORD"
	EnvEmailSender          = "EMAIL_SENDER"
	EnvRedditFlairID        = "REDDIT_FLAIR_ID"
	EnvPublicBaseURL        = "PUBLIC_BASE_URL"
	EnvRedditClientID       = "REDDIT_CLIENT_ID"
	EnvRedditClientSecret   = "REDDIT_CLIENT_SECRET"
	EnvRedditUsername       = "REDDIT_USERNAME"
	EnvRedditPassword       = "REDDIT_PASSWORD"
	EnvRedditUserAgent      = "REDDIT_USER_AGENT"
	EnvYouTubeAPIKey        = "YOUTUBE_API_KEY"
	EnvTwitchClientID       = "TWITCH_CLIENT_ID"
	EnvTwitchClientSecret   = "TWITCH_CLIENT_SECRET"
	EnvWorkerEnvironment    = "WORKER_ENVIRONMENT"
	EnvAPIServerURL         = "API_SERVER_URL"
	// FIXME: remove these probably
	EnvSMTPHost        = "SMTP_HOST"
	EnvSMTPPort        = "SMTP_PORT"
	EnvSMTPUser        = "SMTP_USER"
	EnvSMTPPassword    = "SMTP_PASSWORD"
	EnvSMTPFromAddress = "SMTP_FROM_ADDRESS"
	// Add new constants for Image LLM
	EnvLLMImageProvider = "LLM_IMAGE_PROVIDER" // e.g., "openai", "google"
	EnvLLMImageAPIKey   = "LLM_IMAGE_API_KEY"  // API key for the image provider
	EnvLLMImageModel    = "LLM_IMAGE_MODEL"    // Model name, e.g., "gpt-4-vision-preview"

	// Constants revealed by linter after refactor
	EnvSolanaWSEndpoint      = "SOLANA_WS_ENDPOINT"        // Optional WS endpoint
	EnvYouTubeAppName        = "YOUTUBE_APP_NAME"          // App name for YouTube API
	EnvServerURL             = "ABB_SERVER_URL"            // URL for internal API server
	EnvAuthToken             = "ABB_AUTH_TOKEN"            // Auth token for internal API server
	EnvLLMCheckReqPromptBase = "LLM_CHECK_REQ_PROMPT_BASE" // Base prompt for CheckContentRequirements
	EnvServerSecretKey       = "ABB_SERVER_SECRET_KEY"     // Secret key for internal API server auth/ops
	EnvTargetSubreddit       = "REDDIT_TARGET_SUBREDDIT"   // Subreddit for publishing bounties
	EnvServerEnv             = "ABB_ENV"                   // Environment name (e.g., "dev", "prod")
)

// Default values if Env vars not set
const (
	DefaultLLMCheckReqPromptBase = "You are a meticulous content verification system. Your goal is to determine if the provided Content strictly adheres to ALL of the given Requirements. Focus only on the requirements provided."
)

// --- Start Platform Agnostic Content Structures ---

// ContentKind defines the type of content (e.g., post, comment, video)
type ContentKind string

const (
	ContentKindPost    ContentKind = "post"
	ContentKindComment ContentKind = "comment"
	ContentKindVideo   ContentKind = "video"
	ContentKindClip    ContentKind = "clip"  // For Twitch clips
	ContentKindStory   ContentKind = "story" // For Hacker News stories, could be same as post
	// Add other generic kinds as needed
)

// PlatformKind defines the social media platform
type PlatformKind string

const (
	PlatformReddit     PlatformKind = "reddit"
	PlatformYouTube    PlatformKind = "youtube"
	PlatformTwitch     PlatformKind = "twitch"
	PlatformHackerNews PlatformKind = "hackernews"
	PlatformBluesky    PlatformKind = "bluesky"
	// Add other platforms as needed
)

// --- End Platform Agnostic Content Structures ---

// --- Platform Specific Content Structures Removed (Defined later in the file) ---
/*
type RedditContent struct { ... }
type YouTubeContent struct { ... }
type TwitchVideoContent struct { ... }
type TwitchClipContent struct { ... }
type HackerNewsContent struct { ... }
*/
// --- End Platform Specific Content Structures ---

// --- Start Bluesky Structures ---
// Moved from activity_bluesky.go

// BlueskyDependencies holds dependencies for Bluesky activities (currently none needed for public read)
type BlueskyDependencies struct {
	// Add fields like Handle, AppPassword if authentication is needed later
}

// Type returns the platform type for BlueskyDependencies
func (deps BlueskyDependencies) Type() PlatformKind {
	return PlatformBluesky
}

// MarshalJSON implements json.Marshaler for BlueskyDependencies
func (deps BlueskyDependencies) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{}{}) // Empty object for now
}

// UnmarshalJSON implements json.Unmarshaler for BlueskyDependencies
func (deps *BlueskyDependencies) UnmarshalJSON(data []byte) error {
	var aux struct{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return fmt.Errorf("failed to unmarshal BlueskyDependencies: %w", err)
	}
	return nil
}

// BlueskyContent represents the extracted content from a Bluesky post view
// Based on app.bsky.feed.defs#postView and related schemas
type BlueskyContent struct {
	Uri         string                  `json:"uri"`             // AT URI of the post
	Cid         string                  `json:"cid"`             // CID of the post record
	Author      BlueskyProfileViewBasic `json:"author"`          // Basic profile view of the author
	Record      json.RawMessage         `json:"record"`          // The raw post record (app.bsky.feed.post)
	Embed       *BlueskyEmbedView       `json:"embed,omitempty"` // Embedded content (images, external links, etc.)
	ReplyCount  *int64                  `json:"replyCount,omitempty"`
	RepostCount *int64                  `json:"repostCount,omitempty"`
	LikeCount   *int64                  `json:"likeCount,omitempty"`
	IndexedAt   time.Time               `json:"indexedAt"`
	Labels      []string                `json:"labels,omitempty"` // Assuming labels are strings for simplicity
	// Extracted Text from Record for convenience
	Text string `json:"text,omitempty"`
}

// BlueskyProfileViewBasic represents a subset of app.bsky.actor.defs#profileViewBasic
type BlueskyProfileViewBasic struct {
	Did         string   `json:"did"`
	Handle      string   `json:"handle"`
	DisplayName *string  `json:"displayName,omitempty"`
	Avatar      *string  `json:"avatar,omitempty"`
	Labels      []string `json:"labels,omitempty"` // Assuming labels are strings
}

// BlueskyEmbedView represents possible embed types (simplified)
type BlueskyEmbedView struct {
	Type     string                    `json:"$type"` // e.g., "app.bsky.embed.images#view", "app.bsky.embed.external#view"
	Images   []BlueskyEmbedImageView   `json:"images,omitempty"`
	External *BlueskyEmbedExternalView `json:"external,omitempty"`
	Record   *BlueskyEmbedRecordView   `json:"record,omitempty"` // For quote posts/record embeds
	// Add other embed types as needed (e.g., RecordWithMedia)
}

// BlueskyEmbedImageView represents app.bsky.embed.images#viewImage
type BlueskyEmbedImageView struct {
	Thumb    string `json:"thumb"`    // URL of thumbnail
	Fullsize string `json:"fullsize"` // URL of full-size image
	Alt      string `json:"alt"`      // Alt text
}

// BlueskyEmbedExternalView represents app.bsky.embed.external#viewExternal
type BlueskyEmbedExternalView struct {
	Uri         string `json:"uri"`
	Thumb       string `json:"thumb,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// BlueskyEmbedRecordView represents app.bsky.embed.record#viewRecord (for quote posts)
type BlueskyEmbedRecordView struct {
	Uri    string                  `json:"uri"`
	Cid    string                  `json:"cid"`
	Author BlueskyProfileViewBasic `json:"author"`
	// Value json.RawMessage `json:"value"` // The actual record content - maybe omit for simplicity?
	Embeds *[]BlueskyEmbedView `json:"embeds,omitempty"` // Nested embeds within the quoted post
}

// BlueskyHandleResponse is used to parse the JSON response from com.atproto.identity.resolveHandle
type BlueskyHandleResponse struct {
	DID string `json:"did"`
}

// --- End Bluesky Structures ---

// SolanaConfig holds the necessary configuration for Solana interactions.
type SolanaConfig struct {
	RPCEndpoint      string               `json:"rpc_endpoint"`
	WSEndpoint       string               `json:"ws_endpoint"`
	EscrowPrivateKey *solanago.PrivateKey `json:"escrow_private_key"`
	EscrowWallet     solanago.PublicKey   `json:"escrow_token_account"`
	TreasuryWallet   string               `json:"treasury_wallet"`
	USDCMintAddress  string               `json:"usdc_mint_address"`
}

// Configuration holds all necessary configuration for workflows and activities.
// It is intended to be populated inside activities to avoid non-deterministic behavior.
type Configuration struct {
	SolanaConfig           SolanaConfig           `json:"solana_config"`
	LLMConfig              LLMConfig              `json:"llm_config"`
	ImageLLMConfig         ImageLLMConfig         `json:"image_llm_config"`
	RedditDeps             RedditDependencies     `json:"reddit_deps"`
	YouTubeDeps            YouTubeDependencies    `json:"youtube_deps"`
	TwitchDeps             TwitchDependencies     `json:"twitch_deps"`
	HackerNewsDeps         HackerNewsDependencies `json:"hackernews_deps"`
	BlueskyDeps            BlueskyDependencies    `json:"bluesky_deps"`
	ServerURL              string                 `json:"server_url"`
	AuthToken              string                 `json:"auth_token"`
	Prompt                 string                 `json:"prompt"`
	ABBServerURL           string                 `json:"abb_server_url"`
	ABBServerSecretKey     string                 `json:"abb_server_secret_key"`
	PublishTargetSubreddit string                 `json:"publish_target_subreddit"`
	Environment            string                 `json:"environment"`
	RedditFlairID          string                 `json:"reddit_flair_id"`
	PublicBaseURL          string                 `json:"public_base_url"`
}

// Activities holds all activity implementations and their dependencies
// Dependencies are now passed via parameters or fetched from Configuration.
type Activities struct {
	httpClient *http.Client // Example shared, non-config dependency
}

// NewActivities creates a new Activities instance.
// Dependencies are no longer injected here.
func NewActivities() (*Activities, error) {
	// Initialize only non-config shared state if needed
	return &Activities{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// getConfiguration reads configuration from environment variables.
// NOTE: Storing private keys and API keys directly in env vars might not be secure for production.
// Consider using a secret management system.
func getConfiguration(ctx context.Context) (*Configuration, error) {
	logger := activity.GetLogger(ctx)

	// --- Solana Config ---
	escrowPrivateKeyStr := os.Getenv(EnvSolanaPrivateKey)
	escrowWalletStr := os.Getenv(EnvSolanaEscrowWallet)
	treasuryWalletStr := os.Getenv(EnvSolanaTreasuryWallet)
	usdcMintStr := os.Getenv(EnvSolanaUSDCMint)

	var solanaConfig SolanaConfig
	solanaConfig.RPCEndpoint = os.Getenv(EnvSolanaRPCURL)
	solanaConfig.WSEndpoint = os.Getenv(EnvSolanaWSEndpoint)
	solanaConfig.TreasuryWallet = treasuryWalletStr
	solanaConfig.USDCMintAddress = usdcMintStr

	escrowPrivateKey, err := solanago.PrivateKeyFromBase58(escrowPrivateKeyStr)
	if err != nil {
		logger.Error("Failed to parse escrow private key", "env_var", EnvSolanaPrivateKey, "error", err)
		// Decide if this should return error or proceed with nil key
		return nil, fmt.Errorf("failed to parse escrow private key: %w", err)
	}
	solanaConfig.EscrowPrivateKey = &escrowPrivateKey // Storing private key directly

	escrowWallet, err := solanago.PublicKeyFromBase58(escrowWalletStr)
	if err != nil {
		logger.Error("Failed to parse escrow wallet address", "env_var", EnvSolanaEscrowWallet, "error", err)
		return nil, fmt.Errorf("failed to parse escrow wallet address: %w", err)
	}
	solanaConfig.EscrowWallet = escrowWallet

	// Validate that the private key corresponds to the wallet address
	if !escrowPrivateKey.PublicKey().Equals(escrowWallet) {
		logger.Error("Escrow private key does not match escrow wallet address")
		return nil, fmt.Errorf("escrow private key does not match escrow wallet address")
	}

	// Validate treasury wallet address if provided
	if treasuryWalletStr != "" {
		if _, err := solanago.PublicKeyFromBase58(treasuryWalletStr); err != nil {
			logger.Error("Failed to parse treasury wallet address", "env_var", EnvSolanaTreasuryWallet, "error", err)
			// Decide policy: error out or warn?
			return nil, fmt.Errorf("failed to parse treasury wallet address: %w", err)
		}
	} else {
		logger.Warn("Treasury wallet not set in environment", "env_var", EnvSolanaTreasuryWallet)
	}

	// --- LLM Config ---
	llmProviderName := os.Getenv(EnvLLMProvider)
	llmModel := os.Getenv(EnvLLMModel)
	llmBasePromptFile := os.Getenv(EnvLLMBasePromptFile)

	// Determine API key based on provider
	var llmAPIKey string
	switch llmProviderName {
	case "openai":
		llmAPIKey = os.Getenv(EnvOpenAIAPIKey)
	case "gemini": // Assuming "gemini" is the identifier
		llmAPIKey = os.Getenv(EnvGeminiAPIKey)
	case "anthropic":
		llmAPIKey = os.Getenv(EnvAnthropicAPIKey)
	default:
		// Handle default case or unknown provider - maybe log a warning?
		logger.Warn("Unknown or unsupported LLM provider configured, API key may be missing", "provider", llmProviderName)
		// Optionally try a generic key or leave empty
		// llmAPIKey = os.Getenv("GENERIC_LLM_API_KEY")
	}

	if llmAPIKey == "" {
		logger.Warn("LLM API Key not found for configured provider", "provider", llmProviderName)
		// Decide if this is fatal or can proceed without API key (maybe for providers that don't need one?)
	}

	// --- Base Prompt Loading ---
	promptBase := os.Getenv(EnvLLMCheckReqPromptBase)
	if promptBase == "" {
		if llmBasePromptFile != "" {
			promptBytes, err := os.ReadFile(llmBasePromptFile)
			if err != nil {
				logger.Warn("Failed to read LLM base prompt file, using default", "file", llmBasePromptFile, "error", err)
				promptBase = DefaultLLMCheckReqPromptBase
			} else {
				promptBase = string(promptBytes)
			}
		} else {
			promptBase = DefaultLLMCheckReqPromptBase
		}
	}
	// Trim whitespace from the prompt
	promptBase = strings.TrimSpace(promptBase)
	// --- End Base Prompt Loading ---

	llmConfig := LLMConfig{
		Provider: llmProviderName,
		APIKey:   llmAPIKey, // Use the determined key
		Model:    llmModel,
	}

	// --- Platform Dependencies ---
	// Assuming RedditDependencies, etc. structs are defined elsewhere in the file
	redditDeps := RedditDependencies{
		UserAgent:    os.Getenv(EnvRedditUserAgent),
		Username:     os.Getenv(EnvRedditUsername),
		Password:     os.Getenv(EnvRedditPassword), // Sensitive
		ClientID:     os.Getenv(EnvRedditClientID),
		ClientSecret: os.Getenv(EnvRedditClientSecret), // Sensitive
	}

	youtubeDeps := YouTubeDependencies{
		APIKey:          os.Getenv(EnvYouTubeAPIKey), // Sensitive
		ApplicationName: os.Getenv(EnvYouTubeAppName),
	}

	// --- Twitch Deps ---
	twitchDeps := TwitchDependencies{
		ClientID:     os.Getenv(EnvTwitchClientID),
		ClientSecret: os.Getenv(EnvTwitchClientSecret), // Changed from App Access Token
	}
	if twitchDeps.ClientID == "" {
		logger.Warn("Twitch Client ID not found in environment", "env_var", EnvTwitchClientID)
	}
	if twitchDeps.ClientSecret == "" { // Changed check
		logger.Warn("Twitch Client Secret not found in environment", "env_var", EnvTwitchClientSecret)
	}

	// --- Other Config ---
	serverURL := os.Getenv(EnvServerURL)
	authToken := os.Getenv(EnvAuthToken) // Sensitive

	// --- Config for Publisher Activity ---
	abbServerURL := os.Getenv(EnvServerURL)
	abbServerSecretKey := os.Getenv(EnvServerSecretKey) // Sensitive
	targetSubreddit := os.Getenv(EnvTargetSubreddit)

	// Basic validation for publisher config
	if abbServerURL == "" {
		logger.Error("ABB Server URL not found in environment", "env_var", EnvServerURL)
		return nil, fmt.Errorf("missing env var: %s", EnvServerURL)
	}
	if abbServerSecretKey == "" {
		logger.Error("ABB Server Secret Key not found in environment", "env_var", EnvServerSecretKey)
		return nil, fmt.Errorf("missing env var: %s", EnvServerSecretKey)
	}
	if targetSubreddit == "" {
		logger.Error("Target Subreddit not found in environment", "env_var", EnvTargetSubreddit)
		return nil, fmt.Errorf("missing env var: %s", EnvTargetSubreddit)
	}

	// --- Get Environment ---
	environment := os.Getenv(EnvServerEnv)
	if environment == "" {
		environment = "dev" // Default to dev if not set
		logger.Warn("ENV environment variable not set, defaulting to 'dev'", "env_var", EnvServerEnv)
	}

	flair_id := os.Getenv(EnvRedditFlairID)
	if environment == "prod" && flair_id == "" {
		logger.Error("Reddit Prod Flair ID not found in environment", "env_var", EnvRedditFlairID)
		return nil, fmt.Errorf("missing env var: %s", EnvRedditFlairID)
	}

	// --- Get Public Base URL ---
	publicBaseURL := os.Getenv(EnvPublicBaseURL)
	if publicBaseURL == "" {
		logger.Error("Public Base URL not found in environment", "env_var", EnvPublicBaseURL)
		return nil, fmt.Errorf("missing required env var: %s", EnvPublicBaseURL)
	}
	// Ensure it has a scheme
	if !strings.HasPrefix(publicBaseURL, "http://") && !strings.HasPrefix(publicBaseURL, "https://") {
		logger.Error("Public Base URL must start with http:// or https://", "url", publicBaseURL)
		return nil, fmt.Errorf("invalid %s: must start with http:// or https://", EnvPublicBaseURL)
	}
	publicBaseURL = strings.TrimSuffix(publicBaseURL, "/") // Remove trailing slash if present

	// --- Image LLM Config --- (Assuming similar env vars)
	imageLLMConfig := ImageLLMConfig{
		Provider: os.Getenv(EnvLLMImageProvider),
		APIKey:   os.Getenv(EnvLLMImageAPIKey),
		Model:    os.Getenv(EnvLLMImageModel),
	}
	// Basic validation for Image LLM config (optional, depending on use cases)
	if imageLLMConfig.Provider != "" && imageLLMConfig.APIKey == "" {
		logger.Error("Image LLM API key not found in environment", "env_var", EnvLLMImageAPIKey)
		return nil, fmt.Errorf("missing env var for image LLM: %s", EnvLLMImageAPIKey)
	}

	// --- Assemble Configuration ---
	config := &Configuration{
		SolanaConfig:           solanaConfig,
		LLMConfig:              llmConfig,
		ImageLLMConfig:         imageLLMConfig, // Added Image LLM config
		RedditDeps:             redditDeps,
		YouTubeDeps:            youtubeDeps,
		TwitchDeps:             twitchDeps,
		HackerNewsDeps:         HackerNewsDependencies{},
		BlueskyDeps:            BlueskyDependencies{},
		ServerURL:              serverURL,
		AuthToken:              authToken,
		Prompt:                 promptBase,
		ABBServerURL:           abbServerURL,
		ABBServerSecretKey:     abbServerSecretKey,
		PublishTargetSubreddit: targetSubreddit,
		Environment:            environment,
		RedditFlairID:          flair_id,
		PublicBaseURL:          publicBaseURL, // Added
	}

	return config, nil
}

// --- Start of New Struct and Activity Definitions ---

// PullContentInput represents the input for the PullContentActivity
type PullContentInput struct {
	PlatformType PlatformKind
	ContentKind  ContentKind
	ContentID    string
}

// PullContentActivity fetches content from various platforms based on input.
// Implementation will consolidate logic from individual Pull<Platform>Content activities.
func (a *Activities) PullContentActivity(ctx context.Context, input PullContentInput) ([]byte, error) {
	// Implementation to be added in subsequent steps
	logger := activity.GetLogger(ctx)
	logger.Info("PullContentActivity executing", "platform", input.PlatformType, "contentID", input.ContentID)

	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration in PullContentActivity: %w", err)
	}

	var contentBytes []byte

	switch input.PlatformType {
	case PlatformReddit:
		// --- Start Reddit Logic (from activity_reddit.go/PullRedditContent) ---
		logger.Debug("Executing Reddit pull logic within PullContentActivity")
		redditDeps := cfg.RedditDeps
		contentID := input.ContentID
		switch input.ContentKind {
		case ContentKindPost:
			contentID = "t3_" + strings.TrimPrefix(contentID, "t3_")
		case ContentKindComment:
			contentID = "t1_" + strings.TrimPrefix(contentID, "t1_")
			// Add default case or error handling for unknown ContentKind if necessary
		}

		token, tokenErr := getRedditAuthTokenForPull(a.httpClient, redditDeps) // Assumes helper exists
		if tokenErr != nil {
			logger.Error("Failed to get Reddit token within PullContentActivity", "error", tokenErr)
			return nil, fmt.Errorf("failed to get Reddit token: %w", tokenErr)
		}
		if len(token) > 10 { // Added safety check for slicing
			logger.Debug("Using Reddit token", "token_prefix", token[:10]+"...")
		} else {
			logger.Debug("Using Reddit token", "token_prefix", token)
		}

		redditURL := fmt.Sprintf("https://oauth.reddit.com/api/info.json?id=%s", contentID)
		req, reqErr := http.NewRequestWithContext(ctx, "GET", redditURL, nil)
		if reqErr != nil {
			return nil, fmt.Errorf("failed to create reddit request: %w", reqErr)
		}
		req.Header.Set("User-Agent", redditDeps.UserAgent)
		req.Header.Set("Authorization", "Bearer "+token)

		logger.Info("Making Reddit API request in PullContentActivity", // Added logging
			"url", req.URL.String(),
			"user_agent", req.Header.Get("User-Agent"),
			"authorization_header", req.Header.Get("Authorization"))

		resp, httpErr := a.httpClient.Do(req)
		if httpErr != nil {
			// Add status code logging if possible
			statusCode := -1
			if resp != nil {
				statusCode = resp.StatusCode
			}
			logger.Error("Failed to make Reddit API request in PullContentActivity", "status_code", statusCode, "error", httpErr)
			return nil, fmt.Errorf("failed to make reddit request: %w", httpErr)
		}
		defer resp.Body.Close()

		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			logger.Error("Failed to read Reddit API response body in PullContentActivity", "status_code", resp.StatusCode, "error", readErr)
			return nil, fmt.Errorf("failed to read reddit response body (status %d): %w", resp.StatusCode, readErr)
		}
		logger.Debug("Reddit API Response in PullContentActivity", "status_code", resp.StatusCode, "response", string(body)) // Added logging

		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode == http.StatusForbidden {
				logger.Warn("Reddit API returned 403 Forbidden in PullContentActivity", "status_code", resp.StatusCode, "response_body", string(body))
			} else {
				logger.Error("Reddit API returned non-OK status in PullContentActivity", "status_code", resp.StatusCode, "response_body", string(body))
			}
			return nil, fmt.Errorf("Reddit API returned status %d: %s", resp.StatusCode, string(body))
		}

		var data interface{}
		if jsonErr := json.Unmarshal(body, &data); jsonErr != nil {
			return nil, fmt.Errorf("failed to decode reddit response: %w (body: %s)", jsonErr, string(body))
		}

		expression := "data.children[0].data"
		result, jmesErr := jmespath.Search(expression, data)
		if jmesErr != nil {
			return nil, fmt.Errorf("failed to extract data with JMESPath: %w", jmesErr)
		}
		if result == nil {
			logger.Warn("No content found in Reddit response for ID in PullContentActivity", "content_id", contentID, "full_response", string(body)) // Added logging
			return nil, fmt.Errorf("no content found for ID %s in reddit response", contentID)
		}

		resultJSON, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal JMESPath result: %w", marshalErr)
		}
		logger.Debug("Extracted Reddit Data in PullContentActivity", "data", string(resultJSON)) // Added logging

		var content RedditContent
		if unmarshalErr := json.Unmarshal(resultJSON, &content); unmarshalErr != nil {
			return nil, fmt.Errorf("failed to unmarshal reddit content: %w (extracted JSON: %s)", unmarshalErr, string(resultJSON))
		}
		content.IsComment = strings.HasPrefix(contentID, "t1_")
		contentBytes, err = json.Marshal(content) // Assign to outer contentBytes
		if err != nil {
			// Assign error to the outer err variable to be checked later
			logger.Error("Failed to marshal final Reddit content", "error", err)
			// Return immediately as marshalling the final result failed
			return nil, fmt.Errorf("failed to marshal final Reddit content: %w", err)
		}
		// --- End Reddit Logic ---

	case PlatformYouTube:
		// --- Start YouTube Logic (from activity_youtube.go/PullYouTubeContent) ---
		logger.Debug("Executing YouTube pull logic within PullContentActivity")
		ytDeps := cfg.YouTubeDeps
		videoID := strings.TrimPrefix(input.ContentID, "yt_") // Assuming ContentID is prefixed like "yt_VIDEOID"

		// Fetch metadata - assuming fetchYouTubeVideoMetadata is now internal/accessible
		videoData, metaErr := a.fetchYouTubeVideoMetadata(ctx, ytDeps, a.httpClient, videoID)
		if metaErr != nil {
			logger.Warn("Failed to fetch YouTube video metadata, attempting transcript only", "video_id", videoID, "error", metaErr)
			// Attempt to fetch transcript only - assuming FetchYouTubeTranscriptDirectly is accessible
			transcript, transcriptErr := a.FetchYouTubeTranscriptDirectly(ctx, a.httpClient, videoID, "en")
			if transcriptErr != nil {
				logger.Error("Failed to fetch YouTube transcript after metadata failure", "video_id", videoID, "transcript_error", transcriptErr, "metadata_error", metaErr)
				return nil, fmt.Errorf("failed to fetch metadata (%v) and transcript (%v)", metaErr, transcriptErr)
			}
			// Create content with only transcript
			content := &YouTubeContent{ID: videoID, Transcript: transcript}
			contentBytes, err = json.Marshal(content)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal minimal YouTube content: %w", err)
			}
		} else {
			// Populate content from metadata
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
			// Attempt to fetch transcript - assuming FetchYouTubeTranscriptDirectly is accessible
			transcript, transcriptErr := a.FetchYouTubeTranscriptDirectly(ctx, a.httpClient, videoID, "en")
			if transcriptErr != nil {
				logger.Warn("Failed to fetch YouTube transcript directly (metadata succeeded)", "video_id", videoID, "error", transcriptErr)
				// Proceed without transcript, maybe log?
			} else {
				content.Transcript = transcript
			}
			contentBytes, err = json.Marshal(content)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal full YouTube content: %w", err)
			}
		}
		// --- End YouTube Logic ---

	case PlatformTwitch:
		// --- Start Twitch Logic (from activity_twitch.go/PullTwitchContent) ---
		logger.Debug("Executing Twitch pull logic within PullContentActivity")
		twitchDeps := cfg.TwitchDeps
		// Get App Access Token - assuming getTwitchAppAccessToken is accessible
		token, tokenErr := a.getTwitchAppAccessToken(ctx, twitchDeps, a.httpClient)
		if tokenErr != nil {
			return nil, fmt.Errorf("failed to get Twitch app access token: %w", tokenErr)
		}

		var twitchContent interface{} // Use interface{} to hold either video or clip
		var fetchErr error

		// Determine if it's a video or clip based on ContentKind
		if input.ContentKind == ContentKindVideo {
			// Fetch Video data - assuming fetchTwitchVideo is accessible
			videoData, videoErr := a.fetchTwitchVideo(ctx, twitchDeps, a.httpClient, token, input.ContentID)
			if videoErr != nil {
				logger.Error("Failed to fetch Twitch video", "content_id", input.ContentID, "error", videoErr)
				fetchErr = fmt.Errorf("failed to fetch Twitch video %s: %w", input.ContentID, videoErr)
			} else {
				twitchContent = videoData
			}
		} else if input.ContentKind == ContentKindClip {
			// Fetch Clip data - assuming fetchTwitchClip is accessible
			clipData, clipErr := a.fetchTwitchClip(ctx, twitchDeps, a.httpClient, token, input.ContentID)
			if clipErr != nil {
				logger.Error("Failed to fetch Twitch clip", "content_id", input.ContentID, "error", clipErr)
				fetchErr = fmt.Errorf("failed to fetch Twitch clip %s: %w", input.ContentID, clipErr)
			} else {
				twitchContent = clipData
			}
		} else {
			// If ContentKind is empty or invalid, try fetching as video first, then clip as fallback
			logger.Warn("Twitch ContentKind not specified or invalid, attempting Video then Clip", "content_id", input.ContentID, "kind", input.ContentKind)
			videoData, videoErr := a.fetchTwitchVideo(ctx, twitchDeps, a.httpClient, token, input.ContentID)
			if videoErr == nil {
				twitchContent = videoData
			} else {
				logger.Warn("Failed to fetch Twitch as Video, trying as Clip", "content_id", input.ContentID, "video_error", videoErr)
				clipData, clipErr := a.fetchTwitchClip(ctx, twitchDeps, a.httpClient, token, input.ContentID)
				if clipErr != nil {
					logger.Error("Failed to fetch Twitch content as Video or Clip", "content_id", input.ContentID, "video_error", videoErr, "clip_error", clipErr)
					fetchErr = fmt.Errorf("failed to fetch Twitch content as Video (%v) or Clip (%v)", videoErr, clipErr)
				} else {
					twitchContent = clipData
				}
			}
		}

		if fetchErr != nil {
			return nil, fetchErr // Return error from fetching attempts
		}

		if twitchContent == nil {
			return nil, fmt.Errorf("twitchContent is nil after fetch attempts for %s", input.ContentID) // Should not happen if fetchErr is nil
		}

		contentBytes, err = json.Marshal(twitchContent)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Twitch content: %w", err)
		}
		// --- End Twitch Logic ---

	case PlatformHackerNews:
		// --- Start HackerNews Logic (from activity_hackernews.go/PullHackerNewsContent) ---
		logger.Debug("Executing HackerNews pull logic within PullContentActivity")
		itemURL := fmt.Sprintf("https://hacker-news.firebaseio.com/v0/item/%s.json", input.ContentID)
		req, reqErr := http.NewRequestWithContext(ctx, "GET", itemURL, nil)
		if reqErr != nil {
			return nil, fmt.Errorf("failed to create Hacker News request: %w", reqErr)
		}

		resp, httpErr := a.httpClient.Do(req)
		if httpErr != nil {
			return nil, fmt.Errorf("failed to make Hacker News request: %w", httpErr)
		}
		defer resp.Body.Close()

		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("failed to read Hacker News response body (status %d): %w", resp.StatusCode, readErr)
		}

		if resp.StatusCode != http.StatusOK {
			// Handle null response for invalid IDs gracefully
			if resp.StatusCode == http.StatusOK && string(body) == "null" {
				return nil, fmt.Errorf("no Hacker News item found for ID %s (API returned null)", input.ContentID)
			}
			return nil, fmt.Errorf("Hacker News API returned status %d: %s", resp.StatusCode, string(body))
		}
		// Also check for null after status OK
		if string(body) == "null" {
			return nil, fmt.Errorf("no Hacker News item found for ID %s (API returned null)", input.ContentID)
		}

		var hnContent HackerNewsContent
		if unmarshalErr := json.Unmarshal(body, &hnContent); unmarshalErr != nil {
			return nil, fmt.Errorf("failed to unmarshal Hacker News content: %w (body: %s)", unmarshalErr, string(body))
		}

		// Validate type based on ContentKind (optional but good practice)
		if (input.ContentKind == ContentKindPost && !(hnContent.Type == "story" || hnContent.Type == "job" || hnContent.Type == "poll")) ||
			(input.ContentKind == ContentKindComment && hnContent.Type != "comment") {
			logger.Warn("HackerNews item type mismatch", "expected_kind", input.ContentKind, "actual_type", hnContent.Type, "id", input.ContentID)
			// Decide whether to return error or just proceed
			// return nil, fmt.Errorf("HackerNews item type '%s' does not match expected kind '%s' for ID %s", hnContent.Type, input.ContentKind, input.ContentID)
		}

		contentBytes, err = json.Marshal(hnContent)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal HackerNews content: %w", err)
		}
		// --- End HackerNews Logic ---

	case PlatformBluesky:
		// --- Start Bluesky Logic (incorporating ResolveBlueskyURLToATURI and PullBlueskyContent) ---
		logger.Debug("Executing Bluesky pull logic within PullContentActivity")
		contentIdToUse := input.ContentID
		isURL := strings.HasPrefix(input.ContentID, "http://") || strings.HasPrefix(input.ContentID, "https://")

		// 1. Resolve URL to AT URI if necessary (logic from former ResolveBlueskyURLToATURI activity)
		if isURL {
			logger.Info("Bluesky ContentID is an HTTP URL, resolving to AT URI in PullContentActivity", "url", input.ContentID)
			parsedURL, parseErr := url.Parse(input.ContentID)
			if parseErr != nil {
				return nil, fmt.Errorf("failed to parse Bluesky URL '%s': %w", input.ContentID, parseErr)
			}
			pathParts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
			if len(pathParts) != 4 || pathParts[0] != "profile" || pathParts[2] != "post" {
				return nil, fmt.Errorf("invalid Bluesky URL path format: %s. Expected /profile/{handle}/post/{rkey}", parsedURL.Path)
			}
			handle := pathParts[1]
			rkey := pathParts[3]
			if handle == "" || rkey == "" {
				return nil, fmt.Errorf("could not extract handle or rkey from URL '%s'", input.ContentID)
			}

			// Resolve handle to DID
			resolveHandleURL := fmt.Sprintf("https://public.api.bsky.app/xrpc/com.atproto.identity.resolveHandle?handle=%s", url.QueryEscape(handle))
			resolveReq, resolveReqErr := http.NewRequestWithContext(ctx, "GET", resolveHandleURL, nil)
			if resolveReqErr != nil {
				return nil, fmt.Errorf("failed to create Bluesky DID resolution request: %w", resolveReqErr)
			}
			resolveReq.Header.Set("Accept", "application/json")

			resolveResp, resolveHttpErr := a.httpClient.Do(resolveReq)
			if resolveHttpErr != nil {
				return nil, fmt.Errorf("failed to resolve Bluesky handle '%s': %w", handle, resolveHttpErr)
			}
			defer resolveResp.Body.Close()
			resolveBody, resolveReadErr := io.ReadAll(resolveResp.Body)
			if resolveReadErr != nil {
				return nil, fmt.Errorf("failed to read Bluesky DID resolution response body for handle '%s': %w", handle, resolveReadErr)
			}
			if resolveResp.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("failed to resolve Bluesky handle '%s', status %d: %s", handle, resolveResp.StatusCode, string(resolveBody))
			}
			var handleResponse BlueskyHandleResponse // Assumes BlueskyHandleResponse struct is defined/accessible
			if jsonErr := json.Unmarshal(resolveBody, &handleResponse); jsonErr != nil {
				return nil, fmt.Errorf("failed to unmarshal Bluesky DID for handle '%s': %w", handle, jsonErr)
			}
			if handleResponse.DID == "" {
				return nil, fmt.Errorf("resolved Bluesky DID for handle '%s' is empty", handle)
			}
			contentIdToUse = fmt.Sprintf("at://%s/app.bsky.feed.post/%s", handleResponse.DID, rkey)
			logger.Info("Resolved Bluesky URL to AT URI in PullContentActivity", "url", input.ContentID, "atURI", contentIdToUse)
		} else if !strings.HasPrefix(input.ContentID, "at://") {
			return nil, fmt.Errorf("invalid Bluesky ContentID format: expected HTTP URL or AT URI, got '%s'", input.ContentID)
		}

		// 2. Fetch Post using AT URI (logic from former PullBlueskyContent activity)
		apiURL := "https://public.api.bsky.app/xrpc/app.bsky.feed.getPosts"
		params := url.Values{}
		params.Add("uris", contentIdToUse)
		fullURL := apiURL + "?" + params.Encode()

		req, reqErr := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
		if reqErr != nil {
			return nil, fmt.Errorf("failed to create Bluesky getPosts request: %w", reqErr)
		}
		req.Header.Set("Accept", "application/json")

		resp, httpErr := a.httpClient.Do(req)
		if httpErr != nil {
			return nil, fmt.Errorf("failed to make Bluesky getPosts request: %w", httpErr)
		}
		defer resp.Body.Close()

		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("failed to read Bluesky getPosts response body (status %d): %w", resp.StatusCode, readErr)
		}
		logger.Debug("Bluesky API Response in PullContentActivity", "status_code", resp.StatusCode, "response", string(body))

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Bluesky getPosts API returned status %d: %s", resp.StatusCode, string(body))
		}

		var responseData struct {
			Posts []json.RawMessage `json:"posts"`
		} // Assumes BlueskyContent related structs are defined/accessible
		if jsonErr := json.Unmarshal(body, &responseData); jsonErr != nil {
			return nil, fmt.Errorf("failed to decode Bluesky getPosts response: %w (body: %s)", jsonErr, string(body))
		}
		if len(responseData.Posts) == 0 {
			return nil, fmt.Errorf("no Bluesky post found for URI %s", contentIdToUse)
		}

		// Unmarshal the actual post view
		var content BlueskyContent // Assumes BlueskyContent struct is defined/accessible
		postItemJSON := responseData.Posts[0]
		if jsonErr := json.Unmarshal(postItemJSON, &content); jsonErr != nil {
			return nil, fmt.Errorf("failed to unmarshal Bluesky post view content: %w (json: %s)", jsonErr, string(postItemJSON))
		}

		// Extract text from record
		var postRecord struct {
			Text string `json:"text"`
		}
		if jsonErr := json.Unmarshal(content.Record, &postRecord); jsonErr != nil {
			logger.Warn("Failed to unmarshal Bluesky post record to extract text", "uri", content.Uri, "error", jsonErr)
		} else {
			content.Text = postRecord.Text
		}

		contentBytes, err = json.Marshal(content)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Bluesky content: %w", err)
		}
		// --- End Bluesky Logic ---

	default:
		err = fmt.Errorf("unsupported platform type in PullContentActivity: %s", input.PlatformType)
	}

	// Check for errors assigned within the switch cases or default
	if err != nil {
		return nil, err // Return the specific error from the platform logic or default case
	}

	// Final check before returning (Ensure contentBytes is populated)
	if contentBytes == nil {
		// This case should ideally not be reached if errors are handled properly above
		logger.Error("contentBytes is unexpectedly nil after switch without error", "platform", input.PlatformType)
		return nil, fmt.Errorf("internal error: contentBytes is nil after processing platform %s without error", input.PlatformType)
	}

	logger.Info("PullContentActivity finished successfully", "platform", input.PlatformType, "contentID", input.ContentID)
	return contentBytes, nil
}

// --- End of New Struct and Activity Definitions ---

// --- Helper function stubs (assuming these are moved/inlined into PullContentActivity or kept private) ---

// Placeholder for Twitch clip fetch logic (previously method on Activities)
func (a *Activities) fetchTwitchClip(ctx context.Context, deps TwitchDependencies, client *http.Client, token, clipID string) (*TwitchClipContent, error) {
	// --- Logic from activity_twitch.go/fetchTwitchClip ---
	logger := activity.GetLogger(ctx)
	apiURL := fmt.Sprintf("https://api.twitch.tv/helix/clips?id=%s", clipID) // Use clip ID

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Twitch clip request: %w", err)
	}
	req.Header.Add("Client-ID", deps.ClientID)
	req.Header.Add("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Twitch clip request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Twitch clip response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("Twitch clip request failed", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("Twitch clip request returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []TwitchClipContent `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode Twitch clip response: %w (body: %s)", err, string(body))
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no Twitch clip found for ID %s", clipID)
	}

	logger.Info("Successfully fetched Twitch clip", "id", clipID)
	return &result.Data[0], nil
}

// Placeholder for Twitch auth token logic (previously global func, now method)
func (a *Activities) getTwitchAppAccessToken(ctx context.Context, deps TwitchDependencies, client *http.Client) (string, error) {
	// --- Logic from activity_twitch.go/getTwitchAppAccessToken ---
	logger := activity.GetLogger(ctx)
	logger.Info("Requesting new Twitch App Access Token via internal helper")
	tokenURL := "https://id.twitch.tv/oauth2/token"
	data := url.Values{}
	data.Set("client_id", deps.ClientID)
	data.Set("client_secret", deps.ClientSecret)
	data.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create Twitch token request: %w", err)
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Twitch token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Twitch token response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("Twitch token request failed", "status", resp.StatusCode, "body", string(body))
		return "", fmt.Errorf("Twitch token request returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to decode Twitch token response: %w", err)
	}

	if result.AccessToken == "" {
		return "", fmt.Errorf("Twitch token response did not contain an access token")
	}

	logger.Debug("Successfully obtained Twitch App Access Token via internal helper")
	return result.AccessToken, nil
}

// Placeholder for Twitch video fetch logic (previously method on Activities)
func (a *Activities) fetchTwitchVideo(ctx context.Context, deps TwitchDependencies, client *http.Client, token, videoID string) (*TwitchVideoContent, error) {
	// --- Logic from activity_twitch.go/fetchTwitchVideo ---
	logger := activity.GetLogger(ctx)
	apiURL := fmt.Sprintf("https://api.twitch.tv/helix/videos?id=%s", videoID)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Twitch video request: %w", err)
	}
	req.Header.Add("Client-ID", deps.ClientID)
	req.Header.Add("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Twitch video request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Twitch video response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("Twitch video request failed", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("Twitch video request returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []TwitchVideoContent `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to decode Twitch video response: %w (body: %s)", err, string(body))
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no Twitch video found for ID %s", videoID)
	}

	logger.Info("Successfully fetched Twitch video", "id", videoID)
	return &result.Data[0], nil
}

// --- End Helper function stubs ---
