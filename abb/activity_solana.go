package abb

import (
	"context"
	"fmt"
	"time"

	"github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"go.temporal.io/sdk/activity"
)

type CheckBountyFundedActivityInput struct {
	BountyID          string
	ExpectedRecipient string
	ExpectedAmount    *solana.USDCAmount
	Timeout           time.Duration
}

func (a *Activities) CheckBountyFundedActivity(ctx context.Context, input CheckBountyFundedActivityInput) (string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Checking for bounty funding", "bounty_id", input.BountyID)

	recipientPubKey, err := solanago.PublicKeyFromBase58(input.ExpectedRecipient)
	if err != nil {
		logger.Error("Invalid recipient wallet address in bounty input", "address", input.ExpectedRecipient, "error", err)
		return "", fmt.Errorf("invalid recipient wallet address '%s': %w", input.ExpectedRecipient, err)
	}

	// Use the VerifyPayment activity to check for the funding transaction.
	verifyResult, err := a.VerifyPayment(
		ctx,
		recipientPubKey,
		input.ExpectedAmount,
		input.BountyID,
		input.Timeout,
	)
	if err != nil {
		logger.Error("Error verifying payment for bounty funding", "bounty_id", input.BountyID, "error", err)
		return "", fmt.Errorf("failed to verify payment for bounty %s: %w", input.BountyID, err)
	}

	if !verifyResult.Verified {
		logger.Warn("Bounty funding verification failed.", "bounty_id", input.BountyID, "reason", verifyResult.Error)
		return "", nil // Return empty string to indicate not funded
	}

	logger.Info("Bounty successfully funded.", "bounty_id", input.BountyID, "funder_wallet", verifyResult.FunderWallet)
	return verifyResult.FunderWallet, nil
}
