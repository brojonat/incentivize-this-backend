-- name: CreateContactUsSubmission :one
INSERT INTO contact_us_submissions (name, email, message)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetContactUsSubmission :one
SELECT * FROM contact_us_submissions
WHERE id = $1
LIMIT 1;

-- name: GetAllContactUsSubmissions :many
SELECT * FROM contact_us_submissions
WHERE id >= $1
ORDER BY id ASC
LIMIT $2;
