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
test-ci: ## Run tests for CI (includes race detector, skips integration tests)
	go test -race -short ./...

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
	@$(MAKE) build-cli
	./bin/abb run http-server --temporal-address ${TEMPORAL_ADDRESS} --temporal-namespace ${TEMPORAL_NAMESPACE}

run-http-server-local-air: ## Run the HTTP server with Air hot-reload (uses .env.server.debug)
	$(call setup_env, .env.server.debug)
	@mkdir -p tmp logs
	PATH="$(PATH):$$(go env GOPATH)/bin" air -c .air.server.toml

run-worker-local: ## Run the Temporal worker locally (uses .env.worker.debug)
	$(call setup_env, .env.worker.debug)
	@$(MAKE) build-cli
	./bin/abb run worker --temporal-address ${TEMPORAL_ADDRESS} --temporal-namespace ${TEMPORAL_NAMESPACE}

run-worker-local-air: ## Run the Temporal worker with Air hot-reload (uses .env.worker.debug)
	$(call setup_env, .env.worker.debug)
	@mkdir -p tmp logs
	PATH="$(PATH):$$(go env GOPATH)/bin" air -c .air.worker.toml

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
	sed -e "s;{{DOCKER_REPO}};brojonat/abb-cli;g" \
		-e "s;{{GIT_COMMIT_SHA}};$(GIT_HASH);g" | \
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
	sed -e "s;{{DOCKER_REPO}};brojonat/abb-cli;g" \
		-e "s;{{GIT_COMMIT_SHA}};$(GIT_HASH);g" | \
		kubectl apply -f -
	# No need to patch anymore, the image tag change forces the rollout
	@echo "Worker deployment applied."

# Deploy all components
deploy-all: ## Deploy both server and worker to Kubernetes (prod)
	@$(MAKE) deploy-server
	@$(MAKE) deploy-worker

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
TEMPORAL_FORWARD_CMD := "kubectl port-forward services/temporal-frontend 7233:7233"
SERVER_CMD := $(MAKE) run-http-server-local-air # Command to run the server with Air hot-reload
WORKER_CMD := $(MAKE) run-worker-local-air   # Command to run the worker with Air hot-reload

# Stop existing session (if any) and start a new one
dev-session: stop-dev-session start-dev-session ## Stop (if running) and start a new tmux dev session

# Start the tmux development session
start-dev-session: build-cli ## Start a new tmux development session with port-forward, server, worker, and CLI panes
	@echo "Starting tmux development session: $(TMUX_SESSION)"
	# Create the main session with an initial window named 'dev-main'
	@tmux new-session -d -s $(TMUX_SESSION) -n 'dev-main'

	# Add new, detached windows for the port forwarding commands to run in the background
	@tmux new-window -d -t $(TMUX_SESSION) -n 'TemporalWebForward' "$(PORT_FORWARD_CMD)"
	@tmux new-window -d -t $(TMUX_SESSION) -n 'TemporalFrontendForward' "$(TEMPORAL_FORWARD_CMD)"

	@sleep 1 # Brief pause for session/windows to initialize

	# --- Configure panes in the 'dev-main' window (index 0) ---
	# Pane 0.0 is the initial pane.
	# Split 0.0 vertically. 0.0 becomes top. New pane 0.1 (bottom) runs WORKER_CMD.
	@tmux split-window -v -t $(TMUX_SESSION):0.0 "($(WORKER_CMD)) 2>&1 | tee logs/worker.log"
	# Split 0.0 (top) horizontally. 0.0 becomes top-left. New pane 0.2 (top-right) is created empty (will be CLI).
	@tmux split-window -h -t $(TMUX_SESSION):0.0
	# Split 0.1 (bottom, running WORKER_CMD) horizontally. 0.1 becomes bottom-left. New pane 0.3 (bottom-right) is created empty.
	@tmux split-window -h -t $(TMUX_SESSION):0.1

	# Pane indices before 'select-layout tiled':
	# 0.0: Top-Left (empty, runs CLI)
	# 0.1: Bottom-Right (runs WORKER_CMD)
	# 0.2: Top-Right (runs SERVER_CMD)
	# 0.3: Bottom-Left (empty, runs CLI)

	@tmux select-layout -t $(TMUX_SESSION):0 tiled # Apply tiled layout

	# Send initial commands/messages to the panes (post-tiling)
	# Pane 0.1 (Top-Right): SERVER_CMD
	@tmux send-keys -t $(TMUX_SESSION):0.1 "($(SERVER_CMD)) 2>&1 | tee logs/server.log" C-m
	@tmux send-keys -t $(TMUX_SESSION):0.1 'echo "Server Pane ^ (top-right)"' C-m

	# Pane 0.0 (Top-Left)
	@tmux send-keys -t $(TMUX_SESSION):0.0 'set -o allexport; source .env.worker.debug; set +o allexport; export PATH=$$(pwd)/bin:$$PATH; echo "CLI Pane - .env sourced & ./bin added to PATH (top-left)."' C-m


	# Pane 0.2 (User's Visual Bottom-Left): CLI Setup
	@tmux send-keys -t $(TMUX_SESSION):0.2 'set -o allexport; source .env.server.debug; set +o allexport; export PATH=$$(pwd)/bin:$$PATH; echo "CLI Pane - .env sourced & ./bin added to PATH (bottom-left)."' C-m

	# Pane 0.3 (User's Visual Bottom-Right): Free Pane
	@tmux send-keys -t $(TMUX_SESSION):0.3 'echo "Worker Pane ^ (bottom-right)"' C-m

	# Attach to the session, focusing the CLI pane (0.2 - user's visual Bottom-Left)
	@tmux select-pane -t $(TMUX_SESSION):0.2
	@tmux attach-session -t $(TMUX_SESSION)

# Stop the tmux development session and associated processes
stop-dev-session: ## Stop the tmux development session and kill related processes
	@echo "Stopping tmux development session: $(TMUX_SESSION)"
	@tmux kill-session -t $(TMUX_SESSION) 2>/dev/null && echo "Tmux session stopped." || echo "No tmux session '$(TMUX_SESSION)' found."
	@sleep 1
	@echo "Session cleanup complete. Note: Port-forward processes in background tmux windows will terminate automatically."

# Variables (customize as needed)
ABB_CMD = ./bin/abb
PER_POST_AMOUNT = 0.01
TOTAL_AMOUNT = 0.10
FUND_AMOUNT = $(TOTAL_AMOUNT) # Amount to fund via fund-escrow, assuming it matches total bounty for this script
# Ensure these are set in your environment (e.g., from .env.server.debug or .env.worker.debug)
# Or replace with actual values if not using environment variables for these targets.
ABB_AUTH_TOKEN ?= "your_auth_token_here" # Must be a valid token

# Define requirement strings for bounties
define REDDIT_POST_REQ
This bounty is for a Reddit post. The post must have a score of at least 100 \
and be in the programmerhumor, devops, or programming subreddits \
and it must be implying that LLMs/AI is not particularly useful. \
It should evoke a sense of dissatisfaction with the current state of AI \
and downplay it's effectiveness.
endef
export REDDIT_POST_REQ

define REDDIT_COMMENT_REQ
This bounty is for a Reddit comment. The comment must have a score of at least 100 \
and be in a post talking about hikes in Orange County.
endef
export REDDIT_COMMENT_REQ

define YOUTUBE_REQ
This bounty is for a YouTube video. The video must have at least 1000 views \
and be about cookware. It must not make the cookware look dangerous. \
Video creator must have at least 10k subscribers and the video must have at least 20k views.
endef
export YOUTUBE_REQ

define TWITCH_REQ
This bounty is for a Twitch clip. The content must be about Dota2 and \
have at least 100 views. The thumbnail must include an image of a real person.
endef
export TWITCH_REQ

define BLUESKY_REQ
This bounty is for a Bluesky post. The post should mention technology and \
be about the latest trends in AI.
endef
export BLUESKY_REQ

define HACKERNEWS_COMMENT_REQ
This bounty is for a Hacker News comment. The content must be at \
least 50 characters long. It should be on a Show HN post that discusses IncentivizeThis. \
The comment must portray IncentivizeThis in a positive light, \
otherwise it should not be approved for the bounty.
endef
export HACKERNEWS_COMMENT_REQ

define HACKERNEWS_POST_REQ
This is a multi-line requirement string. \
This is the second line of the requirement. \
And this is the third line.
endef
export HACKERNEWS_POST_REQ

define INSTAGRAM_POST_REQ
The deliverable is an Instagram **post**. \
This Instagram post must feature a video about Kean Coffee in Irvine, CA. \
The video component of the post must be between 15 and 60 seconds long. \
The post's caption must use the hashtag #NewportBeach or #Tustin and tag the coffee shop @KeanCoffee. \
The post must receive at least 100 views and 10 likes.
endef
export INSTAGRAM_POST_REQ

create-reddit-post-bounty: build-cli ## Create a test Reddit Post bounty
	$(call setup_env, .env.server.debug)
	@echo "--- Creating a test Reddit Post Bounty ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "$(REDDIT_POST_REQ)" \
		--per-post "$(PER_POST_AMOUNT)" \
		--total "$(TOTAL_AMOUNT)" \
		--duration "24h"`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.bounty_id'`; \
	echo "Extracted Workflow ID: '$$WORKFLOW_ID'"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ] || ! echo "$$WORKFLOW_ID" | grep -q '^bounty-'; then \
		echo "Error: Could not extract a valid Workflow/Bounty ID (must start with 'bounty-') from create command output." >&2; \
		echo "Output was: $$OUTPUT" >&2; \
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
	@echo "--- Test Reddit Post Bounty created and funding attempted ---"

create-reddit-comment-bounty: build-cli ## Create a test Reddit Comment bounty
	$(call setup_env, .env.server.debug)
	@echo "--- Creating a test Reddit Comment Bounty ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "$(REDDIT_COMMENT_REQ)" \
		--per-post "$(PER_POST_AMOUNT)" \
		--total "$(TOTAL_AMOUNT)" \
		--duration "24h"`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.bounty_id'`; \
	echo "Extracted Workflow ID: '$$WORKFLOW_ID'"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ] || ! echo "$$WORKFLOW_ID" | grep -q '^bounty-'; then \
		echo "Error: Could not extract a valid Workflow/Bounty ID (must start with 'bounty-') from create command output." >&2; \
		echo "Output was: $$OUTPUT" >&2; \
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
	@echo "--- Test Reddit Comment Bounty created and funding attempted ---"

create-youtube-bounty: build-cli ## Create a test YouTube bounty
	$(call setup_env, .env.server.debug)
	@echo "--- Creating a test YouTube Bounty ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "$(YOUTUBE_REQ)" \
		--per-post "$(PER_POST_AMOUNT)" \
		--total "$(TOTAL_AMOUNT)" \
		--duration "24h"`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.bounty_id'`; \
	echo "Extracted Workflow ID: '$$WORKFLOW_ID'"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ] || ! echo "$$WORKFLOW_ID" | grep -q '^bounty-'; then \
		echo "Error: Could not extract a valid Workflow/Bounty ID (must start with 'bounty-') from create command output." >&2; \
		echo "Output was: $$OUTPUT" >&2; \
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
	@echo "--- Test YouTube Bounty created and funding attempted ---"

create-twitch-bounty: build-cli ## Create a test Twitch bounty
	$(call setup_env, .env.server.debug)
	@echo "--- Creating a test Twitch Bounty ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "$(TWITCH_REQ)" \
		--per-post "$(PER_POST_AMOUNT)" \
		--total "$(TOTAL_AMOUNT)" \
		--duration "24h"`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.bounty_id'`; \
	echo "Extracted Workflow ID: '$$WORKFLOW_ID'"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ] || ! echo "$$WORKFLOW_ID" | grep -q '^bounty-'; then \
		echo "Error: Could not extract a valid Workflow/Bounty ID (must start with 'bounty-') from create command output." >&2; \
		echo "Output was: $$OUTPUT" >&2; \
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
	@echo "--- Test Twitch Bounty created and funding attempted ---"

create-bluesky-bounty: build-cli ## Create a test Bluesky bounty
	$(call setup_env, .env.server.debug)
	@echo "--- Creating a test Bluesky Bounty ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "$(BLUESKY_REQ)" \
		--per-post "$(PER_POST_AMOUNT)" \
		--total "$(TOTAL_AMOUNT)" \
		--duration "24h"`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.bounty_id'`; \
	echo "Extracted Workflow ID: '$$WORKFLOW_ID'"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ] || ! echo "$$WORKFLOW_ID" | grep -q '^bounty-'; then \
		echo "Error: Could not extract a valid Workflow/Bounty ID (must start with 'bounty-') from create command output." >&2; \
		echo "Output was: $$OUTPUT" >&2; \
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
	@echo "--- Test Bluesky Bounty created and funding attempted ---"

create-hackernews-comment-bounty: build-cli ## Create a test Hacker News Comment bounty
	$(call setup_env, .env.server.debug)
	@echo "--- Creating a test Hacker News Comment Bounty ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "$(HACKERNEWS_COMMENT_REQ)" \
		--per-post "$(PER_POST_AMOUNT)" \
		--total "$(TOTAL_AMOUNT)" \
		--duration "24h"`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.bounty_id'`; \
	echo "Extracted Workflow ID: '$$WORKFLOW_ID'"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ] || ! echo "$$WORKFLOW_ID" | grep -q '^bounty-'; then \
		echo "Error: Could not extract a valid Workflow/Bounty ID (must start with 'bounty-') from create command output." >&2; \
		echo "Output was: $$OUTPUT" >&2; \
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
	@echo "--- Test Hacker News Comment Bounty created and funding attempted ---"

create-hackernews-post-bounty: build-cli ## Create a simple test Hacker News post bounty
	$(call setup_env, .env.server.debug)
	@echo "--- Creating a test Hacker News Post Bounty ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "$(HACKERNEWS_POST_REQ)" \
		--per-post $(PER_POST_AMOUNT) \
		--total $(TOTAL_AMOUNT) \
		--duration "24h"`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.bounty_id'`; \
	echo "Extracted Workflow ID: '$$WORKFLOW_ID'"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ] || ! echo "$$WORKFLOW_ID" | grep -q '^bounty-'; then \
		echo "Error: Could not extract a valid Workflow/Bounty ID (must start with 'bounty-') from create command output." >&2; \
		echo "Output was: $$OUTPUT" >&2; \
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
	@echo "--- Test Hacker News Post Bounty created and funding attempted ---"

create-instagram-post-bounty: build-cli ## Create a test Instagram Post bounty for Kean Coffee
	$(call setup_env, .env.server.debug)
	@echo "--- Creating a test Instagram Post Bounty (Kean Coffee) ---"
	@OUTPUT=`$(ABB_CMD) admin bounty create \
		-r "$(INSTAGRAM_POST_REQ)" \
		--per-post "$(PER_POST_AMOUNT)" \
		--total "$(TOTAL_AMOUNT)" \
		--duration "24h"`; \
	echo "Create Output: $$OUTPUT"; \
	WORKFLOW_ID=`echo $$OUTPUT | jq -r '.body.bounty_id'`; \
	echo "Extracted Workflow ID: '$$WORKFLOW_ID'"; \
	if [ -z "$$WORKFLOW_ID" ] || [ "$$WORKFLOW_ID" = "null" ] || ! echo "$$WORKFLOW_ID" | grep -q '^bounty-'; then \
		echo "Error: Could not extract a valid Workflow/Bounty ID (must start with 'bounty-') from create command output." >&2; \
		echo "Output was: $$OUTPUT" >&2; \
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
	@echo "--- Test Instagram Post Bounty created and funding attempted ---"

.PHONY: help create-and-fund-bounties create-reddit-bounty create-youtube-bounty create-twitch-bounty create-bluesky-bounty create-hackernews-bounty

help:
	@echo "Available targets:"
	@awk -F ':.*?## ' '/^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort

CONCURRENT_BOUNTY_TARGETS := \
    create-reddit-post-bounty \
    create-youtube-bounty \
    create-twitch-bounty \
    create-bluesky-bounty \
    create-hackernews-comment-bounty \
    create-instagram-post-bounty

# Target to create and fund one bounty for each platform concurrently
create-and-fund-bounties: ## Create and fund example bounties for all platforms (concurrently)
	@mkdir -p logs
	echo "Starting concurrent creation and funding of platform bounties..."
	@pids=""; \
	declare -A pid_to_target; \
	for target_name in $(CONCURRENT_BOUNTY_TARGETS); do \
		echo "Starting $$target_name... Log file: logs/$${target_name}.log"; \
		$(MAKE) $$target_name > logs/$${target_name}.log 2>&1 & \
		pid=$$!; \
		pids="$$pids $$pid"; \
		pid_to_target[$$pid]=$$target_name; \
	done; \
	echo "Waiting for all bounty creation jobs (PIDs:$$pids) to complete..."; \
	any_failed=false; \
	for pid in $$pids; do \
		if ! wait $$pid; then \
			target_name=$${pid_to_target[$$pid]}; \
			echo "!!! ERROR: The '$$target_name' job (PID: $$pid) failed. Check logs/$$target_name.log for details." >&2; \
			any_failed=true; \
		fi; \
	done; \
	if $$any_failed; then \
		echo "One or more bounty creation jobs failed." >&2; \
		exit 1; \
	else \
		echo "All platform bounty creation and funding processes completed successfully."; \
	fi

create-from-yaml: build-cli ## Create and fund all bounties from a given YAML file (e.g., make create-from-yaml YAML_FILE=bounties_bootstrap.prod.yaml)
	$(call setup_env, .env.server.debug)
	@if [ -z "$(YAML_FILE)" ]; then \
		echo "Error: YAML_FILE is not set. Usage: make create-from-yaml YAML_FILE=<path_to_yaml>"; \
		exit 1; \
	fi
	@echo "--- Creating bounties from $(YAML_FILE) ---"
	@CREATE_OUTPUT=`$(ABB_CMD) admin bounty create --file "$(YAML_FILE)"`; \
	echo "Create Output: $$CREATE_OUTPUT"; \
	BOUNTY_DATA=`echo "$$CREATE_OUTPUT" | jq -c '.body[] | {workflow_id: .bounty_id, fund_amount: .total_bounty}'`; \
	if [ -z "$$BOUNTY_DATA" ]; then \
		echo "Error: Could not parse bounty data from create command output." >&2; \
		exit 1; \
	fi; \
	@echo "--- Starting concurrent funding for created bounties ---"; \
	pids=""; \
	echo "$$BOUNTY_DATA" | while IFS= read -r bounty; do \
		WORKFLOW_ID=`echo "$$bounty" | jq -r '.workflow_id'`; \
		FUND_AMOUNT=`echo "$$bounty" | jq -r '.fund_amount'`; \
		if [ -n "$$WORKFLOW_ID" ] && [ -n "$$FUND_AMOUNT" ] && [ "$$WORKFLOW_ID" != "null" ]; then \
			echo "Funding bounty $$WORKFLOW_ID with $$FUND_AMOUNT..."; \
			($(ABB_CMD) admin util fund-escrow --workflow-id "$$WORKFLOW_ID" --amount $$FUND_AMOUNT) & \
			pids="$$pids $$!"; \
		fi; \
	done; \
	@echo "Waiting for all funding jobs (PIDs:$$pids) to complete..."; \
	for pid in $$pids; do \
		wait $$pid || echo "A funding job (PID: $$pid) may have failed."; \
	done; \
	@echo "--- All bounties from $(YAML_FILE) created and funding attempted ---"

migrate:
	$(call setup_env, .env.server.prod)
	migrate -database ${ABB_DATABASE_URL} -path db/migrations up

# Prompt/env generation
# ---------------------
.PHONY: generate-server-prompt-envs generate-worker-prompt-envs

# This target reads prompts from prompts.server.yaml, base64 encodes them,
# and prints the resulting environment variables to standard output for the server.
# It requires `yq` to be installed (`pip install yq`).
generate-server-prompt-envs: ## Generate base64-encoded prompt env vars for the server
	@if ! command -v yq &> /dev/null; then \
		echo "yq is not installed. Please install it to continue: pip install yq"; \
		exit 1; \
	fi
	@yq -r 'to_entries | .[] | "LLM_PROMPT_" + (.key | ascii_upcase | gsub("-";"_")) + "_B64=" + (.value | @base64)' prompts.server.yaml

# This target reads prompts from prompts.worker.yaml, base64 encodes them,
# and prints the resulting environment variables to standard output for the worker.
# It requires `yq` to be installed (`pip install yq`).
generate-worker-prompt-envs: ## Generate base64-encoded prompt env vars for the worker
	@if ! command -v yq &> /dev/null; then \
		echo "yq is not installed. Please install it to continue: pip install yq"; \
		exit 1; \
	fi
	@yq -r 'to_entries | .[] | "LLM_PROMPT_" + (.key | ascii_upcase | gsub("-";"_")) + "_B64=" + (.value | @base64)' prompts.worker.yaml

# This seeds the prod database with some bounties
seed-prod: ## Seed the prod database with some bounties
	abb admin bounty bootstrap -f bounties_bootstrap.prod.yaml -n positive-comment
	abb admin bounty bootstrap -f bounties_bootstrap.prod.yaml -n photoshop-request
	abb admin bounty bootstrap -f bounties_bootstrap.prod.yaml -n reddit-vpn-comment
	abb admin bounty bootstrap -f bounties_bootstrap.prod.yaml -n reddit-heat-pump-comment
	abb admin bounty bootstrap -f bounties_bootstrap.prod.yaml -n tripadvisor-review
	abb admin bounty bootstrap -f bounties_bootstrap.prod.yaml -n incentivize-this-reddit-comment
	abb admin bounty bootstrap -f bounties_bootstrap.prod.yaml -n incentivize-this-bluesky-post
	abb admin bounty bootstrap -f bounties_bootstrap.prod.yaml -n incentivize-this-hackernews-post
	abb admin bounty bootstrap -f bounties_bootstrap.prod.yaml -n incentivize-this-hackernews-comment
