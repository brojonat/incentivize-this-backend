-- name: UpsertBountySummary :exec
INSERT INTO bounty_summaries (bounty_id, summary)
VALUES (@bounty_id, @summary)
ON CONFLICT (bounty_id) DO UPDATE
SET summary = EXCLUDED.summary;

-- name: GetBountySummary :one
SELECT summary
FROM bounty_summaries
WHERE bounty_id = @bounty_id;

-- name: UpdateBountySummary :exec
UPDATE bounty_summaries
SET summary = @summary
WHERE bounty_id = @bounty_id;