# Development Guide for Claude Code When Writing Go

This document provides guidance for Claude Code (and human developers) working on this project. Follow these practices to maintain code quality, consistency, and project health.

## Development Philosophy

This is a production-grade Go library and service intended for use across multiple services. Quality, reliability, and maintainability are paramount.

## Feature Development Workflow

### 1. Plan Before Coding

Before implementing any feature:

- **Understand the requirement**: Clarify the use case and acceptance criteria
- **Design the interface first**: What's the API surface? How will clients use this?
- **Consider dependencies**: What components need to interact? What can be mocked?
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

### 3. Server + Client Development

**Rule**: Every new server feature should have a corresponding client method, ideally one that's accessible via a CLI (sub)command.

**Bad:**

```go
// Server only implementation
func (s *Server) HandleAddWallet(req *AddWalletRequest) error {
    // ...
}
```

**Good:**

```go
// Server implementation
func (s *Server) HandleAddWallet(req *AddWalletRequest) error {
    // ...
}

// Client method (in client package)
func (c *Client) AddWallet(ctx context.Context, address string, interval time.Duration) error {
    req := &AddWalletRequest{Address: address, PollInterval: interval}
    // Make NATS request
    return c.request(ctx, "wallet.add", req)
}

// Test that exercises both
func TestAddWallet_EndToEnd(t *testing.T) {
    // ...
}
```

### 4. Use the Makefile

Put frequently used commands in the `Makefile` for consistency:

```makefile
.PHONY: test
test:
	go test ./... -v -race -cover

.PHONY: test-integration
test-integration:
	go test ./... -v -tags=integration

.PHONY: lint
lint:
	golangci-lint run

.PHONY: build-server
build-server:
	go build -o bin/server ./cmd/server

.PHONY: build-client
build-client:
	go build -o bin/client ./cmd/client

.PHONY: dev
dev:
	air

.PHONY: db-migrate
db-migrate:
	migrate -path migrations -database "${DATABASE_URL}" up

.PHONY: db-reset
db-reset:
	migrate -path migrations -database "${DATABASE_URL}" drop
	migrate -path migrations -database "${DATABASE_URL}" up
```

**Usage:**

```bash
make test           # Run tests
make lint           # Run linter
make dev            # Start with hot reload
make build-server   # Build server binary
```

### 5. Hot Reloading with Air

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

Air will automatically rebuild and restart the server on file changes.

### 6. Leverage tmux for Development

Use [tmux](https://github.com/tmux/tmux) to manage multiple terminal sessions efficiently. This is especially useful for running the server, client, tests, and logs simultaneously.

**Recommended tmux Layout for Development:**

````bash
# Start a new tmux session for this project
tmux new -s my-project-name

# Split window into panes (example layout):
# ┌─────────────────────────────────────┐
# │  1. Server (air hot reload)         │
# ├─────────────────────────────────────┤
# │  2. Database logs  │  3. NATS logs  │
# ├────────────────────┼────────────────┤
# │  4. Tests/Commands │  5. Git/Editor │
# └────────────────────┴────────────────┘

## Git Workflow

### Branch Strategy

- **main**: Production-ready code, always stable
- **Feature branches**: `feature/wallet-polling`, `feature/nats-rpc`
- **Bug fixes**: `fix/jetstream-reconnect`
- **Experiments**: `experiment/timescaledb-partitioning`

**Workflow:**
```bash
# Create feature branch
git checkout -b feature/add-wallet-rpc

# Make changes, commit frequently
git add .
git commit -m "Add wallet.add RPC endpoint"

# Keep up to date with main
git fetch origin
git rebase origin/main

# When ready, merge to main
git checkout main
git merge feature/add-wallet-rpc
git push origin main

# Delete feature branch
git branch -d feature/add-wallet-rpc
````

### Commit Messages

Write comprehensive, descriptive commit messages:

**Bad:**

```
fix bug
update code
wip
```

**Good:**

```
Add NATS RPC endpoint for wallet management

Implements wallet.add, wallet.remove, and wallet.list RPC methods
using NATS request/reply pattern. Includes validation for wallet
addresses and poll interval constraints (minimum 10s).

- Add WalletManager service with NATS integration
- Implement request/reply handlers with timeout handling
- Add client methods: AddWallet, RemoveWallet, ListWallets
- Add integration tests for all RPC endpoints

Closes #42
```

**Format:**

```
[type]: Short summary (50 chars or less)

Detailed explanation of what changed and why. Wrap at 72 characters.
Include motivation, context, and any breaking changes.

- Bullet points for key changes
- Reference issues/PRs: Closes #123, Refs #456
- Note breaking changes: BREAKING: Changed API signature
```

**Types:** feat, fix, docs, refactor, test, chore, perf

## Documentation

### Always Update

When making changes, update relevant documentation:

1. **README.md**: Architecture, usage examples, getting started
2. **CHANGELOG.md**: User-facing changes (see format below)
3. **Code comments**: Complex logic, public APIs, configuration options
4. **Examples**: Add/update examples in `examples/` directory

### CHANGELOG Format

Use [Keep a Changelog](https://keepachangelog.com/) format:

```markdown
# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

- NATS RPC endpoints for wallet management
- JetStream integration for transaction streaming

### Changed

- Switched from HTTP to pure NATS architecture

### Fixed

- Race condition in wallet poller shutdown

## [0.2.0] - 2025-10-05

### Added

- TimescaleDB support for long-term storage
- Transaction memo parsing

### Changed

- Database schema to support hypertables

## [0.1.0] - 2025-09-20

### Added

- Initial release
- Basic Solana wallet polling
- PostgreSQL storage
```

**When to update:**

- During feature development (add to Unreleased section)
- Before releasing (move Unreleased to new version)
- For any user-facing change

## Code Quality

### Testing Standards

**Coverage goals:**

- Unit tests: 80%+ coverage
- Integration tests for all critical paths
- E2E tests for main workflows

**Test types:**

```go
// Unit test: Fast, isolated, no external dependencies
func TestParseTransactionMemo(t *testing.T) {
    memo := `{"workflow_id": "abc123"}`
    result, err := ParseMemo(memo)
    require.NoError(t, err)
    assert.Equal(t, "abc123", result.WorkflowID)
}

// Integration test: Uses real components (DB, NATS)
// +build integration
func TestWalletPoller_WithRealDatabase(t *testing.T) {
    db := setupTestDB(t)
    defer db.Close()
    // ...
}

// E2E test: Full system test
func TestPaymentWorkflow_EndToEnd(t *testing.T) {
    // Start server, client, make real NATS calls
}
```

### Linting

Use `golangci-lint` with strict settings:

```bash
make lint
```

Fix all warnings before committing.

### Error Handling

**Always handle errors:**

```go
// Bad
txns, _ := client.GetTransactions(ctx)

// Good
txns, err := client.GetTransactions(ctx)
if err != nil {
    return fmt.Errorf("failed to get transactions: %w", err)
}
```

**Use structured errors:**

```go
type WalletNotFoundError struct {
    Address string
}

func (e *WalletNotFoundError) Error() string {
    return fmt.Sprintf("wallet not found: %s", e.Address)
}
```

## Project Structure

```
.
├── cmd/
│   ├── server/          # Backend service entry point
│   └── client/          # CLI entry point
├── cli/                 # CLI implementation
├── client/              # Public client library
├── nats/                # NATS integration
├── poller/              # Solana polling logic
├── server/              # Server implementation
├── storage/             # Database layer
├── migrations/          # TimescaleDB migrations
├── examples/            # Usage examples
├── testdata/            # Test fixtures
├── Makefile
├── .air.toml           # Air configuration
├── go.mod
├── README.md
├── CHANGELOG.md
└── CLAUDE.md           # This file
```

## Security

- **Secrets Management**: Never commit credentials; use environment variables and _NEVER_ commit a .env file to version control!

## Development Environment Setup

```bash
# Install dependencies
go mod download

# Install development tools
go install github.com/cosmtrek/air@latest
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Start dependencies (Docker Compose)
docker-compose up -d postgres nats

# Run migrations
make db-migrate

# Start development server
make dev

# Run tests
make test
```

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

This project embraces the Unix philosophy and the Zen of Python to create tools that are simple, composable, and predictable.

### Core Principles

**Do One Thing Extremely Well**

Each component has a single, well-defined responsibility:

- Backend service: Poll Solana wallets and publish transactions
- Client library: Subscribe to transactions and integrate with workflows
- Database: Store transaction history for analytics
- Message broker: Deliver real-time updates

Resist the temptation to add unrelated features. If you need CSV export, write a separate tool that reads from the database. If you need Slack notifications, write a client that subscribes to NATS and posts to Slack.

**Write Programs That Compose**

Design components to work together through standard interfaces:

```bash
# Query transactions from TimescaleDB
psql -c "SELECT * FROM transactions WHERE wallet='...' AND amount > 1000" -t -A -F, | \
  # Process with any tool that reads CSV
  jq -R 'split(",") | {sig: .[0], amount: .[1]}'

# Subscribe to NATS stream and pipe to other tools
nats sub 'txns.>' --raw | jq 'select(.amount > 1000)' | your-notification-tool
```

The backend publishes raw transaction data. Clients decide what to do with it. This enables:

- Analytics tools that aggregate data
- Alerting systems that filter by conditions
- Custom workflows that react to specific patterns

**Simple Is Better Than Complex**

Avoid overengineering. Prefer:

- Plain JSON over custom binary protocols
- Simple NATS pub/sub over complex routing logic
- Standard SQL queries over ORM magic
- Environment variables over elaborate config DSLs

Complex solutions should be justified by complex problems, not anticipated future requirements.

**Make Your Dependencies Explicit**

Following [go-kit](https://gokit.io/) philosophy, all dependencies should be explicit and passed as parameters. Never hide dependencies in global state, singletons, or package-level variables.

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
server := NewServer(poller, store, natsConn)
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

**Example structure:**

```go
type Server struct {
    poller  *WalletPoller
    store   TransactionStore
    nats    *nats.Conn
    logger  *slog.Logger
}

func NewServer(
    poller *WalletPoller,
    store TransactionStore,
    nats *nats.Conn,
    logger *slog.Logger,
) *Server {
    return &Server{
        poller: poller,
        store:  store,
        nats:   nats,
        logger: logger,
    }
}
```

Reading the struct definition tells you everything the server depends on. No surprises.

**Avoid Frameworks, Embrace the Standard Library**

Frameworks often make the above goals harder by hiding complexity and coupling your code to their abstractions. Instead, write functions that return `http.Handler` and use the standard library router.

**Handler Functions Pattern:**

Following [Mat Ryer's](https://pace.dev/blog/2018/05/09/how-I-write-http-services-after-eight-years.html) approach, write functions that return `http.Handler`:

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

Use an `adaptHandler` function to compose middleware. It iterates middleware in reverse order so the first supplied is called first:

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
// Logging adapter
func withLogging(logger *slog.Logger) adapter {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            logger.InfoContext(r.Context(), "request started",
                "method", r.Method,
                "path", r.URL.Path,
            )

            next.ServeHTTP(w, r)

            logger.InfoContext(r.Context(), "request completed",
                "method", r.Method,
                "path", r.URL.Path,
                "duration", time.Since(start),
            )
        })
    }
}

// JWT Authentication adapter
func withJWTAuth(secret []byte) adapter {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Extract token from Authorization header
            authHeader := r.Header.Get("Authorization")
            if authHeader == "" {
                http.Error(w, "missing authorization header", http.StatusUnauthorized)
                return
            }

            // Expect format: "Bearer <token>"
            tokenString := strings.TrimPrefix(authHeader, "Bearer ")
            if tokenString == authHeader {
                http.Error(w, "invalid authorization format", http.StatusUnauthorized)
                return
            }

            // Parse and validate JWT
            token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
                // Validate signing method
                if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
                    return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
                }
                return secret, nil
            })

            if err != nil || !token.Valid {
                http.Error(w, "invalid token", http.StatusUnauthorized)
                return
            }

            // Extract claims and add to context
            if claims, ok := token.Claims.(jwt.MapClaims); ok {
                ctx := context.WithValue(r.Context(), "user_id", claims["sub"])
                ctx = context.WithValue(ctx, "claims", claims)
                next.ServeHTTP(w, r.WithContext(ctx))
                return
            }

            http.Error(w, "invalid token claims", http.StatusUnauthorized)
        })
    }
}

// Request ID adapter
func withRequestID() adapter {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            requestID := uuid.New().String()
            ctx := context.WithValue(r.Context(), "request_id", requestID)
            w.Header().Set("X-Request-ID", requestID)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// Prometheus metrics adapter
func withMetrics(registry *prometheus.Registry) adapter {
    // Define metrics
    httpDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
        Name:    "http_request_duration_seconds",
        Help:    "Duration of HTTP requests in seconds",
        Buckets: prometheus.DefBuckets,
    }, []string{"method", "path", "status"})

    httpRequestsTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "http_requests_total",
        Help: "Total number of HTTP requests",
    }, []string{"method", "path", "status"})

    // Register metrics
    registry.MustRegister(httpDuration, httpRequestsTotal)

    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()

            // Wrap response writer to capture status code
            wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

            next.ServeHTTP(wrapped, r)

            // Record metrics
            duration := time.Since(start).Seconds()
            status := strconv.Itoa(wrapped.statusCode)
            labels := prometheus.Labels{
                "method": r.Method,
                "path":   r.URL.Path,
                "status": status,
            }

            httpDuration.With(labels).Observe(duration)
            httpRequestsTotal.With(labels).Inc()
        })
    }
}

func writeJSONResponse(w http.ResponseWriter, resp interface{}, code int) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(resp)
}

func writeOK(w http.ResponseWriter) {
	resp := map[string]string{"message": "ok"}
	writeJSONResponse(w, resp, http.StatusOK)
}

func writeInternalError(l *slog.Logger, w http.ResponseWriter, e error) {
	l.Error("internal error", "error", e.Error())
	resp := map[string]string{"error": "internal error"}
	writeJSONResponse(w, resp, http.StatusInternalServerError)
}

func writeBadRequestError(l *slog.Logger, w http.ResponseWriter, err error) {
    l.Debug("bad request", "error", err.Error())
	resp := map[string]string{"error": err.Error()}
	writeJSONResponse(w, resp, http.StatusBadRequest)
}

```

**Health Check Handler:**

```go
func handleHealth(db *sql.DB) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()

        // Check database connectivity
        if err := db.PingContext(ctx); err != nil {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusServiceUnavailable)
            json.NewEncoder(w).Encode(map[string]interface{}{
                "status": "unhealthy",
                "error":  "database unavailable",
            })
            return
        }

        // Add more health checks as needed (NATS, external APIs, etc)

        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusOK)
        json.NewEncoder(w).Encode(map[string]interface{}{
            "status": "healthy",
            "timestamp": time.Now().UTC().Format(time.RFC3339),
        })
    })
}
```

**Wire Up Routes with Standard Library Router:**

```go
func main() {
    // Initialize dependencies
    db := setupDatabase()
    store := NewPostgresStore(db)
    logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

    // JWT secret from environment
    jwtSecret := []byte(os.Getenv("JWT_SECRET"))
    if len(jwtSecret) == 0 {
        logger.Error("JWT_SECRET environment variable not set")
        os.Exit(1)
    }

    // Prometheus registry
    promRegistry := prometheus.NewRegistry()

    // Create router (stdlib http.ServeMux)
    mux := http.NewServeMux()

    // Public endpoints (no auth, no metrics to avoid noise)
    mux.Handle("GET /health", adaptHandler(
        handleHealth(db),
        withRequestID(),
        withLogging(logger),
    ))

    // Prometheus metrics endpoint (no auth for scraping, but could add basic auth)
    mux.Handle("GET /metrics", promhttp.HandlerFor(promRegistry, promhttp.HandlerOpts{}))

    // Protected endpoints (with JWT auth and metrics)
    mux.Handle("GET /wallets", adaptHandler(
        handleListWallets(store, logger),
        withRequestID(),
        withLogging(logger),
        withMetrics(promRegistry),
        withJWTAuth(jwtSecret),
    ))

    mux.Handle("POST /wallets", adaptHandler(
        handleAddWallet(store, logger),
        withRequestID(),
        withLogging(logger),
        withMetrics(promRegistry),
        withJWTAuth(jwtSecret),
    ))

    mux.Handle("DELETE /wallets/{address}", adaptHandler(
        handleRemoveWallet(store, logger),
        withRequestID(),
        withLogging(logger),
        withMetrics(promRegistry),
        withJWTAuth(jwtSecret),
    ))

    // Start server
    addr := ":8080"
    logger.Info("starting server", "addr", addr)
    if err := http.ListenAndServe(addr, mux); err != nil {
        logger.Error("server failed", "error", err)
        os.Exit(1)
    }
}
```

**Usage:**

```bash
# Start server with JWT secret
export JWT_SECRET="your-secret-key-here"
./server

# Health check (public)
curl http://localhost:8080/health

# Prometheus metrics (public)
curl http://localhost:8080/metrics

# Protected endpoint (requires JWT)
curl -H "Authorization: Bearer <your-jwt-token>" http://localhost:8080/wallets
```

**Why This Pattern Works:**

1. **Explicit Dependencies**: Handler functions receive exactly what they need
2. **Composable Middleware**: Mix and match adapters for different routes
3. **Clear Order**: First adapter in the list runs first (outer-most)
4. **No Magic**: Just functions and the stdlib - easy to understand and debug
5. **Testable**: Each handler and adapter can be tested independently

**Example Test:**

```go
func TestHandleListWallets(t *testing.T) {
    // Arrange
    mockStore := &MockTransactionStore{
        wallets: []Wallet{{Address: "abc123"}},
    }
    logger := slog.New(slog.NewTextHandler(io.Discard, nil))

    handler := handleListWallets(mockStore, logger)
    req := httptest.NewRequest("GET", "/wallets", nil)
    rec := httptest.NewRecorder()

    // Act
    handler.ServeHTTP(rec, req)

    // Assert
    assert.Equal(t, http.StatusOK, rec.Code)

    var wallets []Wallet
    json.NewDecoder(rec.Body).Decode(&wallets)
    assert.Len(t, wallets, 1)
    assert.Equal(t, "abc123", wallets[0].Address)
}
```

No framework, no magic, just plain Go. This keeps the code simple, explicit, and easy to reason about.

**Go Tooling Preferences**

**CLI with urfave/cli:**

Use the [urfave/cli](https://github.com/urfave/cli) library for building command-line interfaces. It provides a clean, composable API for flags, commands, and subcommands.

```go
package main

import (
    "log"
    "os"

    "github.com/urfave/cli/v2"
)

func main() {
    app := &cli.App{
        Name:  "solana-payment",
        Usage: "Solana wallet payment service",
        Commands: []*cli.Command{
            {
                Name:  "server",
                Usage: "Start the HTTP/NATS server",
                Flags: []cli.Flag{
                    &cli.StringFlag{
                        Name:    "addr",
                        Value:   ":8080",
                        Usage:   "HTTP server address",
                        EnvVars: []string{"SERVER_ADDR"},
                    },
                    &cli.StringFlag{
                        Name:     "db-url",
                        Usage:    "Database connection string",
                        EnvVars:  []string{"DATABASE_URL"},
                        Required: true,
                    },
                    &cli.StringFlag{
                        Name:     "nats-url",
                        Value:    "nats://localhost:4222",
                        Usage:    "NATS server URL",
                        EnvVars:  []string{"NATS_URL"},
                    },
                    &cli.StringFlag{
                        Name:    "log-level",
                        Value:   "warn",
                        Usage:   "Log level (debug, info, warn, error)",
                        EnvVars: []string{"LOG_LEVEL"},
                    },
                },
                Action: runServer,
            },
            {
                Name:  "poller",
                Usage: "Start the wallet poller worker",
                Flags: []cli.Flag{
                    &cli.StringFlag{
                        Name:     "db-url",
                        Usage:    "Database connection string",
                        EnvVars:  []string{"DATABASE_URL"},
                        Required: true,
                    },
                    &cli.StringFlag{
                        Name:     "nats-url",
                        Value:    "nats://localhost:4222",
                        Usage:    "NATS server URL",
                        EnvVars:  []string{"NATS_URL"},
                    },
                },
                Action: runPoller,
            },
            {
                Name:  "migrate",
                Usage: "Run database migrations",
                Flags: []cli.Flag{
                    &cli.StringFlag{
                        Name:     "db-url",
                        Usage:    "Database connection string",
                        EnvVars:  []string{"DATABASE_URL"},
                        Required: true,
                    },
                    &cli.StringFlag{
                        Name:  "direction",
                        Value: "up",
                        Usage: "Migration direction (up or down)",
                    },
                },
                Action: runMigrations,
            },
        },
    }

    if err := app.Run(os.Args); err != nil {
        log.Fatal(err)
    }
}

func runServer(c *cli.Context) error {
    addr := c.String("addr")
    dbURL := c.String("db-url")
    natsURL := c.String("nats-url")
    logLevel := parseLogLevel(c.String("log-level"))

    logger := setupLogger(logLevel)
    logger.Info("starting server",
        "addr", addr,
        "nats_url", natsURL,
    )

    // Initialize and run server...
    return nil
}
```

**Benefits:**

- Clean flag/command API
- Automatic environment variable binding
- Built-in help generation
- Subcommands for different services (server, poller, migrate)
- Consistent CLI experience across all tools

**SQL Generation with sqlc:**

Use [sqlc](https://sqlc.dev/) to generate type-safe Go code from SQL. Write SQL, get Go.

**Installation:**

```bash
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
```

**Configuration (`sqlc.yaml`):**

```yaml
version: "2"
sql:
  - engine: "postgresql"
    queries:
      - "db/sqlc/embeddings.sql"
      - "db/sqlc/bounty_summary.sql"
      - "db/sqlc/gumroad.sql"
      - "db/sqlc/contact_us.sql"
      - "db/sqlc/transaction_history.sql"
    schema: "db/sqlc/schema.sql"
    gen:
      go:
        package: "dbgen"
        out: "db/dbgen"
        sql_package: "pgx/v5"
        emit_json_tags: true
        emit_interface: true
        overrides:
          - db_type: "vector"
            go_type: "github.com/pgvector/pgvector-go.Vector"
```

**Write SQL queries (`db/sqlc/[query_family_name].sql`):**

```sql
-- name: GetTransaction :one
SELECT * FROM transactions
WHERE signature = $1 LIMIT 1;

-- name: ListTransactionsByWallet :many
SELECT * FROM transactions
WHERE wallet_address = $1
ORDER BY block_time DESC
LIMIT $2 OFFSET $3;

-- name: CreateTransaction :one
INSERT INTO transactions (
    signature,
    wallet_address,
    slot,
    block_time,
    amount,
    token_mint,
    memo
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetTransactionsByTimeRange :many
SELECT * FROM transactions
WHERE wallet_address = $1
  AND block_time >= $2
  AND block_time <= $3
ORDER BY block_time DESC;

-- name: CountTransactionsByWallet :one
SELECT COUNT(*) FROM transactions
WHERE wallet_address = $1;
```

**Generate Go code:**

```bash
sqlc generate
```

**Use generated code:**

```go
package main

import (
    "context"
    "database/sql"

    "github.com/yourorg/solana-payment/internal/db"
)

func example(ctx context.Context, conn *sql.DB) error {
    queries := db.New(conn)

    // Type-safe queries with compile-time checking
    txn, err := queries.GetTransaction(ctx, "signature123")
    if err != nil {
        return err
    }

    // Parameters are strongly typed
    txns, err := queries.ListTransactionsByWallet(ctx, db.ListTransactionsByWalletParams{
        WalletAddress: "wallet123",
        Limit:         10,
        Offset:        0,
    })
    if err != nil {
        return err
    }

    // Insert with type safety
    newTxn, err := queries.CreateTransaction(ctx, db.CreateTransactionParams{
        Signature:     "sig456",
        WalletAddress: "wallet123",
        Slot:          12345,
        BlockTime:     time.Now(),
        Amount:        1000000,
        TokenMint:     sql.NullString{String: "EPjFWdd5AufqSSqeM2qN1xzybapC8G4wEGGkZwyTDt1v", Valid: true},
        Memo:          sql.NullString{String: `{"workflow_id": "abc123"}`, Valid: true},
    })
    if err != nil {
        return err
    }

    return nil
}
```

**Benefits:**

- **Type Safety**: Compile-time SQL validation
- **No ORM Magic**: You write SQL, sqlc generates Go
- **Performance**: Direct SQL execution, no reflection
- **Explicit**: Generated code is readable and debuggable
- **Maintainable**: SQL is version controlled alongside code
- **PostgreSQL/TimescaleDB**: Full support for advanced features

**Why sqlc over ORMs:**

- ORMs hide complexity and make optimizations harder
- SQL is explicit and portable
- Generated code is just functions - no framework lock-in
- Easy to optimize queries without fighting the abstraction
- You already know SQL - no need to learn ORM DSL

**Project Structure with sqlc:**

```
.
├── migrations/              # SQL schema migrations
│   ├── 001_initial.up.sql
│   ├── 001_initial.down.sql
│   ├── 002_add_indexes.up.sql
│   └── 002_add_indexes.down.sql
├── queries/                 # SQL queries for sqlc
│   ├── transactions.sql
│   ├── wallets.sql
│   └── analytics.sql
├── internal/
│   └── db/                 # Generated code (from sqlc)
│       ├── db.go
│       ├── models.go
│       ├── transactions.sql.go
│       └── wallets.sql.go
├── sqlc.yaml               # sqlc configuration
└── Makefile
    └── sqlc-generate target
```

**Makefile integration:**

```makefile
.PHONY: sqlc-generate
sqlc-generate:
	sqlc generate

.PHONY: sqlc-verify
sqlc-verify:
	sqlc verify

.PHONY: db-migrate
db-migrate:
	migrate -path migrations -database "${DATABASE_URL}" up

# Run before commits
.PHONY: pre-commit
pre-commit: sqlc-verify test lint
```

Always regenerate sqlc code after schema changes and commit the generated code to version control.

**Be Quiet by Default**

Programs should only output information when there's something unexpected to report:

**Bad:**

```
2025-10-09 10:15:23 INFO Starting wallet poller...
2025-10-09 10:15:23 INFO Connected to database
2025-10-09 10:15:23 INFO Connected to NATS
2025-10-09 10:15:23 INFO Polling wallet Abc123...
2025-10-09 10:15:23 INFO Found 0 new transactions
2025-10-09 10:15:53 INFO Polling wallet Abc123...
2025-10-09 10:15:53 INFO Found 0 new transactions
```

**Good:**

```
# Normal operation: Silent
# Only output on errors or significant events:
2025-10-09 10:15:23 ERROR Failed to connect to NATS: connection refused
```

Use structured logging (JSON) for debugging and metrics, but send it to stderr or a log file, not stdout. Reserve stdout for actionable output.

**Structured Logging with slog**

Use Go's built-in `slog` package for all logging. Default to DEBUG level for most log messages so verbosity can be easily controlled:

```go
// Setup logger with configurable level
func setupLogger(level slog.Level) *slog.Logger {
    opts := &slog.HandlerOptions{
        Level: level,
    }
    return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}

// Usage in code - most logs at DEBUG level
logger.DebugContext(ctx, "polling wallet",
    "wallet", walletAddress,
    "last_slot", lastSlot,
)

logger.DebugContext(ctx, "found new transactions",
    "wallet", walletAddress,
    "count", len(txns),
)

// Only use INFO for significant lifecycle events
logger.InfoContext(ctx, "server started",
    "addr", addr,
    "version", version,
)

// WARN for recoverable issues
logger.WarnContext(ctx, "rate limit exceeded, retrying",
    "wallet", walletAddress,
    "retry_after", retryAfter,
)

// ERROR for failures
logger.ErrorContext(ctx, "failed to store transaction",
    "error", err,
    "signature", sig,
)
```

**Control verbosity via environment variable:**

```go
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

**Development: Logging to Files with tee**

For local development, use `tee` to write logs to both stderr (for console) and files (for agents/debugging):

```bash
# Create logs directory
mkdir -p logs

# Run server with tee to log to file
./server 2>&1 | tee -a logs/server.log

# Run poller separately
./poller 2>&1 | tee -a logs/poller.log

# Run with debug logging enabled
LOG_LEVEL=DEBUG ./server 2>&1 | tee -a logs/server-debug.log

# Rotate logs daily (simple approach)
./server 2>&1 | tee -a logs/server-$(date +%Y-%m-%d).log
```

**Log Directory Structure (Development):**

```
logs/
├── server.log           # Main server logs
├── poller.log          # Wallet poller logs
├── nats.log            # NATS connection logs (if separate process)
└── temporal.log        # Temporal worker logs
```

**Benefits for Development:**

- Logs are persistent in files for later inspection
- Each service has its own log file for easy debugging
- JSON format makes logs easily parseable with `jq`
- Debug level can be toggled without code changes
- Agents can tail logs to monitor system state

**Querying logs with jq (Development):**

```bash
# Find all errors
jq 'select(.level == "ERROR")' logs/server.log

# Find slow requests (over 1 second)
jq 'select(.duration > 1.0)' logs/server.log

# Group errors by type
jq -r 'select(.level == "ERROR") | .error' logs/server.log | sort | uniq -c

# Find all logs for a specific request ID
jq 'select(.request_id == "abc-123")' logs/server.log

# Monitor logs in real-time
tail -f logs/server.log | jq 'select(.level == "ERROR" or .level == "WARN")'
```

**Production Deployment:**

This service is designed to run on Kubernetes. In production, simply log to stderr - no `tee` needed:

```go
// In production, just log to stderr
logger := slog.New(slog.NewJSONHandler(os.Stderr, opts))
```

```bash
# Kubernetes collects stdout/stderr automatically
./server  # Logs go to stderr, Kubernetes handles collection
```

Kubernetes examples and manifests will be provided separately. Kubernetes log aggregation (e.g., Loki, ELK, CloudWatch) handles collection, retention, and querying.

**When You Write to Stdout, Use JSON**

If a program produces output, make it machine-readable:

**Bad:**

```
Wallet: Abc123
Transactions: 5
Last Poll: 2025-10-09 10:15:23
```

**Good:**

```json
{
  "wallet": "Abc123",
  "transaction_count": 5,
  "last_poll": "2025-10-09T10:15:23Z"
}
```

This enables composition:

```bash
# Get transaction count for all wallets
wallet-cli list-wallets | jq '.transaction_count' | awk '{sum+=$1} END {print sum}'

# Find wallets with recent activity
wallet-cli list-wallets | jq 'select(.last_poll > "2025-10-09T10:00:00Z") | .wallet'
```

**Exceptions:**

- Interactive CLI tools can use human-friendly formatting (but offer `--json` flag)
- Error messages to stderr can be plain text
- Log files can use structured formats (JSON, logfmt)

### Practical Applications

**Backend Service:**

- Runs silently in production
- Logs errors/warnings to stderr as JSON
- Exposes metrics via Prometheus endpoint (not stdout)
- No "successfully processed transaction" logs for normal operation

**Client Library:**

- Returns errors, doesn't print them
- No "connecting to NATS..." messages
- Caller decides what to log

**CLI Tools:**

- Default to JSON output on stdout
- Provide `--format=table|json|csv` flag for human use
- Errors go to stderr
- Exit codes indicate success/failure (0 = success, non-zero = error)

### Why This Matters

These principles make the system:

- **Debuggable**: JSON logs are easily parsed and analyzed
- **Composable**: Outputs become inputs for other tools
- **Scriptable**: Predictable behavior enables automation
- **Maintainable**: Simple components are easier to understand and modify
- **Resilient**: Single-purpose tools fail independently

When in doubt, ask: "Does this add essential value, or does it just add complexity?"

## Questions?

When in doubt:

- Check existing code for patterns
- Refer to Go best practices
- Ask for clarification rather than guessing
- Document decisions in commit messages
