package http

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/db/dbgen"
	"github.com/brojonat/affiliate-bounty-board/internal/stools"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
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

type InsertGumroadSalesRequest struct {
	Sales []dbgen.InsertGumroadSaleParams `json:"sales"`
}

func handleInsertGumroadSales(logger *slog.Logger, querier dbgen.Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req InsertGumroadSalesRequest
		if err := stools.DecodeJSONBody(r, &req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request body for inserting gumroad sales: %w", err))
			return
		}

		var insertedCount int
		for _, saleParams := range req.Sales {
			if err := querier.InsertGumroadSale(r.Context(), saleParams); err != nil {
				if err != nil {
					// Check for unique_violation error, which we can ignore because of ON CONFLICT
					if !strings.Contains(err.Error(), "unique_violation") {
						logger.Error("Failed to insert gumroad sale", "sale_id", saleParams.ID, "error", err)
					}
					continue // Continue to next sale even if this one fails insertion
				}
				insertedCount++
			}
		}
		logger.Info("handleInsertGumroadSales finished", "attempted_sales", len(req.Sales), "inserted_sales", insertedCount)
		writeOK(w)
	}
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

		if err := querier.UpdateGumroadSaleNotification(r.Context(), dbgen.UpdateGumroadSaleNotificationParams{
			ID:     req.SaleID,
			ApiKey: pgtype.Text{String: req.APIKey, Valid: true},
		}); err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to update gumroad sale notification status via internal API: %w", err))
			return
		}

		writeOK(w)
	}
}

// handleNotifyGumroadSales fetches unnotified Gumroad sales from the database
// and launches a Temporal workflow for each one to send a notification email.
func handleNotifyGumroadSales(logger *slog.Logger, querier dbgen.Querier, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		unnotifiedSales, err := querier.GetUnnotifiedGumroadSales(r.Context())
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to get unnotified gumroad sales: %w", err))
			return
		}

		if len(unnotifiedSales) == 0 {
			logger.Info("No unnotified Gumroad sales to process.")
			writeOK(w)
			return
		}

		var startedWorkflows int
		var failedWorkflows int

		for _, sale := range unnotifiedSales {
			// create a token for the user
			tokenString, err := createUserToken(sale.Email, time.Now().Add(30*24*time.Hour))
			if err != nil {
				logger.Error("Failed to create token for Gumroad sale notification", "sale_id", sale.ID, "email", sale.Email, "error", err)
				failedWorkflows++
				continue
			}
			workflowID := fmt.Sprintf("email-token-gumroad-%s", sale.ID)
			workflowOptions := client.StartWorkflowOptions{
				ID:        workflowID,
				TaskQueue: os.Getenv(EnvTaskQueue),
				RetryPolicy: &temporal.RetryPolicy{
					MaximumAttempts: 3,
				},
			}
			workflowInput := abb.EmailTokenWorkflowInput{
				SaleID:         sale.ID,
				Email:          sale.Email,
				Token:          tokenString,
				SourcePlatform: abb.PaymentPlatformGumroad,
			}

			_, err = tc.ExecuteWorkflow(r.Context(), workflowOptions, abb.EmailTokenWorkflow, workflowInput)
			if err != nil {
				logger.Error("Failed to start EmailTokenWorkflow for Gumroad sale", "sale_id", sale.ID, "email", sale.Email, "error", err)
				failedWorkflows++
				continue
			}
			startedWorkflows++
		}

		writeOK(w)
	}
}
