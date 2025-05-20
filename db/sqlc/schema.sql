-- schema.sql for sqlc generation, DO NOT use with atlas; use golang-migrate instead.
CREATE TABLE IF NOT EXISTS bounty_embeddings (
    bounty_id VARCHAR(255) NOT NULL,
    embedding_vector VECTOR(1536) NOT NULL,
    PRIMARY KEY (bounty_id)
);
