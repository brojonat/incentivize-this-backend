package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/brojonat/affiliate-bounty-board/abb"
	"github.com/brojonat/affiliate-bounty-board/internal/stools"
	"github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/google/uuid"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
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
	PlatformType       abb.PlatformType `json:"platform_type"`
	PaymentTimeout     int              `json:"payment_timeout"` // Timeout in seconds
}

// BountyListItem represents a single bounty in the list response
type BountyListItem struct {
	WorkflowID        string           `json:"workflow_id"`
	Status            string           `json:"status"`
	Requirements      []string         `json:"requirements"`
	BountyPerPost     float64          `json:"bounty_per_post"`
	TotalBounty       float64          `json:"total_bounty"`
	BountyOwnerWallet string           `json:"bounty_owner_wallet"`
	PlatformType      abb.PlatformType `json:"platform_type"`
	CreatedAt         time.Time        `json:"created_at"`
}

// BountyLister defines the interface for listing bounties
type BountyLister interface {
	ListBounties(ctx context.Context) ([]BountyListItem, error)
}

// AssessContentRequest represents the request body for assessing content against requirements
type AssessContentRequest struct {
	BountyID     string           `json:"bounty_id"`
	ContentID    string           `json:"content_id"`
	PayoutWallet string           `json:"payout_wallet"`
	Platform     abb.PlatformType `json:"platform"`
}

// AssessContentResponse represents the response from content assessment
type AssessContentResponse struct {
	Satisfies bool   `json:"satisfies"`
	Reason    string `json:"reason"`
}

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
		privateKeyStr := os.Getenv("SOLANA_ESCROW_PRIVATE_KEY")
		tokenAccountStr := os.Getenv("SOLANA_ESCROW_TOKEN_ACCOUNT")

		if privateKeyStr == "" {
			writeInternalError(l, w, fmt.Errorf("SOLANA_ESCROW_PRIVATE_KEY must be set"))
			return
		}
		if tokenAccountStr == "" {
			writeInternalError(l, w, fmt.Errorf("SOLANA_ESCROW_TOKEN_ACCOUNT must be set"))
			return
		}

		escrowPrivateKey, err := solanago.PrivateKeyFromBase58(privateKeyStr)
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to parse escrow private key: %w", err))
			return
		}

		escrowTokenAccount, err := solanago.PublicKeyFromBase58(tokenAccountStr)
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to parse escrow token account: %w", err))
			return
		}

		solanaConfig := abb.SolanaConfig{
			RPCEndpoint:      os.Getenv("SOLANA_RPC_ENDPOINT"),
			WSEndpoint:       os.Getenv("SOLANA_WS_ENDPOINT"),
			EscrowPrivateKey: &escrowPrivateKey,
			EscrowWallet:     escrowTokenAccount,
		}

		// Execute workflow
		workflowID := fmt.Sprintf("pay-bounty-%s", uuid.New().String())
		workflowOptions := client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: os.Getenv("TASK_QUEUE"),
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
func handleCreateBounty(logger *slog.Logger, tc client.Client, payoutCalculator PayoutCalculator) http.HandlerFunc {
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

		// Validate platform type
		switch req.PlatformType {
		case abb.PlatformReddit, abb.PlatformYouTube:
			// Valid platform type
		default:
			writeBadRequestError(w, fmt.Errorf("invalid platform_type: must be one of reddit or youtube"))
			return
		}

		paymentTimeoutDuration := time.Duration(req.PaymentTimeout) * time.Second
		if paymentTimeoutDuration <= 0 {
			// Ensure a minimum positive timeout, using default if zero/negative
			logger.Warn("Invalid payment_timeout received, defaulting", "received", req.PaymentTimeout, "default_seconds", 10)
			paymentTimeoutDuration = 10 * time.Second // Default to 10 seconds
		}

		// Apply revenue sharing using the calculator function
		userBountyPerPost := payoutCalculator(req.BountyPerPost)
		userTotalBounty := payoutCalculator(req.TotalBounty)

		// Log the revenue sharing calculation
		logger.Info("Applied revenue sharing to bounty",
			"original_bounty_per_post", req.BountyPerPost,
			"original_total_bounty", req.TotalBounty,
			"user_bounty_per_post", userBountyPerPost,
			"user_total_bounty", userTotalBounty,
			"platform_fee_per_post", req.BountyPerPost-userBountyPerPost,
			"platform_fee_total", req.TotalBounty-userTotalBounty)

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

		// --- Populate SolanaConfig from Server Environment ---
		privateKeyStr := os.Getenv("SOLANA_ESCROW_PRIVATE_KEY")
		escrowWalletStr := os.Getenv("SOLANA_ESCROW_WALLET")
		rpcEndpoint := os.Getenv("SOLANA_RPC_ENDPOINT")
		wsEndpoint := os.Getenv("SOLANA_WS_ENDPOINT") // Optional, ok if empty
		usdcMintAddr := os.Getenv("SOLANA_USDC_MINT_ADDRESS")

		if privateKeyStr == "" {
			writeInternalError(logger, w, fmt.Errorf("server config error: SOLANA_ESCROW_PRIVATE_KEY not set"))
			return
		}
		if escrowWalletStr == "" {
			writeInternalError(logger, w, fmt.Errorf("server config error: SOLANA_ESCROW_WALLET not set"))
			return
		}
		if rpcEndpoint == "" {
			writeInternalError(logger, w, fmt.Errorf("server config error: SOLANA_RPC_ENDPOINT not set"))
			return
		}
		if usdcMintAddr == "" {
			writeInternalError(logger, w, fmt.Errorf("server config error: SOLANA_USDC_MINT_ADDRESS not set"))
			return
		}

		escrowPrivateKey, err := solanago.PrivateKeyFromBase58(privateKeyStr)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to parse escrow private key from env: %w", err))
			return
		}
		escrowWallet, err := solanago.PublicKeyFromBase58(escrowWalletStr)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to parse escrow wallet public key from env: %w", err))
			return
		}

		solanaConfig := abb.SolanaConfig{
			RPCEndpoint:      rpcEndpoint,
			WSEndpoint:       wsEndpoint,
			EscrowPrivateKey: &escrowPrivateKey,
			EscrowWallet:     escrowWallet,
			USDCMintAddress:  usdcMintAddr,
		}
		// --- End SolanaConfig Population ---

		// Convert ORIGINAL total bounty for verification
		originalTotalBountyAmount, err := solana.NewUSDCAmount(req.TotalBounty)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid original total_bounty amount: %w", err))
			return
		}

		// Create workflow input
		input := abb.BountyAssessmentWorkflowInput{
			Requirements:        req.Requirements,
			BountyPerPost:       bountyPerPost,             // User payable amount per post
			TotalBounty:         totalBounty,               // User payable total amount
			OriginalTotalBounty: originalTotalBountyAmount, // Original amount for verification
			BountyOwnerWallet:   req.BountyOwnerWallet,
			BountyFunderWallet:  req.BountyFunderWallet,
			PlatformType:        req.PlatformType,
			Timeout:             24 * time.Hour * 7,     // Default bounty active duration (e.g., 1 week)
			PaymentTimeout:      paymentTimeoutDuration, // Use duration from request
			SolanaConfig:        solanaConfig,           // Assign the populated config
		}

		// Execute workflow
		workflowID := fmt.Sprintf("bounty-%s", uuid.New().String())
		workflowOptions := client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: os.Getenv("TASK_QUEUE"),
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
func handleListBounties(l *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		// List workflows of type BountyAssessmentWorkflow
		// Use a background context for listing, as it might take time
		listCtx, listCancel := context.WithTimeout(context.Background(), 30*time.Second) // Timeout for the whole list operation
		defer listCancel()

		// Combine WorkflowType and ExecutionStatus for the query
		query := fmt.Sprintf(`WorkflowType = '%s' AND ExecutionStatus = 'Running'`, "BountyAssessmentWorkflow")
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

			bounties = append(bounties, BountyListItem{
				WorkflowID:        execution.Execution.WorkflowId,
				Status:            execution.Status.String(),
				Requirements:      input.Requirements,
				BountyPerPost:     input.BountyPerPost.ToUSDC(),
				TotalBounty:       input.TotalBounty.ToUSDC(),
				BountyOwnerWallet: input.BountyOwnerWallet,
				PlatformType:      input.PlatformType,
				CreatedAt:         execution.StartTime.AsTime(),
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

		// Signal the workflow
		err := tc.SignalWorkflow(r.Context(), req.BountyID, "", abb.AssessmentSignalName, abb.AssessContentSignal{
			ContentID:    req.ContentID,
			PayoutWallet: req.PayoutWallet,
			Platform:     req.Platform,
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
