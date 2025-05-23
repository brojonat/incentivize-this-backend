package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/db/dbgen"
	"github.com/brojonat/affiliate-bounty-board/internal/stools"
	"github.com/brojonat/affiliate-bounty-board/solana"
	solanagrpc "github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
)

// PayBountyRequest represents the request body for paying a bounty
type PayBountyRequest struct {
	Amount float64 `json:"amount"` // Amount to pay in USDC
	Wallet string  `json:"wallet"` // Destination wallet address
}

// ReturnBountyToOwnerRequest represents the request body for returning a bounty to the owner
type ReturnBountyToOwnerRequest struct {
	UserID       string  `json:"user_id"`
	Amount       float64 `json:"amount"`
	SolanaWallet string  `json:"solana_wallet"`
	USDCAccount  string  `json:"usdc_account"`
}

// CreateBountyRequest represents the request body for creating a new bounty
type CreateBountyRequest struct {
	Requirements       []string         `json:"requirements"`
	BountyPerPost      float64          `json:"bounty_per_post"`
	TotalBounty        float64          `json:"total_bounty"`
	BountyOwnerWallet  string           `json:"bounty_owner_wallet"`
	BountyFunderWallet string           `json:"bounty_funder_wallet"`
	PlatformType       abb.PlatformKind `json:"platform_kind"`
	ContentKind        abb.ContentKind  `json:"content_kind"`
	TimeoutDuration    string           `json:"timeout_duration"` // Bounty active duration (e.g., "72h", "7d")
}

// BountyListItem represents a single bounty in the list response
type BountyListItem struct {
	WorkflowID           string           `json:"workflow_id"`
	Status               string           `json:"status"`
	Requirements         []string         `json:"requirements"`
	BountyPerPost        float64          `json:"bounty_per_post"`
	TotalBounty          float64          `json:"total_bounty"`
	BountyOwnerWallet    string           `json:"bounty_owner_wallet"`
	PlatformType         abb.PlatformKind `json:"platform_kind"`
	ContentKind          abb.ContentKind  `json:"content_kind"`
	CreatedAt            time.Time        `json:"created_at"`
	EndTime              time.Time        `json:"end_time,omitempty"`
	RemainingBountyValue float64          `json:"remaining_bounty_value"`
}

// PaidBountyItem represents a single paid bounty in the list response
type PaidBountyItem struct {
	Signature            string    `json:"signature"`
	Timestamp            time.Time `json:"timestamp"`
	RecipientOwnerWallet string    `json:"recipient_owner_wallet"`
	Amount               float64   `json:"amount"`         // Amount in USDC
	Memo                 string    `json:"memo,omitempty"` // Transaction memo, if any
}

// AssessContentRequest represents the request body for assessing content against requirements
type AssessContentRequest struct {
	BountyID     string           `json:"bounty_id"`
	ContentID    string           `json:"content_id"`
	PayoutWallet string           `json:"payout_wallet"`
	Platform     abb.PlatformKind `json:"platform"`
	ContentKind  abb.ContentKind  `json:"content_kind"`
}

// AssessContentResponse represents the response from content assessment
type AssessContentResponse struct {
	Satisfies bool   `json:"satisfies"`
	Reason    string `json:"reason"`
}

var ErrRetryNeededAfterRateLimit = errors.New("rate limited, retry needed after wait")

// handlePayBounty handles the payment of a bounty to a user
func handlePayBounty(l *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req PayBountyRequest
		if err := stools.DecodeJSONBody(r, &req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request: %w", err))
			return
		}

		// Validate required field: wallet
		if req.Wallet == "" {
			writeBadRequestError(w, fmt.Errorf("invalid request: wallet is required"))
			return
		}
		// Also validate Wallet is a valid Solana address
		if _, err := solanago.PublicKeyFromBase58(req.Wallet); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid wallet address '%s': %w", req.Wallet, err))
			return
		}

		// Convert amount to USDCAmount
		amount, err := solana.NewUSDCAmount(req.Amount)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid amount: %w", err))
			return
		}

		// Create Solana config (Assuming default escrow account is used for sending)
		privateKeyStr := os.Getenv(EnvSolanaEscrowPrivateKey)
		escrowWalletStr := os.Getenv(EnvSolanaEscrowWallet) // Use owner wallet

		if privateKeyStr == "" {
			writeInternalError(l, w, fmt.Errorf("%s must be set", EnvSolanaEscrowPrivateKey))
			return
		}
		if escrowWalletStr == "" {
			writeInternalError(l, w, fmt.Errorf("%s must be set", EnvSolanaEscrowWallet))
			return
		}

		escrowPrivateKey, err := solanago.PrivateKeyFromBase58(privateKeyStr)
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to parse escrow private key: %w", err))
			return
		}

		escrowWallet, err := solanago.PublicKeyFromBase58(escrowWalletStr) // Parse owner wallet
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to parse escrow wallet: %w", err))
			return
		}

		solanaConfig := abb.SolanaConfig{
			RPCEndpoint:      os.Getenv(EnvSolanaRPCEndpoint),
			WSEndpoint:       os.Getenv(EnvSolanaWSEndpoint),
			EscrowPrivateKey: &escrowPrivateKey,
			EscrowWallet:     escrowWallet, // Assign owner wallet
			USDCMintAddress:  os.Getenv(EnvSolanaUSDCMintAddress),
		}

		// Execute workflow
		workflowID := fmt.Sprintf("pay-bounty-%s", uuid.New().String())
		workflowOptions := client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: os.Getenv(EnvTaskQueue),
		}

		// Pass req.Wallet to the workflow input field (assuming it's named ToAccount there)
		we, err := tc.ExecuteWorkflow(r.Context(), workflowOptions, abb.PayBountyWorkflow, abb.PayBountyWorkflowInput{
			Wallet:       req.Wallet,
			Amount:       amount,
			SolanaConfig: solanaConfig,
		})
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to start workflow: %w", err))
			return
		}

		// Wait for workflow completion
		if err := we.Get(r.Context(), nil); err != nil {
			writeInternalError(l, w, fmt.Errorf("workflow failed: %w", err))
			return
		}

		writeJSONResponse(w, DefaultJSONResponse{
			Message: fmt.Sprintf("Successfully executed payment of %v USDC to %s",
				amount.ToUSDC(), req.Wallet),
		}, http.StatusOK)
	}
}

// handleCreateBounty handles the creation of a new bounty and starts a workflow
func handleCreateBounty(logger *slog.Logger, tc client.Client, payoutCalculator PayoutCalculator, env string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateBountyRequest
		if err := stools.DecodeJSONBody(r, &req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request: %w", err))
			return
		}

		// Validate request
		if len(req.Requirements) == 0 {
			writeBadRequestError(w, fmt.Errorf("requirements is required"))
			return
		}
		if req.BountyPerPost <= 0 {
			writeBadRequestError(w, fmt.Errorf("bounty_per_post must be greater than 0"))
			return
		}
		if req.TotalBounty <= 0 {
			writeBadRequestError(w, fmt.Errorf("total_bounty must be greater than 0"))
			return
		}
		if req.BountyOwnerWallet == "" {
			writeBadRequestError(w, fmt.Errorf("bounty_owner_wallet is required"))
			return
		}
		if req.BountyFunderWallet == "" {
			writeBadRequestError(w, fmt.Errorf("bounty_funder_wallet is required"))
			return
		}

		// --- Requirements Length Check ---
		requirementsStr := strings.Join(req.Requirements, "\n") // Join with newline for length check
		if len(requirementsStr) > abb.MaxRequirementsCharsForLLMCheck {
			warnMsg := fmt.Sprintf("Total length of requirements exceeds maximum limit (%d > %d)", len(requirementsStr), abb.MaxRequirementsCharsForLLMCheck)
			logger.Warn(warnMsg)
			writeBadRequestError(w, fmt.Errorf("%s", warnMsg))
			return
		}
		// --- End Requirements Length Check ---

		// Normalize platform and content kind to lowercase for consistent handling
		normalizedPlatform := abb.PlatformKind(strings.ToLower(string(req.PlatformType)))
		normalizedContentKind := abb.ContentKind(strings.ToLower(string(req.ContentKind)))

		// Validate platform type and content kind using normalized values
		switch normalizedPlatform {
		case abb.PlatformReddit:
			if normalizedContentKind != abb.ContentKindPost && normalizedContentKind != abb.ContentKindComment {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for Reddit: must be '%s' or '%s'", abb.ContentKindPost, abb.ContentKindComment))
				return
			}
		case abb.PlatformYouTube:
			if normalizedContentKind != abb.ContentKindVideo {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for YouTube: must be '%s'", abb.ContentKindVideo))
				return
			}
		case abb.PlatformTwitch:
			if normalizedContentKind != abb.ContentKindVideo && normalizedContentKind != abb.ContentKindClip {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for Twitch: must be '%s' or '%s'", abb.ContentKindVideo, abb.ContentKindClip))
				return
			}
		case abb.PlatformHackerNews:
			if normalizedContentKind != abb.ContentKindPost && normalizedContentKind != abb.ContentKindComment {
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for Hacker News: must be '%s' or '%s'", abb.ContentKindPost, abb.ContentKindComment))
				return
			}
		case abb.PlatformBluesky:
			if normalizedContentKind != abb.ContentKindPost { // Assuming only posts for Bluesky initially
				writeBadRequestError(w, fmt.Errorf("invalid content_kind for Bluesky: must be '%s'", abb.ContentKindPost))
				return
			}
		default:
			writeBadRequestError(w, fmt.Errorf("invalid platform_kind: must be one of %s, %s, %s, %s, or %s", abb.PlatformReddit, abb.PlatformYouTube, abb.PlatformTwitch, abb.PlatformHackerNews, abb.PlatformBluesky))
			return
		}

		// Read and validate overall bounty timeout
		bountyTimeoutDuration := 7 * 24 * time.Hour
		if req.TimeoutDuration != "" {
			duration, err := time.ParseDuration(req.TimeoutDuration)
			if err != nil {
				writeBadRequestError(w, fmt.Errorf("invalid timeout_duration format: %w", err))
				return
			}
			bountyTimeoutDuration = duration
		}

		// Validate minimum duration
		if bountyTimeoutDuration < 24*time.Hour {
			writeBadRequestError(w, fmt.Errorf("timeout_duration must be at least 24 hours (e.g., \"24h\")"))
			return
		}

		// Conditionally add timestamp requirement for prod environment
		if env == "prod" {
			currentTime := time.Now().UTC().Format("2006-01-02")
			timestampReq := fmt.Sprintf("Content must be created after %s", currentTime)
			noEditReq := "If the content contains an field indicating whether it was edited, the content must not have been edited."
			req.Requirements = append(req.Requirements, timestampReq, noEditReq)
		}

		// Apply revenue sharing using the calculator function
		userBountyPerPost := req.BountyPerPost
		userTotalBounty := payoutCalculator(req.TotalBounty)

		// --- START: Validate BountyPerPost against effective TotalBounty ---
		if userBountyPerPost > userTotalBounty {
			errMsg := fmt.Sprintf("bounty_per_post (%.6f) cannot be greater than the effective total_bounty after fees (%.6f)", userBountyPerPost, userTotalBounty)
			logger.Warn(errMsg, "raw_bounty_per_post", req.BountyPerPost, "raw_total_bounty", req.TotalBounty, "effective_total_bounty", userTotalBounty)
			writeBadRequestError(w, fmt.Errorf("%s", errMsg))
			return
		}
		// --- END: Validate BountyPerPost against effective TotalBounty ---

		// Convert amounts to USDCAmount
		bountyPerPost, err := solana.NewUSDCAmount(userBountyPerPost)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid bounty_per_post amount: %w", err))
			return
		}
		totalBounty, err := solana.NewUSDCAmount(userTotalBounty)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid total_bounty amount: %w", err))
			return
		}

		// Convert ORIGINAL total bounty for verification
		totalCharged, err := solana.NewUSDCAmount(req.TotalBounty)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid original total_bounty amount: %w", err))
			return
		}

		// Create workflow input
		input := abb.BountyAssessmentWorkflowInput{
			Requirements:       req.Requirements,
			BountyPerPost:      bountyPerPost,
			TotalBounty:        totalBounty,
			TotalCharged:       totalCharged,
			BountyOwnerWallet:  req.BountyOwnerWallet,
			BountyFunderWallet: req.BountyFunderWallet,
			Platform:           normalizedPlatform,
			ContentKind:        normalizedContentKind,
			Timeout:            bountyTimeoutDuration,
			PaymentTimeout:     10 * time.Minute,
			TreasuryWallet:     os.Getenv(EnvSolanaTreasuryWallet),
			EscrowWallet:       os.Getenv(EnvSolanaEscrowWallet),
		}

		// Execute workflow
		workflowID := fmt.Sprintf("bounty-%s", uuid.New().String())
		now := time.Now().UTC()

		// Create typed search attributes using defined keys
		saMap := temporal.NewSearchAttributes(
			abb.EnvironmentKey.ValueSet(env),
			abb.BountyOwnerWalletKey.ValueSet(input.BountyOwnerWallet),
			abb.BountyFunderWalletKey.ValueSet(input.BountyFunderWallet),
			abb.BountyPlatformKey.ValueSet(string(input.Platform)),
			abb.BountyContentKindKey.ValueSet(string(input.ContentKind)),
			abb.BountyTotalAmountKey.ValueSet(input.TotalBounty.ToUSDC()),
			abb.BountyPerPostAmountKey.ValueSet(input.BountyPerPost.ToUSDC()),
			abb.BountyCreationTimeKey.ValueSet(now),
			abb.BountyTimeoutTimeKey.ValueSet(now.Add(input.Timeout)),
			abb.BountyStatusKey.ValueSet(string(abb.BountyStatusAwaitingFunding)),
			abb.BountyValueRemainingKey.ValueSet(input.TotalBounty.ToUSDC()),
		)

		workflowOptions := client.StartWorkflowOptions{
			ID:                    workflowID,
			TaskQueue:             os.Getenv(EnvTaskQueue),
			TypedSearchAttributes: saMap,
		}

		_, err = tc.ExecuteWorkflow(r.Context(), workflowOptions, abb.BountyAssessmentWorkflow, input)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to start workflow: %w", err))
			return
		}

		writeJSONResponse(w, DefaultJSONResponse{
			Message: fmt.Sprintf("Workflow started: %s", workflowID),
		}, http.StatusOK)
	}
}

// handleListBounties handles listing all bounties
func handleListBounties(l *slog.Logger, tc client.Client, env string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		// List workflows of type BountyAssessmentWorkflow
		// Use a background context for listing, as it might take time
		listCtx, listCancel := context.WithTimeout(context.Background(), 30*time.Second) // Timeout for the whole list operation
		defer listCancel()

		// Combine WorkflowType, ExecutionStatus, and Environment for the query
		query := fmt.Sprintf(`WorkflowType = '%s' AND ExecutionStatus = 'Running' AND %s = '%s'`,
			"BountyAssessmentWorkflow", abb.EnvironmentKey.GetName(), env)
		executions, err := tc.ListWorkflow(listCtx, &workflowservice.ListWorkflowExecutionsRequest{
			Query: query,
		})
		if err != nil {
			if listCtx.Err() == context.DeadlineExceeded {
				writeInternalError(l, w, fmt.Errorf("timed out listing bounties: %w", err))
			} else {
				writeInternalError(l, w, fmt.Errorf("failed to list bounties: %w", err))
			}
			return
		}

		// Convert workflows to bounty list items
		bounties := make([]BountyListItem, 0)
		for _, execution := range executions.Executions {
			// Get workflow input from history
			var input abb.BountyAssessmentWorkflowInput

			// Use a short timeout for fetching history for *each* workflow
			histCtx, histCancel := context.WithTimeout(context.Background(), 5*time.Second)

			historyIterator := tc.GetWorkflowHistory(histCtx, execution.Execution.WorkflowId, execution.Execution.RunId, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
			if !historyIterator.HasNext() {
				l.Error("failed to get workflow history or history is empty", "workflow_id", execution.Execution.WorkflowId)
				histCancel() // Ensure context is cancelled
				continue
			}

			event, err := historyIterator.Next()
			if err != nil {
				l.Error("failed to get first history event", "error", err, "workflow_id", execution.Execution.WorkflowId)
				histCancel()
				continue
			}

			if event.GetEventType() != enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED {
				l.Error("first history event is not WorkflowExecutionStarted", "event_type", event.GetEventType(), "workflow_id", execution.Execution.WorkflowId)
				histCancel()
				continue
			}

			attrs := event.GetWorkflowExecutionStartedEventAttributes()
			if attrs == nil || attrs.Input == nil || len(attrs.Input.Payloads) == 0 {
				l.Error("WorkflowExecutionStarted event missing input attributes", "workflow_id", execution.Execution.WorkflowId)
				histCancel()
				continue
			}

			// Decode the input payload
			// Assuming input is the first payload
			err = converter.GetDefaultDataConverter().FromPayload(attrs.Input.Payloads[0], &input)
			if err != nil {
				l.Error("failed to decode workflow input from history", "error", err, "workflow_id", execution.Execution.WorkflowId)
				histCancel()
				continue
			}

			histCancel() // Cancel context as soon as history is fetched

			// Extract EndTime from Search Attributes (from the ListWorkflow response)
			var endTime time.Time
			var status string

			if execution.SearchAttributes != nil {
				if saPayload, ok := execution.SearchAttributes.GetIndexedFields()[abb.BountyTimeoutTimeKey.GetName()]; ok {
					err = converter.GetDefaultDataConverter().FromPayload(saPayload, &endTime)
					if err != nil {
						l.Warn("Failed to decode BountyTimeoutTime search attribute for list item", "workflow_id", execution.Execution.WorkflowId, "error", err)
						endTime = time.Time{} // Set zero value on error
					}
				} else {
					l.Warn("BountyTimeoutTime search attribute not found for list item", "workflow_id", execution.Execution.WorkflowId)
					endTime = time.Time{} // Set zero value if missing
				}

				// Get BountyStatus from Search Attributes
				if statusPayload, ok := execution.SearchAttributes.GetIndexedFields()[abb.BountyStatusKey.GetName()]; ok {
					var decodedStatus string
					err = converter.GetDefaultDataConverter().FromPayload(statusPayload, &decodedStatus)
					if err != nil {
						l.Warn("Failed to decode BountyStatus search attribute for list item", "workflow_id", execution.Execution.WorkflowId, "error", err)
						status = execution.Status.String() // Fallback to Temporal status
					} else {
						status = decodedStatus
					}
				} else {
					l.Warn("BountyStatus search attribute not found for list item", "workflow_id", execution.Execution.WorkflowId)
					status = execution.Status.String() // Fallback to Temporal status
				}
			} else {
				l.Warn("SearchAttributes missing in ListWorkflow response item", "workflow_id", execution.Execution.WorkflowId)
				endTime = time.Time{}
				status = execution.Status.String()
			}

			var remainingBountyValue float64
			if val, ok := execution.SearchAttributes.GetIndexedFields()[abb.BountyValueRemainingKey.GetName()]; ok {
				err = converter.GetDefaultDataConverter().FromPayload(val, &remainingBountyValue)
				if err != nil {
					l.Error("Failed to decode BountyValueRemainingKey", "error", err, "workflow_id", execution.Execution.WorkflowId)
					remainingBountyValue = 0.0 // Default to 0 on error
				}
			} else {
				l.Warn("BountyValueRemainingKey not found", "workflow_id", execution.Execution.WorkflowId)
				// Default remaining to total if not found (might happen for older workflows or before initial set)
				remainingBountyValue = input.TotalBounty.ToUSDC()
			}

			bounties = append(bounties, BountyListItem{
				WorkflowID:           execution.Execution.WorkflowId,
				Status:               status, // Use the status derived from search attribute
				Requirements:         input.Requirements,
				BountyPerPost:        input.BountyPerPost.ToUSDC(),
				TotalBounty:          input.TotalBounty.ToUSDC(),
				RemainingBountyValue: remainingBountyValue,
				BountyOwnerWallet:    input.BountyOwnerWallet,
				PlatformType:         input.Platform,
				ContentKind:          input.ContentKind,
				CreatedAt:            execution.StartTime.AsTime(),
				EndTime:              endTime,
			})
		}

		writeJSONResponse(w, bounties, http.StatusOK)
	}
}

// handleAssessContent handles assessing content against requirements
func handleAssessContent(l *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req AssessContentRequest
		if err := stools.DecodeJSONBody(r, &req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request: %w", err))
			return
		}

		if req.BountyID == "" || req.ContentID == "" || req.PayoutWallet == "" || req.Platform == "" {
			writeBadRequestError(w, fmt.Errorf("invalid request: missing required fields: bounty_id, content_id, payout_wallet, platform: %v", req))
			return
		}

		// Validate that PayoutWallet is a valid Solana address
		if _, err := solanago.PublicKeyFromBase58(req.PayoutWallet); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid payout_wallet address '%s': %w", req.PayoutWallet, err))
			return
		}

		// Normalize platform and content kind to lowercase for consistent handling
		normalizedPlatform := abb.PlatformKind(strings.ToLower(string(req.Platform)))
		normalizedContentKind := abb.ContentKind(strings.ToLower(string(req.ContentKind)))

		// Signal the workflow
		err := tc.SignalWorkflow(r.Context(), req.BountyID, "", abb.AssessmentSignalName, abb.AssessContentSignal{
			ContentID:    req.ContentID,
			PayoutWallet: req.PayoutWallet,
			Platform:     normalizedPlatform,    // Use normalized value
			ContentKind:  normalizedContentKind, // Use normalized value
		})
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to signal workflow: %w", err))
			return
		}

		// Return success response
		writeJSONResponse(w, DefaultJSONResponse{
			Message: "Content assessment initiated",
		}, http.StatusOK)
	}
}

// handleListPaidBounties handles listing all paid bounties
func handleListPaidBounties(
	l *slog.Logger,
	rpcClient *solanagrpc.Client,
	escrowWallet solanago.PublicKey,
	usdcMintAddress solanago.PublicKey,
	cacheDuration time.Duration,
) http.HandlerFunc {
	// Local struct to represent a generic JSON-RPC response
	// This is needed because the library doesn't export a generic jsonrpc.Response type
	// for direct unmarshalling in the RPCCallWithCallback.
	type jsonRPCResponseWrapper struct {
		JSONRPC string            `json:"jsonrpc"`
		ID      interface{}       `json:"id"` // Can be int or string
		Result  json.RawMessage   `json:"result,omitempty"`
		Error   *jsonrpc.RPCError `json:"error,omitempty"` // Using the exported RPCError from the library
	}

	// cache structure for paid bounties
	type paidBountiesCache struct {
		sync.RWMutex
		data      []PaidBountyItem
		timestamp time.Time
	}

	var listPaidBountiesCache paidBountiesCache
	var initCacheOnce sync.Once         // For initializing the cache struct
	var backgroundRefreshOnce sync.Once // For starting the background refresh goroutine

	initCacheOnce.Do(func() { // Call Do on the instance
		listPaidBountiesCache = paidBountiesCache{
			data:      make([]PaidBountyItem, 0),
			timestamp: time.Time{}, // Initialize timestamp to zero value
		}
	})

	// refreshCacheContent contains the logic to fetch bounties and update the cache
	refreshCacheContent := func() {
		l.Debug("Starting background cache refresh for paid bounties")
		const backgroundFetchLimit = 100 // Define a limit for background fetching

		treasuryWalletAddress := os.Getenv(EnvSolanaTreasuryWallet)
		if treasuryWalletAddress == "" {
			l.Warn("Background refresh: SOLANA_TREASURY_WALLET env var not set. Treasury transaction filtering will be skipped.")
		}
		// Derive the Associated Token Account (ATA) for the escrow wallet
		escrowATA, _, err := solanago.FindAssociatedTokenAddress(escrowWallet, usdcMintAddress)
		if err != nil {
			l.Error("Background refresh: failed to find escrow ATA", "error", err)
			return
		}

		// Fetch recent transaction signatures for the escrow ATA
		fetchLimit := backgroundFetchLimit // Define variable for limit
		signatures, err := rpcClient.GetSignaturesForAddressWithOpts(context.Background(), escrowATA, &solanagrpc.GetSignaturesForAddressOpts{
			Limit:      &fetchLimit, // Use address of the variable
			Commitment: solanagrpc.CommitmentFinalized,
		})
		if err != nil {
			l.Error("Background refresh: failed to get signatures for escrow ATA", "ata", escrowATA.String(), "error", err)
			return
		}

		if len(signatures) == 0 {
			l.Debug("Background refresh: no new signatures found for escrow ATA", "ata", escrowATA.String())
			// Update cache with empty data if no signatures found, to reflect current state
			listPaidBountiesCache.Lock()
			listPaidBountiesCache.data = make([]PaidBountyItem, 0) // Explicitly set to empty, not nil
			listPaidBountiesCache.timestamp = time.Now()
			listPaidBountiesCache.Unlock()
			l.Debug("Background refresh: cache updated with empty paid bounties list.")
			return
		}

		paidBounties := make([]PaidBountyItem, 0, len(signatures))
		for _, sigInfo := range signatures {
			if sigInfo.Err != nil {
				l.Warn("Background refresh: Skipping signature with error", "signature", sigInfo.Signature.String(), "error", sigInfo.Err)
				continue
			}

			var txDetails *solanagrpc.GetTransactionResult
			var attemptError error

			// Retry loop for the current signature
			for {
				maxSupportedTxVersion := uint64(0)
				options := &solanagrpc.GetTransactionOpts{
					Encoding:                       solanago.EncodingBase64,
					Commitment:                     solanagrpc.CommitmentFinalized,
					MaxSupportedTransactionVersion: &maxSupportedTxVersion,
				}
				params := []interface{}{
					sigInfo.Signature.String(),
					options,
				}

				// Variable to store the result from the callback
				var currentTxDetails *solanagrpc.GetTransactionResult

				errCallback := rpcClient.RPCCallWithCallback(
					context.Background(),
					"getTransaction",
					params,
					func(httpRequest *http.Request, httpResponse *http.Response) error {
						if httpResponse.StatusCode == http.StatusTooManyRequests { // 429
							retryAfterHeader := httpResponse.Header.Get("Retry-After")
							var sleepDuration time.Duration
							if retryAfterHeader != "" {
								seconds, errParse := strconv.Atoi(retryAfterHeader)
								if errParse == nil {
									sleepDuration = time.Duration(seconds) * time.Second
								} else {
									l.Warn("Background refresh: could not parse Retry-After header, using default", "header", retryAfterHeader, "error", errParse)
									sleepDuration = 5 * time.Second // Default backoff
								}
							} else {
								sleepDuration = 5 * time.Second // Default backoff
							}
							l.Info("Background refresh: rate limited, sleeping", "duration", sleepDuration, "signature", sigInfo.Signature.String())
							time.Sleep(sleepDuration)
							return ErrRetryNeededAfterRateLimit
						}

						if httpResponse.StatusCode >= 400 {
							bodyBytes, _ := io.ReadAll(httpResponse.Body)
							_ = httpResponse.Body.Close()
							l.Error("Background refresh: HTTP error from RPC", "statusCode", httpResponse.StatusCode, "body", string(bodyBytes), "signature", sigInfo.Signature.String())
							return fmt.Errorf("rpc http error: %d for signature %s", httpResponse.StatusCode, sigInfo.Signature.String())
						}

						bodyBytes, errRead := io.ReadAll(httpResponse.Body)
						if errRead != nil {
							_ = httpResponse.Body.Close()
							return fmt.Errorf("background refresh: failed to read response body for %s: %w", sigInfo.Signature.String(), errRead)
						}
						_ = httpResponse.Body.Close()

						var genericRpcResp jsonRPCResponseWrapper // Use the local wrapper type
						if errUnmarshal := json.Unmarshal(bodyBytes, &genericRpcResp); errUnmarshal != nil {
							return fmt.Errorf("background refresh: failed to unmarshal RPC response for %s: %w", sigInfo.Signature.String(), errUnmarshal)
						}

						if genericRpcResp.Error != nil {
							return fmt.Errorf("background refresh: RPC error for %s: code=%d, message=%s", sigInfo.Signature.String(), genericRpcResp.Error.Code, genericRpcResp.Error.Message)
						}

						var specificResult solanagrpc.GetTransactionResult
						if errUnmarshalResult := json.Unmarshal(genericRpcResp.Result, &specificResult); errUnmarshalResult != nil {
							return fmt.Errorf("background refresh: failed to unmarshal GetTransactionResult for %s: %w", sigInfo.Signature.String(), errUnmarshalResult)
						}
						currentTxDetails = &specificResult
						return nil
					},
				)

				if errCallback == nil {
					txDetails = currentTxDetails // Successfully fetched and decoded
					attemptError = nil
					break // Success, break retry loop
				} else if errors.Is(errCallback, ErrRetryNeededAfterRateLimit) {
					l.Info("Background refresh: retrying signature after rate limit wait", "signature", sigInfo.Signature.String())
					attemptError = errCallback // Store this error, will be overwritten if next attempt succeeds
					continue                   // Retry the same signature
				} else {
					// Any other error from RPCCallWithCallback or the callback itself
					attemptError = errCallback
					l.Error("Background refresh: failed to get transaction details, not retrying", "signature", sigInfo.Signature.String(), "error", attemptError)
					break // Break retry loop, effectively skipping this signature
				}
			} // End retry loop

			if attemptError != nil || txDetails == nil {
				// If after retries (or on first non-retryable error) we still failed or txDetails is nil
				if !errors.Is(attemptError, ErrRetryNeededAfterRateLimit) { // Avoid double logging if last attempt was rate limit
					l.Error("Background refresh: Failed to get transaction details after attempts", "signature", sigInfo.Signature.String(), "final_error", attemptError)
				}
				continue // Move to the next signature
			}

			if txDetails == nil || txDetails.Meta == nil { // txDetails replaces 'tx' from original code
				l.Warn("Background refresh: Skipping transaction without metadata", "signature", sigInfo.Signature.String())
				continue
			}

			var recipientOwnerWallet string
			var transferAmountLamports uint64

			for i, preBal := range txDetails.Meta.PreTokenBalances {
				if i >= len(txDetails.Meta.PostTokenBalances) {
					break
				}
				postBal := txDetails.Meta.PostTokenBalances[i]
				if preBal.Mint.Equals(usdcMintAddress) && preBal.Owner.Equals(escrowWallet) {
					preAmount, _ := strconv.ParseUint(preBal.UiTokenAmount.Amount, 10, 64)
					postAmount, _ := strconv.ParseUint(postBal.UiTokenAmount.Amount, 10, 64)
					if postAmount < preAmount {
						transferAmountLamports = preAmount - postAmount
						for j, destPreBal := range txDetails.Meta.PreTokenBalances {
							if j >= len(txDetails.Meta.PostTokenBalances) {
								break
							}
							destPostBal := txDetails.Meta.PostTokenBalances[j]
							if destPreBal.Mint.Equals(usdcMintAddress) {
								destPreAmount, _ := strconv.ParseUint(destPreBal.UiTokenAmount.Amount, 10, 64)
								destPostAmount, _ := strconv.ParseUint(destPostBal.UiTokenAmount.Amount, 10, 64)
								if destPostAmount > destPreAmount && (destPostAmount-destPreAmount) == transferAmountLamports {
									recipientOwnerWallet = destPostBal.Owner.String()
									break
								}
							}
						}
						break
					}
				}
			}

			if recipientOwnerWallet != "" && transferAmountLamports > 0 {
				// Skip transactions to the treasury wallet
				if treasuryWalletAddress != "" && recipientOwnerWallet == treasuryWalletAddress {
					l.Debug("Background refresh: Skipping transaction to treasury wallet", "signature", sigInfo.Signature.String(), "recipient", recipientOwnerWallet, "amount_lamports", transferAmountLamports)
					continue
				}

				amountUSDC := float64(transferAmountLamports) / math.Pow10(6)
				paidBounty := PaidBountyItem{
					Signature:            sigInfo.Signature.String(),
					Timestamp:            time.Unix(int64(*sigInfo.BlockTime), 0).UTC(),
					RecipientOwnerWallet: recipientOwnerWallet,
					Amount:               amountUSDC,
				}
				if sigInfo.Memo != nil {
					paidBounty.Memo = *sigInfo.Memo
				}
				paidBounties = append(paidBounties, paidBounty)
			} else {
				l.Debug("Background refresh: No outgoing USDC transfer from escrow ATA found in transaction", "signature", sigInfo.Signature.String())
			}
		}

		l.Info("Background refresh: Finished processing signatures", "found_payments", len(paidBounties))

		listPaidBountiesCache.Lock()
		listPaidBountiesCache.data = paidBounties
		listPaidBountiesCache.timestamp = time.Now()
		listPaidBountiesCache.Unlock()
		l.Info("Background refresh: Updated paid bounties cache")
	}
	backgroundRefreshOnce.Do(func() {
		go func() {
			// Perform initial refresh
			refreshCacheContent()

			// Start periodic refresh
			ticker := time.NewTicker(cacheDuration)
			defer ticker.Stop()

			for {
				// Using r.Context().Done() here is not ideal as this goroutine
				// is not tied to a single request. A global application stop channel
				// would be a better way to handle graceful shutdown.
				// For now, the loop will run as long as the application.
				select {
				case <-ticker.C:
					refreshCacheContent()
					// case <-r.Context().Done(): // Example of how one might try to stop it (with caveats)
					// 	l.Info("Background cache refresh stopping.")
					//  return
				}
			}
		}()
		l.Info("Background cache refresh goroutine started.")
	})

	return func(w http.ResponseWriter, r *http.Request) {

		listPaidBountiesCache.RLock()
		cachedData := listPaidBountiesCache.data
		// cacheTime := listPaidBountiesCache.timestamp // Could be used for Last-Modified header
		listPaidBountiesCache.RUnlock()

		dataToServe := cachedData
		if dataToServe == nil {
			// This case means the cache struct is initialized, but the first refresh
			// hasn't completed or resulted in nil (which it shouldn't with make).
			// More likely, refreshCacheContent hasn't run or is in progress for the very first time.
			// Returning an empty list is safe.
			l.Debug("Serving paid bounties: cache is nil (likely pre-initialization or empty state), returning empty list.")
			dataToServe = make([]PaidBountyItem, 0) // Ensure it's an empty slice, not nil for JSON
		}

		// Limit to the 10 most recent bounties
		recentBountyCountLimit := 10
		if r.URL.Query().Get("limit") != "" {
			limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
			if err == nil && limit > 0 {
				recentBountyCountLimit = limit
			}
		}
		if len(dataToServe) > recentBountyCountLimit {
			dataToServe = dataToServe[:recentBountyCountLimit]
		}

		l.Debug("Serving paid bounties from cache", "item_count", len(dataToServe))
		writeJSONResponse(w, dataToServe, http.StatusOK)
	}
}

// handleListPaidBountiesForWorkflow handles listing paid bounties for a specific workflow via a query.
func handleListPaidBountiesForWorkflow(l *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.PathValue("bounty_id")
		if workflowID == "" {
			writeBadRequestError(w, fmt.Errorf("missing bounty_id in path"))
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second) // Use request context with timeout
		defer cancel()

		resp, err := tc.QueryWorkflow(ctx, workflowID, "", abb.GetPaidBountiesQueryType)
		if err != nil {
			var notFoundErr *serviceerror.NotFound
			var queryFailedErr *serviceerror.QueryFailed // Specific error for query failures

			if errors.As(err, &notFoundErr) {
				writeNotFoundError(w)
			} else if errors.As(err, &queryFailedErr) {
				// This can happen if the query handler isn't set up, or panics.
				writeInternalError(l, w, fmt.Errorf("failed to query workflow %s: %w", workflowID, err))
			} else {
				writeInternalError(l, w, fmt.Errorf("error querying workflow %s: %w", workflowID, err))
			}
			return
		}

		var paidBounties []abb.PayoutDetail
		if err := resp.Get(&paidBounties); err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to decode query result for workflow %s: %w", workflowID, err))
			return
		}

		if paidBounties == nil { // if query returns nil, make it an empty slice for JSON response
			paidBounties = make([]abb.PayoutDetail, 0)
		}

		// iterate over paidBounties and strip out content we don't want to expose
		// to the client, like the content_id and payout_wallet
		for i, _ := range paidBounties {
			paidBounties[i].ContentID = ""
			paidBounties[i].PayoutWallet = ""
		}

		writeJSONResponse(w, paidBounties, http.StatusOK)
	}
}

// handleGetBountyByID handles fetching details for a single bounty by its ID
func handleGetBountyByID(l *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workflowID := r.PathValue("id") // Extract ID from path e.g., /bounties/{id}
		if workflowID == "" {
			writeBadRequestError(w, fmt.Errorf("missing bounty ID in path"))
			return
		}

		// Use a background context with timeout for Temporal calls
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// Describe the workflow execution first to get status and potentially run ID
		descResp, err := tc.DescribeWorkflowExecution(ctx, workflowID, "") // RunID can be empty
		if err != nil {
			var notFoundErr *serviceerror.NotFound
			if errors.As(err, &notFoundErr) {
				writeEmptyResultError(w) // Use 404 for not found
			} else {
				writeInternalError(l, w, fmt.Errorf("failed to describe workflow %s: %w", workflowID, err))
			}
			return
		}

		// Ensure we have a valid execution info
		if descResp == nil || descResp.WorkflowExecutionInfo == nil || descResp.WorkflowExecutionInfo.Execution == nil {
			writeInternalError(l, w, fmt.Errorf("invalid description received for workflow %s", workflowID))
			return
		}

		// Get workflow input from history (similar to list handler)
		var input abb.BountyAssessmentWorkflowInput
		historyIterator := tc.GetWorkflowHistory(ctx, workflowID, descResp.WorkflowExecutionInfo.Execution.RunId, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
		if !historyIterator.HasNext() {
			writeInternalError(l, w, fmt.Errorf("failed to get workflow history or history is empty for %s", workflowID))
			return
		}

		event, err := historyIterator.Next()
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to get first history event for %s: %w", workflowID, err))
			return
		}

		if event.GetEventType() != enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED {
			writeInternalError(l, w, fmt.Errorf("first history event is not WorkflowExecutionStarted for %s", workflowID))
			return
		}

		attrs := event.GetWorkflowExecutionStartedEventAttributes()
		if attrs == nil || attrs.Input == nil || len(attrs.Input.Payloads) == 0 {
			writeInternalError(l, w, fmt.Errorf("WorkflowExecutionStarted event missing input attributes for %s", workflowID))
			return
		}

		err = converter.GetDefaultDataConverter().FromPayload(attrs.Input.Payloads[0], &input)
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to decode workflow input from history for %s: %w", workflowID, err))
			return
		}

		// Construct the response using BountyListItem
		// Extract EndTime from Search Attributes
		var endTime time.Time
		var status string // Variable to store the bounty status

		if descResp.WorkflowExecutionInfo != nil && descResp.WorkflowExecutionInfo.SearchAttributes != nil {
			if saPayload, ok := descResp.WorkflowExecutionInfo.SearchAttributes.GetIndexedFields()[abb.BountyTimeoutTimeKey.GetName()]; ok {
				err = converter.GetDefaultDataConverter().FromPayload(saPayload, &endTime)
				if err != nil {
					// Log error but maybe don't fail the request? Or return partial data?
					l.Warn("Failed to decode BountyTimeoutTime search attribute", "workflow_id", workflowID, "error", err)
					// Set endTime to zero value if decoding fails
					endTime = time.Time{}
				} else {
					l.Debug("Successfully decoded end time from search attribute", "workflow_id", workflowID, "end_time", endTime)
				}
			} else {
				l.Warn("BountyTimeoutTime search attribute not found", "workflow_id", workflowID)
				// Set endTime to zero value if attribute is missing
				endTime = time.Time{}
			}

			// Get BountyStatus from Search Attributes
			if statusPayload, ok := descResp.WorkflowExecutionInfo.SearchAttributes.GetIndexedFields()[abb.BountyStatusKey.GetName()]; ok {
				var decodedStatus string
				err = converter.GetDefaultDataConverter().FromPayload(statusPayload, &decodedStatus)
				if err != nil {
					l.Warn("Failed to decode BountyStatus search attribute", "workflow_id", workflowID, "error", err)
					status = descResp.WorkflowExecutionInfo.Status.String() // Fallback to Temporal status
				} else {
					status = decodedStatus
				}
			} else {
				l.Warn("BountyStatus search attribute not found", "workflow_id", workflowID)
				status = descResp.WorkflowExecutionInfo.Status.String() // Fallback to Temporal status
			}
		} else {
			l.Warn("SearchAttributes missing in DescribeWorkflowExecution response", "workflow_id", workflowID)
			endTime = time.Time{}                                   // Set zero value if missing
			status = descResp.WorkflowExecutionInfo.Status.String() // Fallback to Temporal status if all search attributes are missing
		}

		var remainingBountyValue float64
		if descResp.WorkflowExecutionInfo != nil && descResp.WorkflowExecutionInfo.SearchAttributes != nil {
			if val, ok := descResp.WorkflowExecutionInfo.SearchAttributes.GetIndexedFields()[abb.BountyValueRemainingKey.GetName()]; ok {
				err = converter.GetDefaultDataConverter().FromPayload(val, &remainingBountyValue)
				if err != nil {
					l.Error("Failed to decode BountyValueRemainingKey", "error", err, "workflow_id", workflowID)
					remainingBountyValue = 0.0 // Default to 0 on error
				}
			} else {
				l.Warn("BountyValueRemainingKey not found", "workflow_id", workflowID)
				// Default remaining to total if not found (might happen for older workflows or before initial set)
				remainingBountyValue = input.TotalBounty.ToUSDC()
			}
		} else {
			l.Warn("SearchAttributes missing in DescribeWorkflowExecution response", "workflow_id", workflowID)
			remainingBountyValue = 0.0 // Default to 0 if search attributes are missing
		}

		bountyDetail := BountyListItem{
			WorkflowID:           workflowID,
			Status:               status, // Use the status derived from search attribute
			Requirements:         input.Requirements,
			BountyPerPost:        input.BountyPerPost.ToUSDC(),
			TotalBounty:          input.TotalBounty.ToUSDC(),
			RemainingBountyValue: remainingBountyValue,
			BountyOwnerWallet:    input.BountyOwnerWallet,
			PlatformType:         input.Platform,
			ContentKind:          input.ContentKind,
			CreatedAt:            descResp.WorkflowExecutionInfo.StartTime.AsTime(),
			EndTime:              endTime,
		}

		writeJSONResponse(w, bountyDetail, http.StatusOK)
	}
}

// handleStoreBountySummary handles storing the final summary of a bounty.
// This is intended to be called by a Temporal activity at the end of a bounty workflow.
func handleStoreBountySummary(logger *slog.Logger, querier dbgen.Querier) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var summaryData abb.BountySummaryData
		if err := stools.DecodeJSONBody(r, &summaryData); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request body for bounty summary: %w", err))
			return
		}

		if summaryData.BountyID == "" {
			writeBadRequestError(w, fmt.Errorf("bounty_id is required in summary data"))
			return
		}

		// The summaryData is already in the correct struct form (abb.BountySummaryData)
		// We need to marshal it to json.RawMessage for sqlc, as the db column is JSONB.
		summaryJSON, err := json.Marshal(summaryData)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to marshal summary data to JSON for DB: %w", err))
			return
		}

		params := dbgen.UpsertBountySummaryParams{
			BountyID: summaryData.BountyID,
			Summary:  summaryJSON, // Pass the json.RawMessage
		}

		if err := querier.UpsertBountySummary(r.Context(), params); err != nil {
			// Check for specific DB errors if needed, e.g., constraint violations
			writeInternalError(logger, w, fmt.Errorf("failed to store bounty summary in DB: %w", err))
			return
		}

		logger.Info("Successfully stored bounty summary", "bounty_id", summaryData.BountyID, "final_status", summaryData.FinalStatus)
		writeJSONResponse(w, DefaultJSONResponse{Message: "Bounty summary stored successfully"}, http.StatusCreated)
	}
}
