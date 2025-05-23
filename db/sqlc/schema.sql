-- schema.sql for sqlc generation, DO NOT use with atlas; use golang-migrate instead.
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE bounty_embeddings (
    bounty_id VARCHAR(255) PRIMARY KEY,
    embedding vector(1536)
);

CREATE TABLE bounty_summaries (
    bounty_id VARCHAR(255) PRIMARY KEY,
    summary JSONB NOT NULL
);
