package abb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	solanautil "github.com/brojonat/affiliate-bounty-board/solana"
	"github.com/brojonat/forohtoo/client"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

var ErrRetryNeededAfterRateLimit = errors.New("rate limited, retry needed after wait")

// jsonRPCResponseWrapper is a local struct to represent a generic JSON-RPC response.
// This is needed because the library may not export a generic jsonrpc.Response type
// for direct unmarshalling in RPCCallWithCallback.
type jsonRPCResponseWrapper struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      interface{}       `json:"id"` // Can be int or string
	Result  json.RawMessage   `json:"result,omitempty"`
	Error   *jsonrpc.RPCError `json:"error,omitempty"` // Using the exported RPCError
}

// TransferUSDC is an activity that transfers USDC from the escrow account to a user's account
// It now accepts an optional memo field.
// func (a *Activities) TransferUSDC(ctx context.Context, userID string, amount float64) error {
func (a *Activities) TransferUSDC(ctx context.Context, recipientWallet string, amount float64, memo string) error {
	logger := activity.GetLogger(ctx)
	// Get configuration from context
	cfg, err := getConfiguration(ctx)
	if err != nil {
		logger.Error("Failed to get configuration", "error", err)
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	logger.Info("Starting TransferUSDC activity", "recipientWallet", recipientWallet, "amount", amount, "memo", memo)

	// Validate inputs
	if recipientWallet == "" {
		logger.Error("Recipient wallet address is required")
		return fmt.Errorf("recipient wallet address is required")
	}
	recipientPubKey, err := solanago.PublicKeyFromBase58(recipientWallet)
	if err != nil {
		logger.Error("Invalid recipient wallet address", "address", recipientWallet, "error", err)
		return fmt.Errorf("invalid recipient wallet address '%s': %w", recipientWallet, err)
	}
	if amount <= 0 {
		logger.Error("Transfer amount must be positive", "amount", amount)
		return fmt.Errorf("transfer amount must be positive")
	}
	usdcMint := cfg.SolanaConfig.USDCMintAddress

	// Convert amount to lamports
	usdcAmount, err := solanautil.NewUSDCAmount(amount)
	if err != nil {
		logger.Error("Invalid USDC amount", "amount", amount, "error", err)
		return fmt.Errorf("invalid usdc amount: %w", err)
	}
	lamports := usdcAmount.ToSmallestUnit().Uint64()

	rpcClient := solanautil.NewRPCClient(cfg.SolanaConfig.RPCEndpoint)

	// Perform the transfer with memo
	// CRITICAL: Once we call SendUSDCWithMemo, we MUST NOT retry the activity
	// to prevent double-spending. All errors after this point are non-retryable.
	txSig, err := solanautil.SendUSDCWithMemo(
		ctx,
		rpcClient,
		usdcMint,
		*cfg.SolanaConfig.EscrowPrivateKey,
		recipientPubKey,
		lamports,
		memo,
	)
	if err != nil {
		logger.Error("Failed to send USDC transfer", "error", err)
		// Check for insufficient funds error from Solana simulation
		if strings.Contains(err.Error(), "custom program error: 0x1") || strings.Contains(err.Error(), "insufficient funds") {
			// This is a non-retryable error.
			return temporal.NewApplicationErrorWithOptions("insufficient funds for transfer", "InsufficientFunds", temporal.ApplicationErrorOptions{
				NonRetryable: true,
			})
		}
		// CRITICAL: Even if the send "failed", the transaction may have been submitted
		// to the network. We MUST NOT retry to prevent double-spending.
		return temporal.NewApplicationErrorWithOptions(
			fmt.Sprintf("failed to send usdc: %s", err.Error()),
			"SEND_FAILED",
			temporal.ApplicationErrorOptions{
				NonRetryable: true,
			},
		)
	}

	logger.Info("USDC transfer submitted", "signature", txSig.String())

	// CRITICAL: The transaction has been submitted. If confirmation fails, we MUST NOT
	// retry the activity because the transaction may already be on-chain. Retrying would
	// result in a double spend.
	confirmCtx, cancel := context.WithTimeout(ctx, 300*time.Second)
	defer cancel()
	err = solanautil.ConfirmTransaction(confirmCtx, rpcClient, txSig, rpc.CommitmentFinalized)
	if err != nil {
		// CRITICAL: Mark as non-retryable to prevent double-spend
		return temporal.NewApplicationErrorWithOptions(
			fmt.Sprintf("transaction submitted but confirmation failed: %s (txSig: %s)", err.Error(), txSig.String()),
			"CONFIRMATION_FAILED",
			temporal.ApplicationErrorOptions{
				NonRetryable: true,
			},
		)
	}
	return nil
}

// VerifyPaymentResult represents the result of verifying a payment
type VerifyPaymentResult struct {
	Verified     bool
	Amount       *solanautil.USDCAmount
	Error        string
	FunderWallet string // The wallet that funded the bounty
}

// VerifyPayment polls the backend API to check if the corresponding payment
// has been recorded in our database. Currently, we're onlpy polling the escrow
// wallet for USDC transactions, so that's the only wallet callers should
// pass for the expectedRecipient.
func (a *Activities) VerifyPayment(
	ctx context.Context,
	expectedRecipient solanago.PublicKey,
	expectedAmount *solanautil.USDCAmount,
	bountyID string,
	timeout time.Duration,
) (*VerifyPaymentResult, error) {
	logger := activity.GetLogger(ctx)
	expectedAmountLamports := expectedAmount.ToSmallestUnit().Uint64()

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg, err := getConfiguration(ctx)
	if err != nil {
		logger.Error("Failed to get configuration", "error", err)
		return nil, fmt.Errorf("failed to get configuration: %w", err)
	}

	// Create forohtoo client
	fmt.Println("Creating forohtoo client", "url", os.Getenv(EnvForohtooServerURL))
	cl := client.NewClient(os.Getenv(EnvForohtooServerURL), nil, slog.Default())
	network := DetermineForohtooNetwork(cfg.SolanaConfig.RPCEndpoint)

	// start tracking the wallet asset (USDC token)
	if err = cl.RegisterAsset(timeoutCtx, expectedRecipient.String(), network, "spl-token", cfg.SolanaConfig.USDCMintAddress.String()); err != nil {
		logger.Error("Failed to register wallet asset", "error", err)
		return nil, fmt.Errorf("failed to register wallet asset: %w", err)
	}

	// Wait for a transaction that matches the workflow ID in the memo
	// lookback of 5 minutes to catch any transactions that might have arrived slightly before this call
	txn, err := cl.Await(timeoutCtx, expectedRecipient.String(), network, 5*time.Minute, func(txn *client.Transaction) bool {
		// Check if the transaction memo contains the workflow ID
		if txn.Memo == nil {
			return false
		}
		return strings.Contains(*txn.Memo, bountyID) && txn.Amount == int64(expectedAmountLamports)
	})

	if err != nil {
		logger.Error("Failed to receive payment", "error", err)
		return nil, fmt.Errorf("failed to receive payment: %w", err)
	}
	return &VerifyPaymentResult{
		Verified:     true,
		Amount:       expectedAmount,
		FunderWallet: *txn.FromAddress,
	}, nil

	// ticker := time.NewTicker(10 * time.Second) // Poll every 10 seconds
	// defer ticker.Stop()

	// for {
	// 	select {
	// 	case <-timeoutCtx.Done():
	// 		return &VerifyPaymentResult{
	// 			Verified: false,
	// 			Error:    "payment verification timed out",
	// 		}, nil
	// 	case <-ticker.C:
	// 		transactions, err := a.QueryForBountyTransactions(ctx, bountyID)
	// 		if err != nil {
	// 			logger.Error("Failed to query for transaction by bounty ID", "error", err)
	// 			return nil, fmt.Errorf("failed to query for transaction: %w", err)
	// 		}

	// 		// Check transactions matching the workflow bounty ID
	// 		for _, tx := range transactions {
	// 			if tx.RecipientWallet == expectedRecipient.String() &&
	// 				tx.AmountSmallestUnit == int64(expectedAmountLamports) &&
	// 				tx.Memo != nil && strings.Contains(*tx.Memo, bountyID) {
	// 				return &VerifyPaymentResult{
	// 					Verified:     true,
	// 					Amount:       expectedAmount,
	// 					FunderWallet: tx.FunderWallet,
	// 				}, nil
	// 			}
	// 		}
	// 	}
	// }
}

func (a *Activities) PayBountyActivity(
	ctx context.Context,
	bountyID string,
	recipient string,
	amount *solanautil.USDCAmount,
) error {
	memo := fmt.Sprintf("{\"bounty_id\": \"%s\", \"purpose\": \"bounty_payment\"}", bountyID)
	activity.GetLogger(ctx).Info("Executing USDC payment for bounty",
		"bounty_id", bountyID,
		"recipient", recipient,
		"amount", amount.ToUSDC())
	return a.TransferUSDC(ctx, recipient, amount.ToUSDC(), memo)
}

func (a *Activities) RefundBountyActivity(
	ctx context.Context,
	bountyID string,
	refundRecipient string,
	amount *solanautil.USDCAmount,
) error {
	memo := fmt.Sprintf("{\"bounty_id\": \"%s\", \"purpose\": \"refund\"}", bountyID)
	activity.GetLogger(ctx).Info("Executing bounty refund",
		"bounty_id", bountyID,
		"recipient", refundRecipient,
		"amount", amount.ToUSDC())
	return a.TransferUSDC(ctx, refundRecipient, amount.ToUSDC(), memo)
}
