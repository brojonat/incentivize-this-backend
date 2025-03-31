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

deploy-server:
	$(call setup_env, .env.server.prod)
	@$(MAKE) build-push-cli
	kustomize build --load-restrictor=LoadRestrictionsNone service/k8s/prod | \
	sed -e "s;{{DOCKER_REPO}};$(DOCKER_REPO);g" | \
	sed -e "s;{{CLI_IMG_TAG}};$(CLI_IMG_TAG);g" | \
	kubectl apply -f -
	kubectl rollout restart deployment reddit-bounty-board

deploy-worker:
	$(call setup_env, .env.worker.prod)
	@$(MAKE) build-push-cli
	kustomize build --load-restrictor=LoadRestrictionsNone worker/k8s/prod | \
	sed -e "s;{{DOCKER_REPO}};$(DOCKER_REPO);g" | \
	sed -e "s;{{CLI_IMG_TAG}};$(CLI_IMG_TAG);g" | \
	kubectl apply -f -
	kubectl rollout restart deployment reddit-bounty-board-worker

deploy-all:
	@$(MAKE) deploy-server
	@$(MAKE) deploy-worker