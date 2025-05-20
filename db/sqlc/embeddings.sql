-- name: InsertEmbedding :exec
INSERT INTO bounty_embeddings (bounty_id, embedding)
VALUES (@bounty_id, @embedding);

-- name: SearchEmbeddings :many
SELECT bounty_id, embedding
FROM bounty_embeddings
ORDER BY embedding <=> @embedding
LIMIT @limit;

-- name: DeleteEmbedding :exec
DELETE FROM bounty_embeddings
WHERE bounty_id = @bounty_id;
