-- schema.sql for sqlc generation, DO NOT use with atlas; use golang-migrate instead.
CREATE EXTENSION IF NOT EXISTS vector;

-- Placeholder for bounty_embeddings table schema.
-- Actual schema should be managed by golang-migrate.
-- Example:
-- CREATE TABLE bounty_embeddings (
--     bounty_id VARCHAR(255) PRIMARY KEY,
--     embedding vector(768) -- Ensure dimension matches your LLM model (e.g., 768, 1536)
-- );
