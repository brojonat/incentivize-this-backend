package http

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/db/dbgen"
	"github.com/brojonat/affiliate-bounty-board/internal/stools"
	"github.com/jackc/pgx/v5/pgtype"
	"go.temporal.io/sdk/client"
)

// ContactUsRequest is the request body for POST /contact-us
type ContactUsRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Message string `json:"message"`
}

// handleContactUs handles the submission of the contact us form.
func handleContactUs(logger *slog.Logger, querier dbgen.Querier, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ContactUsRequest
		if err := stools.DecodeJSONBody(r, &req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request: %w", err))
			return
		}

		// Basic validation
		if req.Email == "" || req.Message == "" {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `<p class="text-red-600">✗ Email and message are required fields.</p>`)
				return
			}
			writeBadRequestError(w, fmt.Errorf("email and message are required fields"))
			return
		}

		params := dbgen.CreateContactUsSubmissionParams{
			Name:    pgtype.Text{String: req.Name, Valid: req.Name != ""},
			Email:   pgtype.Text{String: req.Email, Valid: true},
			Message: pgtype.Text{String: req.Message, Valid: true},
		}

		submission, err := querier.CreateContactUsSubmission(r.Context(), params)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to create contact us submission: %w", err))
			return
		}

		// kick off workflow to notify admin
		taskQueue := os.Getenv(EnvTaskQueue)
		if taskQueue == "" {
			logger.Error("TASK_QUEUE environment variable not set, cannot kick off notification workflow")
		} else {
			workflowOpts := client.StartWorkflowOptions{
				ID:        fmt.Sprintf("contact-us-%d", submission.ID),
				TaskQueue: taskQueue,
			}
			in := abb.ContactUsNotifyWorkflowInput{
				Name:    req.Name,
				Email:   req.Email,
				Message: req.Message,
			}
			_, err := tc.ExecuteWorkflow(r.Context(), workflowOpts, abb.ContactUsNotifyWorkflow, in)
			if err != nil {
				logger.Error("failed to kick off contact us workflow", "error", err, "submission_id", submission.ID)
			}
		}

		// Check if this is an HTMX request
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `<p class="text-green-600 font-medium">✓ Thank you! Your message has been sent successfully.</p>`)
			return
		}

		writeJSONResponse(w, submission, http.StatusCreated)
	}
}

// handleGetContactUs handles fetching all contact us submissions, with pagination.
// This is an admin-only endpoint.
func handleGetContactUs(logger *slog.Logger, querier dbgen.Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limitStr := r.URL.Query().Get("limit")
		startIDStr := r.URL.Query().Get("start_id")

		limit := 50 // Default limit
		if limitStr != "" {
			if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 200 {
				limit = l
			}
		}

		startID := 0 // Default start_id to fetch from the beginning
		if startIDStr != "" {
			if id, err := strconv.Atoi(startIDStr); err == nil && id >= 0 {
				startID = id
			}
		}

		params := dbgen.GetAllContactUsSubmissionsParams{
			Limit: int32(limit),
			ID:    int32(startID),
		}

		submissions, err := querier.GetAllContactUsSubmissions(r.Context(), params)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to get contact us submissions: %w", err))
			return
		}

		if submissions == nil {
			submissions = make([]dbgen.ContactUsSubmission, 0)
		}

		writeJSONResponse(w, submissions, http.StatusOK)
	}
}
