# BordroPlus developer commands. Requires Docker (compose v2), Go and Node for local work.
COMPOSE ?= docker compose

.PHONY: help up down logs ps build test test-backend test-frontend lint cover clean testdb-up testdb-down

# Integration tests run against a throwaway Postgres (see testdb-up). Unset
# TEST_DATABASE_URL to run unit tests only; DB tests are then skipped.
TEST_DATABASE_URL ?= postgres://test:test@127.0.0.1:55432/test?sslmode=disable
export TEST_DATABASE_URL

# The race detector needs cgo (a C compiler), which Windows machines often lack.
ifeq ($(OS),Windows_NT)
RACE ?=
else
RACE ?= -race
endif

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

test-backend: ## Go tests (race detector off on Windows)
	cd backend && go test $(RACE) -count=1 ./...

test-frontend: ## Frontend typecheck
	cd frontend && npm run typecheck

cover: ## Go coverage report
	cd backend && go test -count=1 -coverpkg=./internal/... -coverprofile=coverage.out ./... && go tool cover -func=coverage.out

lint: ## go vet
	cd backend && go vet ./...

testdb-up: ## Start a throwaway Postgres for integration tests on 127.0.0.1:55432
	docker run -d --rm --name bordroplus-testdb -e POSTGRES_USER=test -e POSTGRES_PASSWORD=test -e POSTGRES_DB=test -p 127.0.0.1:55432:5432 postgres:16-alpine

testdb-down: ## Stop the test Postgres
	docker rm -f bordroplus-testdb

clean: ## Stop the stack and delete volumes
	$(COMPOSE) down -v --remove-orphans
