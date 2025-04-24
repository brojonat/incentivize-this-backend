package http

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/brojonat/affiliate-bounty-board/http/api"
	"github.com/brojonat/affiliate-bounty-board/internal/stools"
	"go.temporal.io/sdk/client"
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
	if pctStr := os.Getenv("USER_REVENUE_SHARE_PCT"); pctStr != "" {
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

	mux.HandleFunc("POST /bounties", stools.AdaptHandler(
		handleCreateBounty(logger, tc, DefaultPayoutCalculator()),
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
