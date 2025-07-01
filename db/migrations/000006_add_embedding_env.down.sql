DROP INDEX IF EXISTS idx_bounty_embeddings_environment;
ALTER TABLE bounty_embeddings DROP COLUMN environment;
