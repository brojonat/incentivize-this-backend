package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/db/dbgen"
	"github.com/brojonat/affiliate-bounty-board/http/api"
	"github.com/brojonat/affiliate-bounty-board/internal/stools"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/client"
)

const (
	gumroadNotificationTokenDuration = 30 * 24 * time.Hour
	defaultHoursAgoForNotification   = 24
)

// MarkGumroadSaleNotifiedRequest is the request body for the /gumroad/notified endpoint.
type MarkGumroadSaleNotifiedRequest struct {
	SaleID string `json:"sale_id"`
	APIKey string `json:"api_key"`
}

// handleMarkGumroadSaleNotified is called by a Temporal activity after an email has been sent.
// It marks the sale as notified and stores the API key.
func handleMarkGumroadSaleNotified(logger *slog.Logger, querier dbgen.Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req MarkGumroadSaleNotifiedRequest
		if err := stools.DecodeJSONBody(r, &req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request body for marking sale notified: %w", err))
			return
		}

		if req.SaleID == "" || req.APIKey == "" {
			writeBadRequestError(w, fmt.Errorf("sale_id and api_key are required"))
			return
		}

		updateParams := dbgen.UpdateGumroadSaleNotificationParams{
			ID:     req.SaleID,
			ApiKey: pgtype.Text{String: req.APIKey, Valid: true},
		}

		if err := querier.UpdateGumroadSaleNotification(r.Context(), updateParams); err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to update gumroad sale notification status via internal API: %w", err))
			return
		}

		writeOK(w)
	}
}

// handleNotifyGumroadSales processes recent Gumroad sales that haven't been notified,
// generates an API key (JWT) for the customer, and triggers a Temporal workflow to send an email
// and subsequently mark the sale as notified via an internal API call from the workflow.
// Assumes createUserToken is in the http package.
func handleNotifyGumroadSales(logger *slog.Logger, querier dbgen.Querier, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		requestCtx := r.Context()

		hoursAgoStr := r.URL.Query().Get("hours_ago")
		hoursAgo := defaultHoursAgoForNotification
		if hoursAgoStr != "" {
			if val, err := strconv.Atoi(hoursAgoStr); err == nil && val > 0 {
				hoursAgo = val
			} else {
				writeBadRequestError(w, fmt.Errorf("invalid 'hours_ago' query parameter: must be a positive integer"))
				return
			}
		}

		minSaleAgeDuration := time.Duration(hoursAgo) * time.Hour
		minTimestamp := time.Now().Add(-minSaleAgeDuration)

		salesToNotify, err := querier.GetUnnotifiedGumroadSales(requestCtx, pgtype.Timestamptz{Time: minTimestamp, Valid: true})
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to get unnotified Gumroad sales: %w", err))
			return
		}

		if len(salesToNotify) == 0 {
			writeJSONResponse(w, api.DefaultJSONResponse{Message: fmt.Sprintf("No new Gumroad sales to notify in the last %d hours.", hoursAgo)}, http.StatusOK)
			return
		}

		var wg sync.WaitGroup
		workflowsInitiated := 0
		var mu sync.Mutex

		taskQueue := os.Getenv(EnvTaskQueue)
		if taskQueue == "" {
			logger.Error("TASK_QUEUE environment variable not set, cannot start EmailTokenWorkflow for Gumroad notifications.")
			writeInternalError(logger, w, fmt.Errorf("server configuration error: task queue not set"))
			return
		}

		for _, saleRecord := range salesToNotify {
			sale := saleRecord // this isn't necessary for recent go versions

			if sale.Email == "" {
				logger.Warn("Gumroad sale record missing email, skipping notification workflow", "sale_id", sale.ID)
				continue
			}

			wg.Add(1)
			go func(currentSale dbgen.GumroadSale) {
				defer wg.Done()

				apiToken, tokenErr := createUserToken(currentSale.Email, time.Now().Add(gumroadNotificationTokenDuration))
				if tokenErr != nil {
					logger.Error("Failed to create token for Gumroad sale notification workflow", "sale_id", currentSale.ID, "email", currentSale.Email, "error", tokenErr)
					return
				}

				wfInput := abb.EmailTokenWorkflowInput{
					SaleID:         currentSale.ID,
					Email:          currentSale.Email,
					Token:          apiToken,
					SourcePlatform: abb.PlatformGumroad,
				}
				wfOptions := client.StartWorkflowOptions{
					ID:        fmt.Sprintf("gumroad-sale-email-token-%s", currentSale.ID), // Ensure unique workflow ID
					TaskQueue: taskQueue,
				}

				_, execErr := tc.ExecuteWorkflow(context.Background(), wfOptions, abb.EmailTokenWorkflow, wfInput)
				if execErr != nil {
					logger.Error("Failed to start EmailTokenWorkflow for Gumroad sale", "error", execErr, "workflowID", wfOptions.ID, "sale_id", currentSale.ID)
					// Do not return here; allow wg.Done() to be called. The error is logged.
				} else {
					mu.Lock()
					workflowsInitiated++
					mu.Unlock()
					logger.Info("Successfully initiated EmailTokenWorkflow for Gumroad sale", "sale_id", currentSale.ID, "email", currentSale.Email, "workflowID", wfOptions.ID)
				}
			}(sale)
		}

		wg.Wait()

		response := map[string]interface{}{
			"message":                     fmt.Sprintf("Gumroad sales email notification workflow initiation process completed for sales in the last %d hours.", hoursAgo),
			"workflows_initiated":         workflowsInitiated,
			"total_sales_queried_pending": len(salesToNotify),
		}
		writeJSONResponse(w, response, http.StatusOK)
	}
}
