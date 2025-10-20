package http

// func handleInsertSolanaTransaction(logger *slog.Logger, querier dbgen.Querier) http.HandlerFunc {
// 	return func(w http.ResponseWriter, r *http.Request) {
// 		var tx api.SolanaTransaction
// 		if err := stools.DecodeJSONBody(r, &tx); err != nil {
// 			writeBadRequestError(w, err)
// 			return
// 		}

// 		bountyID := ""
// 		if tx.BountyID != nil {
// 			bountyID = *tx.BountyID
// 		}
// 		memo := ""
// 		if tx.Memo != nil {
// 			memo = *tx.Memo
// 		}

// 		insertedTx, err := querier.InsertSolanaTransaction(r.Context(), dbgen.InsertSolanaTransactionParams{
// 			Signature:          tx.Signature,
// 			Slot:               tx.Slot,
// 			BlockTime:          pgtype.Timestamptz{Time: tx.BlockTime, Valid: true},
// 			BountyID:           pgtype.Text{String: bountyID, Valid: bountyID != ""},
// 			FunderWallet:       tx.FunderWallet,
// 			RecipientWallet:    tx.RecipientWallet,
// 			AmountSmallestUnit: tx.AmountSmallestUnit,
// 			Memo:               pgtype.Text{String: memo, Valid: true},
// 		})
// 		if err != nil {
// 			writeInternalError(logger, w, err)
// 			return
// 		}

// 		writeJSONResponse(w, insertedTx, http.StatusCreated)
// 	}
// }

// func handleGetBountyTransactions(logger *slog.Logger, querier dbgen.Querier) http.HandlerFunc {
// 	return func(w http.ResponseWriter, r *http.Request) {
// 		bountyID := r.PathValue("bounty_id")
// 		if bountyID == "" {
// 			writeBadRequestError(w, fmt.Errorf("bounty_id is required"))
// 			return
// 		}

// 		txs, err := querier.GetSolanaTransactionsByBountyID(
// 			r.Context(),
// 			pgtype.Text{String: bountyID, Valid: true},
// 		)
// 		if err != nil {
// 			writeInternalError(logger, w, err)
// 			return
// 		}

// 		// Convert dbgen.SolanaTransaction to api.SolanaTransaction
// 		apiTxs := make([]api.SolanaTransaction, len(txs))
// 		for i, tx := range txs {
// 			apiTxs[i] = api.SolanaTransaction{
// 				Signature:          tx.Signature,
// 				Slot:               tx.Slot,
// 				BlockTime:          tx.BlockTime.Time,
// 				BountyID:           &tx.BountyID.String,
// 				FunderWallet:       tx.FunderWallet,
// 				RecipientWallet:    tx.RecipientWallet,
// 				AmountSmallestUnit: tx.AmountSmallestUnit,
// 				Memo:               &tx.Memo.String,
// 				CreatedAt:          tx.CreatedAt.Time,
// 			}
// 		}

// 		writeJSONResponse(w, apiTxs, http.StatusOK)
// 	}
// }

// func handleGetLatestSolanaTransactionForRecipient(logger *slog.Logger, querier dbgen.Querier) http.HandlerFunc {
// 	return func(w http.ResponseWriter, r *http.Request) {
// 		recipientWallet := r.PathValue("recipient_wallet")
// 		if recipientWallet == "" {
// 			writeBadRequestError(w, fmt.Errorf("recipient_wallet is required"))
// 			return
// 		}

// 		tx, err := querier.GetLatestSolanaTransactionForRecipient(r.Context(), recipientWallet)
// 		if err != nil {
// 			// If no rows are found, it's not an error, just means no transactions yet.
// 			// Return a successful response with a null body. The client can handle this.
// 			if err.Error() == "no rows in result set" {
// 				writeJSONResponse(w, nil, http.StatusOK)
// 				return
// 			}
// 			writeInternalError(logger, w, err)
// 			return
// 		}

// 		// Convert dbgen.SolanaTransaction to api.SolanaTransaction
// 		apiTx := api.SolanaTransaction{
// 			Signature:          tx.Signature,
// 			Slot:               tx.Slot,
// 			BlockTime:          tx.BlockTime.Time,
// 			BountyID:           &tx.BountyID.String,
// 			FunderWallet:       tx.FunderWallet,
// 			RecipientWallet:    tx.RecipientWallet,
// 			AmountSmallestUnit: tx.AmountSmallestUnit,
// 			Memo:               &tx.Memo.String,
// 			CreatedAt:          tx.CreatedAt.Time,
// 		}

// 		writeJSONResponse(w, &apiTx, http.StatusOK)
// 	}
// }
