package abb

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"encoding/base64"

	solanago "github.com/gagliardetto/solana-go"
	"go.temporal.io/sdk/activity"
)

// PlatformKind represents the type of platform
type PlatformKind string
type ContentKind string

const (
	ContentKindComment ContentKind = "comment"
	ContentKindPost    ContentKind = "post"
	ContentKindVideo   ContentKind = "video"
	ContentKindClip    ContentKind = "clip"
	ContentKindText    ContentKind = "text"
	ContentKindImage   ContentKind = "image"
	ContentKindAudio   ContentKind = "gif"

	// PlatformReddit represents the Reddit platform
	PlatformReddit PlatformKind = "reddit"
	// PlatformYouTube represents the YouTube platform
	PlatformYouTube PlatformKind = "youtube"
	// PlatformTwitch represents the Twitch platform
	PlatformTwitch PlatformKind = "twitch"
	// PlatformHackerNews represents the Hacker News platform
	PlatformHackerNews PlatformKind = "hackernews"
	// PlatformBluesky represents the Bluesky platform
	PlatformBluesky PlatformKind = "bluesky"

	// Environment Variable Keys for Activities
	EnvSolanaEscrowPrivateKey = "SOLANA_ESCROW_PRIVATE_KEY"
	EnvSolanaEscrowWallet     = "SOLANA_ESCROW_WALLET"
	EnvSolanaTreasuryWallet   = "SOLANA_TREASURY_WALLET"
	EnvSolanaUSDCMintAddress  = "SOLANA_USDC_MINT_ADDRESS"
	EnvSolanaRPCEndpoint      = "SOLANA_RPC_ENDPOINT"
	EnvSolanaWSEndpoint       = "SOLANA_WS_ENDPOINT"

	EnvLLMProvider = "LLM_PROVIDER"
	EnvLLMAPIKey   = "LLM_API_KEY"
	EnvLLMModel    = "LLM_MODEL"

	EnvLLMImageProvider = "LLM_IMAGE_PROVIDER"
	EnvLLMImageAPIKey   = "LLM_IMAGE_API_KEY"
	EnvLLMImageModel    = "LLM_IMAGE_MODEL"

	EnvRedditUserAgent    = "REDDIT_USER_AGENT"
	EnvRedditUsername     = "REDDIT_USERNAME"
	EnvRedditPassword     = "REDDIT_PASSWORD"
	EnvRedditClientID     = "REDDIT_CLIENT_ID"
	EnvRedditClientSecret = "REDDIT_CLIENT_SECRET"
	EnvRedditFlairID      = "REDDIT_FLAIR_ID"

	EnvYouTubeAPIKey  = "YOUTUBE_API_KEY"
	EnvYouTubeAppName = "YOUTUBE_APP_NAME"

	EnvTwitchClientID     = "TWITCH_CLIENT_ID"     // Added Twitch Client ID env var
	EnvTwitchClientSecret = "TWITCH_CLIENT_SECRET" // Changed from App Access Token

	EnvServerURL       = "SERVER_ENDPOINT"
	EnvAuthToken       = "AUTH_TOKEN"
	EnvServerSecretKey = "SERVER_SECRET_KEY"

	EnvLLMCheckReqPromptBase = "LLM_CHECK_REQ_PROMPT_BASE"

	// Environment setting
	EnvServerEnv = "ENV" // e.g., "dev", "prod"
	// Public facing URL base
	EnvPublicBaseURL = "PUBLIC_BASE_URL" // e.g., https://incentivizethis.com

	// Env vars for periodic bounty publisher activity
	EnvTargetSubreddit                   = "PUBLISH_TARGET_SUBREDDIT"
	EnvPeriodicPublisherScheduleID       = "PERIODIC_PUBLISHER_SCHEDULE_ID"
	EnvPeriodicPublisherScheduleInterval = "PERIODIC_PUBLISHER_SCHEDULE_INTERVAL"

	// Env vars for email sending activity
	EnvEmailSMTP     = "GMAIL_SMTP_HOST"
	EnvEmailSMTPPort = "GMAIL_SMTP_PORT"
	EnvEmailPassword = "GMAIL_EMAILER_PWD"
	EnvEmailSender   = "GMAIL_EMAILER_ADDR"

	// LLM Check Content Requirements
	MaxContentCharsForLLMCheck      = 20000 // Approx 5k tokens
	MaxRequirementsCharsForLLMCheck = 5000  // Approx 1.25k tokens
	DefaultLLMCheckReqPromptBase    = `You are a content verification system. Your task is to determine if the given content satisfies the specified requirements.

The content to evaluate is provided as a JSON object below.`
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
	escrowPrivateKeyStr := os.Getenv(EnvSolanaEscrowPrivateKey)
	escrowWalletStr := os.Getenv(EnvSolanaEscrowWallet)
	treasuryWalletStr := os.Getenv(EnvSolanaTreasuryWallet)
	usdcMintStr := os.Getenv(EnvSolanaUSDCMintAddress)

	var solanaConfig SolanaConfig
	solanaConfig.RPCEndpoint = os.Getenv(EnvSolanaRPCEndpoint)
	solanaConfig.WSEndpoint = os.Getenv(EnvSolanaWSEndpoint)
	solanaConfig.TreasuryWallet = treasuryWalletStr
	solanaConfig.USDCMintAddress = usdcMintStr

	escrowPrivateKey, err := solanago.PrivateKeyFromBase58(escrowPrivateKeyStr)
	if err != nil {
		logger.Error("Failed to parse escrow private key", "env_var", EnvSolanaEscrowPrivateKey, "error", err)
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
	// Assuming LLMConfig struct definition is added above or exists elsewhere
	llmConfig := LLMConfig{
		Provider:    os.Getenv(EnvLLMProvider),
		APIKey:      os.Getenv(EnvLLMAPIKey),
		Model:       os.Getenv(EnvLLMModel),
		MaxTokens:   1000, // Default or from env var?
		Temperature: 0.7,  // Default or from env var?
	}
	// Basic validation: check API key presence
	if llmConfig.APIKey == "" {
		logger.Error("LLM API key not found in environment", "env_var", EnvLLMAPIKey)
		return nil, fmt.Errorf("missing env var: %s", EnvLLMAPIKey)
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
		ClientSecret: os.Getenv(EnvTwitchClientSecret), // Changed from AppAccessToken
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

	promptBaseEncoded := os.Getenv(EnvLLMCheckReqPromptBase)
	var promptBase string
	if promptBaseEncoded == "" {
		logger.Warn("Environment variable not set, using default prompt base", "env_var", EnvLLMCheckReqPromptBase)
		promptBase = DefaultLLMCheckReqPromptBase
	} else {
		decodedBytes, err := base64.StdEncoding.DecodeString(promptBaseEncoded)
		if err != nil {
			logger.Error("Failed to decode Base64 prompt from environment", "env_var", EnvLLMCheckReqPromptBase, "error", err)
			return nil, fmt.Errorf("failed to decode base64 env var %s: %w", EnvLLMCheckReqPromptBase, err)
		}
		promptBase = string(decodedBytes)
	}

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
