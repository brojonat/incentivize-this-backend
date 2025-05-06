package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"encoding/base64"

	"github.com/brojonat/affiliate-bounty-board/http/api"
	solanautil "github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/memo"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/jmespath/go-jmespath"
	"go.temporal.io/sdk/activity"
	temporal_log "go.temporal.io/sdk/log"
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
)

const (
	// PlatformReddit represents the Reddit platform
	PlatformReddit PlatformKind = "reddit"
	// PlatformYouTube represents the YouTube platform
	PlatformYouTube PlatformKind = "youtube"
	// PlatformTwitch represents the Twitch platform
	PlatformTwitch PlatformKind = "twitch"
)

// Environment Variable Keys for Activities
const (
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
)

// Default base prompt for CheckContentRequirements
const DefaultLLMCheckReqPromptBase = `You are a content verification system. Your task is to determine if the given content satisfies the specified requirements.

The content to evaluate is provided as a JSON object below.`

// Configuration holds all necessary configuration for workflows and activities.
// It is intended to be populated inside activities to avoid non-deterministic behavior.
type Configuration struct {
	SolanaConfig           SolanaConfig        `json:"solana_config"`
	LLMConfig              LLMConfig           `json:"llm_config"`
	ImageLLMConfig         ImageLLMConfig      `json:"image_llm_config"`
	RedditDeps             RedditDependencies  `json:"reddit_deps"`
	YouTubeDeps            YouTubeDependencies `json:"youtube_deps"`
	TwitchDeps             TwitchDependencies  `json:"twitch_deps"` // Added Twitch dependencies
	EmailConfig            EmailConfig         `json:"email_config"`
	ServerURL              string              `json:"server_url"`
	AuthToken              string              `json:"auth_token"`
	Prompt                 string              `json:"prompt"`
	ABBServerURL           string              `json:"abb_server_url"`
	ABBServerSecretKey     string              `json:"abb_server_secret_key"`
	PublishTargetSubreddit string              `json:"publish_target_subreddit"`
	Environment            string              `json:"environment"`
	RedditFlairID          string              `json:"reddit_flair_id"`
	PublicBaseURL          string              `json:"public_base_url"` // Added: Public Base URL
}

// SolanaConfig holds the necessary configuration for Solana interactions.
type SolanaConfig struct {
	RPCEndpoint      string               `json:"rpc_endpoint"`
	WSEndpoint       string               `json:"ws_endpoint"`
	EscrowPrivateKey *solanago.PrivateKey `json:"escrow_private_key"`
	EscrowWallet     solanago.PublicKey   `json:"escrow_token_account"`
	TreasuryWallet   string               `json:"treasury_wallet"`
	USDCMintAddress  string               `json:"usdc_mint_address"`
}

// LLMConfig holds configuration for the LLM provider.

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

	// --- Assemble Configuration ---
	config := &Configuration{
		SolanaConfig:           solanaConfig,
		LLMConfig:              llmConfig,
		RedditDeps:             redditDeps,
		YouTubeDeps:            youtubeDeps,
		TwitchDeps:             twitchDeps, // Added Twitch dependencies
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

// CheckContentRequirements checks if the content satisfies the requirements
func (a *Activities) CheckContentRequirements(ctx context.Context, content []byte, requirements []string) (CheckContentRequirementsResult, error) {
	logger := activity.GetLogger(ctx)

	// Get fresh config
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to get configuration in CheckContentRequirements: %w", err)
	}

	logger.Debug("Starting content requirements check",
		"content_length", len(content),
		"requirements_count", len(requirements))

	// Convert content bytes to string for the prompt
	contentStr := string(content)

	// --- Input Size Checks ---
	if len(contentStr) > MaxContentCharsForLLMCheck {
		reason := fmt.Sprintf("Content exceeds maximum character limit (%d > %d)", len(contentStr), MaxContentCharsForLLMCheck)
		logger.Warn(reason)
		return CheckContentRequirementsResult{Satisfies: false, Reason: reason}, nil // Not an error, just failed check
	}

	requirementsStr := strings.Join(requirements, "\n")
	if len(requirementsStr) > MaxRequirementsCharsForLLMCheck {
		reason := fmt.Sprintf("Requirements exceed maximum character limit (%d > %d)", len(requirementsStr), MaxRequirementsCharsForLLMCheck)
		logger.Warn(reason)
		return CheckContentRequirementsResult{Satisfies: false, Reason: reason}, nil // Not an error, just failed check
	}
	// --- End Input Size Checks ---

	// Construct the final prompt. This part of the prompt includes the specification
	// for the LLM to follow when checking the content against the requirements. That
	// is to say, you may influence the LLM's behavior by changing the base prompt
	// on the provider, but this is where the LLM's behavior is specified to be
	// in accordance with the code (i.e., requiring a particular format for the response).
	prompt := fmt.Sprintf(`%s

Requirements:
%s

Content (JSON):
%s

You must respond ONLY with a valid JSON object containing two keys: "satisfies" (a boolean indicating if the content meets the requirements) and "reason" (a string explaining your decision). Example: {"satisfies": true, "reason": "Content meets all criteria."}`, cfg.Prompt, strings.Join(requirements, "\n"), contentStr)

	// Log estimated token count
	estimatedTokens := len(prompt) / 4
	logger.Info("Sending prompt to LLM", "estimated_tokens", estimatedTokens, "prompt_length_chars", len(prompt))

	// Create LLM provider instance from config fetched within the activity
	llmProvider, err := NewLLMProvider(cfg.LLMConfig) // Use cfg.LLMConfig
	if err != nil {
		logger.Error("Failed to create LLM provider from config", "error", err)
		return CheckContentRequirementsResult{}, fmt.Errorf("failed to create LLM provider: %w", err)
	}

	// Call the LLM service
	resp, err := llmProvider.Complete(ctx, prompt)
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

// TransferUSDC is an activity that transfers USDC from the escrow account to a user's account
// It now accepts an optional memo field.
// func (a *Activities) TransferUSDC(ctx context.Context, userID string, amount float64) error {
func (a *Activities) TransferUSDC(ctx context.Context, recipientWallet string, amount float64, memo string) error {
	logger := activity.GetLogger(ctx)
	// Get configuration from context
	cfg, err := getConfiguration(ctx)
	if err != nil {
		logger.Error("Failed to get configuration", "error", err)
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	logger.Info("Starting TransferUSDC activity", "recipientWallet", recipientWallet, "amount", amount, "memo", memo)

	// Validate inputs
	if recipientWallet == "" {
		logger.Error("Recipient wallet address is required")
		return fmt.Errorf("recipient wallet address is required")
	}
	recipientPubKey, err := solanago.PublicKeyFromBase58(recipientWallet)
	if err != nil {
		logger.Error("Invalid recipient wallet address", "address", recipientWallet, "error", err)
		return fmt.Errorf("invalid recipient wallet address '%s': %w", recipientWallet, err)
	}
	if amount <= 0 {
		logger.Error("Transfer amount must be positive", "amount", amount)
		return fmt.Errorf("transfer amount must be positive")
	}
	// Ensure Solana config is properly loaded
	if cfg.SolanaConfig.EscrowPrivateKey == nil {
		logger.Error("Escrow private key not configured")
		return fmt.Errorf("escrow private key not configured")
	}
	if cfg.SolanaConfig.USDCMintAddress == "" {
		logger.Error("USDC Mint Address not configured")
		return fmt.Errorf("usdc mint address not configured")
	}
	usdcMint, err := solanago.PublicKeyFromBase58(cfg.SolanaConfig.USDCMintAddress)
	if err != nil {
		logger.Error("Invalid USDC Mint Address", "address", cfg.SolanaConfig.USDCMintAddress, "error", err)
		return fmt.Errorf("invalid usdc mint address '%s': %w", cfg.SolanaConfig.USDCMintAddress, err)
	}

	// Convert amount to lamports
	usdcAmount, err := solanautil.NewUSDCAmount(amount)
	if err != nil {
		logger.Error("Invalid USDC amount", "amount", amount, "error", err)
		return fmt.Errorf("invalid usdc amount: %w", err)
	}
	lamports := usdcAmount.ToSmallestUnit().Uint64()

	logger.Debug("Transfer details", "from_escrow", cfg.SolanaConfig.EscrowWallet.String(), "to_recipient", recipientPubKey.String(), "mint", usdcMint.String(), "lamports", lamports)

	// Create RPC Client (using config)
	rpcClient := solanautil.NewRPCClient(cfg.SolanaConfig.RPCEndpoint)

	// Perform the transfer with memo
	txSig, err := solanautil.SendUSDCWithMemo(
		ctx, // Use activity context
		rpcClient,
		usdcMint,
		*cfg.SolanaConfig.EscrowPrivateKey, // Use configured private key
		recipientPubKey,
		lamports,
		memo, // Pass the memo here
	)
	if err != nil {
		logger.Error("Failed to send USDC transfer", "error", err)
		return fmt.Errorf("failed to send usdc: %w", err)
	}

	logger.Info("USDC transfer submitted", "signature", txSig.String())

	// Optionally wait for confirmation (consider timeout)
	confirmCtx, cancel := context.WithTimeout(ctx, 60*time.Second) // Example timeout
	defer cancel()
	err = solanautil.ConfirmTransaction(confirmCtx, rpcClient, txSig, rpc.CommitmentFinalized)
	if err != nil {
		// Log warning but maybe don't fail the activity entirely?
		// The transfer might still succeed even if confirmation times out here.
		logger.Warn("Failed to confirm transaction within timeout, but proceeding", "signature", txSig.String(), "error", err)
	} else {
		logger.Info("USDC transfer confirmed", "signature", txSig.String())
	}

	return nil
}

// Define the structure for the payout memo
// type PayoutMemoData struct {
// 	WorkflowID  string `json:"workflow_id"`
// 	ContentID   string `json:"content_id"`
// 	ContentKind string `json:"content_kind,omitempty"` // Initially null
// }

// RefundMemoData defines the structure for the refund memo
// type RefundMemoData struct {
// 	WorkflowID string `json:"workflow_id"`
// }

// DirectPaymentMemoData defines the structure for direct payment memos
// type DirectPaymentMemoData struct {
// 	WorkflowID string `json:"workflow_id"`
// }

// VerifyPayment is an activity that checks if a specific payment has been received.
// It polls the Solana network for transactions matching the criteria.

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
func (deps YouTubeDependencies) Type() PlatformKind {
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
func (a *Activities) PullYouTubeContent(ctx context.Context, contentID string, contentKind ContentKind) (*YouTubeContent, error) {
	logger := activity.GetLogger(ctx)

	// Get fresh config
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration in PullYouTubeContent: %w", err)
	}
	ytDeps := cfg.YouTubeDeps // Use the fetched YouTubeDependencies

	logger.Info("Pulling YouTube content", "content_id", contentID)

	httpClient := a.httpClient
	videoID := strings.TrimPrefix(contentID, "yt_")

	// Fetch video metadata using YouTube Data API (still useful)
	videoData, err := a.fetchYouTubeVideoMetadata(ctx, ytDeps, httpClient, videoID)
	if err != nil {
		// Don't fail outright if metadata fetch fails, maybe transcript is enough?
		logger.Warn("Failed to fetch YouTube video metadata, proceeding to transcript fetch", "video_id", videoID, "error", err)
		// Create a minimal content struct if metadata fails
		content := &YouTubeContent{
			ID: videoID,
		}
		// Attempt to fetch transcript even if metadata failed
		transcript, transcriptErr := a.FetchYouTubeTranscriptDirectly(ctx, httpClient, videoID, "en") // Default to English
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
	transcript, err := a.FetchYouTubeTranscriptDirectly(ctx, httpClient, videoID, "en") // Default to English
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
func (a *Activities) fetchYouTubeVideoMetadata(ctx context.Context, ytDeps YouTubeDependencies, httpClient *http.Client, videoID string) (*YouTubeVideoData, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Fetching YouTube video metadata", "video_id", videoID)

	// Create request to YouTube Data API
	url := fmt.Sprintf("https://www.googleapis.com/youtube/v3/videos?part=snippet,statistics,contentDetails&id=%s&key=%s",
		videoID, ytDeps.APIKey)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set application name in the request headers
	if ytDeps.ApplicationName != "" {
		req.Header.Set("X-Goog-Api-Key", ytDeps.APIKey)
		req.Header.Set("X-Goog-Api-Client", ytDeps.ApplicationName)
	}

	// Make request using the passed httpClient
	resp, err := httpClient.Do(req)
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
func (a *Activities) FetchYouTubeTranscriptDirectly(ctx context.Context, httpClient *http.Client, videoID string, preferredLanguage string) (string, error) {
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

	// Use the passed httpClient
	resp, err := httpClient.Do(req)
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
	captionResp, err := httpClient.Do(captionReq)
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

// VerifyPaymentResult represents the result of verifying a payment
type VerifyPaymentResult struct {
	Verified bool
	Amount   *solanautil.USDCAmount
	Error    string
}

// VerifyPayment verifies that a specific payment transaction has been received. More specifically,
// it verifies that a transaction has been received from the expected sender to the expected recipient's
// associated token account with the expected amount of USDC.
func (a *Activities) VerifyPayment(
	ctx context.Context,
	expectedSender solanago.PublicKey,
	expectedRecipient solanago.PublicKey,
	expectedAmount *solanautil.USDCAmount,
	expectedMemo string,
	timeout time.Duration,
) (*VerifyPaymentResult, error) {
	logger := activity.GetLogger(ctx)

	// Get fresh config
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration in VerifyPayment: %w", err)
	}
	solCfg := cfg.SolanaConfig // Use fetched config

	expectedAmountUint64 := expectedAmount.ToSmallestUnit().Uint64()
	logger.Info("VerifyPayment started",
		"workflow_id", expectedMemo,
		"expected_sender", expectedSender.String(),
		"expected_recipient", expectedRecipient.String(),
		"expected_amount_lamports", expectedAmountUint64,
		"timeout", timeout,
		"rpc_endpoint", solCfg.RPCEndpoint,
		"usdc_mint", solCfg.USDCMintAddress, // Use fetched config
		"escrow_owner", solCfg.EscrowWallet.String()) // Use fetched config

	// Create a ticker to check transactions periodically
	ticker := time.NewTicker(10 * time.Second) // Check more frequently for debugging
	defer ticker.Stop()

	// Create a timeout context for the entire verification process
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rpcClient := solanautil.NewRPCClient(solCfg.RPCEndpoint)

	// Parse the USDC mint address from config
	usdcMint, err := solanago.PublicKeyFromBase58(solCfg.USDCMintAddress)
	if err != nil {
		logger.Error("Failed to parse USDC mint address from config", "mint", solCfg.USDCMintAddress, "error", err)
		return nil, fmt.Errorf("invalid USDC mint address in config: %w", err)
	}

	// Derive the expected recipient's Associated Token Account (ATA)
	expectedRecipientATA, _, err := solanago.FindAssociatedTokenAddress(expectedRecipient, usdcMint)
	if err != nil {
		logger.Error("Failed to derive expected recipient ATA", "recipient", expectedRecipient.String(), "mint", usdcMint.String(), "error", err)
		return nil, fmt.Errorf("failed to derive recipient ATA: %w", err)
	}

	logger.Info("Verifying payment TO derived ATA", "ata_address", expectedRecipientATA.String(), "recipient", expectedRecipient.String())

	// Keep track of signatures we've already checked to avoid redundant lookups
	checkedSignatures := make(map[solanago.Signature]bool)

	for {
		select {
		case <-timeoutCtx.Done():
			logger.Warn("Payment verification timed out", "expected_sender", expectedSender.String(), "expected_recipient_ata", expectedRecipientATA.String())
			return &VerifyPaymentResult{
				Verified: false,
				Error:    "payment verification timed out",
			}, nil // Timeout is not a processing error, just verification failure

		case <-ticker.C:
			logger.Debug("Checking for recent transactions...", "recipient_ata", expectedRecipientATA.String())
			// Get recent signatures for the recipient ATA, as we expect an incoming transfer there.
			limitSignatures := 15
			signatures, err := rpcClient.GetSignaturesForAddressWithOpts(
				ctx,
				expectedRecipientATA, // Fetch history for the account we expect to receive funds
				&rpc.GetSignaturesForAddressOpts{
					Limit:      &limitSignatures,
					Commitment: rpc.CommitmentFinalized,
				},
			)
			if err != nil {
				logger.Error("Failed to get signatures for escrow ATA", "account", expectedRecipientATA.String(), "error", err)
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
				// Look for the specific transfer: from 'expectedSender', to 'expectedRecipientATA', correct USDC amount.
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

					// --- Verify Memo ---
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
							if memoContent == expectedMemo {
								memoMatches = true
								break // Found matching memo
							}
						}
					}
					logger.Debug("Memo check result", "signature", sig.String(), "found_memo", memoContent, "expected_memo", expectedMemo, "match", memoMatches)

					// if memo does not match, we don't need to check the balances
					if !memoMatches {
						logger.Debug("Memo does not match, skipping balance check", "signature", sig.String(), "expected_memo", expectedMemo, "found_memo", memoContent)
						continue
					}

					// --- Check 2: Verify Token Balances ---
					balancesMatch, err := checkTokenBalancesForTransfer(
						logger,
						txResult.Meta.PreTokenBalances,
						txResult.Meta.PostTokenBalances,
						accountKeys,
						expectedSender,
						expectedRecipientATA,
						usdcMint,
						expectedAmountUint64,
					)
					if err != nil {
						logger.Error("Error checking token balances for tx", "signature", sig.String(), "error", err)
						continue // Move to next signature
					}

					// --- Final Verification ---
					if balancesMatch && memoMatches {
						logger.Info("Matching payment transaction found (balances and memo match)",
							"signature", sig.String(),
							"from_owner", expectedSender.String(),
							"to_ata", expectedRecipientATA.String(),
							"amount_lamports", expectedAmountUint64)
						// We found the specific transaction we were looking for.
						return &VerifyPaymentResult{
							Verified: true,
							Amount:   expectedAmount, // Return the amount we were looking for
						}, nil
					}
				} else {
					logger.Debug("Transaction missing pre/post token balances, cannot verify via balance diff", "signature", sig.String())
				}
				// --- End Transaction Parsing ---
			}
			logger.Debug("Finished checking batch of signatures")
		}
	}
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
		// return fmt.Errorf("unexpected type for created_utc: %T", v)
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
		Thumbnail   string `json:"thumbnail"` // Added Thumbnail field mapping
	}

	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		// Log the raw data if unmarshal fails
		// logger := activity.GetLogger(context.Background()) // Need context for logger?
		// logger.Error("Failed to unmarshal Reddit aux struct", "error", err, "rawData", string(data))
		return fmt.Errorf("failed to unmarshal reddit aux struct: %w (data: %s)", err, string(data))
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
	r.Thumbnail = aux.Thumbnail // Copy the thumbnail field

	return nil
}

// PullRedditContent pulls content from Reddit
func (a *Activities) PullRedditContent(ctx context.Context, contentID string, contentKind ContentKind) (*RedditContent, error) {
	logger := activity.GetLogger(ctx)

	// Get fresh config
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration in PullRedditContent: %w", err)
	}
	redditDeps := cfg.RedditDeps // Use fetched deps

	switch contentKind {
	case ContentKindPost:
		contentID = "t3_" + strings.TrimPrefix(contentID, "t3_")
	case ContentKindComment:
		contentID = "t1_" + strings.TrimPrefix(contentID, "t1_")

	}

	logger.Info("Pulling Reddit content", "content_id", contentID)

	client := a.httpClient

	// Get auth token using fetched credentials
	token, err := getRedditToken(client, redditDeps)
	if err != nil {
		logger.Error("Failed to get Reddit token", "error", err, "clientId", redditDeps.ClientID)
		return nil, fmt.Errorf("failed to get Reddit token: %w", err)
	}
	// Log the token being used
	logger.Info("Using Reddit token", "token_prefix", token[:min(10, len(token))]+"...") // Log prefix for security

	// Create request
	url := fmt.Sprintf("https://oauth.reddit.com/api/info.json?id=%s", contentID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers using fetched deps
	req.Header.Set("User-Agent", redditDeps.UserAgent)
	req.Header.Set("Authorization", "Bearer "+token)

	// --- Debug Logging ---
	logger.Info("Making Reddit API request",
		"url", req.URL.String(),
		"user_agent", req.Header.Get("User-Agent"),
		"authorization_header", req.Header.Get("Authorization")) // Log the full header being sent
	// --- End Debug Logging ---

	// Make request
	resp, err := client.Do(req)
	if err != nil {
		// Log the status code even if making request fails
		logger.Error("Failed to make Reddit API request", "status_code", resp.StatusCode, "error", err)
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// Log the status code even if reading body fails
		logger.Error("Failed to read Reddit API response body", "status_code", resp.StatusCode, "error", err)
		return nil, fmt.Errorf("failed to read response body (status %d): %w", resp.StatusCode, err)
	}

	// Log the raw response for debugging
	logger.Debug("Reddit API Response", "status_code", resp.StatusCode, "response", string(body))

	// Check status code
	if resp.StatusCode != http.StatusOK {
		// Log the error response body for easier debugging, especially for 403
		if resp.StatusCode == http.StatusForbidden {
			// Use Warn for 403 to make it stand out
			logger.Warn("Reddit API returned 403 Forbidden", "status_code", resp.StatusCode, "response_body", string(body))
		} else {
			// Use Error for other non-OK statuses
			logger.Error("Reddit API returned non-OK status", "status_code", resp.StatusCode, "response_body", string(body))
		}
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
		// Log the full response if content is not found
		logger.Warn("No content found in Reddit response for ID", "content_id", contentID, "full_response", string(body))
		return nil, fmt.Errorf("no content found for ID %s", contentID)
	}

	// Convert the result to JSON and then to our struct
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JMESPath result: %w", err)
	}

	// Log the extracted data for debugging
	logger.Debug("Extracted Reddit Data", "data", string(resultJSON))

	var content RedditContent
	if err := json.Unmarshal(resultJSON, &content); err != nil {
		return nil, fmt.Errorf("failed to unmarshal content: %w (extracted JSON: %s)", err, string(resultJSON))
	}

	// Set additional fields
	// content.ID should come from the JSON data itself, verify if necessary
	// The ID passed in (contentID) might be the prefixed one (e.g., t3_...). Ensure the struct field matches the data.
	// If `content.ID` is populated correctly by UnmarshalJSON, we might not need this line.
	// For now, assume UnmarshalJSON populates it correctly based on the `id` field in the response.
	content.IsComment = strings.HasPrefix(contentID, "t1_") // Use the passed-in ID for type check

	return &content, nil
}

// Helper function to prevent panics on short tokens in logging
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- Bounty Publisher Activity ---

// PublishBountiesReddit fetches bounties from the ABB server and posts them to Reddit.
func (a *Activities) PublishBountiesReddit(ctx context.Context) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Running PublishBountiesReddit activity...")

	cfg, err := getConfiguration(ctx)
	if err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	// Use the shared httpClient from the Activities struct
	client := a.httpClient

	// 1. Get ABB Auth Token
	abbToken, err := a.getABBAuthToken(ctx, logger, cfg, client)
	if err != nil {
		logger.Error("Failed to get ABB auth token", "error", err)
		return err // Return error to let Temporal handle retries
	}
	logger.Debug("Obtained ABB auth token")

	// 2. Fetch Bounties
	bounties, err := a.fetchBounties(ctx, logger, cfg, client, abbToken)
	if err != nil {
		logger.Error("Failed to fetch bounties", "error", err)
		return err
	}
	if len(bounties) == 0 {
		logger.Info("No active bounties found to post.")
		return nil // Not an error, just nothing to do
	}
	logger.Info("Fetched bounties", "count", len(bounties))

	// 3. Format Bounties
	postTitle, postBody, err := a.formatBountiesForReddit(bounties, cfg.PublicBaseURL)
	if err != nil {
		logger.Error("Failed to format bounties", "error", err)
		return fmt.Errorf("internal error formatting bounties: %w", err) // Non-retryable likely
	}
	logger.Debug("Formatted bounties for Reddit post")

	// 4. Get Reddit Auth Token
	redditToken, err := a.getRedditToken(ctx, logger, cfg, client)
	if err != nil {
		logger.Error("Failed to get Reddit auth token", "error", err)
		return err
	}
	logger.Debug("Obtained Reddit auth token")

	// 5. Post to Reddit
	err = a.postToReddit(ctx, logger, cfg, client, redditToken, postTitle, postBody, cfg.RedditFlairID)
	if err != nil {
		logger.Error("Failed to post to Reddit", "error", err)
		return err
	}

	logger.Info("Successfully posted bounties to Reddit", "subreddit", cfg.PublishTargetSubreddit)
	return nil
}

// --- Helper methods for PublishBountiesReddit (moved into Activities struct) ---

// Fetches an auth token from the ABB /token endpoint
func (a *Activities) getABBAuthToken(ctx context.Context, logger temporal_log.Logger, cfg *Configuration, client *http.Client) (string, error) {
	form := url.Values{}
	form.Add("username", "temporal-bounty-poster") // Use a specific username
	form.Add("password", cfg.ABBServerSecretKey)

	tokenURL := fmt.Sprintf("%s/token", strings.TrimSuffix(cfg.ABBServerURL, "/"))
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

	var tokenResp api.DefaultJSONResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode ABB token response: %w", err)
	}

	if tokenResp.Message == "" {
		return "", fmt.Errorf("ABB token response did not contain a token in the message field")
	}

	return tokenResp.Message, nil
}

// Fetches the list of bounties from the ABB /bounties endpoint
func (a *Activities) fetchBounties(ctx context.Context, logger temporal_log.Logger, cfg *Configuration, client *http.Client, token string) ([]api.BountyListItem, error) {
	bountiesURL := fmt.Sprintf("%s/bounties", strings.TrimSuffix(cfg.ABBServerURL, "/"))
	req, err := http.NewRequestWithContext(ctx, "GET", bountiesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create bounties request: %w", err)
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

	var bounties []api.BountyListItem
	if err := json.NewDecoder(resp.Body).Decode(&bounties); err != nil {
		// Log the body for debugging decode errors
		bodyBytes, _ := io.ReadAll(resp.Body) // Reread might be needed if decoder consumed it
		logger.Error("Failed to decode bounties response", "error", err, "response_body", string(bodyBytes))
		return nil, fmt.Errorf("failed to decode bounties response: %w", err)
	}

	return bounties, nil
}

// Formats the list of bounties into a Reddit post title and body (Markdown)
func (a *Activities) formatBountiesForReddit(bounties []api.BountyListItem, publicBaseURL string) (string, string, error) {
	// Use the public facing domain for links from parameter
	// publicBaseURL := cfg.PublicBaseURL // Removed direct cfg access

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
		// Construct link using publicBaseURL
		line := fmt.Sprintf("| [%s](%s/bounties/%s) | %s | $%.2f | $%.2f | %s |\n",
			b.WorkflowID,
			publicBaseURL,
			b.WorkflowID,
			b.PlatformType,
			b.BountyPerPost,
			b.TotalBounty,
			reqSummary,
		)
		body.WriteString(line)
	}
	return title, body.String(), nil
}

// Gets an OAuth2 token from Reddit
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

// Posts the formatted content to the specified subreddit
func (a *Activities) postToReddit(ctx context.Context, logger temporal_log.Logger, cfg *Configuration, client *http.Client, token, title, body, flairID string) error {
	submitURL := "https://oauth.reddit.com/api/submit"

	form := url.Values{}
	form.Add("api_type", "json")
	form.Add("kind", "self")
	form.Add("sr", cfg.PublishTargetSubreddit)
	form.Add("title", title)
	form.Add("text", body)
	form.Add("flair_id", flairID)

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
		return fmt.Errorf("reddit submit request returned status %d", resp.StatusCode)
	}

	var result struct {
		JSON struct {
			Errors [][]string `json:"errors"`
			Data   struct {
				URL string `json:"url"`
			} `json:"data"`
		} `json:"json"`
	}

	if err := json.Unmarshal(respBodyBytes, &result); err != nil {
		logger.Warn("Failed to decode reddit submit response JSON, but status was OK", "error", err, "response_body", string(respBodyBytes))
	} else if len(result.JSON.Errors) > 0 {
		logger.Error("Reddit API reported errors after submission", "errors", result.JSON.Errors, "response_body", string(respBodyBytes))
		return fmt.Errorf("reddit API returned errors: %v", result.JSON.Errors)
	} else {
		logger.Info("Reddit post submitted successfully", "post_url", result.JSON.Data.URL)
	}

	return nil
}

// --- Twitch Content Structures ---

// TwitchVideoContent represents the extracted content for a Twitch Video (VOD/Archive)
type TwitchVideoContent struct {
	ID            string      `json:"id"`
	StreamID      string      `json:"stream_id,omitempty"` // May not always be present
	UserID        string      `json:"user_id"`
	UserLogin     string      `json:"user_login"`
	UserName      string      `json:"user_name"`
	Title         string      `json:"title"`
	Description   string      `json:"description"`
	CreatedAt     time.Time   `json:"created_at"`
	PublishedAt   time.Time   `json:"published_at"`
	URL           string      `json:"url"`
	ThumbnailURL  string      `json:"thumbnail_url"`
	Viewable      string      `json:"viewable"`
	ViewCount     int64       `json:"view_count"` // Using int64 for direct parsing
	Language      string      `json:"language"`
	Type          string      `json:"type"`     // e.g., "archive", "highlight", "upload"
	Duration      string      `json:"duration"` // Twitch API provides duration as string (e.g., "3h15m20s")
	MutedSegments *[]struct { // Pointer to slice, can be null
		Duration int `json:"duration"`
		Offset   int `json:"offset"`
	} `json:"muted_segments"`
}

// TwitchClipContent represents the extracted content for a Twitch Clip
type TwitchClipContent struct {
	ID              string    `json:"id"`
	URL             string    `json:"url"`
	EmbedURL        string    `json:"embed_url"`
	BroadcasterID   string    `json:"broadcaster_id"`
	BroadcasterName string    `json:"broadcaster_name"`
	CreatorID       string    `json:"creator_id"`
	CreatorName     string    `json:"creator_name"`
	VideoID         string    `json:"video_id,omitempty"` // Can be empty if the VOD is deleted
	GameID          string    `json:"game_id"`
	Language        string    `json:"language"`
	Title           string    `json:"title"`
	ViewCount       int64     `json:"view_count"` // Using int64
	CreatedAt       time.Time `json:"created_at"`
	ThumbnailURL    string    `json:"thumbnail_url"`
	Duration        float64   `json:"duration"`   // Duration in seconds
	VODOffset       *int      `json:"vod_offset"` // Offset in seconds from the start of the VOD (can be null)
}

// PullTwitchContent pulls content from Twitch (Video or Clip)
func (a *Activities) PullTwitchContent(ctx context.Context, contentID string, contentKind ContentKind) (interface{}, error) {
	logger := activity.GetLogger(ctx)

	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration in PullTwitchContent: %w", err)
	}
	twitchDeps := cfg.TwitchDeps
	if twitchDeps.ClientID == "" || twitchDeps.ClientSecret == "" { // Changed check
		return nil, fmt.Errorf("twitch ClientID or ClientSecret not configured")
	}

	logger.Info("Pulling Twitch content", "content_id", contentID, "content_kind", contentKind)

	// --- Fetch App Access Token --- dynamically
	accessToken, err := getTwitchAppAccessToken(a.httpClient, twitchDeps)
	if err != nil {
		return nil, fmt.Errorf("failed to get Twitch App Access Token: %w", err)
	}
	// ---------------------------------

	var apiURL string
	params := url.Values{}
	params.Add("id", contentID) // Use the 'id' parameter for both videos and clips

	switch contentKind {
	case ContentKindVideo:
		apiURL = "https://api.twitch.tv/helix/videos"
	case ContentKindClip:
		apiURL = "https://api.twitch.tv/helix/clips"
	default:
		return nil, fmt.Errorf("unsupported Twitch content kind: %s", contentKind)
	}

	fullURL := apiURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Twitch API request: %w", err)
	}

	// Set required headers
	req.Header.Set("Client-Id", twitchDeps.ClientID)
	req.Header.Set("Authorization", "Bearer "+accessToken) // Use dynamically fetched token
	req.Header.Set("Accept", "application/json")

	// Use the shared httpClient
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make Twitch API request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Twitch API response body: %w", err)
	}

	logger.Debug("Twitch API Response", "status_code", resp.StatusCode, "response", string(body))

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Twitch API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response - Structure is {"data": [item], "pagination": {}}
	var responseData struct {
		Data       []json.RawMessage `json:"data"`
		Pagination struct{}          `json:"pagination"` // Ignore pagination for single ID lookup
	}

	if err := json.Unmarshal(body, &responseData); err != nil {
		return nil, fmt.Errorf("failed to decode Twitch API response structure: %w (body: %s)", err, string(body))
	}

	if len(responseData.Data) == 0 {
		return nil, fmt.Errorf("no content found for ID %s (Kind: %s)", contentID, contentKind)
	}

	// Unmarshal the actual content item based on Kind
	contentItemJSON := responseData.Data[0]
	switch contentKind {
	case ContentKindVideo:
		var videoContent TwitchVideoContent
		if err := json.Unmarshal(contentItemJSON, &videoContent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal Twitch video content: %w (json: %s)", err, string(contentItemJSON))
		}
		return &videoContent, nil // Return pointer to the struct
	case ContentKindClip:
		var clipContent TwitchClipContent
		if err := json.Unmarshal(contentItemJSON, &clipContent); err != nil {
			return nil, fmt.Errorf("failed to unmarshal Twitch clip content: %w (json: %s)", err, string(contentItemJSON))
		}
		return &clipContent, nil // Return pointer to the struct
	default:
		// Should be caught earlier, but defensive check
		return nil, fmt.Errorf("internal error: unsupported Twitch content kind reached parsing: %s", contentKind)
	}
}

// TwitchDependencies holds the dependencies for Twitch-related activities
type TwitchDependencies struct {
	ClientID     string
	ClientSecret string // Changed from AppAccessToken
}

// Type returns the platform type for TwitchDependencies
func (deps TwitchDependencies) Type() PlatformKind {
	return PlatformTwitch
}

// MarshalJSON implements json.Marshaler for TwitchDependencies
func (deps TwitchDependencies) MarshalJSON() ([]byte, error) {
	type Aux struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"` // Changed
	}
	aux := Aux{
		ClientID:     deps.ClientID,
		ClientSecret: deps.ClientSecret, // Changed
	}
	return json.Marshal(aux)
}

// UnmarshalJSON implements json.Unmarshaler for TwitchDependencies
func (deps *TwitchDependencies) UnmarshalJSON(data []byte) error {
	type Aux struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"` // Changed
	}
	var aux Aux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	deps.ClientID = aux.ClientID
	deps.ClientSecret = aux.ClientSecret // Changed
	return nil
}

// getTwitchAppAccessToken obtains an App Access Token from Twitch using Client Credentials Flow
func getTwitchAppAccessToken(client *http.Client, deps TwitchDependencies) (string, error) {
	formData := url.Values{
		"client_id":     {deps.ClientID},
		"client_secret": {deps.ClientSecret},
		"grant_type":    {"client_credentials"},
	}

	tokenURL := "https://id.twitch.tv/oauth2/token"
	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create Twitch token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Twitch token request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read Twitch token response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Twitch token request returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"` // We are not using expiry for now, fetching fresh token each time
		TokenType   string `json:"token_type"`
	}

	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode Twitch token response: %w (body: %s)", err, string(bodyBytes))
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("Twitch token response did not contain an access token")
	}

	// Could potentially log expiry/type if needed for debugging
	// logger.Debug("Fetched Twitch App Access Token", "expires_in", tokenResp.ExpiresIn, "type", tokenResp.TokenType)

	return tokenResp.AccessToken, nil
}

// AnalyzeImageURL downloads an image from a URL and uses a configured image LLM
// to analyze it based on a provided text prompt, returning a structured result.
func (a *Activities) AnalyzeImageURL(ctx context.Context, imageUrl string, prompt string) (CheckContentRequirementsResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Starting image analysis", "imageUrl", imageUrl)

	// --- Load Image LLM Configuration --- (Directly using os.Getenv for simplicity here)
	providerName := os.Getenv(EnvLLMImageProvider)
	apiKey := os.Getenv(EnvLLMImageAPIKey)
	modelName := os.Getenv(EnvLLMImageModel)

	if providerName == "" || apiKey == "" || modelName == "" {
		logger.Error("Image LLM environment variables not fully configured",
			"provider_env", EnvLLMImageProvider, "key_env", EnvLLMImageAPIKey, "model_env", EnvLLMImageModel)
		// Fail the activity if essential config is missing
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Image LLM provider configuration incomplete (check env vars %s, %s, %s)",
				EnvLLMImageProvider, EnvLLMImageAPIKey, EnvLLMImageModel)}, fmt.Errorf("image LLM provider configuration incomplete (check env vars %s, %s, %s)",
				EnvLLMImageProvider, EnvLLMImageAPIKey, EnvLLMImageModel)
	}

	// --- Create Image LLM Provider --- (Using a hypothetical NewImageLLMProvider)
	// We'll need to implement this provider logic in llm.go
	// For now, assume it takes a simple config struct
	imageLLMConfig := ImageLLMConfig{
		Provider: providerName,
		APIKey:   apiKey,
		Model:    modelName,
		// Add other potential image-specific config here (e.g., detail level for vision models)
	}
	imageProvider, err := NewImageLLMProvider(imageLLMConfig) // Needs implementation in llm.go
	if err != nil {
		logger.Error("Failed to create image LLM provider", "error", err)
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Image LLM provider failed: %v", err)}, fmt.Errorf("failed to create image LLM provider: %w", err)
	}

	// --- Download Image ---
	logger.Debug("Downloading image", "url", imageUrl)
	// Use the shared httpClient, but create a new request specific to this activity context
	req, err := http.NewRequestWithContext(ctx, "GET", imageUrl, nil)
	if err != nil {
		logger.Error("Failed to create image download request", "url", imageUrl, "error", err)
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Failed to download image: %v", err)}, fmt.Errorf("failed to create image request for %s: %w", imageUrl, err)
	}
	// Add a generic User-Agent
	req.Header.Set("User-Agent", "AffiliateBountyBoard-Worker/1.0")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		logger.Error("Failed to download image", "url", imageUrl, "error", err)
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Failed to download image: %v", err)}, fmt.Errorf("failed to download image from %s: %w", imageUrl, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Error("Failed to download image, bad status", "url", imageUrl, "status_code", resp.StatusCode)
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Failed to download image, status: %d", resp.StatusCode)}, fmt.Errorf("failed to download image from %s, status: %d", imageUrl, resp.StatusCode)
	}

	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read image data", "url", imageUrl, "error", err)
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Failed to read image data: %v", err)}, fmt.Errorf("failed to read image data from %s: %w", imageUrl, err)
	}
	logger.Debug("Image downloaded successfully", "url", imageUrl, "size_bytes", len(imageData))

	// --- Analyze Image using Image LLM Provider ---
	logger.Info("Sending image data to image LLM for analysis", "provider", providerName, "model", modelName)
	analysisResult, err := imageProvider.AnalyzeImage(ctx, imageData, prompt)
	if err != nil {
		logger.Error("Image LLM analysis failed", "error", err)
		// Return a default 'false' result along with the error
		return CheckContentRequirementsResult{Satisfies: false, Reason: fmt.Sprintf("Image LLM provider failed: %v", err)}, fmt.Errorf("image LLM analysis failed: %w", err)
	}

	logger.Info("Image analysis successful", "satisfies", analysisResult.Satisfies, "reason", analysisResult.Reason)
	return analysisResult, nil // Return the structured result and nil error
}

// EmailConfig holds configuration for sending emails.
type EmailConfig struct {
	Provider string `json:"provider"` // e.g., "sendgrid", "ses"
	APIKey   string `json:"api_key"`
	Sender   string `json:"sender"` // The 'From' email address
}

// SendTokenEmail sends an email containing a token to the specified address.
func (a *Activities) SendTokenEmail(ctx context.Context, email string, token string) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Sending token email", "email", email, "token", token)

	// Get SMTP server and port from env
	smtpServer := os.Getenv(EnvEmailSMTP)
	if smtpServer == "" {
		return fmt.Errorf("EMAIL_SMTP environment variable not set")
	}
	smtpPort := os.Getenv(EnvEmailSMTPPort)
	if smtpPort == "" {
		return fmt.Errorf("EMAIL_SMTP_PORT environment variable not set")
	}

	// Get password from env
	pwd := os.Getenv(EnvEmailPassword)
	if pwd == "" {
		return fmt.Errorf("EMAIL_PASSWORD environment variable not set")
	}

	// Get sender email from env
	sender := os.Getenv(EnvEmailSender)
	if sender == "" {
		return fmt.Errorf("EMAIL_SENDER environment variable not set")
	}

	// Send using vanilla smtp client
	auth := smtp.PlainAuth("", sender, pwd, smtpServer)
	msg := []byte("To: " + email + "\r\n" +
		"Subject: Your IncentivizeThis Token\r\n" +
		"\r\n" +
		"Your token is: " + token + "\r\n")
	err := smtp.SendMail(smtpServer+":"+smtpPort, auth, sender, []string{email}, msg)
	if err != nil {
		return fmt.Errorf("failed to send email: %w", err)
	}

	return nil
}

// Maximum character limits for LLM inputs
const (
	MaxContentCharsForLLMCheck      = 20000 // Approx 5k tokens
	MaxRequirementsCharsForLLMCheck = 5000  // Approx 1.25k tokens
)
