-- name: InsertEmbedding :exec
INSERT INTO bounty_embeddings (bounty_id, embedding)
VALUES (@bounty_id, @embedding)
ON CONFLICT (bounty_id) DO UPDATE SET
embedding = EXCLUDED.embedding;

-- name: SearchEmbeddings :many
SELECT bounty_id, embedding
FROM bounty_embeddings
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
