-- schema.sql for sqlc generation, DO NOT use with atlas; use golang-migrate instead.
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE bounty_embeddings (
    bounty_id VARCHAR(255) PRIMARY KEY,
    embedding vector(1536),
    environment VARCHAR(255) NOT NULL
);

CREATE TABLE bounty_summaries (
    bounty_id VARCHAR(255) PRIMARY KEY,
    summary JSONB NOT NULL
);

CREATE TABLE gumroad_sales (
    id VARCHAR(255) PRIMARY KEY,
    product_id VARCHAR(255) NOT NULL,
    product_name TEXT,
    permalink VARCHAR(255),
    product_permalink TEXT,
    email VARCHAR(255) NOT NULL,
    price BIGINT,
    gumroad_fee BIGINT,
    currency VARCHAR(10),
    quantity INTEGER,
    discover_fee_charged BOOLEAN,
    can_contact BOOLEAN,
    referrer TEXT,
    order_number BIGINT,
    sale_id VARCHAR(255),
    sale_timestamp TIMESTAMPTZ,
    purchaser_id VARCHAR(255),
    subscription_id VARCHAR(255),
    license_key VARCHAR(255),
    is_multiseat_license BOOLEAN,
    ip_country VARCHAR(255),
    recurrence VARCHAR(50),
    is_gift_receiver_purchase BOOLEAN,
    refunded BOOLEAN,
    disputed BOOLEAN,
    dispute_won BOOLEAN,
    created_at TIMESTAMPTZ NOT NULL,
    chargebacked BOOLEAN,
    subscription_ended_at TIMESTAMPTZ,
    subscription_cancelled_at TIMESTAMPTZ,
    subscription_failed_at TIMESTAMPTZ,
    -- IncentivizeThis-specific fields
    it_notified BOOLEAN DEFAULT FALSE,
    it_api_key TEXT
);

CREATE TABLE contact_us_submissions (
    id SERIAL PRIMARY KEY,
    name TEXT,
    email TEXT,
    message TEXT,
    created_at TIMESTAMPTZ NOT NULL
);

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
