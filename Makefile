.PHONY: help build test clean up down logs migrate proto

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

proto: ## Generate Go code from proto files (requires protoc, protoc-gen-go, protoc-gen-go-grpc)
	@mkdir -p gen
	PATH="$$PATH:$$(go env GOPATH)/bin" protoc --go_out=. --go_opt=module=github.com/snappy-loop/stories \
		--go-grpc_out=. --go-grpc_opt=module=github.com/snappy-loop/stories \
		proto/segmentation/v1/segmentation.proto \
		proto/audio/v1/audio.proto \
		proto/image/v1/image.proto
	@echo "Proto code generated in gen/"

build: ## Build all binaries
	@echo "Building binaries..."
	@mkdir -p bin
	CGO_ENABLED=0 go build -o bin/stories-api ./cmd/api
	CGO_ENABLED=0 go build -o bin/stories-worker ./cmd/worker
	CGO_ENABLED=0 go build -o bin/stories-dispatcher ./cmd/dispatcher
	CGO_ENABLED=0 go build -o bin/stories-agents ./cmd/agents
	@echo "Done!"

test: ## Run tests
	go test -v -race ./...

test-coverage: ## Run tests with coverage
	go test -v -race -coverprofile=coverage.txt -covermode=atomic ./...
	go tool cover -html=coverage.txt -o coverage.html

clean: ## Clean build artifacts
	rm -rf bin/
	rm -f coverage.txt coverage.html

up: ## Start all services with docker-compose
	docker-compose up -d

down: ## Stop all services
	docker-compose down

logs: ## Show logs from all services
	docker-compose logs -f

migrate: ## Run database migrations
	docker-compose exec api ./stories-api migrate

psql: ## Connect to postgres
	docker-compose exec postgres psql -U stories -d stories

kafka-topics: ## List Kafka topics
	docker-compose exec kafka rpk topic list

minio-console: ## Open MinIO console
	@echo "MinIO Console: http://localhost:9001"
	@echo "Username: minioadmin"
	@echo "Password: minioadmin"

dev-api: ## Run API server locally
	go run ./cmd/api

dev-worker: ## Run worker locally
	go run ./cmd/worker

dev-dispatcher: ## Run dispatcher locally
	go run ./cmd/dispatcher

lint: ## Run linters
	golangci-lint run ./...

fmt: ## Format code
	go fmt ./...
	goimports -w .

tidy: ## Tidy go.mod
	go mod tidy

docker-build: ## Build docker image
	docker build -t stories:latest .

.DEFAULT_GOAL := help
