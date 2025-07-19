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

	"github.com/Rhymond/go-money"
	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/db/dbgen"
	"github.com/brojonat/affiliate-bounty-board/http/api"
	"github.com/brojonat/affiliate-bounty-board/internal/stools"
	solanago "github.com/gagliardetto/solana-go"
	solanarpc "github.com/gagliardetto/solana-go/rpc"
	"github.com/gorilla/handlers"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
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
	}
	CORS struct {
		AllowedOrigins []string
		AllowedMethods []string
		AllowedHeaders []string
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

// PayoutCalculator is a function that calculates the available payout amount
// given the total bounty amount specified by an advertiser
type PayoutCalculator func(totalAmount float64) float64

// DefaultPayoutCalculator creates a calculator that applies a percentage-based revenue share
func DefaultPayoutCalculator(userRevSharePct float64) PayoutCalculator {
	// Return a calculator function that applies the percentage
	return func(totalAmount float64) float64 {
		totalMoney := money.NewFromFloat(totalAmount, money.USD)
		userShare := int(userRevSharePct)
		platformShare := 100 - userShare
		// Allocate can take ratios
		parties, err := totalMoney.Allocate(userShare, platformShare)
		if err != nil {
			// Since this function can't return an error, log it and return a value
			// that indicates failure, like the original amount, so it can be handled upstream.
			// Ideally, the signature would allow for an error return.
			slog.Default().Error("failed to allocate money in DefaultPayoutCalculator", "error", err)
			return totalAmount
		}
		userPayoutMoney := parties[0]
		return userPayoutMoney.AsMajorUnits()
	}
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

	// --- Setup Temporal Schedules ---
	if err := setupPeriodicPublisherSchedule(ctx, logger, tc, cfg.Environment); err != nil {
		logger.Error("Failed to set up periodic publisher schedule", "error", err)
	}
	if err := setupPruneStaleEmbeddingsSchedule(ctx, logger, tc, cfg.Environment); err != nil {
		logger.Error("Failed to set up prune stale embeddings schedule", "error", err)
	}
	if err := setupGumroadNotifySchedule(ctx, logger, tc, cfg.Environment); err != nil {
		logger.Error("Failed to set up Gumroad notify schedule", "error", err)
	}

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
		withLogging(logger),
	))

	mux.HandleFunc("POST /token", stools.AdaptHandler(
		handleIssueSudoToken(logger),
		withLogging(logger),
		atLeastOneAuth(oauthAuthorizerForm(getSecretKey)),
	))

	// Route for getting a specific bounty by ID
	mux.HandleFunc("GET /bounties/{id}", stools.AdaptHandler(
		handleGetBountyByID(logger, tc),
		withLogging(logger),
	))

	// listing bounties routes
	mux.HandleFunc("GET /bounties", stools.AdaptHandler(
		handleListBounties(logger, tc, cfg.Environment),
		withLogging(logger),
	))

	mux.HandleFunc("GET /bounties/paid", stools.AdaptHandler(
		handleListPaidBounties(logger, rpcClient, cfg.Solana.EscrowWallet, cfg.Solana.USDCMintAddress, 10*time.Minute),
		withLogging(logger),
	))

	// Route for getting paid bounties for a specific workflow
	mux.HandleFunc("GET /bounties/{bounty_id}/paid", stools.AdaptHandler(
		handleListPaidBountiesForWorkflow(logger, tc),
		withLogging(logger),
	))

	// create bounty routes
	mux.HandleFunc("POST /bounties", stools.AdaptHandler(
		handleCreateBounty(logger, tc, llmProvider, llmEmbedProvider, DefaultPayoutCalculator(cfg.UserRevenueSharePct), cfg.Environment, cfg.Prompts),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		requireStatus(UserStatusSudo),
	))

	// pay/funding/transactional bounty routes
	mux.HandleFunc("POST /bounties/pay", stools.AdaptHandler(
		handlePayBounty(logger, tc),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		requireStatus(UserStatusSudo),
	))

	mux.HandleFunc("POST /bounties/assess", stools.AdaptHandler(
		handleAssessContent(logger, tc),
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

// setupPeriodicPublisherSchedule sets up a Temporal schedule for periodic publisher
func setupPeriodicPublisherSchedule(ctx context.Context, logger *slog.Logger, tc client.Client, env string) error {
	scheduleClient := tc.ScheduleClient()

	// Construct environment-specific schedule ID
	scheduleID := fmt.Sprintf("bounty-publisher-%s", env)

	// Try to get a handle to the schedule to check if it exists
	// Getting a handle doesn't guarantee existence, but Describe seems unavailable directly
	// We'll rely on the Create call to fail if it already exists.
	// Let's remove the explicit check for now and handle the error during Create.

	// Schedule does not exist (or we assume it doesn't), proceed to create it
	taskQueue := os.Getenv(EnvTaskQueue)
	if taskQueue == "" {
		return fmt.Errorf("cannot create schedule: %s env var not set", EnvTaskQueue)
	}

	logger.Info("Attempting to create periodic publisher schedule", "schedule_id", scheduleID)

	// Attempt to Create the schedule
	_, err := scheduleClient.Create(ctx, client.ScheduleOptions{
		ID: scheduleID,
		Spec: client.ScheduleSpec{
			Intervals: []client.ScheduleIntervalSpec{
				{
					Every: 8 * time.Hour,
				},
			},
		},
		Action: &client.ScheduleWorkflowAction{
			Workflow:  abb.PublishBountiesWorkflow,
			ID:        fmt.Sprintf("bounty-publisher-%s", env),
			TaskQueue: taskQueue,
			TypedSearchAttributes: temporal.NewSearchAttributes(
				abb.EnvironmentKey.ValueSet(env),
			),
		},
	})

	if err != nil {
		// Check if the error is specifically that the schedule already exists
		if strings.Contains(err.Error(), "schedule already exists") {
			logger.Info("Periodic publisher schedule already exists, no action taken.", "schedule_id", scheduleID)
			return nil // Not an error in this context
		}
		// For any other error, return it
		return fmt.Errorf("failed to create schedule %s: %w", scheduleID, err)
	}

	// If err is nil, creation was successful
	logger.Info("Successfully created periodic publisher schedule", "schedule_id", scheduleID)

	return nil
}

// setupPruneStaleEmbeddingsSchedule sets up a Temporal schedule for pruning stale embeddings.
func setupPruneStaleEmbeddingsSchedule(ctx context.Context, logger *slog.Logger, tc client.Client, env string) error {
	scheduleClient := tc.ScheduleClient()

	// Construct environment-specific schedule ID
	scheduleID := fmt.Sprintf("prune-stale-embeddings-%s", env)

	taskQueue := os.Getenv(EnvTaskQueue)
	if taskQueue == "" {
		return fmt.Errorf("cannot create prune schedule: %s env var not set", EnvTaskQueue)
	}

	logger.Info("Attempting to create prune stale embeddings schedule", "schedule_id", scheduleID)

	// this will prune stale embeddings every 20 minutes. Now that we've added the proper
	// workflow cancellation signal handling, this shouldn't be necessary, but if a workflow
	// is ever terminated, we'll need to prune the embeddings because the cleanup activities
	// will not be triggered. As a result, I've opted to leave this in place for now but
	// we can remove it if we're confident that the workflow cancellation signal handling
	// is sufficient.
	_, err := scheduleClient.Create(ctx, client.ScheduleOptions{
		ID: scheduleID,
		Spec: client.ScheduleSpec{
			Intervals: []client.ScheduleIntervalSpec{{Every: 20 * time.Minute}},
		},
		Action: &client.ScheduleWorkflowAction{
			Workflow:  abb.PruneStaleEmbeddingsWorkflow,
			ID:        fmt.Sprintf("prune-stale-embeddings-workflow-%s", env), // Unique ID for workflow executions started by this schedule
			TaskQueue: taskQueue,
			TypedSearchAttributes: temporal.NewSearchAttributes(
				abb.EnvironmentKey.ValueSet(env),
			),
		},
	})

	if err != nil {
		// Check if the error is specifically that the schedule already exists
		if strings.Contains(err.Error(), "schedule already exists") {
			logger.Info("Prune stale embeddings schedule already exists, no action taken.", "schedule_id", scheduleID)
			return nil // Not an error in this context
		}
		// For any other error, return it
		return fmt.Errorf("failed to create prune stale embeddings schedule %s: %w", scheduleID, err)
	}

	logger.Info("Successfully created prune stale embeddings schedule", "schedule_id", scheduleID)
	return nil
}

// setupGumroadNotifySchedule sets up a Temporal schedule for Gumroad notify
func setupGumroadNotifySchedule(ctx context.Context, logger *slog.Logger, tc client.Client, env string) error {
	scheduleID := fmt.Sprintf("gumroad-notify-schedule-%s", env)
	taskQueue := os.Getenv(EnvTaskQueue)
	if taskQueue == "" {
		return fmt.Errorf("TASK_QUEUE environment variable not set, cannot set up schedule %s", scheduleID)
	}

	scheduleInput := abb.GumroadNotifyWorkflowInput{
		LookbackDuration: time.Hour, // Look back 1 hour
	}

	scheduleOptions := client.ScheduleOptions{
		ID: scheduleID, // The ID for the schedule itself
		Spec: client.ScheduleSpec{
			CronExpressions: []string{"* * * * *"}, // Every minute
		},
		Action: &client.ScheduleWorkflowAction{
			Workflow:  abb.GumroadNotifyWorkflow,
			Args:      []interface{}{scheduleInput},
			TaskQueue: taskQueue,
			// Optionally, provide a base ID for workflow executions started by this schedule
			ID: fmt.Sprintf("gumroad-notify-workflow-%s", env),
		},
		Paused: false,
		// Other fields like OverlapPolicy, Jitter, etc., can be added if available and needed
		// For example, if your SDK version supports it:
		// OverlapPolicy: client.ScheduleOverlapPolicySkip,
	}

	logger.Info("Attempting to create Gumroad Notify schedule", "scheduleID", scheduleID, "cron", scheduleOptions.Spec.CronExpressions)

	// Use the same pattern as setupPeriodicPublisherSchedule and setupPruneStaleEmbeddingsSchedule
	_, err := tc.ScheduleClient().Create(ctx, scheduleOptions)

	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			logger.Info("Gumroad Notify schedule already exists, no action taken.", "scheduleID", scheduleID)
			// If update logic is needed and supported by your SDK version, it would go here.
			// For now, returning nil to avoid failing server startup.
			return nil
		}
		logger.Error("Failed to create Gumroad Notify schedule", "scheduleID", scheduleID, "error", err)
		return fmt.Errorf("failed to create Gumroad Notify schedule %s: %w", scheduleID, err)
	}

	logger.Info("Successfully created Gumroad Notify schedule", "scheduleID", scheduleID)
	return nil
}

func decodeBase64(s string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
