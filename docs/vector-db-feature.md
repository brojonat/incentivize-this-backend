# semantic search

## Goal: enable semantic search for bounties

I want to enable semantic search for bounties from the client via a text input. The user will type in some text, it will get sent to our backend `/bounties/search` route, and we'll embed the text, perform a search in the embedding vector space, and return the closest bounties.

## Setting up the DB Connection

We need to add an environment variable `ABB_DATABASE_URL` that looks like `DATABASE_URL=postgresql://user:password@ep-rough-fire-a6hhnhit.us-west-2.aws.neon.tech/incentivizethis?sslmode=require`. The database already has the `pgvector` extension installed and the incentivizethis DB already exists. We're going to use `golang-migrate` to handle migrations; I've added a Makefile command for migrating the DB. When the HTTP server initializes, we'll connect to the DB, with retries, like we connect to other external services like Temporal.

Next during setup. we'll pass in the DB connection pool to the handlers that need it. Here's how we'll get the connection pool:

```go
func getConnPool(
	ctx context.Context,
	url string,
	ac func(context.Context, *pgx.Conn) error,
) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	cfg.AfterConnect = ac
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to database: %v", err)
	}
	if err = pool.Ping(ctx); err != nil {
		return nil, err
	}
	return pool, nil
}
```

Then in RunServer:

```go
    // set up the conn...
	p, err := getConnPool(
		ctx, dbHost,
		func(ctx context.Context, c *pgx.Conn) error { return nil },
	)
	if err != nil {
		return fmt.Errorf("could not connect to db: %w", err)
	}
    q := dbgen.New(p)
```

Here are our database dependencies:

```go
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
```

## Interfacing with the DB

We're going to use `sqlc` to interface with the DB (hence the `dbgen` call above). I've created the `embeddings.sql` file which should contain the following SQL:

```sql
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
```

Create the necessary `sqlc.yaml` using this as a starting point (fix any bugs with this):

```yaml
version: "2"
sql:
  - engine: "postgresql"
    queries:
      - "db/sqlc/embeddings.sql"
    schema: "db/sqlc/schema.sql"
    gen:
      go:
        package: "dbgen"
        out: "db/dbgen"
        sql_package: "pgx/v5"
        emit_json_tags: true
```

## Populating the Vector DB

We're going to populate the vector DB whenever we start a bounty. That is, the first activity of a bounty workflow is going to be to construct a chunk of text like the following:

```json
{
  "content_platform": "reddit",
  "content_kind": "comment",
  "requirements": "foo bar baz"
}
```

Feel free to add any additional fields you think would be valuable for the embedding text.

We'll use the LLM provider to construct an embedding (this will require an additional env `LLM_EMBEDDING_MODEL` which has been added to the .env files but we still need to pull into the code under the `EnvLLMEmbeddingModel` name). Then the activity will get a token from the ABB backend server, and then make an HTTP request to the server's `POST /bounties/embeddings` route. This route accepts a bounty_id and a vector embedding. The handler will write this embedding to the DB. On conflict it will update (update the sql if necessary). DO NOT worry about writing any DB implementation code; REMEMBER, just write the SQL and we'll use `sqlc` to generate the actual code.

## Tests

We want tests for this new feature as well. Write tests as you go.
