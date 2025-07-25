package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/db/dbgen"
	"github.com/brojonat/affiliate-bounty-board/http/api"
	"github.com/brojonat/affiliate-bounty-board/internal/stools"
	solanago "github.com/gagliardetto/solana-go"
	solanarpc "github.com/gagliardetto/solana-go/rpc"
	"github.com/gorilla/handlers"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
)

// Environment Variable Keys
const (
	EnvServerSecretKey                   = "ABB_SECRET_KEY"
	EnvServerEnv                         = "ENV"
	EnvTaskQueue                         = "TASK_QUEUE"
	EnvUserRevenueSharePct               = "USER_REVENUE_SHARE_PCT"
	EnvPublishTargetSubreddit            = "PUBLISH_TARGET_SUBREDDIT"
	EnvPeriodicPublisherScheduleID       = "PERIODIC_PUBLISHER_SCHEDULE_ID"
	EnvPeriodicPublisherScheduleInterval = "PERIODIC_PUBLISHER_SCHEDULE_INTERVAL"
	EnvSolanaEscrowPrivateKey            = "SOLANA_ESCROW_PRIVATE_KEY"
	EnvSolanaEscrowWallet                = "SOLANA_ESCROW_WALLET"
	EnvSolanaRPCEndpoint                 = "SOLANA_RPC_ENDPOINT"
	EnvSolanaWSEndpoint                  = "SOLANA_WS_ENDPOINT"
	EnvSolanaUSDCMintAddress             = "SOLANA_USDC_MINT_ADDRESS"
	EnvSolanaTreasuryWallet              = "SOLANA_TREASURY_WALLET"
	EnvAbbDatabaseURL                    = "ABB_DATABASE_URL"
	EnvLLMEmbeddingModelName             = "LLM_EMBEDDING_MODEL"
	EnvLLMProvider                       = "LLM_PROVIDER"
	EnvLLMModel                          = "LLM_MODEL"
	EnvLLMAPIKey                         = "LLM_API_KEY"
	EnvLLMMaxTokens                      = "LLM_MAX_TOKENS"
	EnvLLMInferBountyTitlePrompt         = "LLM_PROMPT_INFER_BOUNTY_TITLE_B64"
	EnvLLMInferContentParamsPrompt       = "LLM_PROMPT_INFER_CONTENT_PARAMS_B64"
	EnvLLMContentModerationPrompt        = "LLM_PROMPT_CONTENT_MODERATION_B64"
	EnvDefaultRateLimitPerMinute         = "RATE_LIMIT_DEFAULT_PER_MINUTE"
	EnvLLMRateLimitPerMinute             = "RATE_LIMIT_LLM_PER_MINUTE"

	DefaultLLMMaxTokens = 10000 // Default max tokens if not set
)

// Config holds all the configuration for the HTTP server, loaded from environment variables.
type Config struct {
	SecretKey           string
	Environment         string
	TaskQueue           string
	UserRevenueSharePct float64
	DatabaseURL         string
	Solana              struct {
		RPCEndpoint     string
		EscrowWallet    solanago.PublicKey
		USDCMintAddress solanago.PublicKey
	}
	LLM struct {
		Provider       string
		Model          string
		APIKey         string
		MaxTokens      int
		EmbeddingModel string
	}
	Prompts struct {
		InferBountyTitle   string
		InferContentParams string
		ContentModeration  string
	}
	CORS struct {
		AllowedOrigins []string
		AllowedMethods []string
		AllowedHeaders []string
	}
	RateLimit struct {
		DefaultPerMinute int
		LLMPerMinute     int
	}
}

// NewConfigFromEnv creates a new Config struct populated from environment variables.
func NewConfigFromEnv(logger *slog.Logger) (*Config, error) {
	cfg := &Config{}

	// Core Config
	cfg.SecretKey = os.Getenv(EnvServerSecretKey)
	if cfg.SecretKey == "" {
		return nil, fmt.Errorf("server startup error: %s not set", EnvServerSecretKey)
	}
	cfg.Environment = os.Getenv(EnvServerEnv)
	if cfg.Environment == "" {
		return nil, fmt.Errorf("server startup error: %s not set", EnvServerEnv)
	}
	cfg.TaskQueue = os.Getenv(EnvTaskQueue)
	if cfg.TaskQueue == "" {
		return nil, fmt.Errorf("server startup error: %s not set", EnvTaskQueue)
	}
	cfg.DatabaseURL = os.Getenv(EnvAbbDatabaseURL)
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("server startup error: %s not set", EnvAbbDatabaseURL)
	}

	// Payout Config
	pctStr := os.Getenv(EnvUserRevenueSharePct)
	if pctStr == "" {
		return nil, fmt.Errorf("server startup error: %s not set", EnvUserRevenueSharePct)
	}
	pct, err := strconv.ParseFloat(pctStr, 64)
	if err != nil || pct < 0 || pct > 100 {
		return nil, fmt.Errorf("server startup error: invalid value for %s: '%s'", EnvUserRevenueSharePct, pctStr)
	}
	cfg.UserRevenueSharePct = pct

	// Solana Config
	cfg.Solana.RPCEndpoint = os.Getenv(EnvSolanaRPCEndpoint)
	if cfg.Solana.RPCEndpoint == "" {
		return nil, fmt.Errorf("server startup error: %s not set", EnvSolanaRPCEndpoint)
	}
	escrowWalletStr := os.Getenv(EnvSolanaEscrowWallet)
	if escrowWalletStr == "" {
		return nil, fmt.Errorf("server startup error: %s not set", EnvSolanaEscrowWallet)
	}
	escrowWallet, err := solanago.PublicKeyFromBase58(escrowWalletStr)
	if err != nil {
		return nil, fmt.Errorf("server startup error: failed to parse escrow wallet public key '%s': %w", escrowWalletStr, err)
	}
	cfg.Solana.EscrowWallet = escrowWallet

	usdcMintAddressStr := os.Getenv(EnvSolanaUSDCMintAddress)
	if usdcMintAddressStr == "" {
		return nil, fmt.Errorf("server startup error: %s not set", EnvSolanaUSDCMintAddress)
	}
	usdcMintAddress, err := solanago.PublicKeyFromBase58(usdcMintAddressStr)
	if err != nil {
		return nil, fmt.Errorf("server startup error: failed to parse USDC mint address '%s': %w", usdcMintAddressStr, err)
	}
	cfg.Solana.USDCMintAddress = usdcMintAddress

	// LLM Config
	cfg.LLM.Provider = os.Getenv(EnvLLMProvider)
	cfg.LLM.Model = os.Getenv(EnvLLMModel)
	cfg.LLM.APIKey = os.Getenv(EnvLLMAPIKey)
	cfg.LLM.EmbeddingModel = os.Getenv(EnvLLMEmbeddingModelName)
	if cfg.LLM.Provider == "" || cfg.LLM.Model == "" || cfg.LLM.APIKey == "" || cfg.LLM.EmbeddingModel == "" {
		return nil, fmt.Errorf("server startup error: LLM configuration (Provider, Model, APIKey, EmbeddingModel) not fully set")
	}

	maxTokensStr := os.Getenv(EnvLLMMaxTokens)
	if maxTokensStr == "" {
		return nil, fmt.Errorf("server startup error: %s not set", EnvLLMMaxTokens)
	}
	maxTokens, errAtoi := strconv.Atoi(maxTokensStr)
	if errAtoi != nil || maxTokens <= 0 {
		return nil, fmt.Errorf("server startup error: invalid value for %s: '%s'", EnvLLMMaxTokens, maxTokensStr)
	}
	cfg.LLM.MaxTokens = maxTokens

	// Prompts
	inferTitlePrompt, err := decodeBase64(os.Getenv(EnvLLMInferBountyTitlePrompt))
	if err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", EnvLLMInferBountyTitlePrompt, err)
	}
	cfg.Prompts.InferBountyTitle = inferTitlePrompt

	inferParamsPrompt, err := decodeBase64(os.Getenv(EnvLLMInferContentParamsPrompt))
	if err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", EnvLLMInferContentParamsPrompt, err)
	}
	cfg.Prompts.InferContentParams = inferParamsPrompt

	contentModerationPrompt, err := decodeBase64(os.Getenv(EnvLLMContentModerationPrompt))
	if err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", EnvLLMContentModerationPrompt, err)
	}
	cfg.Prompts.ContentModeration = contentModerationPrompt

	// Rate Limiting Config
	defaultRateLimitStr := os.Getenv(EnvDefaultRateLimitPerMinute)
	if defaultRateLimitStr == "" {
		defaultRateLimitStr = "100" // Default to 100 requests per minute
	}
	defaultRateLimit, err := strconv.Atoi(defaultRateLimitStr)
	if err != nil || defaultRateLimit < 0 {
		return nil, fmt.Errorf("invalid value for %s: '%s'", EnvDefaultRateLimitPerMinute, defaultRateLimitStr)
	}
	cfg.RateLimit.DefaultPerMinute = defaultRateLimit

	llmRateLimitStr := os.Getenv(EnvLLMRateLimitPerMinute)
	if llmRateLimitStr == "" {
		llmRateLimitStr = "20" // Default to 20 requests per minute for expensive routes
	}
	llmRateLimit, err := strconv.Atoi(llmRateLimitStr)
	if err != nil || llmRateLimit < 0 {
		return nil, fmt.Errorf("invalid value for %s: '%s'", EnvLLMRateLimitPerMinute, llmRateLimitStr)
	}
	cfg.RateLimit.LLMPerMinute = llmRateLimit

	// CORS Config
	allowedOriginsEnv := os.Getenv("CORS_ORIGINS")
	if allowedOriginsEnv == "" {
		return nil, fmt.Errorf("server startup error: CORS_ORIGINS not set")
	}
	if allowedOriginsEnv == "*" {
		cfg.CORS.AllowedOrigins = []string{"*"}
		logger.Warn("CORS configured to allow all origins (*)")
	} else if allowedOriginsEnv != "" {
		cfg.CORS.AllowedOrigins = strings.Split(allowedOriginsEnv, ",")
		logger.Info("CORS configured with specific origins", "origins", cfg.CORS.AllowedOrigins)
	} else {
		// This case is now covered by the check above, but we keep the block for clarity
		// on what happens with an empty but present variable if needed in the future.
		// For now, it's effectively dead code.
		cfg.CORS.AllowedOrigins = []string{}
	}

	allowedMethodsEnv := os.Getenv("CORS_METHODS")
	if allowedMethodsEnv != "" {
		cfg.CORS.AllowedMethods = strings.Split(allowedMethodsEnv, ",")
	} else {
		cfg.CORS.AllowedMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"} // Default
	}

	allowedHeadersEnv := os.Getenv("CORS_HEADERS")
	if allowedHeadersEnv != "" {
		cfg.CORS.AllowedHeaders = strings.Split(allowedHeadersEnv, ",")
	} else {
		cfg.CORS.AllowedHeaders = []string{"Authorization", "Content-Type", "X-Requested-With"} // Default
	}

	return cfg, nil
}

type corsConfigKey struct{}

// GetCORSConfig retrieves CORS configuration from the context
func GetCORSConfig(ctx context.Context) (headers, methods, origins []string) {
	if v := ctx.Value(corsConfigKey{}); v != nil {
		config := v.(struct {
			headers []string
			methods []string
			origins []string
		})
		return config.headers, config.methods, config.origins
	}
	return nil, nil, nil
}

// WithCORSConfig adds CORS configuration to the context
func WithCORSConfig(ctx context.Context, headers, methods, origins []string) context.Context {
	return context.WithValue(ctx, corsConfigKey{}, struct {
		headers []string
		methods []string
		origins []string
	}{headers, methods, origins})
}

func writeOK(w http.ResponseWriter) {
	resp := api.DefaultJSONResponse{Message: "ok"}
	writeJSONResponse(w, resp, http.StatusOK)
}

func writeInternalError(l *slog.Logger, w http.ResponseWriter, e error) {
	l.Error("internal error", "error", e.Error())
	resp := api.DefaultJSONResponse{Error: "internal error"}
	writeJSONResponse(w, resp, http.StatusInternalServerError)
}

func writeBadRequestError(w http.ResponseWriter, err error) {
	resp := api.DefaultJSONResponse{Error: err.Error()}
	writeJSONResponse(w, resp, http.StatusBadRequest)
}

func writeNotFoundError(w http.ResponseWriter) {
	resp := api.DefaultJSONResponse{Error: "not found"}
	writeJSONResponse(w, resp, http.StatusNotFound)
}

func writeEmptyResultError(w http.ResponseWriter) {
	resp := api.DefaultJSONResponse{Error: "empty result set"}
	writeJSONResponse(w, resp, http.StatusNotFound)
}

func writeUnauthorized(w http.ResponseWriter) {
	resp := api.DefaultJSONResponse{Error: "unauthorized"}
	writeJSONResponse(w, resp, http.StatusUnauthorized)
}

func writePaymentRequired(w http.ResponseWriter) {
	resp := api.DefaultJSONResponse{Error: "payment required"}
	writeJSONResponse(w, resp, http.StatusPaymentRequired)
}

func writeMethodNotAllowedError(w http.ResponseWriter) {
	resp := api.DefaultJSONResponse{Error: "method not allowed"}
	writeJSONResponse(w, resp, http.StatusMethodNotAllowed)
}

func writeJSONResponse(w http.ResponseWriter, resp interface{}, code int) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(resp)
}

// getConnPool establishes a connection pool to the database with retries.
func getConnPool(ctx context.Context, dbURL string, logger *slog.Logger, maxRetries int, retryInterval time.Duration) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool
	var err error

	for i := 0; i < maxRetries; i++ {
		cfg, parseErr := pgxpool.ParseConfig(dbURL)
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse database URL: %w", parseErr)
		}

		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			// Ping the database to ensure connectivity
			pingErr := pool.Ping(ctx)
			if pingErr == nil {
				return pool, nil
			}
			// If ping fails, set err and close the potentially created pool before retrying
			err = fmt.Errorf("failed to ping database: %w", pingErr)
			pool.Close() // Close the pool as ping failed
			pool = nil   // Set pool to nil as it's not usable
		} // If NewWithConfig failed, err is already set

		logger.Error("Failed to connect to database", "attempt", i+1, "max_attempts", maxRetries, "error", err)
		if i < maxRetries-1 {
			logger.Info("Retrying database connection", "interval", retryInterval)
			time.Sleep(retryInterval)
		}
	}
	return nil, fmt.Errorf("could not connect to database after %d attempts: %w", maxRetries, err)
}

// RunServer starts the HTTP server with the given configuration
func RunServer(ctx context.Context, logger *slog.Logger, tc client.Client, port string) error {
	cfg, err := NewConfigFromEnv(logger)
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	mux := http.NewServeMux()

	ctx = WithCORSConfig(ctx, cfg.CORS.AllowedHeaders, cfg.CORS.AllowedMethods, cfg.CORS.AllowedOrigins)

	rpcClient := solanarpc.New(cfg.Solana.RPCEndpoint)
	_, err = rpcClient.GetHealth(ctx)
	if err != nil {
		return fmt.Errorf("server startup error: Solana RPC health check failed for %s: %w", cfg.Solana.RPCEndpoint, err)
	}
	logger.Debug("Successfully connected to Solana RPC", "endpoint", cfg.Solana.RPCEndpoint)

	// Create Rate Limiter for JWT-based assessment endpoint
	jwtAssessLimiter := NewRateLimiter(1*time.Hour, 10) // 10 requests per hour per JWT

	// --- Database Connection ---
	var querier dbgen.Querier // Define querier, to be initialized if dbURL is set
	var dbPool *pgxpool.Pool

	dbPool, errDb := getConnPool(ctx, cfg.DatabaseURL, logger, 5, 5*time.Second)
	if errDb != nil {
		return fmt.Errorf("failed to connect to database: %w", errDb)
	}
	querier = dbgen.New(dbPool) // Initialize querier with the pool
	defer dbPool.Close()
	logger.Info("Database connection established.")

	// --- Create shared rate limiters ---
	defaultRateLimiter := NewRateLimiter(1*time.Minute, cfg.RateLimit.DefaultPerMinute)
	llmRateLimiter := NewRateLimiter(1*time.Minute, cfg.RateLimit.LLMPerMinute)

	// --- Initialize LLMProviders ---
	// standard provider
	llmProvider, err := abb.NewLLMProvider(abb.LLMConfig{
		Provider:  cfg.LLM.Provider,
		APIKey:    cfg.LLM.APIKey,
		Model:     cfg.LLM.Model,
		MaxTokens: cfg.LLM.MaxTokens,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize LLM Provider: %w", err)
	}

	// embedding provider
	llmEmbedProvider, err := abb.NewLLMEmbeddingProvider(abb.EmbeddingConfig{
		Provider: cfg.LLM.Provider,
		APIKey:   cfg.LLM.APIKey,
		Model:    cfg.LLM.EmbeddingModel,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize LLM Embedding Provider: %w", err)
	}

	// Add routes
	mux.HandleFunc("GET /ping", stools.AdaptHandler(
		handlePing(),
		withLogging(logger),
	))

	mux.HandleFunc("GET /config", stools.AdaptHandler(
		handleGetConfig(cfg.Solana.USDCMintAddress.String(), cfg.Solana.EscrowWallet.String()),
		apiMode(logger, defaultRateLimiter, 1024*1024, cfg.CORS.AllowedHeaders, cfg.CORS.AllowedMethods, cfg.CORS.AllowedOrigins),
		withLogging(logger),
	))

	mux.HandleFunc("POST /token", stools.AdaptHandler(
		handleIssueSudoToken(logger),
		withLogging(logger),
		atLeastOneAuth(oauthAuthorizerForm(getSecretKey)),
	))

	mux.HandleFunc("POST /token/user", stools.AdaptHandler(
		handleIssueUserToken(logger),
		withLogging(logger),
		atLeastOneAuth(oauthAuthorizerForm(getSecretKey)),
	))

	// Route for getting a specific bounty by ID
	mux.HandleFunc("GET /bounties/{id}", stools.AdaptHandler(
		handleGetBountyByID(logger, tc),
		apiMode(logger, defaultRateLimiter, 1024*1024, cfg.CORS.AllowedHeaders, cfg.CORS.AllowedMethods, cfg.CORS.AllowedOrigins),
		withLogging(logger),
	))

	// listing bounties routes
	mux.HandleFunc("GET /bounties", stools.AdaptHandler(
		handleListBounties(logger, tc, cfg.Environment),
		apiMode(logger, defaultRateLimiter, 1024*1024, cfg.CORS.AllowedHeaders, cfg.CORS.AllowedMethods, cfg.CORS.AllowedOrigins),
		withLogging(logger),
	))

	mux.HandleFunc("GET /bounties/paid", stools.AdaptHandler(
		handleListPaidBounties(logger, rpcClient, cfg.Solana.EscrowWallet, cfg.Solana.USDCMintAddress, 10*time.Minute),
		apiMode(logger, defaultRateLimiter, 1024*1024, cfg.CORS.AllowedHeaders, cfg.CORS.AllowedMethods, cfg.CORS.AllowedOrigins),
		withLogging(logger),
	))

	// Route for getting paid bounties for a specific workflow
	mux.HandleFunc("GET /bounties/{bounty_id}/paid", stools.AdaptHandler(
		handleListPaidBountiesForWorkflow(logger, tc),
		apiMode(logger, defaultRateLimiter, 1024*1024, cfg.CORS.AllowedHeaders, cfg.CORS.AllowedMethods, cfg.CORS.AllowedOrigins),
		withLogging(logger),
	))

	// create bounty routes
	mux.HandleFunc("POST /bounties", stools.AdaptHandler(
		handleCreateBounty(logger, tc, llmProvider, llmEmbedProvider, cfg.UserRevenueSharePct, cfg.Environment, cfg.Prompts),
		apiMode(logger, llmRateLimiter, 1024*1024, cfg.CORS.AllowedHeaders, cfg.CORS.AllowedMethods, cfg.CORS.AllowedOrigins),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		requireStatus(UserStatusDefault),
	))

	// pay/funding/transactional bounty routes
	mux.HandleFunc("POST /bounties/assess", stools.AdaptHandler(
		handleAssessContent(logger, tc),
		apiMode(logger, llmRateLimiter, 1024*1024, cfg.CORS.AllowedHeaders, cfg.CORS.AllowedMethods, cfg.CORS.AllowedOrigins),
		withLogging(logger),
		jwtRateLimitMiddleware(jwtAssessLimiter, "email"),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		requireStatus(UserStatusDefault),
	))

	mux.HandleFunc("POST /bounties/embeddings", stools.AdaptHandler(
		handleStoreBountyEmbedding(logger, querier),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		requireStatus(UserStatusSudo),
	))

	mux.HandleFunc("DELETE /bounties/embeddings/{bounty_id}", stools.AdaptHandler(
		handleDeleteBountyEmbedding(logger, querier),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		requireStatus(UserStatusSudo),
	))

	mux.HandleFunc("GET /bounties/search", stools.AdaptHandler(
		handleSearchBounties(logger, querier, tc, llmEmbedProvider, cfg.Environment),
		apiMode(logger, llmRateLimiter, 1024*1024, cfg.CORS.AllowedHeaders, cfg.CORS.AllowedMethods, cfg.CORS.AllowedOrigins),
		withLogging(logger),
	))

	mux.HandleFunc("POST /bounties/summaries", stools.AdaptHandler(
		handleStoreBountySummary(logger, querier),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		requireStatus(UserStatusSudo),
	))

	// Route for pruning stale embeddings (sudo access required)
	mux.HandleFunc("POST /embeddings/prune", stools.AdaptHandler(
		handlePruneStaleEmbeddings(logger, tc, querier),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		requireStatus(UserStatusSudo),
	))

	// Route for inserting gumroad sales (sudo access required)
	mux.HandleFunc("POST /gumroad", stools.AdaptHandler(
		handleInsertGumroadSales(logger, querier),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		requireStatus(UserStatusSudo),
	))

	// Route for notifying gumroad sales (sudo access required). This will fetch unnotified gumroad sales
	// and launch a workflow to notify them.
	mux.HandleFunc("POST /gumroad/notify", stools.AdaptHandler(
		handleNotifyGumroadSales(logger, querier, tc),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		requireStatus(UserStatusSudo),
	))

	// Route for marking a Gumroad sale as notified (sudo access required)
	mux.HandleFunc("POST /gumroad/notified", stools.AdaptHandler(
		handleMarkGumroadSaleNotified(logger, querier),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		requireStatus(UserStatusSudo),
	))

	mux.HandleFunc("POST /contact-us", stools.AdaptHandler(
		handleContactUs(logger, querier, tc),
		apiMode(logger, defaultRateLimiter, 1024*1024, cfg.CORS.AllowedHeaders, cfg.CORS.AllowedMethods, cfg.CORS.AllowedOrigins),
		withLogging(logger),
	))

	mux.HandleFunc("GET /contact-us", stools.AdaptHandler(
		handleGetContactUs(logger, querier),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		requireStatus(UserStatusSudo),
	))

	// Apply CORS globally
	corsHandler := handlers.CORS(
		handlers.AllowedHeaders(cfg.CORS.AllowedHeaders),
		handlers.AllowedMethods(cfg.CORS.AllowedMethods),
		handlers.AllowedOrigins(cfg.CORS.AllowedOrigins),
		handlers.AllowCredentials(),
	)(mux)

	// Start server
	server := &http.Server{
		Addr:    ":" + port,
		Handler: corsHandler,
	}

	// Start server in a goroutine
	go func() {
		logger.Info("http server listening", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
		}
	}()

	// Wait for context cancellation
	<-ctx.Done()
	logger.Info("shutting down HTTP server")
	return server.Shutdown(context.Background())
}

// withLogging wraps a handler with logging middleware
func withLogging(logger *slog.Logger) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next(w, r)
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"duration", time.Since(start),
				"remote_addr", r.RemoteAddr,
			)
		}
	}
}

// handlePing returns a handler for the ping endpoint
func handlePing() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSONResponse(w, api.DefaultJSONResponse{Message: "pong"}, http.StatusOK)
	}
}

func decodeBase64(s string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
