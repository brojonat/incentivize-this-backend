package http

import (
	"context"
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
)

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
func DefaultPayoutCalculator() PayoutCalculator {
	// Parse user revenue share percentage from environment variable (default to 50%)
	userRevSharePct := 50.0
	if pctStr := os.Getenv(EnvUserRevenueSharePct); pctStr != "" {
		if pct, err := strconv.ParseFloat(pctStr, 64); err == nil {
			// Ensure the percentage is within bounds
			if pct >= 0 && pct <= 100 {
				userRevSharePct = pct
			}
		}
	}

	// Return a calculator function that applies the percentage
	return func(totalAmount float64) float64 {
		return totalAmount * (userRevSharePct / 100.0)
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
	mux := http.NewServeMux()

	// --- Read and Apply CORS Configuration from Env Vars ---
	allowedOriginsEnv := os.Getenv("CORS_ORIGINS")
	var allowedOrigins []string
	if allowedOriginsEnv == "*" {
		allowedOrigins = []string{"*"}
		logger.Warn("CORS configured to allow all origins (*)")
	} else if allowedOriginsEnv != "" {
		allowedOrigins = strings.Split(allowedOriginsEnv, ",")
		logger.Info("CORS configured with specific origins", "origins", allowedOrigins)
	} else {
		logger.Warn("CORS_ORIGINS not set, CORS might not function correctly")
		// Default to empty list, effectively disabling CORS unless middleware handles nil gracefully
		allowedOrigins = []string{}
	}

	// Read CORS Methods
	allowedMethodsEnv := os.Getenv("CORS_METHODS")
	var allowedMethods []string
	if allowedMethodsEnv != "" {
		allowedMethods = strings.Split(allowedMethodsEnv, ",")
		logger.Info("CORS configured with specific methods", "methods", allowedMethods)
	} else {
		logger.Warn("CORS_METHODS not set, using default methods")
		allowedMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"} // Default if not set
	}

	// Read CORS Headers
	allowedHeadersEnv := os.Getenv("CORS_HEADERS")
	var allowedHeaders []string
	if allowedHeadersEnv != "" {
		allowedHeaders = strings.Split(allowedHeadersEnv, ",")
		logger.Info("CORS configured with specific headers", "headers", allowedHeaders)
	} else {
		logger.Warn("CORS_HEADERS not set, using default headers")
		allowedHeaders = []string{"Authorization", "Content-Type", "X-Requested-With"} // Default if not set
	}

	ctx = WithCORSConfig(ctx, allowedHeaders, allowedMethods, allowedOrigins)
	// --- End CORS Configuration ---

	// Now retrieve the config from context (it should be populated now)
	// headers, methods, origins := GetCORSConfig(ctx) // No longer needed here

	rpcEndpoint := os.Getenv(EnvSolanaRPCEndpoint)
	if rpcEndpoint == "" {
		return fmt.Errorf("server startup error: %s not set", EnvSolanaRPCEndpoint)
	}
	escrowWalletStr := os.Getenv(EnvSolanaEscrowWallet)
	if escrowWalletStr == "" {
		return fmt.Errorf("server startup error: %s not set", EnvSolanaEscrowWallet)
	}
	escrowWallet, err := solanago.PublicKeyFromBase58(escrowWalletStr)
	if err != nil {
		return fmt.Errorf("server startup error: failed to parse escrow wallet public key '%s': %w", escrowWalletStr, err)
	}
	usdcMintAddressStr := os.Getenv(EnvSolanaUSDCMintAddress)
	if usdcMintAddressStr == "" {
		return fmt.Errorf("server startup error: %s not set", EnvSolanaUSDCMintAddress)
	}
	usdcMintAddress, err := solanago.PublicKeyFromBase58(usdcMintAddressStr)
	if err != nil {
		return fmt.Errorf("server startup error: failed to parse USDC mint address '%s': %w", usdcMintAddressStr, err)
	}

	rpcClient := solanarpc.New(rpcEndpoint)
	_, err = rpcClient.GetHealth(ctx)
	if err != nil {
		return fmt.Errorf("server startup error: Solana RPC health check failed for %s: %w", rpcEndpoint, err)
	}
	logger.Debug("Successfully connected to Solana RPC", "endpoint", rpcEndpoint)

	// Get current environment (e.g., "dev", "prod")
	currentEnv := os.Getenv(EnvServerEnv) // Read the ENV variable
	if currentEnv == "" {
		currentEnv = "dev" // Default to "dev" if not set
		logger.Warn("ENV environment variable not set, defaulting to 'dev'")
	}

	// --- Setup Temporal Schedule for Periodic Publisher ---
	if err := setupPeriodicPublisherSchedule(ctx, logger, tc, currentEnv); err != nil {
		// Log error but don't prevent server startup
		logger.Error("Failed to set up periodic publisher schedule", "error", err)
	}
	// --- End Schedule Setup ---

	// Create Rate Limiter for JWT-based assessment endpoint
	jwtAssessLimiter := NewRateLimiter(1*time.Hour, 10) // 10 requests per hour per JWT

	// --- Database Connection ---
	dbURL := os.Getenv(EnvAbbDatabaseURL)
	var querier dbgen.Querier // Define querier, to be initialized if dbURL is set
	var dbPool *pgxpool.Pool

	if dbURL != "" {
		var errDb error
		dbPool, errDb = getConnPool(ctx, dbURL, logger, 5, 5*time.Second)
		if errDb != nil {
			// Depending on criticality, you might want to return errDb here and fail server startup
			logger.Error("Failed to connect to database, semantic search features will be unavailable", "error", errDb)
		} else {
			querier = dbgen.New(dbPool) // Initialize querier with the pool
			defer dbPool.Close()
			logger.Info("Database connection established for semantic search.")
		}
	} else {
		logger.Warn("ABB_DATABASE_URL not set, semantic search features will be disabled.")
	}
	// --- End Database Connection ---

	// --- LLM Embedding Provider Initialization (Placeholder) ---
	// We will stub it for now, the handleSearchBounties will need it.
	var llmEmbedProvider abb.LLMEmbeddingProvider // Keep as nil for now, to be implemented

	// --- Initialize LLMEmbeddingProvider ---
	llmProviderName := os.Getenv(abb.EnvLLMProvider)         // e.g., "openai"
	llmAPIKey := os.Getenv(abb.EnvLLMAPIKey)                 // Shared API key
	llmEmbeddingModel := os.Getenv(abb.EnvLLMEmbeddingModel) // e.g., "text-embedding-ada-002"

	if llmProviderName != "" && llmAPIKey != "" && llmEmbeddingModel != "" {
		embeddingCfg := abb.EmbeddingConfig{
			Provider: llmProviderName,
			APIKey:   llmAPIKey,
			Model:    llmEmbeddingModel,
			// BaseURL: os.Getenv("OPENAI_BASE_URL"), // Optional: if you need to override OpenAI endpoint
		}
		var errProvider error
		llmEmbedProvider, errProvider = abb.NewLLMEmbeddingProvider(embeddingCfg)
		if errProvider != nil {
			logger.Error("Failed to initialize LLM Embedding Provider, search will be impaired.", "error", errProvider)
			llmEmbedProvider = nil // Ensure it's nil if init fails
		} else {
			logger.Info("LLM Embedding Provider initialized successfully.", "provider", llmProviderName, "model", llmEmbeddingModel)
		}
	} else {
		logger.Warn("LLM Embedding Provider not configured due to missing env vars (LLM_PROVIDER, LLM_API_KEY, LLM_EMBEDDING_MODEL). Search endpoint will not work.")
	}

	// Add routes
	mux.HandleFunc("GET /ping", stools.AdaptHandler(
		handlePing(),
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
		handleListBounties(logger, tc, currentEnv),
		withLogging(logger),
	))

	mux.HandleFunc("GET /bounties/paid", stools.AdaptHandler(
		handleListPaidBounties(logger, rpcClient, escrowWallet, usdcMintAddress, 10*time.Minute),
		withLogging(logger),
	))

	// Route for getting paid bounties for a specific workflow
	mux.HandleFunc("GET /bounties/{bounty_id}/paid", stools.AdaptHandler(
		handleListPaidBountiesForWorkflow(logger, tc),
		withLogging(logger),
	))

	// create bounty routes
	mux.HandleFunc("POST /bounties", stools.AdaptHandler(
		handleCreateBounty(logger, tc, DefaultPayoutCalculator(), currentEnv),
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

	mux.HandleFunc("GET /bounties/search", stools.AdaptHandler(
		handleSearchBounties(logger, querier, tc, llmEmbedProvider, currentEnv),
		withLogging(logger),
	))

	// Apply CORS globally
	corsHandler := handlers.CORS(
		handlers.AllowedHeaders(allowedHeaders),
		handlers.AllowedMethods(allowedMethods),
		handlers.AllowedOrigins(allowedOrigins),
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
			ID:        fmt.Sprintf("bounty-publisher"),
			TaskQueue: taskQueue,
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
