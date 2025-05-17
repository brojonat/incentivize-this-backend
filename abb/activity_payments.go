package abb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	solanautil "github.com/brojonat/affiliate-bounty-board/solana"
	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/memo"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
	"go.temporal.io/sdk/activity"
	temporal_log "go.temporal.io/sdk/log"
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
	Verified bool
	Amount   *solanautil.USDCAmount
	Error    string
}

// VerifyPayment verifies that a specific payment transaction has been received. More specifically,
// it verifies that a transaction has been received from the expected sender to the expected recipient's
// associated token account with the expected amount of USDC.
func (a *Activities) VerifyPayment(
	ctx context.Context,
	expectedSender solanago.PublicKey,
	expectedRecipient solanago.PublicKey,
	expectedAmount *solanautil.USDCAmount,
	expectedMemo string,
	timeout time.Duration,
) (*VerifyPaymentResult, error) {
	logger := activity.GetLogger(ctx)

	// Get fresh config
	cfg, err := getConfiguration(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration in VerifyPayment: %w", err)
	}
	solCfg := cfg.SolanaConfig // Use fetched config

	expectedAmountUint64 := expectedAmount.ToSmallestUnit().Uint64()
	logger.Info("VerifyPayment started",
		"workflow_id", expectedMemo,
		"expected_sender", expectedSender.String(),
		"expected_recipient", expectedRecipient.String(),
		"expected_amount_lamports", expectedAmountUint64,
		"timeout", timeout,
		"rpc_endpoint", solCfg.RPCEndpoint,
		"usdc_mint", solCfg.USDCMintAddress, // Use fetched config
		"escrow_owner", solCfg.EscrowWallet.String()) // Use fetched config

	// Create a ticker to check transactions periodically
	ticker := time.NewTicker(30 * time.Second) // Increased from 10 seconds
	defer ticker.Stop()

	// Create a timeout context for the entire verification process
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	rpcClient := solanautil.NewRPCClient(solCfg.RPCEndpoint)

	// Parse the USDC mint address from config
	usdcMint, err := solanago.PublicKeyFromBase58(solCfg.USDCMintAddress)
	if err != nil {
		logger.Error("Failed to parse USDC mint address from config", "mint", solCfg.USDCMintAddress, "error", err)
		return nil, fmt.Errorf("invalid USDC mint address in config: %w", err)
	}

	// Derive the expected recipient's Associated Token Account (ATA)
	expectedRecipientATA, _, err := solanago.FindAssociatedTokenAddress(expectedRecipient, usdcMint)
	if err != nil {
		logger.Error("Failed to derive expected recipient ATA", "recipient", expectedRecipient.String(), "mint", usdcMint.String(), "error", err)
		return nil, fmt.Errorf("failed to derive recipient ATA: %w", err)
	}

	logger.Info("Verifying payment TO derived ATA", "ata_address", expectedRecipientATA.String(), "recipient", expectedRecipient.String())

	// Keep track of signatures we've already checked to avoid redundant lookups
	checkedSignatures := make(map[solanago.Signature]bool)

	for {
		select {
		case <-timeoutCtx.Done():
			logger.Warn("Payment verification timed out", "expected_sender", expectedSender.String(), "expected_recipient_ata", expectedRecipientATA.String())
			return &VerifyPaymentResult{
				Verified: false,
				Error:    "payment verification timed out",
			}, nil // Timeout is not a processing error, just verification failure

		case <-ticker.C:
			logger.Debug("Checking for recent transactions...", "recipient_ata", expectedRecipientATA.String())
			// Get recent signatures for the recipient ATA, as we expect an incoming transfer there.
			limitSignatures := 15
			signatures, err := rpcClient.GetSignaturesForAddressWithOpts(
				ctx,
				expectedRecipientATA, // Fetch history for the account we expect to receive funds
				&rpc.GetSignaturesForAddressOpts{
					Limit:      &limitSignatures,
					Commitment: rpc.CommitmentFinalized,
				},
			)
			if err != nil {
				logger.Error("Failed to get signatures for escrow ATA", "account", expectedRecipientATA.String(), "error", err)
				// Don't fail immediately, could be transient RPC issue, wait for next tick or timeout
				continue
			}

			logger.Debug("Fetched signatures", "count", len(signatures))

			// Process signatures from oldest in batch to newest to find the first matching one
			for i := len(signatures) - 1; i >= 0; i-- {
				sigResult := signatures[i]
				// Defensive nil check
				if sigResult == nil {
					logger.Warn("Received nil signature result in batch", "index", i)
					continue
				}
				sig := sigResult.Signature
				logger.Debug("Checking signature", "index", i, "sig", sig.String())

				if checkedSignatures[sig] {
					logger.Debug("Signature already checked, skipping", "sig", sig.String())
					continue // Already processed this one
				}
				checkedSignatures[sig] = true // Mark as checked

				// Check if transaction actually succeeded before fetching details
				if sigResult.Err != nil {
					logger.Debug("Skipping failed transaction from GetSignaturesForAddress", "signature", sig.String(), "error", sigResult.Err)
					continue
				}

				logger.Debug("Fetching transaction details with retry logic", "signature", sig.String())

				var txResult *rpc.GetTransactionResult
				var attemptError error
				const maxRetries = 5                           // Define max retries for a signature
				var currentTxDetails *rpc.GetTransactionResult // Variable to store the result from the callback

				for attempt := 0; attempt < maxRetries; attempt++ {
					if checkedSignatures[sig] { // Re-check in case it was marked by a failed attempt from another concurrent check (if any)
						// Or if the outer loop already processed it due to some complex logic.
						// This check might be redundant if VerifyPayment is strictly single-threaded per call.
						logger.Debug("Signature already processed during retry attempts, breaking", "sig", sig.String())
						attemptError = errors.New("signature processed by another path during retries") // Mark as an error to skip
						break
					}

					maxSupportedTxVersion := uint64(0)
					rpcOpts := &rpc.GetTransactionOpts{
						Encoding:                       solanago.EncodingBase64,
						Commitment:                     rpc.CommitmentFinalized,
						MaxSupportedTransactionVersion: &maxSupportedTxVersion,
					}
					params := []interface{}{
						sig.String(),
						rpcOpts,
					}

					currentTxDetails = nil // Reset for each attempt

					errCallback := rpcClient.RPCCallWithCallback(
						timeoutCtx, // Use the timeout context for the RPC call
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
										logger.Warn("Could not parse Retry-After header, using default", "header", retryAfterHeader, "error", errParse)
										sleepDuration = 5 * time.Second // Default backoff
									}
								} else {
									sleepDuration = 5 * time.Second // Default backoff
								}
								logger.Info("Rate limited by RPC, sleeping", "duration", sleepDuration, "signature", sig.String(), "attempt", attempt+1)

								// Respect context timeout during sleep
								select {
								case <-time.After(sleepDuration):
									// continue with retry
								case <-timeoutCtx.Done():
									logger.Warn("VerifyPayment timeout hit during rate limit sleep", "signature", sig.String())
									return timeoutCtx.Err() // Propagate timeout error
								}
								return ErrRetryNeededAfterRateLimit
							}

							if httpResponse.StatusCode >= 400 {
								bodyBytes, _ := io.ReadAll(httpResponse.Body)
								_ = httpResponse.Body.Close() // Ensure body is closed
								logger.Error("HTTP error from RPC", "statusCode", httpResponse.StatusCode, "body", string(bodyBytes), "signature", sig.String())
								return fmt.Errorf("rpc http error: %d for signature %s", httpResponse.StatusCode, sig.String())
							}

							bodyBytes, errRead := io.ReadAll(httpResponse.Body)
							if errRead != nil {
								_ = httpResponse.Body.Close() // Ensure body is closed
								return fmt.Errorf("failed to read response body for %s: %w", sig.String(), errRead)
							}
							_ = httpResponse.Body.Close() // Ensure body is closed

							var genericRpcResp jsonRPCResponseWrapper
							if errUnmarshal := json.Unmarshal(bodyBytes, &genericRpcResp); errUnmarshal != nil {
								return fmt.Errorf("failed to unmarshal RPC response for %s: %w", sig.String(), errUnmarshal)
							}

							if genericRpcResp.Error != nil {
								return fmt.Errorf("RPC error for %s: code=%d, message=%s", sig.String(), genericRpcResp.Error.Code, genericRpcResp.Error.Message)
							}

							var specificResult rpc.GetTransactionResult
							if errUnmarshalResult := json.Unmarshal(genericRpcResp.Result, &specificResult); errUnmarshalResult != nil {
								return fmt.Errorf("failed to unmarshal GetTransactionResult for %s: %w", sig.String(), errUnmarshalResult)
							}
							currentTxDetails = &specificResult
							return nil
						},
					)

					if errCallback == nil {
						txResult = currentTxDetails // Successfully fetched and decoded
						attemptError = nil
						break // Success, break retry loop
					} else if errors.Is(errCallback, ErrRetryNeededAfterRateLimit) {
						logger.Info("Retrying signature after rate limit wait", "signature", sig.String(), "attempt", attempt+1)
						attemptError = errCallback // Store this error, will be overwritten if next attempt succeeds
						// Continue to the next iteration of the retry loop.
					} else if errors.Is(errCallback, context.DeadlineExceeded) || errors.Is(errCallback, context.Canceled) {
						logger.Warn("Context timeout/canceled during RPCCallWithCallback", "signature", sig.String(), "error", errCallback)
						attemptError = errCallback
						break // Break retry loop, context is done
					} else {
						// Any other error from RPCCallWithCallback or the callback itself
						attemptError = errCallback
						logger.Warn("Failed to get transaction details, non-retryable or max retries reached", "signature", sig.String(), "error", attemptError, "attempt", attempt+1)
						break // Break retry loop
					}
				} // End retry loop for current signature

				checkedSignatures[sig] = true // Mark as checked AFTER attempting to fetch/process it.

				if attemptError != nil || txResult == nil {
					// If after retries (or on first non-retryable error) we still failed or txResult is nil
					if !errors.Is(attemptError, ErrRetryNeededAfterRateLimit) { // Avoid double logging if last attempt was rate limit
						logger.Warn("Failed to get transaction details after attempts", "signature", sig.String(), "final_error", attemptError)
					}
					// else, if it IS ErrRetryNeededAfterRateLimit, it means we hit max retries ending on a rate limit,
					// or timeout during sleep. The warning for rate limit sleep timeout is handled inside the callback.
					continue // Move to the next signature
				}

				// txResult is now populated if successful
				// The logic below remains mostly the same, using the txResult from the retry block.

				if txResult.Meta == nil { // txResult itself is checked for nil above
					logger.Warn("Received nil transaction or meta", "signature", sig.String())
					continue
				}
				// Double-check transaction success via meta error
				if txResult.Meta.Err != nil {
					logger.Debug("Skipping transaction with meta error", "signature", sig.String(), "error", txResult.Meta.Err)
					continue
				}

				// --- Transaction Parsing Logic ---
				// Look for the specific transfer: from 'expectedSender', to 'expectedRecipientATA', correct USDC amount.
				// Using token balances is often the most reliable way across instruction types.
				if txResult.Meta != nil && txResult.Meta.PreTokenBalances != nil && txResult.Meta.PostTokenBalances != nil && txResult.Transaction != nil {

					// Get account keys from the meta - this might be more reliable than parsing the tx itself
					// Need to reconstruct the PublicKey list from meta if possible, or find another way
					// For now, let's attempt to get it from the (potentially unparsed) transaction envelope first.
					// If GetTransaction fails due to parsing later, this check might need refinement.
					var accountKeys []solanago.PublicKey
					rawTx, err := txResult.Transaction.GetTransaction()
					if err != nil {
						logger.Warn("Could not decode raw transaction from envelope", "signature", sig.String(), "error", err)
						// Even if we can't check memo, maybe balances are enough? Let checkTokenBalances run.
						// Setting accountKeys to nil might break checkTokenBalances, needs review.
						// For now, let's assume we need the decoded tx for reliable keys.
						logger.Error("Cannot verify transaction without decoded account keys", "signature", sig.String())
						continue
					}
					if rawTx == nil || len(rawTx.Message.AccountKeys) == 0 {
						logger.Warn("Decoded transaction or account keys are nil/empty, cannot verify transaction", "signature", sig.String())
						continue
					}
					accountKeys = rawTx.Message.AccountKeys

					// --- Verify Memo ---
					memoMatches := false
					memoContent := ""
					for _, ix := range rawTx.Message.Instructions {
						progKey, err := rawTx.Message.Program(ix.ProgramIDIndex)
						if err != nil {
							logger.Warn("Could not get program key for instruction", "index", ix.ProgramIDIndex, "error", err)
							continue
						}
						if progKey.Equals(memo.ProgramID) {
							memoContent = string(ix.Data)
							if memoContent == expectedMemo {
								memoMatches = true
								break // Found matching memo
							}
						}
					}
					logger.Debug("Memo check result", "signature", sig.String(), "found_memo", memoContent, "expected_memo", expectedMemo, "match", memoMatches)

					// if memo does not match, we don't need to check the balances
					if !memoMatches {
						logger.Debug("Memo does not match, skipping balance check", "signature", sig.String(), "expected_memo", expectedMemo, "found_memo", memoContent)
						continue
					}

					// --- Check 2: Verify Token Balances ---
					balancesMatch, err := checkTokenBalancesForTransfer(
						logger,
						txResult.Meta.PreTokenBalances,
						txResult.Meta.PostTokenBalances,
						accountKeys,
						expectedSender,
						expectedRecipientATA,
						usdcMint,
						expectedAmountUint64,
					)
					if err != nil {
						logger.Error("Error checking token balances for tx", "signature", sig.String(), "error", err)
						continue // Move to next signature
					}

					// --- Final Verification ---
					if balancesMatch && memoMatches {
						logger.Info("Matching payment transaction found (balances and memo match)",
							"signature", sig.String(),
							"from_owner", expectedSender.String(),
							"to_ata", expectedRecipientATA.String(),
							"amount_lamports", expectedAmountUint64)
						// We found the specific transaction we were looking for.
						return &VerifyPaymentResult{
							Verified: true,
							Amount:   expectedAmount, // Return the amount we were looking for
						}, nil
					}
				} else {
					logger.Debug("Transaction missing pre/post token balances, cannot verify via balance diff", "signature", sig.String())
				}
				// --- End Transaction Parsing ---
			}
			logger.Debug("Finished checking batch of signatures")
		}
	}
}

// checkTokenBalancesForTransfer parses token balances to find a specific transfer.
// Returns true if the specified transfer is found, false otherwise.
func checkTokenBalancesForTransfer(
	logger temporal_log.Logger,
	preBalances []rpc.TokenBalance,
	postBalances []rpc.TokenBalance,
	accountKeys []solanago.PublicKey, // Added: List of accounts from the transaction message
	expectedSourceOwner solanago.PublicKey,
	expectedDestATA solanago.PublicKey,
	expectedMint solanago.PublicKey,
	expectedAmountLamports uint64,
) (bool, error) {

	logger.Debug("checkTokenBalances: Input",
		"expectedSourceOwner", expectedSourceOwner.String(),
		"expectedDestATA", expectedDestATA.String(),
		"expectedMint", expectedMint.String(),
		"expectedAmountLamports", expectedAmountLamports)

	// Create maps for easy lookup PREFERABLY from ATA address -> balance info
	preBalanceMap := make(map[solanago.PublicKey]rpc.TokenBalance) // Correct type pointer usage
	for _, b := range preBalances {
		if int(b.AccountIndex) >= len(accountKeys) {
			logger.Error("PreBalance AccountIndex out of bounds", "index", b.AccountIndex, "accountKeysLen", len(accountKeys))
			continue // Skip invalid index
		}
		accountAddr := accountKeys[b.AccountIndex]
		preBalanceMap[accountAddr] = b
	}
	postBalanceMap := make(map[solanago.PublicKey]rpc.TokenBalance) // Correct type pointer usage
	for _, b := range postBalances {
		if int(b.AccountIndex) >= len(accountKeys) {
			logger.Error("PostBalance AccountIndex out of bounds", "index", b.AccountIndex, "accountKeysLen", len(accountKeys))
			continue // Skip invalid index
		}
		accountAddr := accountKeys[b.AccountIndex]
		postBalanceMap[accountAddr] = b
		logger.Debug("checkTokenBalances: Post balance entry", "accountIndex", b.AccountIndex, "accountAddr", accountAddr.String(), "owner", b.Owner.String(), "mint", b.Mint.String(), "amount", b.UiTokenAmount.Amount)
	}

	// Check Destination ATA
	postDestBal, destExists := postBalanceMap[expectedDestATA]
	if !destExists {
		logger.Debug("checkTokenBalances: Expected destination ATA not found in post balances", "dest_ata", expectedDestATA.String())
		return false, nil
	}
	if !postDestBal.Mint.Equals(expectedMint) {
		logger.Debug("checkTokenBalances: Destination ATA mint mismatch", "dest_ata", expectedDestATA.String(), "found_mint", postDestBal.Mint.String(), "expected_mint", expectedMint.String())
		return false, nil
	}
	preDestBal, ok := preBalanceMap[expectedDestATA] // Okay if it didn't exist before

	preDestAmountLamports := uint64(0)
	// Only parse if pre-balance existed and amount is valid
	if ok && preDestBal.UiTokenAmount.Amount != "" {
		var err error
		preDestAmountLamports, err = strconv.ParseUint(preDestBal.UiTokenAmount.Amount, 10, 64)
		if err != nil {
			logger.Warn("checkTokenBalances: Failed to parse preDestAmountLamports", "value", preDestBal.UiTokenAmount.Amount, "error", err)
			// Treat unparseable balance as 0 or handle error appropriately
			preDestAmountLamports = 0
		}
	}
	postDestAmountLamports, err := strconv.ParseUint(postDestBal.UiTokenAmount.Amount, 10, 64)
	if err != nil {
		logger.Error("checkTokenBalances: Failed to parse postDestAmountLamports", "value", postDestBal.UiTokenAmount.Amount, "error", err)
		return false, fmt.Errorf("failed to parse post-destination balance: %w", err) // Critical parsing failure
	}

	destIncrease := postDestAmountLamports - preDestAmountLamports
	logger.Debug("checkTokenBalances: Destination balance check", "dest_ata", expectedDestATA.String(), "pre", preDestAmountLamports, "post", postDestAmountLamports, "increase", destIncrease, "expected", expectedAmountLamports)
	if destIncrease != expectedAmountLamports {
		logger.Debug("checkTokenBalances: Destination ATA balance did not increase by expected amount")
		return false, nil // Didn't receive the right amount
	}
	logger.Debug("Destination ATA balance increase matches expected amount", "dest_ata", expectedDestATA.String(), "increase", expectedAmountLamports)

	// Check Source Account
	foundSourceMatch := false
	for sourceAta, preSourceBal := range preBalanceMap {
		// Check mint and owner match
		logger.Debug("checkTokenBalances: Checking potential source account", "source_ata", sourceAta.String(), "owner", preSourceBal.Owner.String(), "expected_owner", expectedSourceOwner.String(), "mint", preSourceBal.Mint.String(), "expected_mint", expectedMint.String())
		if preSourceBal.Mint.Equals(expectedMint) && preSourceBal.Owner.Equals(expectedSourceOwner) {
			postSourceBal, sourcePostExists := postBalanceMap[sourceAta]
			if !sourcePostExists { // Source account might have been closed, check balance went to 0
				logger.Warn("checkTokenBalances: Source ATA not found in post balances, potentially closed?", "ata", sourceAta.String())
				continue // Cannot verify decrease if post balance is missing
			}

			preSourceAmountLamports, err := strconv.ParseUint(preSourceBal.UiTokenAmount.Amount, 10, 64)
			if err != nil {
				logger.Warn("checkTokenBalances: Failed to parse preSourceAmountLamports", "value", preSourceBal.UiTokenAmount.Amount, "error", err)
				continue // Skip if pre-balance is unparseable
			}
			postSourceAmountLamports, err := strconv.ParseUint(postSourceBal.UiTokenAmount.Amount, 10, 64)
			if err != nil {
				logger.Warn("checkTokenBalances: Failed to parse postSourceAmountLamports", "value", postSourceBal.UiTokenAmount.Amount, "error", err)
				continue // Skip if post-balance is unparseable
			}

			sourceDecrease := preSourceAmountLamports - postSourceAmountLamports
			logger.Debug("checkTokenBalances: Source balance check", "source_ata", sourceAta.String(), "pre", preSourceAmountLamports, "post", postSourceAmountLamports, "decrease", sourceDecrease, "expected", expectedAmountLamports)

			// Check if balance decreased by the expected amount
			if sourceDecrease == expectedAmountLamports {
				logger.Debug("Source account balance decrease matches expected amount and owner",
					"source_ata", sourceAta.String(),
					"source_owner", expectedSourceOwner.String(),
					"decrease", expectedAmountLamports)
				foundSourceMatch = true
				break // Found the matching source decrease
			}
		}
	}

	if !foundSourceMatch {
		logger.Debug("checkTokenBalances: Did not find any source ATA owned by expected owner with matching balance decrease")
		return false, nil
	}

	// If we passed the destination check AND found a matching source owned by the correct wallet
	return true, nil
}
