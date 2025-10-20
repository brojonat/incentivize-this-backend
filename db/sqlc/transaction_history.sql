-- -- name: InsertSolanaTransaction :one
-- INSERT INTO solana_transactions (
--     signature,
--     slot,
--     block_time,
--     bounty_id,
--     funder_wallet,
--     recipient_wallet,
--     amount_smallest_unit,
--     memo
-- ) VALUES (
--     $1, $2, $3, $4, $5, $6, $7, $8
-- )
-- ON CONFLICT (signature) DO NOTHING
-- RETURNING *;

-- -- name: GetSolanaTransactionsByBountyID :many
-- SELECT * FROM solana_transactions
-- WHERE bounty_id = $1;

-- -- name: GetLatestSolanaTransactionForRecipient :one
-- SELECT * FROM solana_transactions
-- WHERE recipient_wallet = $1
-- ORDER BY block_time DESC
-- LIMIT 1;

-- -- name: GetLatestSolanaTransactionForWallet :one
-- SELECT * FROM solana_transactions
-- WHERE funder_wallet = $1 OR recipient_wallet = $1
-- ORDER BY block_time DESC
-- LIMIT 1;

-- -- name: GetOutgoingSolanaTransactions :many
-- SELECT * FROM solana_transactions
-- WHERE funder_wallet = $1
-- ORDER BY block_time DESC
-- LIMIT $2;

-- -- name: GetSolanaTransactionsForBounty :many
-- SELECT * FROM solana_transactions
-- WHERE bounty_id = $1
-- ORDER BY block_time DESC
-- LIMIT $2;

-- -- name: GetIncomingSolanaTransactions :many
-- SELECT * FROM solana_transactions
-- WHERE recipient_wallet = $1
-- ORDER BY block_time DESC
-- LIMIT $2;
