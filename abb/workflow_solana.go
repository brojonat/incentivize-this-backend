package abb

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/brojonat/affiliate-bounty-board/http/api"
	"github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/workflow"
)

type PollAndStoreTransactionsInput struct {
	EscrowWallet string
}

func PollSolanaTransactionsWorkflow(ctx workflow.Context, input PollAndStoreTransactionsInput) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	err := workflow.ExecuteActivity(ctx, "PollAndStoreTransactionsActivity", input).Get(ctx, nil)
	if err != nil {
		workflow.GetLogger(ctx).Error("Polling activity failed.", "Error", err)
	}
	return err
}

func (a *Activities) PollAndStoreTransactionsActivity(ctx context.Context, input PollAndStoreTransactionsInput) error {
	logger := activity.GetLogger(ctx)
	logger.Info("Starting PollAndStoreTransactionsActivity", "escrowWallet", input.EscrowWallet)

	// Get configuration from context
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return fmt.Errorf("failed to get configuration: %w", err)
	}

	rpcClient := solana.NewRPCClient(cfg.SolanaConfig.RPCEndpoint)
	escrowPubKey, err := solanago.PublicKeyFromBase58(input.EscrowWallet)
	if err != nil {
		return fmt.Errorf("invalid escrow wallet address: %w", err)
	}

	// get the latest transaction to use as a watermark
	latestTx, err := a.GetLatestSolanaTransactionForRecipient(ctx, input.EscrowWallet)
	if err != nil {
		return fmt.Errorf("failed to get latest transaction: %w", err)
	}

	limit := 1000
	opts := &rpc.GetSignaturesForAddressOpts{
		Limit: &limit,
	}
	if latestTx != nil {
		sig, err := solanago.SignatureFromBase58(latestTx.Signature)
		if err != nil {
			return fmt.Errorf("failed to parse signature from latest transaction: %w", err)
		}
		opts.Until = sig
	}

	signatures, err := rpcClient.GetSignaturesForAddressWithOpts(ctx, escrowPubKey, opts)
	if err != nil {
		return fmt.Errorf("failed to get signatures for address: %w", err)
	}

	for _, sigInfo := range signatures {
		if sigInfo.Err != nil {
			continue
		}

		if sigInfo.BlockTime == nil {
			logger.Warn("Signature has no block time, skipping", "signature", sigInfo.Signature)
			continue
		}

		tx, err := rpcClient.GetTransaction(ctx, sigInfo.Signature, &rpc.GetTransactionOpts{
			Encoding:                       solanago.EncodingJSONParsed,
			MaxSupportedTransactionVersion: new(uint64),
		})
		if err != nil {
			logger.Warn("Failed to get transaction details", "signature", sigInfo.Signature, "error", err)
			continue
		}

		memo := ""
		if tx.Meta != nil && tx.Meta.LogMessages != nil {
			for _, log := range tx.Meta.LogMessages {
				if len(log) > 23 && log[:23] == "Program log: Memo Program" {
					memo = log[25 : len(log)-1]
					break
				}
			}
		}

		// This is a simplified parsing logic. A more robust implementation would
		// inspect pre and post token balances to accurately determine the sender,
		// recipient, and amount.
		rawTx, err := tx.Transaction.GetTransaction()
		if err != nil {
			logger.Warn("could not get raw transaction", "err", err.Error())
		}
		if rawTx != nil && len(rawTx.Message.Instructions) > 0 {
			// Naive assumption: first account is funder, second is recipient
			if len(rawTx.Message.AccountKeys) >= 2 {
				funder := rawTx.Message.AccountKeys[0]
				recipient := rawTx.Message.AccountKeys[1]
				// This is a placeholder for the actual amount logic.
				var amount int64 = 0

				postBalances := tx.Meta.PostTokenBalances
				preBalances := tx.Meta.PreTokenBalances

				for _, postBalance := range postBalances {
					if postBalance.Owner.Equals(recipient) {
						for _, preBalance := range preBalances {
							if preBalance.Owner.Equals(recipient) {
								postAmount, _ := strconv.ParseInt(postBalance.UiTokenAmount.Amount, 10, 64)
								preAmount, _ := strconv.ParseInt(preBalance.UiTokenAmount.Amount, 10, 64)
								amount = postAmount - preAmount
							}
						}
					}
				}

				bountyID := parseBountyIDFromMemo(memo)
				solanaTx := api.SolanaTransaction{
					Signature:       sigInfo.Signature.String(),
					Slot:            int64(tx.Slot),
					BlockTime:       time.Unix(int64(*sigInfo.BlockTime), 0),
					BountyID:        &bountyID,
					FunderWallet:    funder.String(),
					RecipientWallet: recipient.String(),
					AmountLamports:  amount,
					Memo:            &memo,
				}

				if err := a.PostSolanaTransaction(ctx, solanaTx); err != nil {
					logger.Error("Failed to post transaction to DB", "signature", solanaTx.Signature, "error", err)
				}
			}
		}
	}
	return nil
}

func parseBountyIDFromMemo(memo string) string {
	// a json object may be embedded in the memo, so we need to extract it
	re := regexp.MustCompile(`bounty-([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})`)
	matches := re.FindStringSubmatch(memo)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}
