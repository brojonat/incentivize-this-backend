package abb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/jmespath/go-jmespath"
	"go.temporal.io/sdk/activity"
)

// Environment Variable Keys for Configuration
const (
	EnvABBServerEnv     = "ENV"
	EnvABBAPIEndpoint   = "ABB_API_ENDPOINT"
	EnvABBSecretKey     = "ABB_SECRET_KEY"
	EnvABBAuthToken     = "ABB_AUTH_TOKEN"
	EnvABBPublicBaseURL = "ABB_PUBLIC_BASE_URL"
	EnvABBDatabaseURL   = "ABB_DATABASE_URL"

	EnvLLMAPIKey         = "LLM_API_KEY"
	EnvLLMProvider       = "LLM_PROVIDER"
	EnvLLMModel          = "LLM_MODEL"
	EnvLLMMaxTokens      = "LLM_MAX_TOKENS"
	EnvLLMBasePromptFile = "LLM_BASE_PROMPT_FILE"
	EnvLLMEmbeddingModel = "LLM_EMBEDDING_MODEL"

	EnvSolanaRPCEndpoint      = "SOLANA_RPC_ENDPOINT"
	EnvSolanaWSEndpoint       = "SOLANA_WS_ENDPOINT"
	EnvSolanaEscrowPrivateKey = "SOLANA_ESCROW_PRIVATE_KEY" // Base58 encoded private key for escrow
	EnvSolanaTreasuryWallet   = "SOLANA_TREASURY_WALLET"    // Base58 public key
	EnvSolanaEscrowWallet     = "SOLANA_ESCROW_WALLET"      // Base58 public key (can be same as Treasury)
	EnvSolanaUSDCMint         = "SOLANA_USDC_MINT_ADDRESS"  // Base58 public key of USDC mint

	EnvEmailSender   = "EMAIL_SENDER"
	EnvEmailPassword = "EMAIL_PASSWORD"
	EnvEmailSMTPHost = "EMAIL_SMTP_HOST"
	EnvEmailSMTPPort = "EMAIL_SMTP_PORT"

	EnvRedditFlairID          = "REDDIT_FLAIR_ID"
	EnvRedditClientID         = "REDDIT_CLIENT_ID"
	EnvRedditClientSecret     = "REDDIT_CLIENT_SECRET"
	EnvRedditUsername         = "REDDIT_USERNAME"
	EnvRedditPassword         = "REDDIT_PASSWORD"
	EnvRedditUserAgent        = "REDDIT_USER_AGENT"
	EnvRedditPublishSubreddit = "REDDIT_PUBLISH_SUBREDDIT"

	EnvYouTubeAppName     = "YOUTUBE_APP_NAME"
	EnvYouTubeAPIKey      = "YOUTUBE_API_KEY"
	EnvTwitchClientID     = "TWITCH_CLIENT_ID"
	EnvTwitchClientSecret = "TWITCH_CLIENT_SECRET"

	EnvLLMImageProvider           = "LLM_IMAGE_PROVIDER"
	EnvLLMImageAPIKey             = "LLM_IMAGE_API_KEY"
	EnvLLMImageModel              = "LLM_IMAGE_MODEL"
	EnvLLMCheckReqPromptBase      = "LLM_CHECK_REQ_PROMPT_BASE_B64"
	EnvLLMImageAnalysisPromptBase = "LLM_IMAGE_ANALYSIS_PROMPT_BASE_B64"

	DefaultLLMCheckReqPromptBase = `You are an AI assistant evaluating content based on a set of requirements.
Determine if the provided content satisfies ALL the given requirements.
Respond with a JSON object: {"satisfies": boolean, "reason": "string explaining why or why not"}.
Content will be provided after "CONTENT:", and requirements after "REQUIREMENTS:".`

	DefaultLLMImageAnalysisPromptBase = `You are an AI assistant evaluating an image based on a set of requirements.
Your task is to determine if the provided image (referred to by its URL) *visually violates* any of the given requirements that pertain to visual aspects.
- If the image *clearly violates* one or more visual requirements, respond with {"satisfies": false, "reason": "Explain which requirement(s) are violated and why."}.
- If the image *does not clearly violate* any visual requirements, or if the requirements cannot be assessed from the image alone (e.g., a requirement about view count, which is not typically visible in a thumbnail), respond with {"satisfies": true, "reason": "The image does not visually violate any of the specified requirements, or the requirements cannot be assessed from the image alone."}.
Respond with a JSON object: {"satisfies": boolean, "reason": "string explaining your assessment based on the rules above"}.
Requirements will be provided after "REQUIREMENTS:". Image URL will be part of the user message context or explicitly passed.`

	DefaultShouldPerformImageAnalysisPromptBase = `You are an AI assistant determining if a set of bounty requirements necessitates visual image/thumbnail analysis.
Based on the following requirements, respond with a JSON object: {"should_analyze": boolean, "reason": "string explaining why (e.g., specific requirement found) or why not (e.g., no visual criteria found)"}.
If requirements mention aspects like 'thumbnail content', 'image appropriateness', 'visual relevance', 'no NSFW images', etc., then analysis is likely needed.
If requirements only pertain to text, links, engagement metrics, or other non-visual aspects, then analysis is likely not needed.
Requirements will be provided after "REQUIREMENTS:".`

	MaxRequirementsCharsForLLMCheck = 5000
	MaxContentCharsForLLMCheck      = 80000
	DefaultLLMMaxTokens             = 10000 // Default max tokens if not set
)

// --- Start Platform Agnostic Content Structures ---

// ContentKind defines the type of content (e.g., post, comment, video)
type ContentKind string

// PlatformKind defines the social media platform
type PlatformKind string

const (
	PlatformReddit     PlatformKind = "reddit"
	PlatformYouTube    PlatformKind = "youtube"
	PlatformTwitch     PlatformKind = "twitch"
	PlatformHackerNews PlatformKind = "hackernews"
	PlatformBluesky    PlatformKind = "bluesky"

	ContentKindPost    ContentKind = "post"
	ContentKindComment ContentKind = "comment"
	ContentKindVideo   ContentKind = "video"
	ContentKindClip    ContentKind = "clip"
	ContentKindStory   ContentKind = "story"
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

type AbbServerConfig struct {
	APIEndpoint       string `json:"api_endpoint"`
	SecretKey         string `json:"secret_key"`
	AuthToken         string `json:"auth_token"`
	PublicBaseURL     string `json:"public_base_url"`
	LLMEmbeddingModel string `json:"llm_embedding_model"`
	DatabaseURL       string `json:"database_url"`
}

// Configuration holds all necessary configuration for workflows and activities.
// It is intended to be populated inside activities to avoid non-deterministic behavior.
type Configuration struct {
	ABBServerConfig        AbbServerConfig        `json:"abb_server_config"`
	SolanaConfig           SolanaConfig           `json:"solana_config"`
	LLMConfig              LLMConfig              `json:"llm_config"`
	ImageLLMConfig         ImageLLMConfig         `json:"image_llm_config"`
	EmbeddingConfig        EmbeddingConfig        `json:"embedding_config"`
	RedditDeps             RedditDependencies     `json:"reddit_deps"`
	YouTubeDeps            YouTubeDependencies    `json:"youtube_deps"`
	TwitchDeps             TwitchDependencies     `json:"twitch_deps"`
	HackerNewsDeps         HackerNewsDependencies `json:"hackernews_deps"`
	BlueskyDeps            BlueskyDependencies    `json:"bluesky_deps"`
	Prompt                 string                 `json:"prompt"`
	PublishTargetSubreddit string                 `json:"publish_target_subreddit"`
	Environment            string                 `json:"environment"`
	RedditFlairID          string                 `json:"reddit_flair_id"`
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
	// Use "ENV" environment variable directly
	currentEnv := os.Getenv("ENV")

	var solanaConfig SolanaConfig

	if currentEnv == "test" {
		logger.Info("ENV is 'test', using dummy Solana configuration.")
		dummyEscrowWallet := solanago.NewWallet()
		solanaConfig.RPCEndpoint = "https://api.devnet.solana.com"                    // Default to devnet for local/test
		solanaConfig.WSEndpoint = "wss://api.devnet.solana.com"                       // Default to devnet for local/test
		solanaConfig.EscrowPrivateKey = &dummyEscrowWallet.PrivateKey                 // Dummy key
		solanaConfig.EscrowWallet = dummyEscrowWallet.PublicKey()                     // Corresponding dummy pubkey
		solanaConfig.USDCMintAddress = "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v" // Common USDC mint
		dummyTreasury := solanago.NewWallet()                                         // Dummy treasury
		solanaConfig.TreasuryWallet = dummyTreasury.PublicKey().String()
	} else {
		// Production, development, or other modes that require real keys from env
		escrowPrivateKeyStr := os.Getenv(EnvSolanaEscrowPrivateKey)
		escrowWalletStr := os.Getenv(EnvSolanaEscrowWallet)
		treasuryWalletStr := os.Getenv(EnvSolanaTreasuryWallet) // Can be optional
		usdcMintStr := os.Getenv(EnvSolanaUSDCMint)
		rpcURL := os.Getenv(EnvSolanaRPCEndpoint)
		wsURL := os.Getenv(EnvSolanaWSEndpoint) // Can be optional or default

		if escrowPrivateKeyStr == "" {
			logger.Error("Escrow private key not set in environment", "env_var", EnvSolanaEscrowPrivateKey, "current_env", currentEnv)
			return nil, fmt.Errorf("%s must be set for ENV '%s'", EnvSolanaEscrowPrivateKey, currentEnv)
		}
		if escrowWalletStr == "" {
			logger.Error("Escrow wallet address not set in environment", "env_var", EnvSolanaEscrowWallet, "current_env", currentEnv)
			return nil, fmt.Errorf("%s must be set for ENV '%s'", EnvSolanaEscrowWallet, currentEnv)
		}
		if usdcMintStr == "" {
			logger.Error("USDC Mint address not set in environment", "env_var", EnvSolanaUSDCMint, "current_env", currentEnv)
			return nil, fmt.Errorf("%s must be set for ENV '%s'", EnvSolanaUSDCMint, currentEnv)
		}
		if rpcURL == "" {
			logger.Error("Solana RPC endpoint not set in environment", "env_var", EnvSolanaRPCEndpoint, "current_env", currentEnv)
			return nil, fmt.Errorf("%s must be set for ENV '%s'", EnvSolanaRPCEndpoint, currentEnv)
		}

		solanaConfig.RPCEndpoint = rpcURL
		solanaConfig.WSEndpoint = wsURL // If empty, downstream functions might default or fail
		solanaConfig.TreasuryWallet = treasuryWalletStr
		solanaConfig.USDCMintAddress = usdcMintStr

		escrowPrivateKey, err := solanago.PrivateKeyFromBase58(escrowPrivateKeyStr)
		if err != nil {
			logger.Error("Failed to parse escrow private key", "env_var", EnvSolanaEscrowPrivateKey, "error", err)
			return nil, fmt.Errorf("failed to parse escrow private key: %w", err)
		}
		solanaConfig.EscrowPrivateKey = &escrowPrivateKey

		escrowWallet, err := solanago.PublicKeyFromBase58(escrowWalletStr)
		if err != nil {
			logger.Error("Failed to parse escrow wallet address", "env_var", EnvSolanaEscrowWallet, "error", err)
			return nil, fmt.Errorf("failed to parse escrow wallet address: %w", err)
		}
		solanaConfig.EscrowWallet = escrowWallet

		if !escrowPrivateKey.PublicKey().Equals(escrowWallet) {
			logger.Error("Escrow private key does not match escrow wallet address")
			return nil, fmt.Errorf("escrow private key does not match escrow wallet address")
		}

		if treasuryWalletStr != "" {
			if _, err := solanago.PublicKeyFromBase58(treasuryWalletStr); err != nil {
				logger.Error("Failed to parse treasury wallet address", "env_var", EnvSolanaTreasuryWallet, "error", err)
				return nil, fmt.Errorf("failed to parse treasury wallet address: %w", err)
			}
		} else {
			logger.Info("Treasury wallet not set in environment (optional)", "env_var", EnvSolanaTreasuryWallet)
		}
	}

	// --- LLM Config ---
	llmProviderName := os.Getenv(EnvLLMProvider)
	llmModelName := os.Getenv(EnvLLMModel)
	llmBasePromptFile := os.Getenv(EnvLLMBasePromptFile)
	llmAPIKey := os.Getenv(EnvLLMAPIKey)

	if llmAPIKey == "" && llmProviderName != "" {
		return nil, fmt.Errorf("LLM API Key not found for configured provider %s", llmProviderName)
	}

	maxTokensStr := os.Getenv(EnvLLMMaxTokens)
	maxTokens := DefaultLLMMaxTokens
	if maxTokensStr != "" {
		parsedMaxTokens, err := strconv.Atoi(maxTokensStr)
		if err == nil && parsedMaxTokens > 0 {
			maxTokens = parsedMaxTokens
		} else {
			logger.Warn("Invalid LLM_MAX_TOKENS value, using default", "value", maxTokensStr, "default", DefaultLLMMaxTokens, "error", err)
		}
	}
	llmConfig := LLMConfig{
		Provider:  llmProviderName,
		APIKey:    llmAPIKey,
		Model:     llmModelName,
		MaxTokens: maxTokens,
	}

	// --- Image LLM Config ---
	imageLLMConfig := ImageLLMConfig{
		Provider: os.Getenv(EnvLLMImageProvider),
		APIKey:   os.Getenv(EnvLLMImageAPIKey),
		Model:    os.Getenv(EnvLLMImageModel),
		// BasePrompt is set belo
	}

	// --- Embedding Config ---
	embeddingConfig := EmbeddingConfig{
		Provider: os.Getenv(EnvLLMProvider),
		APIKey:   os.Getenv(EnvLLMAPIKey),
		Model:    os.Getenv(EnvLLMEmbeddingModel),
	}

	// --- Base Prompt Loading (for CheckContentRequirements) ---
	promptBaseB64 := os.Getenv(EnvLLMCheckReqPromptBase)
	var promptBase string
	if promptBaseB64 != "" {
		decoded, err := decodeBase64(promptBaseB64)
		if err != nil {
			logger.Warn("Failed to decode LLM_CHECK_REQ_PROMPT_BASE_B64, using default", "error", err)
			promptBase = DefaultLLMCheckReqPromptBase
		} else {
			promptBase = decoded
		}
	} else if llmBasePromptFile != "" {
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
	llmConfig.BasePrompt = strings.TrimSpace(promptBase)

	// --- Base Prompt Loading (for AnalyzeImageURL) ---
	imagePromptBaseB64 := os.Getenv(EnvLLMImageAnalysisPromptBase)
	var imagePromptBase string
	if imagePromptBaseB64 != "" {
		decoded, err := decodeBase64(imagePromptBaseB64)
		if err != nil {
			logger.Warn("Failed to decode LLM_IMAGE_ANALYSIS_PROMPT_BASE_B64, using default", "error", err)
			imagePromptBase = DefaultLLMImageAnalysisPromptBase
		} else {
			imagePromptBase = decoded
		}
	} else {
		// No file fallback for image prompt base in this example, directly use default.
		imagePromptBase = DefaultLLMImageAnalysisPromptBase
	}
	imageLLMConfig.BasePrompt = strings.TrimSpace(imagePromptBase)

	// --- Reddit Dependencies ---
	redditDeps := RedditDependencies{
		ClientID:     os.Getenv(EnvRedditClientID),
		ClientSecret: os.Getenv(EnvRedditClientSecret),
		Username:     os.Getenv(EnvRedditUsername),
		Password:     os.Getenv(EnvRedditPassword),
		UserAgent:    os.Getenv(EnvRedditUserAgent),
	}
	targetSubreddit := os.Getenv(EnvRedditPublishSubreddit)
	flairID := os.Getenv(EnvRedditFlairID)

	// --- YouTube Dependencies ---
	youtubeDeps := YouTubeDependencies{
		APIKey: os.Getenv(EnvYouTubeAPIKey),
		// ApplicationName can be hardcoded or also from env if needed
		ApplicationName: os.Getenv(EnvYouTubeAppName), // Use defined constant
		MaxResults:      10,                           // Default, can be made configurable
	}

	// --- Twitch Dependencies ---
	twitchDeps := TwitchDependencies{
		ClientID:     os.Getenv(EnvTwitchClientID),
		ClientSecret: os.Getenv(EnvTwitchClientSecret),
	}

	// --- Bluesky Dependencies ---
	blueskyDeps := BlueskyDependencies{
		PDS: os.Getenv("BLUESKY_PDS_URL"), // Example: "https://bsky.social"
	}

	// --- ABB Server Config ---
	abbServerConfig := AbbServerConfig{
		APIEndpoint:       os.Getenv(EnvABBAPIEndpoint),
		SecretKey:         os.Getenv(EnvABBSecretKey),
		AuthToken:         os.Getenv(EnvABBAuthToken),
		PublicBaseURL:     os.Getenv(EnvABBPublicBaseURL),
		LLMEmbeddingModel: os.Getenv(EnvLLMEmbeddingModel),
		DatabaseURL:       os.Getenv(EnvABBDatabaseURL),
	}

	environmentToStore := currentEnv
	if environmentToStore == "" {
		environmentToStore = "development"
		logger.Info("ENV was not set, inferred as 'development' for config field.")
	}

	config := &Configuration{
		ABBServerConfig:        abbServerConfig,
		SolanaConfig:           solanaConfig,
		LLMConfig:              llmConfig,
		ImageLLMConfig:         imageLLMConfig,
		EmbeddingConfig:        embeddingConfig,
		RedditDeps:             redditDeps,
		YouTubeDeps:            youtubeDeps,
		TwitchDeps:             twitchDeps,
		HackerNewsDeps:         HackerNewsDependencies{},
		BlueskyDeps:            blueskyDeps,
		Prompt:                 llmConfig.BasePrompt,
		PublishTargetSubreddit: targetSubreddit,
		Environment:            environmentToStore,
		RedditFlairID:          flairID,
	}
	return config, nil
}

// decodeBase64 is a helper to decode base64 strings, used for prompts.
func decodeBase64(s string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(data), nil
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
