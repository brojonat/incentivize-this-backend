package abb

import (
	"bytes"
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

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/brojonat/affiliate-bounty-board/db/dbgen"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/jmespath/go-jmespath"
)

// Environment Variable Keys for Configuration
const (
	EnvABBServerEnv     = "ENV"
	EnvABBAPIEndpoint   = "ABB_API_ENDPOINT"
	EnvABBSecretKey     = "ABB_SECRET_KEY"
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

	EnvEmailSender         = "EMAIL_SENDER"
	EnvEmailPassword       = "EMAIL_PASSWORD"
	EnvEmailSMTPHost       = "EMAIL_SMTP_HOST"
	EnvEmailSMTPPort       = "EMAIL_SMTP_PORT"
	EnvEmailAdminRecipient = "EMAIL_ADMIN_RECIPIENT"
	EnvGumroadAccessToken  = "GUMROAD_ACCESS_TOKEN"
	EnvGumroadProductID    = "GUMROAD_PRODUCT_ID"
	EnvTripAdvisorAPIKey   = "TRIPADVISOR_API_KEY"

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

	EnvOpenDotaAPIKey = "OPENDOTA_API_KEY"

	EnvLLMImageProvider                        = "LLM_IMAGE_PROVIDER"
	EnvLLMImageAPIKey                          = "LLM_IMAGE_API_KEY"
	EnvLLMImageModel                           = "LLM_IMAGE_MODEL"
	EnvLLMCheckContentRequirementsPromptBase   = "LLM_PROMPT_CHECK_CONTENT_REQUIREMENTS_BASE_B64"
	EnvLLMValidatePayoutWalletPromptBase       = "LLM_PROMPT_VALIDATE_PAYOUT_WALLET_BASE_B64"
	EnvLLMShouldPerformImageAnalysisPromptBase = "LLM_PROMPT_SHOULD_PERFORM_IMAGE_ANALYSIS_BASE_B64"
	EnvLLMImageAnalysisPromptBase              = "LLM_PROMPT_IMAGE_ANALYSIS_BASE_B64"
	EnvLLMMaliciousContentPromptBase           = "LLM_PROMPT_MALICIOUS_CONTENT_BASE_B64"
	EnvLLMOrchestratorPromptBase               = "LLM_PROMPT_ORCHESTRATOR_PROMPT_BASE_B64"

	EnvDiscordBotToken  = "DISCORD_BOT_TOKEN"
	EnvDiscordChannelID = "DISCORD_CHANNEL_ID"

	EnvRapidAPIInstagramKey = "RAPIDAPI_INSTAGRAM_KEY"
	EnvTripadvisorAPIKey    = "TRIPADVISOR_API_KEY"

	MaxRequirementsCharsForLLMCheck = 5000
	MaxContentCharsForLLMCheck      = 80000
	DefaultLLMMaxTokens             = 10000 // Default max tokens if not set
)

var ErrWalletNotFound = fmt.Errorf("no wallet address found in profile description")

// --- Start Platform Agnostic Content Structures ---

// ContentKind defines the type of content (e.g., post, comment, video)
type ContentKind string

// PlatformKind defines the social media platform
type PlatformKind string
type PaymentPlatformKind string

const (
	PlatformReddit          PlatformKind = "reddit"
	PlatformYouTube         PlatformKind = "youtube"
	PlatformTwitch          PlatformKind = "twitch"
	PlatformHackerNews      PlatformKind = "hackernews"
	PlatformBluesky         PlatformKind = "bluesky"
	PlatformInstagram       PlatformKind = "instagram"
	PlatformIncentivizeThis PlatformKind = "incentivizethis"
	PlatformTripAdvisor     PlatformKind = "tripadvisor"
	PlatformSteam           PlatformKind = "steam"
	PlatformGitHub          PlatformKind = "github"

	ContentKindPost      ContentKind = "post"
	ContentKindComment   ContentKind = "comment"
	ContentKindVideo     ContentKind = "video"
	ContentKindClip      ContentKind = "clip"
	ContentKindBounty    ContentKind = "bounty"
	ContentKindReview    ContentKind = "review"
	ContentKindDota2Chat ContentKind = "dota2chat"
	ContentKindIssue     ContentKind = "issue"

	PaymentPlatformGumroad PaymentPlatformKind = "gumroad"
	PaymentPlatformBMC     PaymentPlatformKind = "bmc"
)

// SolanaConfig holds the necessary configuration for Solana interactions.
type SolanaConfig struct {
	RPCEndpoint      string               `json:"rpc_endpoint"`
	WSEndpoint       string               `json:"ws_endpoint"`
	EscrowPrivateKey *solanago.PrivateKey `json:"escrow_private_key"`
	EscrowWallet     solanago.PublicKey   `json:"escrow_token_account"`
	TreasuryWallet   *solanago.PublicKey  `json:"treasury_wallet,omitempty"`
	USDCMintAddress  solanago.PublicKey   `json:"usdc_mint_address"`
}

// AbbServerConfig holds configuration related to the ABB server itself.
type DiscordConfig struct {
	BotToken  string `json:"bot_token"`
	ChannelID string `json:"channel_id"`
}

type AbbServerConfig struct {
	APIEndpoint       string `json:"api_endpoint"`
	SecretKey         string `json:"secret_key"`
	PublicBaseURL     string `json:"public_base_url"`
	LLMEmbeddingModel string `json:"llm_embedding_model"`
	DatabaseURL       string `json:"database_url"`
}

// Configuration holds all necessary configuration for workflows and activities.
// It is intended to be populated inside activities to avoid non-deterministic behavior.
type Configuration struct {
	ABBServerConfig                  AbbServerConfig             `json:"abb_server_config"`
	SolanaConfig                     SolanaConfig                `json:"solana_config"`
	LLMConfig                        LLMConfig                   `json:"llm_config"`
	ImageLLMConfig                   ImageLLMConfig              `json:"image_llm_config"`
	EmbeddingConfig                  EmbeddingConfig             `json:"embedding_config"`
	RedditDeps                       RedditDependencies          `json:"reddit_deps"`
	YouTubeDeps                      YouTubeDependencies         `json:"youtube_deps"`
	TwitchDeps                       TwitchDependencies          `json:"twitch_deps"`
	HackerNewsDeps                   HackerNewsDependencies      `json:"hackernews_deps"`
	BlueskyDeps                      BlueskyDependencies         `json:"bluesky_deps"`
	InstagramDeps                    InstagramDependencies       `json:"instagram_deps"`
	IncentivizeThisDeps              IncentivizeThisDependencies `json:"incentivizethis_deps"`
	TripadvisorDeps                  TripadvisorDependencies     `json:"tripadvisor_deps"`
	SteamDeps                        SteamDependencies           `json:"steam_deps"`
	DiscordConfig                    DiscordConfig               `json:"discord_config"`
	CheckContentRequirementsPrompt   string                      `json:"check_content_requirements_prompt"`
	ValidatePayoutWalletPrompt       string                      `json:"validate_payout_wallet_prompt"`
	ShouldPerformImageAnalysisPrompt string                      `json:"should_perform_image_analysis_prompt"`
	MaliciousContentPrompt           string                      `json:"malicious_content_prompt"`
	OrchestratorPrompt               string                      `json:"orchestrator_prompt"`
	PublishTargetSubreddit           string                      `json:"publish_target_subreddit"`
	Environment                      string                      `json:"environment"`
	RedditFlairID                    string                      `json:"reddit_flair_id"`
	GitHubDeps                       GitHubDependencies          `json:"github_deps"`
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

// GenerateResponse is an activity that wraps the LLM provider's GenerateResponse method.
func (a *Activities) GenerateResponse(ctx context.Context, messages []Message, tools []Tool) (*LLMResponse, error) {
	logger := activity.GetLogger(ctx)
	cfg, err := getConfiguration(ctx)
	if err != nil {
		logger.Error("Failed to get configuration in GenerateResponse activity", "error", err)
		return nil, fmt.Errorf("failed to get configuration: %w", err)
	}

	llmProvider, err := NewLLMProvider(cfg.LLMConfig)
	if err != nil {
		logger.Error("Failed to create LLM provider in GenerateResponse activity", "error", err)
		return nil, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	logger.Info("Calling LLM provider", "message_count", len(messages), "tool_count", len(tools))

	resp, err := llmProvider.GenerateResponse(ctx, messages, tools)
	if err != nil {
		logger.Error("LLM provider call failed in GenerateResponse activity", "error", err)
		return nil, fmt.Errorf("llmprovider.GenerateResponse failed: %w", err)
	}

	return resp, nil
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
		solanaConfig.RPCEndpoint = "https://api.devnet.solana.com"
		solanaConfig.WSEndpoint = "wss://api.devnet.solana.com"
		solanaConfig.EscrowPrivateKey = &dummyEscrowWallet.PrivateKey
		solanaConfig.EscrowWallet = dummyEscrowWallet.PublicKey()
		// Use devnet USDC mint for test mode, or respect environment variable if set
		usdcMintStr := os.Getenv(EnvSolanaUSDCMint)
		if usdcMintStr == "" {
			solanaConfig.USDCMintAddress = solanago.MustPublicKeyFromBase58("4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU") // Devnet USDC
		} else {
			usdcMint, err := solanago.PublicKeyFromBase58(usdcMintStr)
			if err != nil {
				logger.Error("Failed to parse USDC Mint address in test mode", "env_var", EnvSolanaUSDCMint, "error", err)
				return nil, fmt.Errorf("failed to parse USDC mint address in test mode: %w", err)
			}
			solanaConfig.USDCMintAddress = usdcMint
		}
		dummyTreasury := solanago.NewWallet().PublicKey()
		solanaConfig.TreasuryWallet = &dummyTreasury
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
		solanaConfig.WSEndpoint = wsURL
		usdcMint, err := solanago.PublicKeyFromBase58(usdcMintStr)
		if err != nil {
			logger.Error("Failed to parse USDC Mint address", "env_var", EnvSolanaUSDCMint, "error", err)
			return nil, fmt.Errorf("failed to parse USDC mint address: %w", err)
		}
		solanaConfig.USDCMintAddress = usdcMint

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
			return nil, fmt.Errorf("escrow private key does not match escrow wallet address")
		}

		treasuryWallet, err := solanago.PublicKeyFromBase58(treasuryWalletStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse treasury wallet address: %w", err)
		}
		solanaConfig.TreasuryWallet = &treasuryWallet

	}

	// --- LLM Config ---
	llmProviderName := os.Getenv(EnvLLMProvider)
	llmModelName := os.Getenv(EnvLLMModel)
	llmAPIKey := os.Getenv(EnvLLMAPIKey)
	if llmAPIKey == "" && llmProviderName != "" {
		return nil, fmt.Errorf("LLM API Key not found for configured provider %s", llmProviderName)
	}
	// --- Prompts ---
	llmCheckContentRequirementsPromptBase, err := decodeBase64(os.Getenv(EnvLLMCheckContentRequirementsPromptBase))
	if err != nil {
		return nil, fmt.Errorf("failed to decode LLM_PROMPT_CHECK_CONTENT_REQUIREMENTS_BASE_B64: %w", err)
	}
	llmValidatePayoutWalletPromptBase, err := decodeBase64(os.Getenv(EnvLLMValidatePayoutWalletPromptBase))
	if err != nil {
		return nil, fmt.Errorf("failed to decode LLM_PROMPT_VALIDATE_PAYOUT_WALLET_BASE_B64: %w", err)
	}
	llmImageAnalysisPromptBase, err := decodeBase64(os.Getenv(EnvLLMImageAnalysisPromptBase))
	if err != nil {
		return nil, fmt.Errorf("failed to decode LLM_PROMPT_IMAGE_ANALYSIS_BASE_B64: %w", err)
	}
	llmShouldPerformImageAnalysisPromptBase, err := decodeBase64(os.Getenv(EnvLLMShouldPerformImageAnalysisPromptBase))
	if err != nil {
		return nil, fmt.Errorf("failed to decode LLM_PROMPT_SHOULD_PERFORM_IMAGE_ANALYSIS_BASE_B64: %w", err)
	}
	llmMaliciousContentPromptBase, err := decodeBase64(os.Getenv(EnvLLMMaliciousContentPromptBase))
	if err != nil {
		return nil, fmt.Errorf("failed to decode LLM_PROMPT_MALICIOUS_CONTENT_BASE_B64: %w", err)
	}
	llmOrchestratorPromptBase, err := decodeBase64(os.Getenv(EnvLLMOrchestratorPromptBase))
	if err != nil {
		return nil, fmt.Errorf("failed to decode LLM_PROMPT_ORCHESTRATOR_PROMPT_BASE_B64: %w", err)
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
		Provider:   llmProviderName,
		APIKey:     llmAPIKey,
		Model:      llmModelName,
		MaxTokens:  maxTokens,
		BasePrompt: llmCheckContentRequirementsPromptBase,
	}

	// --- Image LLM Config ---
	imageLLMConfig := ImageLLMConfig{
		Provider:   os.Getenv(EnvLLMImageProvider),
		APIKey:     os.Getenv(EnvLLMImageAPIKey),
		Model:      os.Getenv(EnvLLMImageModel),
		BasePrompt: llmImageAnalysisPromptBase,
		// BasePrompt is set below
	}

	// --- Embedding Config ---
	embeddingConfig := EmbeddingConfig{
		Provider: os.Getenv(EnvLLMProvider),
		APIKey:   os.Getenv(EnvLLMAPIKey),
		Model:    os.Getenv(EnvLLMEmbeddingModel),
	}

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
		APIKey:          os.Getenv(EnvYouTubeAPIKey),
		ApplicationName: os.Getenv(EnvYouTubeAppName),
		MaxResults:      50,
	}

	// --- Twitch Dependencies ---
	twitchDeps := TwitchDependencies{
		ClientID:     os.Getenv(EnvTwitchClientID),
		ClientSecret: os.Getenv(EnvTwitchClientSecret),
	}

	// --- Bluesky Dependencies ---
	blueskyDeps := BlueskyDependencies{
		PDS: os.Getenv("BLUESKY_PDS_URL"),
	}

	// --- Instagram Dependencies ---
	instagramDeps := InstagramDependencies{
		RapidAPIKey: os.Getenv(EnvRapidAPIInstagramKey),
	}

	// --- IncentivizeThis Dependencies ---
	incentivizeThisDeps := IncentivizeThisDependencies{
		APIEndpoint:   os.Getenv(EnvABBAPIEndpoint),   // Use the common ABB API endpoint
		PublicBaseURL: os.Getenv(EnvABBPublicBaseURL), // Use the common ABB Public Base URL
	}

	// --- Tripadvisor Dependencies ---
	tripadvisorDeps := TripadvisorDependencies{
		APIKey: os.Getenv(EnvTripadvisorAPIKey),
	}

	// --- Steam Dependencies ---
	steamDeps := SteamDependencies{
		APIKey: os.Getenv(EnvOpenDotaAPIKey),
	}

	// --- Discord Config ---
	discordConfig := DiscordConfig{
		BotToken:  os.Getenv(EnvDiscordBotToken),
		ChannelID: os.Getenv(EnvDiscordChannelID),
	}

	// --- ABB Server Config ---
	abbServerConfig := AbbServerConfig{
		APIEndpoint:       os.Getenv(EnvABBAPIEndpoint),
		SecretKey:         os.Getenv(EnvABBSecretKey),
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
		ABBServerConfig:                  abbServerConfig,
		SolanaConfig:                     solanaConfig,
		LLMConfig:                        llmConfig,
		ImageLLMConfig:                   imageLLMConfig,
		EmbeddingConfig:                  embeddingConfig,
		RedditDeps:                       redditDeps,
		YouTubeDeps:                      youtubeDeps,
		TwitchDeps:                       twitchDeps,
		HackerNewsDeps:                   HackerNewsDependencies{},
		BlueskyDeps:                      blueskyDeps,
		InstagramDeps:                    instagramDeps,
		IncentivizeThisDeps:              incentivizeThisDeps,
		TripadvisorDeps:                  tripadvisorDeps,
		SteamDeps:                        steamDeps,
		DiscordConfig:                    discordConfig,
		CheckContentRequirementsPrompt:   llmCheckContentRequirementsPromptBase,
		ValidatePayoutWalletPrompt:       llmValidatePayoutWalletPromptBase,
		ShouldPerformImageAnalysisPrompt: llmShouldPerformImageAnalysisPromptBase,
		MaliciousContentPrompt:           llmMaliciousContentPromptBase,
		OrchestratorPrompt:               llmOrchestratorPromptBase,
		PublishTargetSubreddit:           targetSubreddit,
		Environment:                      environmentToStore,
		RedditFlairID:                    flairID,
		GitHubDeps:                       GitHubDependencies{},
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
	PlatformType PlatformKind `json:"platform"`
	ContentKind  ContentKind  `json:"content_kind"`
	ContentID    string       `json:"content_id"`
}

// PullContentActivity fetches content from various platforms based on input.
// Implementation will consolidate logic from individual Pull<Platform>Content activities.
// NOTE: this activity may modify the content before returning it in order to redact
// certain text that may interfere with the bounty assessment, such as malicious
// instructions and/or jailbreaking attempts.
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

		// Determine the PDS host, strip any scheme, and set a default if not provided.
		pdsHost := cfg.BlueskyDeps.PDS
		pdsHost = strings.TrimPrefix(pdsHost, "https://")
		pdsHost = strings.TrimPrefix(pdsHost, "http://")

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
			resolveHandleURL := fmt.Sprintf("https://%s/xrpc/com.atproto.identity.resolveHandle?handle=%s", pdsHost, url.QueryEscape(handle))
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
			var handleResponse BlueskyHandleResponse
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

		// 2. Pull content using AT URI
		getPostsURL := fmt.Sprintf("https://%s/xrpc/app.bsky.feed.getPosts?uris=%s", pdsHost, url.QueryEscape(contentIdToUse))
		req, reqErr := http.NewRequestWithContext(ctx, "GET", getPostsURL, nil)
		if reqErr != nil {
			return nil, fmt.Errorf("failed to create Bluesky getPosts request: %w", reqErr)
		}
		// No auth typically needed for public.api.bsky.app getPosts

		resp, httpErr := a.httpClient.Do(req)
		if httpErr != nil {
			return nil, fmt.Errorf("failed to make Bluesky getPosts request: %w", httpErr)
		}
		defer resp.Body.Close()

		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("failed to read Bluesky getPosts response body (status %d): %w", resp.StatusCode, readErr)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("Bluesky API returned status %d for getPosts: %s (URL: %s)", resp.StatusCode, string(body), getPostsURL)
		}

		var responseData struct {
			Posts []json.RawMessage `json:"posts"`
		}
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
			return nil, fmt.Errorf("failed to unmarshal Bluesky post record to extract text: %w (uri: %s)", jsonErr, content.Uri)
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

	case PlatformInstagram:
		logger.Debug("Executing Instagram pull logic within PullContentActivity")
		// Ensure ContentKind is Post for Instagram, as we only support posts (reels) for now
		if input.ContentKind != ContentKindPost {
			return nil, fmt.Errorf("unsupported content kind for Instagram: %s. Only '%s' is supported", input.ContentKind, ContentKindPost)
		}
		instagramContent, err := a.PullInstagramContentActivity(ctx, cfg.InstagramDeps, input.ContentID)
		if err != nil {
			return nil, fmt.Errorf("failed to pull Instagram content: %w", err)
		}
		contentBytes, err = json.Marshal(instagramContent)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Instagram content: %w", err)
		}

	case PlatformIncentivizeThis:
		logger.Debug("Executing IncentivizeThis pull logic within PullContentActivity")
		// Ensure ContentKind is Post for IncentivizeThis, as it's a general promotional message
		if input.ContentKind != ContentKindPost {
			return nil, fmt.Errorf("unsupported content kind for IncentivizeThis: %s. Only '%s' is supported", input.ContentKind, ContentKindPost)
		}
		incentivizeThisContent, err := a.PullIncentivizeThisContentActivity(ctx, cfg.IncentivizeThisDeps, input.ContentID) // Call the new activity
		if err != nil {
			return nil, fmt.Errorf("failed to pull IncentivizeThis content: %w", err)
		}
		contentBytes, err = json.Marshal(incentivizeThisContent)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal IncentivizeThis content: %w", err)
		}

	case PlatformTripAdvisor:
		logger.Debug("Executing Tripadvisor pull logic within PullContentActivity")
		if input.ContentKind != ContentKindReview {
			return nil, fmt.Errorf("unsupported content kind for Tripadvisor: %s. Only '%s' is supported", input.ContentKind, ContentKindReview)
		}
		tripadvisorContent, err := a.PullTripadvisorContentActivity(ctx, cfg.TripadvisorDeps, input.ContentID)
		if err != nil {
			return nil, fmt.Errorf("failed to pull Tripadvisor content: %w", err)
		}
		contentBytes, err = json.Marshal(tripadvisorContent)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Tripadvisor content: %w", err)
		}

	case PlatformSteam:
		logger.Debug("Executing Steam pull logic within PullContentActivity")
		if input.ContentKind != ContentKindDota2Chat {
			return nil, fmt.Errorf("unsupported content kind for Steam: %s. Only '%s' is supported", input.ContentKind, ContentKindDota2Chat)
		}
		steamContent, err := a.fetchDota2Chat(ctx, cfg.SteamDeps, input.ContentID)
		if err != nil {
			return nil, fmt.Errorf("failed to pull Steam content: %w", err)
		}
		contentBytes, err = json.Marshal(steamContent)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal Steam content: %w", err)
		}

	case PlatformGitHub:
		deps := cfg.GitHubDeps
		if deps.Type() != PlatformGitHub {
			// This is a sanity check and should never happen.
			return nil, fmt.Errorf("internal configuration error: GitHubDeps type is not PlatformGitHub")
		}
		switch input.ContentKind {
		case ContentKindIssue:
			owner, repo, issueNum, err := a.parseGitHubURL(input.ContentID)
			if err != nil {
				return nil, fmt.Errorf("failed to parse github url: %w", err)
			}
			content, err := a.GetGitHubIssue(ctx, owner, repo, issueNum)
			if err != nil {
				return nil, err
			}
			contentBytes, err = json.Marshal(content)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal github issue: %w", err)
			}
		default:
			return nil, fmt.Errorf("unsupported content kind for github: %s", input.ContentKind)
		}
	default:
		err = fmt.Errorf("unsupported platform type in PullContentActivity: %s", input.PlatformType)
	}

	// Perform malicious content detection.
	detectionResult, err := a.DetectMaliciousContent(ctx, contentBytes)
	if err != nil {
		// If the detection activity itself fails, we should not proceed.
		logger.Error("Malicious content detection activity failed", "error", err)
		return nil, fmt.Errorf("security check failed: %w", err)
	}

	if detectionResult.IsMalicious {
		// If malicious content is detected, we fail the activity with a specific application error.
		logger.Warn("Malicious content detected, rejecting content.", "reason", detectionResult.Reason)
		err := temporal.NewApplicationError(
			fmt.Sprintf("Content rejected by security filter: %s", detectionResult.Reason),
			"MaliciousContentDetected",
		)
		return nil, err
	}

	return contentBytes, nil
}

// DeleteBountyEmbeddingViaHTTPActivityInput defines input for deleting embedding via HTTP.
type DeleteBountyEmbeddingViaHTTPActivityInput struct {
	BountyID string
}

// DeleteBountyEmbeddingViaHTTPActivity calls the server's HTTP endpoint to delete an embedding.
func (a *Activities) DeleteBountyEmbeddingViaHTTPActivity(ctx context.Context, input DeleteBountyEmbeddingViaHTTPActivityInput) error {
	logger := activity.GetLogger(ctx)

	if input.BountyID == "" {
		logger.Error("DeleteBountyEmbeddingViaHTTPActivity called with empty BountyID")
		return temporal.NewApplicationError("BountyID cannot be empty for HTTP deletion", "INVALID_ARGUMENT")
	}

	cfg, err := getConfiguration(ctx)
	if err != nil {
		logger.Error("Failed to get configuration in DeleteBountyEmbeddingViaHTTPActivity", "error", err)
		return fmt.Errorf("failed to get configuration for HTTP deletion: %w", err)
	}

	if cfg.ABBServerConfig.APIEndpoint == "" {
		logger.Error("APIEndpoint is missing in configuration for HTTP deletion")
		return temporal.NewApplicationError("Server APIEndpoint not configured", "CONFIG_ERROR")
	}

	authToken, err := a.getABBAuthToken(ctx, logger, cfg, a.httpClient)
	if err != nil {
		logger.Error("Failed to get ABB auth token for embedding deletion", "error", err)
		return fmt.Errorf("failed to get ABB auth token: %w", err)
	}

	deleteURL := fmt.Sprintf("%s/bounties/embeddings/%s", strings.TrimSuffix(cfg.ABBServerConfig.APIEndpoint, "/"), input.BountyID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		logger.Error("Failed to create HTTP DELETE request for embedding", "url", deleteURL, "error", err)
		return fmt.Errorf("failed to create HTTP DELETE request for %s: %w", deleteURL, err)
	}
	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Accept", "application/json")

	logger.Info("Attempting to delete embedding via HTTP DELETE", "url", deleteURL)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP DELETE request to %s failed: %w", deleteURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		logger.Error("HTTP DELETE request for embedding returned non-success status", "url", deleteURL, "status_code", resp.StatusCode, "response_body", string(bodyBytes))
		return fmt.Errorf("failed to delete embedding via HTTP: status %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// MarkGumroadSaleNotifiedActivityInput defines the input for the activity
// that marks a Gumroad sale as notified via an HTTP call to the ABB server.
type MarkGumroadSaleNotifiedActivityInput struct {
	SaleID string `json:"sale_id"`
	APIKey string `json:"api_key"` // This is the token generated and emailed to the user
}

// MarkGumroadSaleNotifiedActivity makes an internal HTTP call to the ABB server
// to mark a Gumroad sale as notified and record the API key sent to the user.
func (a *Activities) MarkGumroadSaleNotifiedActivity(ctx context.Context, input MarkGumroadSaleNotifiedActivityInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("MarkGumroadSaleNotifiedActivity started", "SaleID", input.SaleID)

	cfg, err := getConfiguration(ctx) // Get full config to access AbbServerConfig
	if err != nil {
		logger.Error("Failed to get configuration for MarkGumroadSaleNotifiedActivity", "error", err)
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	apiEndpoint := cfg.ABBServerConfig.APIEndpoint
	if apiEndpoint == "" {
		logger.Error("ABB_API_ENDPOINT not configured for MarkGumroadSaleNotifiedActivity")
		return fmt.Errorf("abb API endpoint not configured")
	}

	authToken, err := a.getABBAuthToken(ctx, logger, cfg, a.httpClient)
	if err != nil {
		logger.Error("Failed to get ABB auth token for MarkGumroadSaleNotifiedActivity", "error", err)
		return fmt.Errorf("failed to get ABB auth token: %w", err)
	}

	targetURL := fmt.Sprintf("%s/gumroad/notified", strings.TrimRight(apiEndpoint, "/"))

	requestBody := map[string]string{
		"sale_id": input.SaleID,
		"api_key": input.APIKey,
	}
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		logger.Error("Failed to marshal request body for MarkGumroadSaleNotifiedActivity", "error", err)
		return fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		logger.Error("Failed to create HTTP request for MarkGumroadSaleNotifiedActivity", "error", err)
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+authToken) // Use Bearer token authentication

	client := a.httpClient
	if client == nil { // Fallback if not initialized, though NewActivities does it
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		logger.Error("Failed to execute HTTP request for MarkGumroadSaleNotifiedActivity", "error", err, "url", targetURL)
		return fmt.Errorf("failed to execute request to %s: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		logger.Error("MarkGumroadSaleNotifiedActivity HTTP request failed", "statusCode", resp.StatusCode, "url", targetURL, "responseBody", string(bodyBytes))
		return fmt.Errorf("http request to %s failed with status %d: %s", targetURL, resp.StatusCode, string(bodyBytes))
	}

	logger.Info("MarkGumroadSaleNotifiedActivity completed successfully", "SaleID", input.SaleID)
	return nil
}

// CallGumroadNotifyActivityInput defines the input for the CallGumroadNotifyActivity.
// Currently, only lookback duration is needed, but it's structured for future flexibility.
type CallGumroadNotifyActivityInput struct {
	LookbackDuration time.Duration `json:"lookback_duration"`
}

// CallGumroadNotifyActivity makes an HTTP POST request to the /gumroad/notify endpoint.
// This is intended to be called by a scheduled workflow to periodically trigger notifications
// for Gumroad sales within the specified lookback window.
func (a *Activities) CallGumroadNotifyActivity(ctx context.Context, input CallGumroadNotifyActivityInput) error {
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}
	logger := activity.GetLogger(ctx)

	if cfg.Environment != "prod" {
		logger.Info("Skipping Gumroad notification activity in non-prod environment", "env", cfg.Environment)
		return nil
	}

	// 1. Fetch sales from Gumroad
	gumroadClient, err := NewGumroadClient()
	if err != nil {
		return fmt.Errorf("failed to create gumroad client: %w", err)
	}

	accessToken := os.Getenv(EnvGumroadAccessToken)
	if accessToken == "" {
		return fmt.Errorf("%s must be set", EnvGumroadAccessToken)
	}

	salesResp, err := gumroadClient.GetSales("", accessToken, input.LookbackDuration, "", "")
	if err != nil {
		return fmt.Errorf("failed to get sales from gumroad: %w", err)
	}
	logger.Info("Fetched sales from gumroad", "count", len(salesResp.Sales))

	// Get an auth token to talk to our server
	authToken, err := a.getABBAuthToken(ctx, logger, cfg, a.httpClient)
	if err != nil {
		return fmt.Errorf("failed to get ABB auth token: %w", err)
	}

	if len(salesResp.Sales) > 0 {

		// 2. Make an HTTP call to our own server to insert the sales
		insertReqBody, err := json.Marshal(map[string][]dbgen.InsertGumroadSaleParams{"sales": salesResp.Sales})
		if err != nil {
			return fmt.Errorf("failed to marshal sales for insert request: %w", err)
		}

		insertURL := fmt.Sprintf("%s/gumroad", cfg.ABBServerConfig.APIEndpoint)
		httpReq, err := http.NewRequestWithContext(ctx, "POST", insertURL, bytes.NewBuffer(insertReqBody))
		if err != nil {
			return fmt.Errorf("failed to create insert request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))

		resp, err := a.httpClient.Do(httpReq)
		if err != nil {
			return fmt.Errorf("failed to execute insert request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("insert request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
		}
		logger.Info("Successfully called internal endpoint to insert Gumroad sales.")
	}

	// 3. Now that sales are in the DB, trigger the notification workflows for unnotified sales
	// We call the notify endpoint which is now responsible for this part.
	notifyURL := fmt.Sprintf("%s/gumroad/notify", cfg.ABBServerConfig.APIEndpoint)
	notifyReq, err := http.NewRequestWithContext(ctx, "POST", notifyURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create notify request: %w", err)
	}
	notifyReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))

	notifyResp, err := a.httpClient.Do(notifyReq)
	if err != nil {
		return fmt.Errorf("failed to execute notify request: %w", err)
	}
	defer notifyResp.Body.Close()

	if notifyResp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(notifyResp.Body)
		return fmt.Errorf("notify request failed with status %d: %s", notifyResp.StatusCode, string(bodyBytes))
	}

	return nil
}

// PruneStaleEmbeddingsActivityInput defines the input for the PruneStaleEmbeddingsActivity.
// Currently, it's empty as the endpoint `/embeddings/prune` is called without parameters.
type PruneStaleEmbeddingsActivityInput struct{}

// PruneStaleEmbeddingsActivity calls the server's HTTP endpoint to prune stale embeddings.
// It returns a string message from the server upon success.
func (a *Activities) PruneStaleEmbeddingsActivity(ctx context.Context, input PruneStaleEmbeddingsActivityInput) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("PruneStaleEmbeddingsActivity started")

	cfg, err := getConfiguration(ctx)
	if err != nil {
		logger.Error("Failed to get configuration in PruneStaleEmbeddingsActivity", "error", err)
		return "", fmt.Errorf("failed to get configuration for pruning: %w", err)
	}

	if cfg.ABBServerConfig.APIEndpoint == "" {
		logger.Error("APIEndpoint is missing in configuration for pruning")
		return "", temporal.NewApplicationError("Server APIEndpoint not configured", "CONFIG_ERROR")
	}

	authToken, err := a.getABBAuthToken(ctx, logger, cfg, a.httpClient)
	if err != nil {
		logger.Error("Failed to get ABB auth token for pruning", "error", err)
		return "", fmt.Errorf("failed to get ABB auth token: %w", err)
	}

	pruneURL := strings.TrimSuffix(cfg.ABBServerConfig.APIEndpoint, "/") + "/embeddings/prune"

	// Using POST as "pruning" is an action. If it were parameterless GET, it would be http.MethodGet.
	// The request body is nil as PruneStaleEmbeddingsActivityInput is empty.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pruneURL, nil)
	if err != nil {
		logger.Error("Failed to create HTTP POST request for pruning embeddings", "url", pruneURL, "error", err)
		return "", fmt.Errorf("failed to create HTTP POST request for %s: %w", pruneURL, err)
	}

	req.Header.Set("Authorization", "Bearer "+authToken)
	req.Header.Set("Accept", "application/json")       // Expecting a JSON response or at least text
	req.Header.Set("Content-Type", "application/json") // Even with empty body, good practice for POST

	logger.Info("Attempting to prune stale embeddings via HTTP POST", "url", pruneURL)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP POST request to %s failed: %w", pruneURL, err)
	}
	defer resp.Body.Close()

	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		logger.Error("Failed to read response body from prune endpoint", "url", pruneURL, "status_code", resp.StatusCode, "error", readErr)
		return "", fmt.Errorf("failed to read response body from %s (status %s): %w", pruneURL, resp.Status, readErr)
	}

	if resp.StatusCode != http.StatusOK {
		logger.Error("HTTP POST request for pruning embeddings returned non-success status", "url", pruneURL, "status_code", resp.StatusCode, "response_body", string(bodyBytes))
		return "", fmt.Errorf("prune endpoint %s returned status %d: %s", pruneURL, resp.StatusCode, string(bodyBytes))
	}

	// The workflow expects a string result. Assuming the server returns a plain text or JSON string message.
	// If it's JSON, we might want to parse it and extract a specific field. For now, return the body as string.
	responseMessage := string(bodyBytes)
	logger.Info("Successfully pruned stale embeddings", "url", pruneURL, "response_message", responseMessage)
	return responseMessage, nil
}

func (a *Activities) GetOrchestratorPromptActivity(ctx context.Context) (string, error) {
	logger := activity.GetLogger(ctx)
	cfg, err := getConfiguration(ctx)
	if err != nil {
		logger.Error("Failed to get configuration in GetOrchestratorPromptActivity", "error", err)
		return "", fmt.Errorf("failed to get configuration: %w", err)
	}
	if cfg.OrchestratorPrompt == "" {
		return "", fmt.Errorf("orchestrator prompt is not configured")
	}
	return cfg.OrchestratorPrompt, nil
}
