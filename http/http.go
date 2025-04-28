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
	"github.com/brojonat/affiliate-bounty-board/http/api"
	"github.com/brojonat/affiliate-bounty-board/internal/stools"
	solanago "github.com/gagliardetto/solana-go"
	solanarpc "github.com/gagliardetto/solana-go/rpc"
	"go.temporal.io/sdk/client"
)

// Environment Variable Keys
const (
	EnvSolanaEscrowPrivateKey            = "SOLANA_ESCROW_PRIVATE_KEY"
	EnvSolanaEscrowWallet                = "SOLANA_ESCROW_WALLET"
	EnvSolanaRPCEndpoint                 = "SOLANA_RPC_ENDPOINT"
	EnvSolanaWSEndpoint                  = "SOLANA_WS_ENDPOINT"
	EnvSolanaUSDCMintAddress             = "SOLANA_USDC_MINT_ADDRESS"
	EnvTaskQueue                         = "TASK_QUEUE"
	EnvUserRevenueSharePct               = "USER_REVENUE_SHARE_PCT"
	EnvServerSecretKey                   = "SERVER_SECRET_KEY"
	EnvSolanaTreasuryWallet              = "SOLANA_TREASURY_WALLET"
	EnvPeriodicPublisherScheduleID       = "PERIODIC_PUBLISHER_SCHEDULE_ID"
	EnvPeriodicPublisherScheduleInterval = "PERIODIC_PUBLISHER_SCHEDULE_INTERVAL"
	EnvPublishTargetSubreddit            = "PUBLISH_TARGET_SUBREDDIT"
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

// RunServer starts the HTTP server with the given configuration
func RunServer(ctx context.Context, logger *slog.Logger, tc client.Client, port string) error {
	mux := http.NewServeMux()
	maxBytes := int64(1048576)
	headers, methods, origins := GetCORSConfig(ctx)

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

	// --- Setup Temporal Schedule for Periodic Publisher ---
	if err := setupPeriodicPublisherSchedule(ctx, logger, tc, "periodic-publisher"); err != nil {
		// Log error but don't prevent server startup
		logger.Error("Failed to set up periodic publisher schedule", "error", err)
	}
	// --- End Schedule Setup ---

	// Get current environment (e.g., "dev", "prod")
	currentEnv := os.Getenv("ENV") // Read the ENV variable
	if currentEnv == "" {
		currentEnv = "dev" // Default to "dev" if not set
		logger.Warn("ENV environment variable not set, defaulting to 'dev'")
	}

	// Add routes
	mux.HandleFunc("GET /ping", stools.AdaptHandler(
		handlePing(),
		withLogging(logger),
		apiMode(logger, maxBytes, headers, methods, origins),
	))

	mux.HandleFunc("POST /token", stools.AdaptHandler(
		handleIssueSudoToken(logger),
		withLogging(logger),
		atLeastOneAuth(oauthAuthorizerForm(getSecretKey)),
		apiMode(logger, maxBytes, headers, methods, origins),
	))

	mux.HandleFunc("GET /bounties", stools.AdaptHandler(
		handleListBounties(logger, tc),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		apiMode(logger, maxBytes, headers, methods, origins),
	))

	mux.HandleFunc("GET /bounties/paid", stools.AdaptHandler(
		handleListPaidBounties(logger, rpcClient, escrowWallet, usdcMintAddress, 1*time.Minute),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		apiMode(logger, maxBytes, headers, methods, origins),
	))

	mux.HandleFunc("POST /bounties", stools.AdaptHandler(
		handleCreateBounty(logger, tc, DefaultPayoutCalculator(), currentEnv),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		apiMode(logger, maxBytes, headers, methods, origins),
	))

	mux.HandleFunc("POST /assess", stools.AdaptHandler(
		handleAssessContent(logger, tc),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		apiMode(logger, maxBytes, headers, methods, origins),
	))

	mux.HandleFunc("POST /bounties/pay", stools.AdaptHandler(
		handlePayBounty(logger, tc),
		withLogging(logger),
		atLeastOneAuth(bearerAuthorizerCtxSetToken(getSecretKey)),
		apiMode(logger, maxBytes, headers, methods, origins),
	))

	// Start server
	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
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
func setupPeriodicPublisherSchedule(ctx context.Context, logger *slog.Logger, tc client.Client, scheduleID string) error {
	scheduleClient := tc.ScheduleClient()

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
