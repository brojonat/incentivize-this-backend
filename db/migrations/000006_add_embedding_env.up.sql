ALTER TABLE bounty_embeddings ADD COLUMN environment VARCHAR(255) NOT NULL DEFAULT 'prod';
ALTER TABLE bounty_embeddings ALTER COLUMN environment DROP DEFAULT;
CREATE INDEX idx_bounty_embeddings_environment ON bounty_embeddings(environment);
