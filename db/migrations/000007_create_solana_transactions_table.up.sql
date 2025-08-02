CREATE TABLE solana_transactions (
    signature VARCHAR(88) PRIMARY KEY,
    slot BIGINT NOT NULL,
    block_time TIMESTAMPTZ NOT NULL,
    bounty_id VARCHAR(255),
    funder_wallet VARCHAR(44) NOT NULL,
    recipient_wallet VARCHAR(44) NOT NULL,
    amount_smallest_unit BIGINT NOT NULL,
    memo TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_solana_transactions_bounty_id ON solana_transactions (bounty_id);

CREATE INDEX idx_solana_transactions_recipient_wallet_block_time ON solana_transactions (recipient_wallet, block_time DESC);
