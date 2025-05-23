-- Create the vector extension if it doesn't already exist.
CREATE EXTENSION IF NOT EXISTS vector;

-- Create the bounty_embeddings table.
CREATE TABLE bounty_embeddings (
    bounty_id VARCHAR(255) PRIMARY KEY,
    embedding vector(1536) -- Assuming OpenAI's text-embedding-ada-002 or similar
);