package abb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	solanautil "github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
	"go.temporal.io/sdk/activity"
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
	// Ensure Solana config is properly loaded
	if cfg.SolanaConfig.EscrowPrivateKey == nil {
		logger.Error("Escrow private key not configured")
		return fmt.Errorf("escrow private key not configured")
	}
	if cfg.SolanaConfig.USDCMintAddress == "" {
		logger.Error("USDC Mint Address not configured")
		return fmt.Errorf("usdc mint address not configured")
	}
	usdcMint, err := solanago.PublicKeyFromBase58(cfg.SolanaConfig.USDCMintAddress)
	if err != nil {
		logger.Error("Invalid USDC Mint Address", "address", cfg.SolanaConfig.USDCMintAddress, "error", err)
		return fmt.Errorf("invalid usdc mint address '%s': %w", cfg.SolanaConfig.USDCMintAddress, err)
	}

	// Convert amount to lamports
	usdcAmount, err := solanautil.NewUSDCAmount(amount)
	if err != nil {
		logger.Error("Invalid USDC amount", "amount", amount, "error", err)
		return fmt.Errorf("invalid usdc amount: %w", err)
	}
	lamports := usdcAmount.ToSmallestUnit().Uint64()

	logger.Debug("Transfer details", "from_escrow", cfg.SolanaConfig.EscrowWallet.String(), "to_recipient", recipientPubKey.String(), "mint", usdcMint.String(), "lamports", lamports)

	// Create RPC Client (using config)
	rpcClient := solanautil.NewRPCClient(cfg.SolanaConfig.RPCEndpoint)

	// Perform the transfer with memo
	txSig, err := solanautil.SendUSDCWithMemo(
		ctx, // Use activity context
		rpcClient,
		usdcMint,
		*cfg.SolanaConfig.EscrowPrivateKey, // Use configured private key
		recipientPubKey,
		lamports,
		memo, // Pass the memo here
	)
	if err != nil {
		logger.Error("Failed to send USDC transfer", "error", err)
		return fmt.Errorf("failed to send usdc: %w", err)
	}

	logger.Info("USDC transfer submitted", "signature", txSig.String())

	// Optionally wait for confirmation (consider timeout)
	confirmCtx, cancel := context.WithTimeout(ctx, 60*time.Second) // Example timeout
	defer cancel()
	err = solanautil.ConfirmTransaction(confirmCtx, rpcClient, txSig, rpc.CommitmentFinalized)
	if err != nil {
		// Log warning but maybe don't fail the activity entirely?
		// The transfer might still succeed even if confirmation times out here.
		logger.Warn("Failed to confirm transaction within timeout, but proceeding", "signature", txSig.String(), "error", err)
	} else {
		logger.Info("USDC transfer confirmed", "signature", txSig.String())
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

	ticker := time.NewTicker(10 * time.Second) // Poll every 10 seconds
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCtx.Done():
			return &VerifyPaymentResult{
				Verified: false,
				Error:    "payment verification timed out",
			}, nil
		case <-ticker.C:
			transactions, err := a.QueryForTransaction(ctx, bountyID)
			if err != nil {
				logger.Error("Failed to query for transaction", "error", err)
				return nil, fmt.Errorf("failed to query for transaction: %w", err)
			}

			// check if the transaction is in the list
			for _, tx := range transactions {
				if tx.RecipientWallet == expectedRecipient.String() &&
					tx.AmountLamports == int64(expectedAmountLamports) &&
					tx.Memo != nil && strings.Contains(*tx.Memo, bountyID) {
					return &VerifyPaymentResult{
						Verified:     true,
						Amount:       expectedAmount,
						FunderWallet: tx.FunderWallet,
					}, nil
				}
			}
		}
	}
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
