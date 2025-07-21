package abb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
	"go.temporal.io/sdk/activity"
)

// BountySummaryData holds all the information to be stored as a JSON summary for a bounty.
type BountySummaryData struct {
	BountyID             string             `json:"bounty_id"`
	Title                string             `json:"title"`
	Requirements         []string           `json:"requirements"`
	Platform             PlatformKind       `json:"platform"`
	ContentKind          ContentKind        `json:"content_kind"`
	Tier                 BountyTier         `json:"tier"`
	BountyFunderWallet   string             `json:"bounty_funder_wallet"`
	OriginalTotalBounty  *solana.USDCAmount `json:"original_total_bounty"`  // This is TotalCharged from workflow input
	EffectiveTotalBounty *solana.USDCAmount `json:"effective_total_bounty"` // This is TotalBounty from workflow input
	BountyPerPost        *solana.USDCAmount `json:"bounty_per_post"`
	TotalAmountPaid      *solana.USDCAmount `json:"total_amount_paid"`
	AmountRefunded       *solana.USDCAmount `json:"amount_refunded"`
	Payouts              []PayoutDetail     `json:"payouts"`
	FinalStatus          string             `json:"final_status"` // e.g., "COMPLETED_EMPTY", "TIMED_OUT_REFUNDED", "CANCELLED_REFUNDED", "AWAITING_FUNDING_FAILED"
	WorkflowStartTime    time.Time          `json:"workflow_start_time"`
	WorkflowEndTime      time.Time          `json:"workflow_end_time"`
	TimeoutDuration      string             `json:"timeout_duration"` // Original timeout duration string
	FeeAmount            *solana.USDCAmount `json:"fee_amount"`
	ErrorDetails         string             `json:"error_details,omitempty"` // If the workflow ended in an error state
}

// SummarizeAndStoreBountyActivityInput is the input for the SummarizeAndStoreBountyActivity.
type SummarizeAndStoreBountyActivityInput struct {
	SummaryData BountySummaryData `json:"summary_data"`
}

// SummarizeAndStoreBountyActivity sends the bounty summary to the server to be stored.
func (a *Activities) SummarizeAndStoreBountyActivity(ctx context.Context, input SummarizeAndStoreBountyActivityInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("SummarizeAndStoreBountyActivity started", "bounty_id", input.SummaryData.BountyID, "final_status", input.SummaryData.FinalStatus)

	cfg, err := getConfiguration(ctx)
	if err != nil {
		logger.Error("Failed to get configuration for summary activity", "bounty_id", input.SummaryData.BountyID, "error", err)
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	if cfg.ABBServerConfig.APIEndpoint == "" {
		logger.Error("ABB API Endpoint not configured, cannot store bounty summary", "bounty_id", input.SummaryData.BountyID)
		return fmt.Errorf("ABB_API_ENDPOINT not configured")
	}

	abbToken, err := a.getABBAuthToken(ctx, logger, cfg, a.httpClient)
	if err != nil {
		logger.Error("Failed to get ABB auth token for summary storage", "bounty_id", input.SummaryData.BountyID, "error", err)
		return fmt.Errorf("failed to get ABB auth token: %w", err)
	}

	payloadBytes, err := json.Marshal(input.SummaryData)
	if err != nil {
		logger.Error("Failed to marshal bounty summary data", "bounty_id", input.SummaryData.BountyID, "error", err)
		return fmt.Errorf("failed to marshal bounty summary: %w", err)
	}

	// Using a more specific internal endpoint
	storeURL := strings.TrimSuffix(cfg.ABBServerConfig.APIEndpoint, "/") + "/bounties/summaries"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, storeURL, bytes.NewReader(payloadBytes))
	if err != nil {
		logger.Error("Failed to create store bounty summary HTTP request", "bounty_id", input.SummaryData.BountyID, "error", err)
		return fmt.Errorf("failed to create store summary HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+abbToken)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		logger.Error("Store bounty summary HTTP request failed", "bounty_id", input.SummaryData.BountyID, "url", storeURL, "error", err)
		return fmt.Errorf("store summary HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		logger.Error("Failed to store bounty summary, server returned error",
			"bounty_id", input.SummaryData.BountyID,
			"url", storeURL,
			"status_code", resp.StatusCode,
			"response_body", string(bodyBytes))
		return fmt.Errorf("store summary request to %s returned status %d. Body: %s", storeURL, resp.StatusCode, string(bodyBytes))
	}

	logger.Info("Successfully stored bounty summary", "bounty_id", input.SummaryData.BountyID, "final_status", input.SummaryData.FinalStatus)
	return nil
}
