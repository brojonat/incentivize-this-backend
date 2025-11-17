# Development Guide for Claude Code When Writing Go

This document provides guidance for Claude Code (and human developers) working
on this project. Follow these practices to maintain code quality, consistency,
and project health.

## Development Philosophy

This is a production-grade Go library and service intended for use across
multiple services. Quality, reliability, and maintainability are paramount.

## Feature Development Workflow

### 1. Plan Before Coding

Before implementing any feature:

- **Understand the requirement**: Clarify the use case and acceptance criteria
- **Design the interface first**: What's the API surface? How will clients use
  this?
- **Consider dependencies**: What components need to interact? What can be
  mocked?
- **Identify edge cases**: What can go wrong? How should errors be handled?
- **Document the plan**: Write down the approach in comments or a design doc

### 2. Write Tests First (TDD)

Follow Test-Driven Development (within reason):

```go
// 1. Write the test (it will fail)
func TestWalletPoller_PollNewTransactions(t *testing.T) {
    // Arrange
    mockClient := &MockSolanaClient{}
    poller := NewWalletPoller(mockClient)

    // Act
    txns, err := poller.Poll(ctx, walletAddress)

    // Assert
    require.NoError(t, err)
    assert.Len(t, txns, 5)
}

// 2. Write minimal code to pass the test
// 3. Refactor while keeping tests green
```

**Benefits:**

- Tests document intended behavior
- Forces you to think about the interface
- Prevents untested code
- Makes refactoring safer

### 3. Use the Makefile

Put frequently used commands in the `Makefile` for consistency:

```makefile
.PHONY: test
test:
	go test ./... -v -race -cover

.PHONY: lint
lint:
	golangci-lint run

.PHONY: dev
dev:
	air

.PHONY: db-migrate
db-migrate:
	migrate -path migrations -database "${DATABASE_URL}" up
```

### 4. Hot Reloading with Air

Use [Air](https://github.com/cosmtrek/air) for development:

**Configure** (`.air.toml`):

```toml
root = "."
tmp_dir = "tmp"

[build]
  bin = "./tmp/main"
  cmd = "go build -o ./tmp/main ./cmd/server"
  delay = 1000
  exclude_dir = ["assets", "tmp", "vendor", "frontend"]
  include_ext = ["go", "tpl", "tmpl", "html"]
  exclude_regex = ["_test\\.go"]
```

**Run:**

```bash
make dev  # or: air
```

Air will automatically rebuild and restart the server on file changes. Note that
every component of the application that's run via the main binary should be
hot-reloaded. That is, if the project has a server and a worker, both processes
should be targeted by `air`.

## Git Workflow

### Commit Messages

Write descriptive commit messages following conventional commits format:

```
type: short summary (50 chars or less)

Detailed explanation of what changed and why.
- Bullet points for key changes
- Reference issues: Closes #123

Types: feat, fix, docs, refactor, test, chore
```

## Documentation

### Always Update

When making changes, update relevant documentation:

1. **README.md**: Architecture, usage examples, getting started
2. **CHANGELOG.md**: User-facing changes (see format below)
3. **Code comments**: Complex logic, public APIs, configuration options
4. **Examples**: Add/update examples in `examples/` directory

### CHANGELOG Format

Use [Keep a Changelog](https://keepachangelog.com/) format. Update during feature development and before releasing.

## Code Quality

### Tests

Write tests where appropriate. Do not compromise implementation simplicity or
otherwise overcomplicate things for the sake of writing tests.

### Linting

Use `golangci-lint` with strict settings:

```bash
make lint
```

Fix all warnings before committing.

### Error Handling

Always handle errors. Use structured errors when appropriate.

## Security

- **Secrets Management**: Never commit credentials; use environment variables
  and _NEVER_ commit a .env file to version control!

## Development Environment Setup

Use Makefile targets "start-dev-session" and "stop-dev-session". These should do
things like build the binary, set up necessary port forwarding, tear down tmux
sessions, etc. These targets may transparently call simple, maintainable bash
scripts that do the underlying work.

## Common Tasks

### Adding a Database Migration

```bash
# Create migration files
migrate create -ext sql -dir migrations -seq add_transaction_index

# Edit migrations/000001_add_transaction_index.up.sql
# Edit migrations/000001_add_transaction_index.down.sql

# Test migration
make db-reset
make db-migrate
```

## Design Philosophy

This project embraces the Unix philosophy, Go proverbs, and the Zen of Python to
create tools that are simple, composable, and predictable.

### Core Principles

**Do One Thing Extremely Well**

Each component has a single, well-defined responsibility:

Resist the temptation to add unrelated features or "convenient" "fall back"
approaches.

**Simple Is Better Than Complex**

Avoid overengineering. Prefer plain JSON, standard SQL, and simple patterns over complex abstractions.

**Make Your Dependencies Explicit**

Following [go-kit](https://gokit.io/) philosophy, all dependencies should be
explicit and passed as parameters. Never hide dependencies in global state,
singletons, or package-level variables.

**Bad:**

```go
// Hidden dependency on global database connection
var db *sql.DB

func SaveTransaction(txn *Transaction) error {
    // Where did 'db' come from? Hard to test, hidden coupling
    _, err := db.Exec("INSERT INTO transactions ...")
    return err
}
```

**Good:**

```go
// Dependency is explicit in the function signature
func SaveTransaction(ctx context.Context, db *sql.DB, txn *Transaction) error {
    _, err := db.ExecContext(ctx, "INSERT INTO transactions ...")
    return err
}

// Even better: Use constructor injection with interfaces
type TransactionStore interface {
    Save(ctx context.Context, txn *Transaction) error
}

type PostgresStore struct {
    db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
    return &PostgresStore{db: db}
}

func (s *PostgresStore) Save(ctx context.Context, txn *Transaction) error {
    _, err := s.db.ExecContext(ctx, "INSERT INTO transactions ...")
    return err
}

// Usage: Dependencies are clear at construction time
store := NewPostgresStore(db)
poller := NewWalletPoller(solanaClient, store, natsConn)

```

**Benefits:**

- **Testability**: Easy to mock dependencies in tests
- **Clarity**: You can see exactly what a component needs
- **Flexibility**: Swap implementations (e.g., Postgres → SQLite for tests)
- **No hidden coupling**: Dependencies are visible in type signatures
- **Lifecycle management**: Clear ownership of resources

**Apply this everywhere:**

- Constructors take dependencies as parameters
- Use interfaces for external dependencies (DB, NATS, Solana RPC)
- Avoid `init()` functions that set up global state
- Avoid package-level variables for stateful dependencies
- Pass `context.Context` as the first parameter to all functions

**Avoid Frameworks, Embrace the Standard Library**

Frameworks often make the above goals harder by hiding complexity and coupling
your code to their abstractions. Instead, write functions that return
`http.Handler` and use the standard library router.

**Handler Functions Pattern:**

Following
[Mat Ryer's](https://pace.dev/blog/2018/05/09/how-I-write-http-services-after-eight-years.html)
approach, write functions that return `http.Handler`. These functions accept the
dependencies and are closed over in the handler implementation. Use this pattern
instead of a huge "Server" that has a bunch of methods and unnecessary state:

```go
// Handler function takes dependencies and returns http.Handler
func handleListWallets(store TransactionStore, logger *slog.Logger) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()

        wallets, err := store.ListWallets(ctx)
        if err != nil {
            logger.ErrorContext(ctx, "failed to list wallets", "error", err)
            http.Error(w, "internal error", http.StatusInternalServerError)
            return
        }

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(wallets)
    })
}
```

**Benefits:**

- Dependencies are explicit (passed as parameters)
- Easy to test (just call the function and test the handler)
- No framework magic or hidden behavior
- Handler has everything it needs in its closure

**Middleware Pattern with adaptHandler:**

Use an `adaptHandler` function to compose middleware. It iterates middleware in
reverse order so the first supplied is called first:

```go
// adapter wraps a handler and returns a new handler
type adapter func(http.Handler) http.Handler

// adaptHandler applies adapters to a handler in reverse order
// so the first adapter in the list is the outermost (called first)
func adaptHandler(h http.Handler, adapters ...adapter) http.Handler {
    // Apply in reverse order
    for i := len(adapters) - 1; i >= 0; i-- {
        h = adapters[i](h)
    }
    return h
}
```

**Example Middleware (adapters):**

```go
// Example adapter: Logging
func withLogging(logger *slog.Logger) adapter {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            next.ServeHTTP(w, r)
            logger.InfoContext(r.Context(), "request",
                "method", r.Method,
                "path", r.URL.Path,
                "duration", time.Since(start),
            )
        })
    }
}
```

**Go Tooling Preferences**

**CLI with urfave/cli:**

Use [urfave/cli](https://github.com/urfave/cli) for building CLIs. It provides clean flag/command API with automatic environment variable binding.

**SQL Generation with sqlc:**

Use [sqlc](https://sqlc.dev/) to generate type-safe Go code from SQL:

```bash
# Install
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

# Generate
sqlc generate
```

Write SQL queries, sqlc generates type-safe Go code. No ORM magic, just plain SQL with compile-time type checking. Commit generated code to version control.

## Logging

**Be Quiet by Default**: Only log on errors or significant events. Most logs should be DEBUG level.

Use Go's built-in `slog` package:

```go
// Setup logger with configurable level
func setupLogger(level slog.Level) *slog.Logger {
    opts := &slog.HandlerOptions{
        Level: level,
    }
    return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}

func main() {
    // Default to WARN in production, DEBUG in development
    logLevel := slog.LevelWarn
    if level := os.Getenv("LOG_LEVEL"); level != "" {
        switch strings.ToUpper(level) {
        case "DEBUG":
            logLevel = slog.LevelDebug
        case "INFO":
            logLevel = slog.LevelInfo
        case "WARN":
            logLevel = slog.LevelWarn
        case "ERROR":
            logLevel = slog.LevelError
        }
    }

    logger := setupLogger(logLevel)
    // ...
}
```

**Development**: Use `tee` to persist logs: `./server 2>&1 | tee -a logs/server.log`

**Production**: Log to stderr, let the container runtime handle collection.

**CLI Output**: When writing to stdout, use JSON for machine-readable output.

## Questions?

When in doubt:

- Check existing code for patterns
- Refer to Go best practices
- Ask for clarification rather than guessing
- Document decisions in commit messages
