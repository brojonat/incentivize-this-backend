# Repository Guidelines

## Project Structure & Module Organization
- `cmd/abb`: CLI entrypoints (`main.go`, `server.go`, `worker.go`) that launch the HTTP server and Temporal worker.
- `http/`: `net/http` + Gorilla handlers, middleware, and API wiring; start here for request/response changes.
- `abb/`: Temporal workflows and activities (integrations, payouts, embeddings) with co-located tests.
- `worker/`, `server/`: Deployment manifests (`k8s/`) and service bootstrap helpers.
- `db/`: SQL migrations, sqlc query definitions, and `dbgen` fixtures—keep schema and queries in sync.
- `solana/`: Signing and RPC helpers used by `abb/workflow_solana.go`.
- `docs/`: Supporting reference material; update when architecture or prompts shift.

## Build, Test, and Development Commands
- `make start-dev-session`: Launches the tmux-based dev stack, builds the CLI, and forwards Temporal ports.
- `make run-http-server-local` / `make run-worker-local`: Execute services individually after sourcing debug `.env` files.
- `make build-cli`: Produce the `bin/abb` binary for manual runs or image builds.
- `make test`, `make test-workflow`, `make test-solana`, `make test-abb`: Run the full or targeted Go test suites.
- `make test-coverage` / `make test-coverage-summary`: Emit HTML or terminal coverage reports.

## Coding Style & Naming Conventions
- Run `go fmt ./...` (or `goimports`) before committing; CI expects formatted code.
- Exported API uses PascalCase, package internals use lowerCamelCase, filenames stay snake_case.
- Keep Go’s tab indentation; align struct literals and key maps for quick scans.
- Factor shared logic into `abb/` or `internal/stools` to avoid oversized handlers or workflows.

## Testing Guidelines
- Unit tests live beside implementations (`*_test.go`); integration-style cases belong under `http/` or `abb/`.
- Name tests `Test<Feature>` and favor table-driven cases for permutations.
- Run `make test` before every push; add `make test-workflow` for Temporal edits and `make test-solana` for payout paths.
- Include coverage output (`coverage.html` or summary) when shipping high-risk changes.

## Commit & Pull Request Guidelines
- Mirror history: short, imperative subjects (`fix tests`, `implement versioning!`) under ~60 characters.
- Squash noisy WIP commits; link issues or Temporal tickets when available.
- PRs should explain the change, list validation commands (`make test`, coverage reports), and flag config or schema updates.
- Attach screenshots or sample responses when tweaking HTTP handlers or onboarding flows.

## Environment & Secrets
- Copy `.env.server.example` and `.env.worker.example` into environment files (`.env.server.debug`, `.env.worker.prod`) before running locally.
- Do not commit secrets; use `make update-secrets-server` and `make update-secrets-worker` to sync Kubernetes secrets.
- Keep Temporal endpoints and Solana keys consistent between server and worker configs before deploying.
