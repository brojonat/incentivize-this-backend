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
	go build -o ./bin/rbb cmd/rbb/*.go

build-push-cli:
	$(call setup_env, .env.server.prod)
	CGO_ENABLED=0 GOOS=linux go build -o ./bin/rbb cmd/rbb/*.go
	docker build -f Dockerfile -t ${CLI_IMG_TAG} .
	docker push ${CLI_IMG_TAG}

refresh-token:
	$(call setup_env, .env.server.debug)
	@$(MAKE) build-cli
	./bin/rbb admin auth get-token --email ${ADMIN_EMAIL} --env-file .env.server.debug

run-http-server-local:
	$(call setup_env, .env.server.debug)
	@if ! pgrep -f "kubectl port-forward.*temporal-frontend" > /dev/null; then \
		kubectl port-forward services/temporal-frontend 7233:7233 & \
		sleep 2; \
	fi
	@$(MAKE) build-cli
	./bin/rbb run http-server --temporal-address localhost:7233 --temporal-namespace default

run-worker-local:
	$(call setup_env, .env.worker.debug)
	@if ! pgrep -f "kubectl port-forward.*temporal-frontend" > /dev/null; then \
		kubectl port-forward services/temporal-frontend 7233:7233 & \
		sleep 2; \
	fi
	@$(MAKE) build-cli
	./bin/rbb run worker --temporal-address localhost:7233 --temporal-namespace default

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
	kustomize build --load-restrictor=LoadRestrictionsNone worker/k8s | \
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
	kubectl delete -f worker/k8s/worker.yaml || true
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
		--from-env-file=server/.env.server.prod \
		--dry-run=client -o yaml | kubectl apply -f -

update-secrets-worker:
	kubectl create secret generic affiliate-bounty-board-secret-worker-envs \
		--from-env-file=worker/.env.prod \
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