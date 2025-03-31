package http

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/brojonat/reddit-bounty-board/internal/stools"
	"github.com/brojonat/reddit-bounty-board/rbb"
	"github.com/brojonat/reddit-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/google/uuid"
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
	RequirementsDescription string  `json:"requirements_description"`
	BountyPerPost           float64 `json:"bounty_per_post"`
	TotalBounty             float64 `json:"total_bounty"`
	OwnerID                 string  `json:"owner_id"`
	SolanaWallet            string  `json:"solana_wallet"`
	USDCAccount             string  `json:"usdc_account"`
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
		workflow := rbb.NewWorkflow(tc)
		err = workflow.PayBounty(r.Context(), rbb.PayBountyWorkflowInput{
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
		workflow := rbb.NewWorkflow(tc)
		err = workflow.ReturnBountyToOwner(r.Context(), rbb.ReturnBountyToOwnerWorkflowInput{
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
func handleCreateBounty(l *slog.Logger, tc client.Client) http.HandlerFunc {
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

		// Convert amounts to USDCAmount
		bountyPerPost, err := solana.NewUSDCAmount(req.BountyPerPost)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid bounty_per_post amount: %w", err))
			return
		}
		totalBounty, err := solana.NewUSDCAmount(req.TotalBounty)
		if err != nil {
			writeBadRequestError(w, fmt.Errorf("invalid total_bounty amount: %w", err))
			return
		}

		// Create workflow input
		input := rbb.BountyAssessmentWorkflowInput{
			RequirementsDescription: req.RequirementsDescription,
			BountyPerPost:           bountyPerPost,
			TotalBounty:             totalBounty,
			OwnerID:                 req.OwnerID,
			SolanaWallet:            req.SolanaWallet,
			USDCAccount:             req.USDCAccount,
			ServerURL:               os.Getenv("SERVER_URL"),
			AuthToken:               os.Getenv("AUTH_TOKEN"),
		}

		// Start workflow
		workflowID := fmt.Sprintf("bounty-%s-%s", req.OwnerID, uuid.New().String())
		workflowOptions := client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: rbb.TaskQueueName,
		}

		we, err := tc.ExecuteWorkflow(r.Context(), workflowOptions, rbb.BountyAssessmentWorkflow, input)
		if err != nil {
			writeInternalError(l, w, fmt.Errorf("failed to start workflow: %w", err))
			return
		}

		writeJSONResponse(w, DefaultJSONResponse{
			Message: fmt.Sprintf("Successfully started bounty assessment workflow with ID %s", we.GetID()),
		}, http.StatusOK)
	}
}
