#!/usr/bin/env bash

# Set the shell for make explicitly
SHELL := /bin/bash

define setup_env
	$(eval ENV_FILE := $(1))
	$(eval include $(1))
	$(eval export)
endef

# Core test targets
test: ## Run all tests
	go test -v ./...

test-workflow: ## Run only workflow-related tests
	go test -v ./... -run "Test.*Workflow"

test-solana: ## Run only Solana package tests
	go test -v ./solana/...

test-abb: ## Run only ABB package tests
	go test -v ./abb/...

test-rbb: ## Run only RBB package tests
	go test -v ./rbb/...

test-coverage: ## Generate HTML test coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

test-coverage-summary: ## Display test coverage summary in terminal
	go test -cover ./...

# CI integration
test-ci: ## Run tests for CI (includes race detector and coverage function output)
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

build-cli: ## Build the abb CLI binary
	go build -o ./bin/abb cmd/abb/*.go

build-push-cli: ## Build and push CLI Docker image with git hash tag (used by deploy targets)
	$(call setup_env, .env.server.prod)
	$(eval GIT_HASH := $(shell git rev-parse --short HEAD))
	$(eval DYNAMIC_TAG := brojonat/abb-cli:$(GIT_HASH))
	@echo "Building and pushing image: $(DYNAMIC_TAG)"
	docker build -f Dockerfile -t $(DYNAMIC_TAG) .
	docker push $(DYNAMIC_TAG)

refresh-token-debug: ## Refresh auth token for debugging (uses .env.server.debug)
	$(call setup_env, .env.server.debug)
	@$(MAKE) build-cli
	./bin/abb admin auth get-token --email ${ADMIN_EMAIL} --env-file .env.server.debug

run-http-server-local: ## Run the HTTP server locally (uses .env.server.debug)
	$(call setup_env, .env.server.debug)
	@if ! pgrep -f "kubectl port-forward.*temporal-frontend" > /dev/null; then \
		kubectl port-forward services/temporal-frontend 7233:7233 & \
		sleep 2; \
	fi
	@$(MAKE) build-cli
	./bin/abb run http-server --temporal-address ${TEMPORAL_ADDRESS} --temporal-namespace ${TEMPORAL_NAMESPACE}

run-worker-local: ## Run the Temporal worker locally (uses .env.worker.debug)
	$(call setup_env, .env.worker.debug)
	@if ! pgrep -f "kubectl port-forward.*temporal-frontend" > /dev/null; then \
		kubectl port-forward services/temporal-frontend 7233:7233 & \
		sleep 2; \
	fi
	@$(MAKE) build-cli
	./bin/abb run worker --temporal-address ${TEMPORAL_ADDRESS} --temporal-namespace ${TEMPORAL_NAMESPACE}

# Deployment targets
.PHONY: deploy-server deploy-worker deploy-all delete-server delete-worker delete-all

# Deploy server component
deploy-server: ## Deploy server to Kubernetes (prod)
	$(call setup_env, .env.server.prod)
	@$(MAKE) build-push-cli
	$(eval GIT_HASH := $(shell git rev-parse --short HEAD))
	$(eval DYNAMIC_TAG := brojonat/abb-cli:$(GIT_HASH))
	@echo "Applying server deployment with image: $(DYNAMIC_TAG)"
	kustomize build --load-restrictor=LoadRestrictionsNone server/k8s/prod | \
	sed -e "s;{{DOCKER_REPO}};$(DOCKER_REPO);g" \
		-e "s;{{CLI_IMG_TAG}};$(DYNAMIC_TAG);g" | \
		kubectl apply -f -
	# No need to patch anymore, the image tag change forces the rollout
	@echo "Server deployment applied."

# Deploy worker component
deploy-worker: ## Deploy worker to Kubernetes (prod)
	$(call setup_env, .env.worker.prod)
	@$(MAKE) build-push-cli
	$(eval GIT_HASH := $(shell git rev-parse --short HEAD))
	$(eval DYNAMIC_TAG := brojonat/abb-cli:$(GIT_HASH))
	@echo "Applying worker deployment with image: $(DYNAMIC_TAG)"
	kustomize build --load-restrictor=LoadRestrictionsNone worker/k8s/prod | \
	sed -e "s;{{DOCKER_REPO}};$(DOCKER_REPO);g" \
		-e "s;{{CLI_IMG_TAG}};$(DYNAMIC_TAG);g" | \
		kubectl apply -f -
	# No need to patch anymore, the image tag change forces the rollout
	@echo "Worker deployment applied."

# Deploy all components
deploy-all: ## Deploy both server and worker to Kubernetes (prod)
	$(MAKE) deploy-server
	$(MAKE) deploy-worker

# Delete server component
delete-server: ## Delete server from Kubernetes (prod)
	kubectl delete -f server/k8s/prod/ingress.yaml || true
	kubectl delete -f server/k8s/prod/server.yaml || true
	kubectl delete secret abb-secret-server-envs || true

# Delete worker component
delete-worker: ## Delete worker from Kubernetes (prod)
	kubectl delete -f worker/k8s/prod/worker.yaml || true
	kubectl delete secret affiliate-bounty-board-secret-worker-envs || true

# Delete all components
delete-all: ## Delete both server and worker from Kubernetes (prod)
	$(MAKE) delete-server
	$(MAKE) delete-worker

# View logs
.PHONY: logs-server logs-worker

logs-server: ## Tail logs for the server deployment
	kubectl logs -f deployment/affiliate-bounty-board-backend

logs-worker: ## Tail logs for the worker deployment
	kubectl logs -f deployment/affiliate-bounty-board-workers

# Port forwarding for local development
.PHONY: port-forward-server

port-forward-server: ## Port-forward the Kubernetes server service to localhost:8080
	kubectl port-forward svc/affiliate-bounty-board-backend 8080:80

# Check deployment status
.PHONY: status

status: ## Show status of Kubernetes deployments and pods
	@echo "=== Server Status ==="
	kubectl get deployment affiliate-bounty-board-backend -o wide
	@echo "\n=== Worker Status ==="
	kubectl get deployment affiliate-bounty-board-workers -o wide
	@echo "\n=== Pods Status ==="
	kubectl get pods -l app=affiliate-bounty-board

# Update secrets
.PHONY: update-secrets-server update-secrets-worker

update-secrets-server: ## Update Kubernetes secrets for the server from .env.server.prod
	kubectl create secret generic abb-secret-server-envs \
		--from-env-file=.env.server.prod \
		--dry-run=client -o yaml | kubectl apply -f -

update-secrets-worker: ## Update Kubernetes secrets for the worker from .env.worker.prod
	kubectl create secret generic affiliate-bounty-board-secret-worker-envs \
		--from-env-file=.env.worker.prod \
		--dry-run=client -o yaml | kubectl apply -f -

# Restart deployments
.PHONY: restart-server restart-worker

restart-server: ## Restart the server deployment
	kubectl rollout restart deployment affiliate-bounty-board-backend

restart-worker: ## Restart the worker deployment
	kubectl rollout restart deployment affiliate-bounty-board-workers

# Describe resources
.PHONY: describe-server describe-worker

describe-server: ## Describe Kubernetes resources for the server
	kubectl describe deployment affiliate-bounty-board-backend
	kubectl describe service affiliate-bounty-board-backend
	kubectl describe ingress affiliate-bounty-board-backend-ingress

describe-worker: ## Describe Kubernetes resources for the worker
	kubectl describe deployment affiliate-bounty-board-workers

# Tmux Development Session
# ------------------------
.PHONY: dev-session start-dev-session stop-dev-session

# Variables for tmux session
TMUX_SESSION := abb-dev
PORT_FORWARD_CMD := "kubectl port-forward service/temporal-web 8081:8080"
SERVER_CMD := $(MAKE) run-http-server-local # Command to run the server
WORKER_CMD := $(MAKE) run-worker-local   # Command to run the worker

# Stop existing session (if any) and start a new one
dev-session: stop-dev-session start-dev-session ## Stop (if running) and start a new tmux dev session

# Start the tmux development session
start-dev-session: build-cli ## Start a new tmux development session with port-forward, server, worker, and CLI panes
	@echo "Starting tmux development session: $(TMUX_SESSION)"
	# Create session in detached mode with port-forward. Ignore error if session already exists.
	@/usr/local/bin/tmux new-session -d -s $(TMUX_SESSION) -n 'DevEnv' "$(PORT_FORWARD_CMD)" || true
	# Add a brief pause to allow the server to initialize
	@sleep 1
	# Configure panes for a 2x2 layout
	@/usr/local/bin/tmux split-window -v -t $(TMUX_SESSION):0 "$(WORKER_CMD) 2>&1 | tee logs/worker.log" # Split vertically, run worker, pipe to tee
	@/usr/local/bin/tmux select-pane -t 0                                     # Select Port Forward pane (0)
	@/usr/local/bin/tmux split-window -h -t $(TMUX_SESSION):0.0 "$(SERVER_CMD) 2>&1 | tee logs/server.log" # Split horizontally, run server, pipe to tee
	@/usr/local/bin/tmux select-pane -t 1                                     # Select Worker pane (1)
	@/usr/local/bin/tmux split-window -h -t $(TMUX_SESSION):0.1                  # Split horizontally for CLI (pane 3)
	@/usr/local/bin/tmux select-layout -t $(TMUX_SESSION):0 tiled             # Apply tiled layout
	# Send messages to panes
	@/usr/local/bin/tmux select-pane -t 0 # Select Port Forward pane (index 0)
	@/usr/local/bin/tmux send-keys -t 0 'echo "Port Forward Pane ^"' C-m
	@/usr/local/bin/tmux select-pane -t 1 # Select Pane 1 (Should be Server)
	@/usr/local/bin/tmux send-keys -t 1 'echo "Server Pane ^"' C-m
	@/usr/local/bin/tmux select-pane -t 2 # Select Pane 2 (Should be CLI)
	@/usr/local/bin/tmux send-keys -t 2 'set -o allexport; source .env.server.debug; set +o allexport; export PATH=$$(pwd)/bin:$$PATH; echo "CLI Pane - .env sourced & ./bin added to PATH."' C-m
	@/usr/local/bin/tmux select-pane -t 3 # Select Pane 3 (Should be Worker)
	@/usr/local/bin/tmux send-keys -t 3 'echo "Worker Pane ^"' C-m
	# Attach to the session, focusing the CLI pane (index 2)
	@/usr/local/bin/tmux select-pane -t 2
	@/usr/local/bin/tmux attach-session -t $(TMUX_SESSION)

# Stop the tmux development session and associated processes
stop-dev-session: ## Stop the tmux development session and kill related processes
	@echo "Stopping background processes..."
	# Attempt to kill the port-forward command (adjust pattern if needed)
	@pkill -f "kubectl port-forward service/temporal-web" || true
	# Attempt to kill processes started by the make commands (adjust patterns if needed)
	# Using the make target names might be specific enough
	@pkill -f "$(MAKE) run-http-server-local" || true
	@pkill -f "$(MAKE) run-worker-local" || true
	# If the Go executables have specific names you build, you could target those too
	# @pkill -f "./bin/abb run http-server" || true
	# @pkill -f "./bin/abb run worker" || true
	@sleep 1 # Give processes a moment to terminate
	@echo "Stopping tmux development session: $(TMUX_SESSION)"
	@/usr/local/bin/tmux kill-session -t $(TMUX_SESSION) || true # Ignore error if session doesn't exist

# Variables (customize as needed)
ABB_CMD = abb
PER_POST_AMOUNT = 0.01
TOTAL_AMOUNT = 0.10
FUND_AMOUNT = $(TOTAL_AMOUNT) # Amount to fund via fund-escrow, assuming it matches total bounty for this script

create-reddit-bounty: ## Create and fund a Reddit example bounty
	@echo "--- Creating and Funding Reddit Bounty ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "Post must mention 'Temporal'" \
		-r "Must have positive score" \
		--per-post $(PER_POST_AMOUNT) --total $(TOTAL_AMOUNT) \
		--platform reddit --content-kind post`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.message | sub("Workflow started: "; "")'`; \
	echo "Extracted Workflow ID: '$$WORKFLOW_ID'"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ]; then \
		echo "Error: Could not extract Workflow ID from create command output." >&2; \
		exit 1; \
	fi; \
	echo "Attempting to fund workflow '$$WORKFLOW_ID' with amount $(FUND_AMOUNT)"; \
	echo "Suggestion: If fund-escrow outputs a transaction signature, please verify it on a Solana explorer."; \
	if $(ABB_CMD) admin util fund-escrow --workflow-id "$$WORKFLOW_ID" --amount $(FUND_AMOUNT); then \
		echo "--- fund-escrow command succeeded for $$WORKFLOW_ID (Makefile check) ---"; \
	else \
		echo "!!! fund-escrow command FAILED for $$WORKFLOW_ID (exit code $$?) !!!" >&2; \
		exit 1; \
	fi
	@echo "--- Reddit Bounty Funded (according to Makefile logic) ---"

create-youtube-bounty: ## Create and fund a YouTube example bounty
	@echo "--- Creating and Funding YouTube Bounty ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "Video must be about 'Go Programming'" \
		-r "Video must have transcript" \
		-r "Must have at least 10 views" \
		--per-post $(PER_POST_AMOUNT) --total $(TOTAL_AMOUNT) \
		--platform youtube --content-kind video`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.message | sub("Workflow started: "; "")'`; \
	echo "Extracted Workflow ID: $$WORKFLOW_ID"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ]; then \
		echo "Error: Could not extract Workflow ID from create command output." >&2; \
		exit 1; \
	fi; \
	$(ABB_CMD) admin util fund-escrow \
		--workflow-id $$WORKFLOW_ID \
		--amount $(FUND_AMOUNT)
	@echo "--- YouTube Bounty Funded ---"

create-twitch-bounty: ## Create and fund a Twitch example bounty
	@echo "--- Creating and Funding Twitch Bounty ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "Video must be about dota2" \
		-r "Video must include a thumbail with a TI (The International) trophy" \
		-r "Video must have at least 100 views" \
		--per-post $(PER_POST_AMOUNT) --total $(TOTAL_AMOUNT) \
		--platform twitch --content-kind video`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.message | sub("Workflow started: "; "")'`; \
	echo "Extracted Workflow ID: $$WORKFLOW_ID"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ]; then \
		echo "Error: Could not extract Workflow ID from create command output." >&2; \
		exit 1; \
	fi; \
	$(ABB_CMD) admin util fund-escrow \
		--workflow-id $$WORKFLOW_ID \
		--amount $(FUND_AMOUNT)
	@echo "--- Twitch Bounty Funded ---"

create-bluesky-bounty: ## Create and fund a Bluesky example bounty
	@echo "--- Creating and Funding Bluesky Bounty ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "Post must tag @incentivizethis.com" \
		-r "Must include the hashtag #bounty" \
		--per-post $(PER_POST_AMOUNT) --total $(TOTAL_AMOUNT) \
		--platform bluesky --content-kind post`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.message | sub("Workflow started: "; "")'`; \
	echo "Extracted Workflow ID: $$WORKFLOW_ID"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ]; then \
		echo "Error: Could not extract Workflow ID from create command output." >&2; \
		exit 1; \
	fi; \
	$(ABB_CMD) admin util fund-escrow \
		--workflow-id $$WORKFLOW_ID \
		--amount $(FUND_AMOUNT)
	@echo "--- Bluesky Bounty Funded ---"

create-hackernews-bounty: ## Create and fund a Hacker News example bounty
	@echo "--- Creating and Funding Hacker News Bounty ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "Comment must be constructive feedback on the linked article" \
		-r "Must be at least 50 characters long" \
		--per-post $(PER_POST_AMOUNT) --total $(TOTAL_AMOUNT) \
		--platform hackernews --content-kind comment`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.message | sub("Workflow started: "; "")'`; \
	echo "Extracted Workflow ID: $$WORKFLOW_ID"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ]; then \
		echo "Error: Could not extract Workflow ID from create command output." >&2; \
		exit 1; \
	fi; \
	$(ABB_CMD) admin util fund-escrow \
		--workflow-id $$WORKFLOW_ID \
		--amount $(FUND_AMOUNT)
	@echo "--- Hacker News Bounty Funded ---"

.PHONY: help create-and-fund-bounties create-reddit-bounty create-youtube-bounty create-twitch-bounty create-bluesky-bounty create-hackernews-bounty

help: ## Show this help message
	@echo "Available targets:"
	@awk -F ':.*?## ' '/^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort

CONCURRENT_BOUNTY_TARGETS := \
    create-reddit-bounty \
    create-youtube-bounty \
    create-twitch-bounty \
    create-bluesky-bounty \
    create-hackernews-bounty

# Target to create and fund one bounty for each platform concurrently
create-and-fund-bounties: ## Create and fund example bounties for all platforms (concurrently)
	echo "Starting concurrent creation and funding of platform bounties..."
	@pids=""; \
	for target_name in $(CONCURRENT_BOUNTY_TARGETS); do \
		echo "Starting $$target_name..."; \
		sleep 8; \
		$(MAKE) $$target_name > logs/$${target_name}.log 2>&1 & \
		pids="$$pids $$!"; \
	done; \
	echo "Waiting for all bounty creation jobs (PIDs:$$pids) to complete..."; \
	for pid in $$pids; do \
		wait $$pid || echo "A bounty creation job (PID: $$pid) may have failed."; \
	done; \
	echo "All platform bounty creation and funding processes complete."