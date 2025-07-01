-- name: InsertEmbedding :exec
INSERT INTO bounty_embeddings (bounty_id, embedding, environment)
VALUES (@bounty_id, @embedding, @environment)
ON CONFLICT (bounty_id) DO UPDATE SET
embedding = EXCLUDED.embedding,
environment = EXCLUDED.environment;

-- name: SearchEmbeddings :many
SELECT bounty_id, embedding
FROM bounty_embeddings
WHERE environment = @environment
ORDER BY embedding <=> @embedding
LIMIT @row_count;

-- name: ListBountyIDs :many
SELECT bounty_id
FROM bounty_embeddings;

-- name: DeleteEmbedding :exec
DELETE FROM bounty_embeddings
WHERE bounty_id = @bounty_id;

-- name: DeleteEmbeddings :exec
DELETE FROM bounty_embeddings
WHERE bounty_id = ANY(@bounty_ids);

-- name: DeleteEmbeddingsNotIn :exec
DELETE FROM bounty_embeddings
WHERE NOT bounty_id=ANY(@bounty_ids);
