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
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

// PayBountyRequest represents the request body for paying a bounty
type PayBountyRequest struct {
	UserID      string  `json:"user_id,omitempty"`      // Optional user ID (for tracking)
	Amount      float64 `json:"amount"`                 // Amount to pay in USDC
	ToAccount   string  `json:"to_account"`             // Destination wallet address
	FromAccount string  `json:"from_account,omitempty"` // Source account (defaults to escrow if empty)
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
	Requirements  []string         `json:"requirements"`
	BountyPerPost float64          `json:"bounty_per_post"`
	TotalBounty   float64          `json:"total_bounty"`
	OwnerID       string           `json:"owner_id"`
	SolanaWallet  string           `json:"solana_wallet"`
	USDCAccount   string           `json:"usdc_account"`
	PlatformType  abb.PlatformType `json:"platform_type"`
}

// BountyListItem represents a single bounty in the list response
type BountyListItem struct {
	WorkflowID    string           `json:"workflow_id"`
	Status        string           `json:"status"`
	Requirements  []string         `json:"requirements"`
	BountyPerPost float64          `json:"bounty_per_post"`
	TotalBounty   float64          `json:"total_bounty"`
	OwnerID       string           `json:"owner_id"`
	PlatformType  abb.PlatformType `json:"platform_type"`
	CreatedAt     time.Time        `json:"created_at"`
}

// BountyLister defines the interface for listing bounties
type BountyLister interface {
	ListBounties(ctx context.Context) ([]BountyListItem, error)
}

// AssessContentRequest represents the request body for assessing content against requirements
type AssessContentRequest struct {
	BountyID  string           `json:"bounty_id"`
	ContentID string           `json:"content_id"`
	UserID    string           `json:"user_id"`
	Platform  abb.PlatformType `json:"platform"`
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

		// Validate required fields
		if req.ToAccount == "" {
			writeBadRequestError(w, fmt.Errorf("invalid request: to_account is required"))
			return
		}

		// Convert amount to USDCAmount
		amount, err := solana.NewUSDCAmount(req.Amount)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid amount: %w", err))
			return
		}

		// Create Solana config
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
			RPCEndpoint:        os.Getenv("SOLANA_RPC_ENDPOINT"),
			WSEndpoint:         os.Getenv("SOLANA_WS_ENDPOINT"),
			EscrowPrivateKey:   &escrowPrivateKey,
			EscrowTokenAccount: escrowTokenAccount,
		}

		// If FromAccount is not specified, use the escrow account
		sourceAccount := req.FromAccount
		if sourceAccount == "" {
			sourceAccount = tokenAccountStr
		}

		// Execute workflow
		workflowID := fmt.Sprintf("pay-bounty-%s", uuid.New().String())
		workflowOptions := client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: os.Getenv("TASK_QUEUE"),
		}

		we, err := tc.ExecuteWorkflow(r.Context(), workflowOptions, abb.PayBountyWorkflow, abb.PayBountyWorkflowInput{
			ToAccount:    req.ToAccount,
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
			Message: fmt.Sprintf("Successfully executed payment of %v USDC from %s to %s",
				amount.ToUSDC(), sourceAccount, req.ToAccount),
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
			Requirements:  req.Requirements,
			BountyPerPost: bountyPerPost,
			TotalBounty:   totalBounty,
			OwnerID:       req.OwnerID,
			SolanaWallet:  req.SolanaWallet,
			USDCAccount:   req.USDCAccount,
			ServerURL:     os.Getenv("SERVER_URL"),
			AuthToken:     os.Getenv("AUTH_TOKEN"),
			PlatformType:  req.PlatformType,
		}

		// Start workflow
		workflowID := fmt.Sprintf("bounty-%s-%s", req.OwnerID, uuid.New().String())
		workflowOptions := client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: os.Getenv("TASK_QUEUE"),
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
				WorkflowID:    execution.Execution.WorkflowId,
				Status:        execution.Status.String(),
				Requirements:  input.Requirements,
				BountyPerPost: input.BountyPerPost.ToUSDC(),
				TotalBounty:   input.TotalBounty.ToUSDC(),
				OwnerID:       input.OwnerID,
				PlatformType:  input.PlatformType,
				CreatedAt:     execution.StartTime.AsTime(),
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

		if req.BountyID == "" || req.ContentID == "" || req.UserID == "" || req.Platform == "" {
			writeBadRequestError(w, fmt.Errorf("invalid request: missing required fields: %v", req))
			return
		}

		// Signal the workflow
		err := tc.SignalWorkflow(r.Context(), req.BountyID, "", "assess_content", abb.AssessContentSignal{
			ContentID: req.ContentID,
			UserID:    req.UserID,
			Platform:  req.Platform,
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
