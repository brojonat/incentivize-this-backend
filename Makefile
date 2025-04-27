#!/usr/bin/env bash

# Set the shell for make explicitly
SHELL := /bin/bash

define setup_env
	$(eval ENV_FILE := $(1))
	$(eval include $(1))
	$(eval export)
endef

# Core test targets
test:
	go test -v ./...

test-workflow:
	go test -v ./... -run "Test.*Workflow"

test-solana:
	go test -v ./solana/...

test-abb:
	go test -v ./abb/...

test-rbb:
	go test -v ./rbb/...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

test-coverage-summary:
	go test -cover ./...

# CI integration
test-ci:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

build-cli:
	go build -o ./bin/abb cmd/abb/*.go

build-push-cli:
	$(call setup_env, .env.server.prod)
	docker build -f Dockerfile -t ${CLI_IMG_TAG} .
	docker push ${CLI_IMG_TAG}

refresh-token-debug:
	$(call setup_env, .env.server.debug)
	@$(MAKE) build-cli
	./bin/abb admin auth get-token --email ${ADMIN_EMAIL} --env-file .env.server.debug

run-http-server-local:
	$(call setup_env, .env.server.debug)
	@if ! pgrep -f "kubectl port-forward.*temporal-frontend" > /dev/null; then \
		kubectl port-forward services/temporal-frontend 7233:7233 & \
		sleep 2; \
	fi
	@$(MAKE) build-cli
	./bin/abb run http-server --temporal-address ${TEMPORAL_ADDRESS} --temporal-namespace ${TEMPORAL_NAMESPACE}

run-worker-local:
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
deploy-server:
	$(call setup_env, .env.server.prod)
	@$(MAKE) build-push-cli
	kustomize build --load-restrictor=LoadRestrictionsNone server/k8s/prod | \
	sed -e "s;{{DOCKER_REPO}};$(DOCKER_REPO);g" \
		-e "s;{{CLI_IMG_TAG}};$(CLI_IMG_TAG);g" | \
		kubectl apply -f -
	kubectl rollout restart deployment affiliate-bounty-board-backend

# Deploy worker component
deploy-worker:
	$(call setup_env, .env.worker.prod)
	@$(MAKE) build-push-cli
	kustomize build --load-restrictor=LoadRestrictionsNone worker/k8s/prod | \
	sed -e "s;{{DOCKER_REPO}};$(DOCKER_REPO);g" \
		-e "s;{{CLI_IMG_TAG}};$(CLI_IMG_TAG);g" | \
		kubectl apply -f -
	kubectl rollout restart deployment affiliate-bounty-board-workers

# Deploy all components
deploy-all: deploy-server deploy-worker

# Delete server component
delete-server:
	kubectl delete -f server/k8s/prod/ingress.yaml || true
	kubectl delete -f server/k8s/prod/server.yaml || true
	kubectl delete secret abb-secret-server-envs || true

# Delete worker component
delete-worker:
	kubectl delete -f worker/k8s/prod/worker.yaml || true
	kubectl delete secret affiliate-bounty-board-secret-worker-envs || true

# Delete all components
delete-all: delete-server delete-worker

# View logs
.PHONY: logs-server logs-worker

logs-server:
	kubectl logs -f deployment/affiliate-bounty-board-backend

logs-worker:
	kubectl logs -f deployment/affiliate-bounty-board-workers

# Port forwarding for local development
.PHONY: port-forward-server

port-forward-server:
	kubectl port-forward svc/affiliate-bounty-board-backend 8080:80

# Check deployment status
.PHONY: status

status:
	@echo "=== Server Status ==="
	kubectl get deployment affiliate-bounty-board-backend -o wide
	@echo "\n=== Worker Status ==="
	kubectl get deployment affiliate-bounty-board-workers -o wide
	@echo "\n=== Pods Status ==="
	kubectl get pods -l app=affiliate-bounty-board

# Update secrets
.PHONY: update-secrets-server update-secrets-worker

update-secrets-server:
	kubectl create secret generic abb-secret-server-envs \
		--from-env-file=.env.server.prod \
		--dry-run=client -o yaml | kubectl apply -f -

update-secrets-worker:
	kubectl create secret generic affiliate-bounty-board-secret-worker-envs \
		--from-env-file=.env.worker.prod \
		--dry-run=client -o yaml | kubectl apply -f -

# Restart deployments
.PHONY: restart-server restart-worker

restart-server:
	kubectl rollout restart deployment affiliate-bounty-board-backend

restart-worker:
	kubectl rollout restart deployment affiliate-bounty-board-workers

# Describe resources
.PHONY: describe-server describe-worker

describe-server:
	kubectl describe deployment affiliate-bounty-board-backend
	kubectl describe service affiliate-bounty-board-backend
	kubectl describe ingress affiliate-bounty-board-backend-ingress

describe-worker:
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
dev-session: stop-dev-session start-dev-session

# Start the tmux development session
start-dev-session: build-cli # Ensure CLI is built
	@echo "Starting tmux development session: $(TMUX_SESSION)"
	# Create session in detached mode with port-forward. Ignore error if session already exists.
	@/usr/local/bin/tmux new-session -d -s $(TMUX_SESSION) -n 'DevEnv' "$(PORT_FORWARD_CMD)" || true
	# Add a brief pause to allow the server to initialize
	@sleep 0.5
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
stop-dev-session:
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