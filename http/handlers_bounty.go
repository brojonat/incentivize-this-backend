package http

import (
	"context"
	"encoding/json"
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
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

// PayBountyRequest represents the request body for paying a bounty
type PayBountyRequest struct {
	UserID       string  `json:"user_id"`
	Amount       float64 `json:"amount"`
	SolanaWallet string  `json:"solana_wallet"`
	USDCAccount  string  `json:"usdc_account"`
}

// ReturnBountyToOwnerRequest represents the request body for returning a bounty to the owner
type ReturnBountyToOwnerRequest struct {
	OwnerID      string  `json:"owner_id"`
	Amount       float64 `json:"amount"`
	SolanaWallet string  `json:"solana_wallet"`
	USDCAccount  string  `json:"usdc_account"`
}

// CreateBountyRequest represents the request body for creating a new bounty
type CreateBountyRequest struct {
	RequirementsDescription string           `json:"requirements_description"`
	BountyPerPost           float64          `json:"bounty_per_post"`
	TotalBounty             float64          `json:"total_bounty"`
	OwnerID                 string           `json:"owner_id"`
	SolanaWallet            string           `json:"solana_wallet"`
	USDCAccount             string           `json:"usdc_account"`
	PlatformType            abb.PlatformType `json:"platform_type"`
}

// BountyListItem represents a single bounty in the list response
type BountyListItem struct {
	WorkflowID              string           `json:"workflow_id"`
	Status                  string           `json:"status"`
	RequirementsDescription string           `json:"requirements_description"`
	BountyPerPost           float64          `json:"bounty_per_post"`
	TotalBounty             float64          `json:"total_bounty"`
	OwnerID                 string           `json:"owner_id"`
	PlatformType            abb.PlatformType `json:"platform_type"`
	CreatedAt               time.Time        `json:"created_at"`
}

// BountyLister defines the interface for listing bounties
type BountyLister interface {
	ListBounties(ctx context.Context) ([]BountyListItem, error)
}

// handlePayBounty handles the payment of a bounty to a user
func handlePayBounty(l *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowedError(w)
			return
		}

		var req PayBountyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request body: %w", err))
			return
		}

		// Convert amount to USDCAmount
		amount, err := solana.NewUSDCAmount(req.Amount)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid amount: %w", err))
			return
		}

		// Create Solana config
		escrowWallet := solanago.NewWallet()
		solanaConfig := solana.SolanaConfig{
			RPCEndpoint:        os.Getenv("SOLANA_RPC_URL"),
			WSEndpoint:         os.Getenv("SOLANA_WS_URL"),
			EscrowPrivateKey:   &escrowWallet.PrivateKey,
			EscrowTokenAccount: escrowWallet.PublicKey(),
		}

		// Execute workflow
		workflow := abb.NewWorkflow(tc)
		err = workflow.PayBounty(r.Context(), abb.PayBountyWorkflowInput{
			FromAccount:  req.USDCAccount,
			ToAccount:    req.SolanaWallet,
			Amount:       amount,
			SolanaConfig: solanaConfig,
		})
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to pay bounty: %w", err))
			return
		}

		writeJSONResponse(w, DefaultJSONResponse{
			Message: fmt.Sprintf("Successfully initiated bounty payment of %v USDC", amount.ToUSDC()),
		}, http.StatusOK)
	}
}

// handleReturnBountyToOwner handles returning a bounty to its owner
func handleReturnBountyToOwner(l *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeMethodNotAllowedError(w)
			return
		}

		var req ReturnBountyToOwnerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid request body: %w", err))
			return
		}

		// Convert amount to USDCAmount
		amount, err := solana.NewUSDCAmount(req.Amount)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid amount: %w", err))
			return
		}

		// Create Solana config
		escrowWallet := solanago.NewWallet()
		solanaConfig := solana.SolanaConfig{
			RPCEndpoint:        os.Getenv("SOLANA_RPC_URL"),
			WSEndpoint:         os.Getenv("SOLANA_WS_URL"),
			EscrowPrivateKey:   &escrowWallet.PrivateKey,
			EscrowTokenAccount: escrowWallet.PublicKey(),
		}

		// Execute workflow
		workflow := abb.NewWorkflow(tc)
		err = workflow.ReturnBountyToOwner(r.Context(), abb.ReturnBountyToOwnerWorkflowInput{
			ToAccount:    req.SolanaWallet,
			Amount:       amount,
			SolanaConfig: solanaConfig,
		})
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to return bounty to owner: %w", err))
			return
		}

		writeJSONResponse(w, DefaultJSONResponse{
			Message: fmt.Sprintf("Successfully initiated bounty return of %v USDC", amount.ToUSDC()),
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
		if req.RequirementsDescription == "" {
			writeBadRequestError(w, fmt.Errorf("requirements_description is required"))
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
		if req.OwnerID == "" {
			writeBadRequestError(w, fmt.Errorf("owner_id is required"))
			return
		}
		if req.SolanaWallet == "" {
			writeBadRequestError(w, fmt.Errorf("solana_wallet is required"))
			return
		}
		if req.USDCAccount == "" {
			writeBadRequestError(w, fmt.Errorf("usdc_account is required"))
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

		// Create workflow input
		input := abb.BountyAssessmentWorkflowInput{
			RequirementsDescription: req.RequirementsDescription,
			BountyPerPost:           bountyPerPost,
			TotalBounty:             totalBounty,
			OwnerID:                 req.OwnerID,
			SolanaWallet:            req.SolanaWallet,
			USDCAccount:             req.USDCAccount,
			ServerURL:               os.Getenv("SERVER_URL"),
			AuthToken:               os.Getenv("AUTH_TOKEN"),
			PlatformType:            req.PlatformType,
		}

		// Start workflow
		workflowID := fmt.Sprintf("bounty-%s-%s", req.OwnerID, uuid.New().String())
		workflowOptions := client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: abb.TaskQueueName,
		}

		we, err := tc.ExecuteWorkflow(r.Context(), workflowOptions, abb.BountyAssessmentWorkflow, input)
		if err != nil {
			writeInternalError(logger, w, fmt.Errorf("failed to start workflow: %w", err))
			return
		}

		writeJSONResponse(w, DefaultJSONResponse{
			Message: fmt.Sprintf("Successfully started bounty assessment workflow with ID %s", we.GetID()),
		}, http.StatusOK)
	}
}

// handleListBounties handles listing all bounties
func handleListBounties(l *slog.Logger, tc client.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowedError(w)
			return
		}

		// List workflows of type BountyAssessmentWorkflow
		query := fmt.Sprintf(`WorkflowType = '%s'`, "BountyAssessmentWorkflow")
		executions, err := tc.ListWorkflow(r.Context(), &workflowservice.ListWorkflowExecutionsRequest{
			Query: query,
		})
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to list bounties: %w", err))
			return
		}

		// Convert workflows to bounty list items
		bounties := make([]BountyListItem, 0)
		for _, execution := range executions.Executions {
			// Get workflow input
			var input abb.BountyAssessmentWorkflowInput
			err = tc.GetWorkflow(r.Context(), execution.Execution.WorkflowId, execution.Execution.RunId).Get(r.Context(), &input)
			if err != nil {
				l.Error("failed to get workflow input", "error", err, "workflow_id", execution.Execution.WorkflowId)
				continue
			}

			bounties = append(bounties, BountyListItem{
				WorkflowID:              execution.Execution.WorkflowId,
				Status:                  execution.Status.String(),
				RequirementsDescription: input.RequirementsDescription,
				BountyPerPost:           input.BountyPerPost.ToUSDC(),
				TotalBounty:             input.TotalBounty.ToUSDC(),
				OwnerID:                 input.OwnerID,
				PlatformType:            input.PlatformType,
				CreatedAt:               execution.StartTime.AsTime(),
			})
		}

		writeJSONResponse(w, bounties, http.StatusOK)
	}
}
