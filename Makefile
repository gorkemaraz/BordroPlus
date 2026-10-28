# BordroPlus developer commands. Requires Docker (compose v2), Go and Node for local work.
COMPOSE ?= docker compose

.PHONY: help up down logs ps build test test-backend test-frontend lint cover clean

help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-16s %s\n", $$1, $$2}'

.env:
	cp .env.example .env

up: .env ## Build and start the whole stack
	$(COMPOSE) up --build -d --wait

down: ## Stop the stack (keeps data)
	$(COMPOSE) down

logs: ## Follow logs
	$(COMPOSE) logs -f

ps: ## Show service status
	$(COMPOSE) ps

build: ## Build all images
	$(COMPOSE) build

test: test-backend test-frontend ## Run all unit tests

test-backend: ## Go tests with race detector
	cd backend && go test -race -count=1 ./...

test-frontend: ## Frontend typecheck
	cd frontend && npm run typecheck

cover: ## Go coverage report
	cd backend && go test -count=1 -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

lint: ## go vet
	cd backend && go vet ./...

clean: ## Stop the stack and delete volumes
	$(COMPOSE) down -v --remove-orphans
