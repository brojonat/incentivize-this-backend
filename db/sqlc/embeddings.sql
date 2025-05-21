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

-- name: DeleteEmbedding :exec
DELETE FROM bounty_embeddings
WHERE bounty_id = @bounty_id;
