package abb

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/brojonat/affiliate-bounty-board/http/api"
	"github.com/brojonat/affiliate-bounty-board/solana"
	bin "github.com/gagliardetto/binary"
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
		StartToCloseTimeout: 10 * time.Minute,
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

	// Derive the USDC Associated Token Account (ATA) for the escrow wallet
	// This is where USDC tokens are actually held and transferred
	usdcATA, _, err := solanago.FindAssociatedTokenAddress(
		escrowPubKey,
		cfg.SolanaConfig.USDCMintAddress,
	)
	if err != nil {
		return fmt.Errorf("failed to derive USDC ATA: %w", err)
	}

	// get the latest transaction to use as a watermark
	latestTx, err := a.GetLatestSolanaTransactionForRecipient(ctx, input.EscrowWallet)
	if err != nil {
		return fmt.Errorf("failed to get latest transaction: %w", err)
	}

	opts := &rpc.GetSignaturesForAddressOpts{}
	if latestTx != nil {
		// This is a subsequent run. Poll for all transactions since the last one we saw.
		sig, err := solanago.SignatureFromBase58(latestTx.Signature)
		if err != nil {
			return fmt.Errorf("failed to parse signature from latest transaction: %w", err)
		}
		opts.Until = sig
	} else {
		// This is the first time we're polling this wallet.
		// We only fetch the single most recent transaction to establish a starting point.
		// This avoids pulling and processing thousands of historical transactions unnecessarily.
		logger.Warn("No watermark transaction found. Fetching recent transactions to establish a watermark.", "wallet", input.EscrowWallet, "usdcATA", usdcATA.String())
		limit := 100
		opts.Limit = &limit
	}

	signatures, err := rpcClient.GetSignaturesForAddressWithOpts(ctx, usdcATA, opts)
	if err != nil {
		return fmt.Errorf("failed to get signatures for address: %w", err)
	}
	logger.Debug("Fetched transaction signatures", "count", len(signatures), "address", usdcATA.String())

	for _, sigInfo := range signatures {
		// Add a small delay to avoid overwhelming the RPC endpoint.
		time.Sleep(1 * time.Second)

		if sigInfo.Err != nil {
			logger.Warn("Signature has an error, skipping", "signature", sigInfo.Signature, "error", sigInfo.Err)
			continue
		}

		if sigInfo.BlockTime == nil {
			logger.Warn("Signature has no block time, skipping", "signature", sigInfo.Signature)
			continue
		}

		var tx *rpc.GetTransactionResult
		var err error
		const maxRetries = 5
		const retryDelay = 15 * time.Second

		for i := range maxRetries {
			// First, try to get the transaction with modern version support.
			tx, err = rpcClient.GetTransaction(ctx, sigInfo.Signature, &rpc.GetTransactionOpts{
				Encoding:                       solanago.EncodingBase64,
				MaxSupportedTransactionVersion: new(uint64),
			})

			if err == nil {
				break // success
			}

			// If we got a parsing error, it's likely a legacy transaction.
			// Let's retry immediately without version support.
			if strings.Contains(err.Error(), "expects '\"' or 'n', but found '{'") {
				logger.Warn("Could not parse as versioned tx, retrying as legacy.", "signature", sigInfo.Signature)
				tx, err = rpcClient.GetTransaction(ctx, sigInfo.Signature, &rpc.GetTransactionOpts{
					Encoding: solanago.EncodingBase64,
				})
				if err == nil {
					break // Success on fallback!
				}
			}

			// If we are here, it means we still have an error. Check if it's a rate limit error.
			// FIXME: this is a brittle hack. We should use a more robust error handling mechanism.
			if strings.Contains(err.Error(), "429") {
				logger.Warn("Rate limit hit, retrying...", "signature", sigInfo.Signature, "attempt", i+1)
				time.Sleep(retryDelay)
				continue // Go to next iteration of the retry loop.
			}

			// If it's some other unrecoverable error, break the retry loop and fail for this signature.
			break
		}

		if err != nil {
			logger.Warn("Failed to get transaction details after retries", "signature", sigInfo.Signature, "error", err)
			continue
		}

		// Manually parse the transaction from the base64 data
		var parsedTx *solanago.Transaction
		if tx.Transaction != nil {
			binaryData := tx.Transaction.GetBinary()
			var err error
			parsedTx, err = solanago.TransactionFromDecoder(bin.NewBinDecoder(binaryData))
			if err != nil {
				logger.Warn("Failed to parse transaction from binary data", "signature", sigInfo.Signature, "error", err)
				continue
			}
		}

		memo := ""
		if parsedTx != nil {
			for _, instruction := range parsedTx.Message.Instructions {
				programID := parsedTx.Message.AccountKeys[instruction.ProgramIDIndex]
				// The memo program is a well-known program ID
				if programID.Equals(solanago.MemoProgramID) {
					memo = string(instruction.Data)
					break
				}
			}
		}

		// Log the pre and post token balances for debugging
		preBalancesJSON, _ := json.Marshal(tx.Meta.PreTokenBalances)
		postBalancesJSON, _ := json.Marshal(tx.Meta.PostTokenBalances)
		logger.Debug("Processing transaction token balances", "signature", sigInfo.Signature.String(), "pre_token_balances", string(preBalancesJSON), "post_token_balances", string(postBalancesJSON))

		// This is the parsing logic that inspects token balances to determine
		// the sender, recipient, and amount for SPL token transfers.
		usdcMintAddress := cfg.SolanaConfig.USDCMintAddress

		var funderWallet, recipientWallet string
		var amount int64

		// Identify the funder, recipient, and amount by finding changes in USDC balances.
		// We are only interested in transactions where the escrow wallet is the recipient.

		var debitedOwner, creditedOwner string
		var changeAmount uint64

		// Create maps for easier lookup.
		preBalances := make(map[uint16]rpc.TokenBalance)
		for _, balance := range tx.Meta.PreTokenBalances {
			if balance.Mint.String() == usdcMintAddress.String() {
				preBalances[balance.AccountIndex] = balance
			}
		}

		for _, postBalance := range tx.Meta.PostTokenBalances {
			if postBalance.Mint.String() != usdcMintAddress.String() {
				continue
			}

			preBalance, hasPreBalance := preBalances[postBalance.AccountIndex]
			preAmount := uint64(0)
			if hasPreBalance {
				preAmount, _ = strconv.ParseUint(preBalance.UiTokenAmount.Amount, 10, 64)
			}
			postAmount, _ := strconv.ParseUint(postBalance.UiTokenAmount.Amount, 10, 64)

			if postAmount > preAmount {
				creditedOwner = postBalance.Owner.String()
				changeAmount = postAmount - preAmount
			} else if preAmount > postAmount {
				// The owner of a token account doesn't change in a transfer,
				// so using postBalance.Owner is safe.
				debitedOwner = postBalance.Owner.String()
			}
		}

		// If we haven't found a debited owner, check the parsed transaction instructions
		// for a transfer that might indicate the funder.
		if debitedOwner == "" && parsedTx != nil {
			for _, instruction := range parsedTx.Message.Instructions {
				programID := parsedTx.Message.AccountKeys[instruction.ProgramIDIndex]
				if programID.Equals(solanago.TokenProgramID) {
					// This is a token program instruction. Check if it's a transfer.
					// The instruction data for a transfer contains the amount.
					// A simple transfer instruction data is 9 bytes long (1 byte for instruction, 8 for amount).
					if len(instruction.Data) >= 9 && instruction.Data[0] == 3 { // 3 is the instruction for Transfer
						sourceAccountIndex := instruction.Accounts[0]
						// Find the owner of the source account in the pre-balances
						for _, balance := range tx.Meta.PreTokenBalances {
							if balance.AccountIndex == uint16(sourceAccountIndex) {
								debitedOwner = balance.Owner.String()
								break
							}
						}
						// If we found the source account owner, we can stop.
						if debitedOwner != "" {
							break
						}
					}
				}
			}
		}

		// If we have a clear debit and credit, and the escrow wallet is the recipient, we have a valid transaction.
		if debitedOwner != "" && creditedOwner != "" && changeAmount > 0 {
			if creditedOwner == escrowPubKey.String() && debitedOwner != escrowPubKey.String() {
				funderWallet = debitedOwner
				recipientWallet = creditedOwner
				amount = int64(changeAmount)
			}
		}

		logger.Debug("Parsed transaction", "signature", sigInfo.Signature.String(), "funder", funderWallet, "recipient", recipientWallet, "amount", amount, "memo", memo)

		// If we have a valid transaction, store it.
		if amount > 0 && funderWallet != "" && recipientWallet != "" {
			logger.Debug("Storing transaction", "signature", sigInfo.Signature.String(), "funder", funderWallet, "recipient", recipientWallet, "amount", amount, "memo", memo)
			bountyID := parseBountyIDFromMemo(memo)
			solanaTx := api.SolanaTransaction{
				Signature:          sigInfo.Signature.String(),
				Slot:               int64(tx.Slot),
				BlockTime:          time.Unix(int64(*sigInfo.BlockTime), 0),
				BountyID:           &bountyID,
				FunderWallet:       funderWallet,
				RecipientWallet:    recipientWallet,
				AmountSmallestUnit: amount,
				Memo:               &memo,
			}

			if err := a.PostSolanaTransaction(ctx, solanaTx); err != nil {
				logger.Error("Failed to post transaction to DB", "signature", solanaTx.Signature, "error", err)
			}
		}
	}
	return nil
}

func parseBountyIDFromMemo(memo string) string {
	// a json object may be embedded in the memo, so we need to extract it
	re := regexp.MustCompile(`(bounty-[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})`)
	matches := re.FindStringSubmatch(memo)
	if len(matches) > 1 {
		return matches[1] // Now returns the full "bounty-{uuid}" format
	}
	return ""
}
