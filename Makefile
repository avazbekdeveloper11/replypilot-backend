.PHONY: run run-worker-ai run-token-refresh build build-migrate build-worker-ai build-token-refresh test lint tidy swagger \
	migrate-up migrate-down migrate-down-all migrate-version migrate-force migrate-create \
	docker-up docker-down

APP_NAME := replypilot-api
MAIN := ./cmd/api
MIGRATE_MAIN := ./cmd/migrate
WORKER_AI_MAIN := ./cmd/worker-ai
TOKEN_REFRESH_MAIN := ./cmd/token-refresh
MIGRATIONS_DIR := internal/migrations

run:
	go run $(MAIN)

# Runs the AI reply pipeline worker (see cmd/worker-ai's doc comment). Run
# this in a second terminal alongside `make run` — the API alone ingests
# DMs but never replies to them.
run-worker-ai:
	go run $(WORKER_AI_MAIN)

# Runs one pass of the token-refresh job (see cmd/token-refresh's doc
# comment) and exits. Not a long-running process — in production this is
# invoked by an external scheduler (cron / a Kubernetes CronJob), not
# `make run`-style.
run-token-refresh:
	go run $(TOKEN_REFRESH_MAIN)

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/$(APP_NAME) $(MAIN)

build-migrate:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/replypilot-migrate $(MIGRATE_MAIN)

build-worker-ai:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/replypilot-worker-ai $(WORKER_AI_MAIN)

build-token-refresh:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/replypilot-token-refresh $(TOKEN_REFRESH_MAIN)

test:
	go test ./... -race -cover

lint:
	go vet ./...
	@which golangci-lint > /dev/null || (echo "install golangci-lint: https://golangci-lint.run/usage/install/" && exit 1)
	golangci-lint run

# go.sum was intentionally not committed (see README "Known limitations") —
# run this once after cloning, before anything else.
tidy:
	go mod tidy

# Regenerates docs/docs.go + docs/swagger.json + docs/swagger.yaml from the
# @Summary/@Param annotations in internal/delivery/http/v1, using the
# @title/@BasePath block in internal/delivery/http/router.go as the entry
# point. Overwrites the placeholder docs/docs.go committed in this repo.
swagger:
	@which swag > /dev/null || go install github.com/swaggo/swag/cmd/swag@latest
	swag init -g internal/delivery/http/router.go -o docs

# --- Migrations (cmd/migrate, embedded SQL in internal/migrations) ----------
# These run the compiled migrate tool, which reads DB connection details from
# the same env/config as the API (.env locally). No separate DATABASE_URL and
# no direct psql against schema.sql — the versioned migrations are the single
# source of truth for schema now.

migrate-up:
	go run $(MIGRATE_MAIN) up

migrate-down:
	go run $(MIGRATE_MAIN) down

migrate-down-all:
	go run $(MIGRATE_MAIN) down-all

migrate-version:
	go run $(MIGRATE_MAIN) version

# Recovery only: mark a version as current after a crash left the DB "dirty".
# Verify the database state by hand first. Usage: make migrate-force V=2
migrate-force:
	go run $(MIGRATE_MAIN) force $(V)

# Scaffolds an empty up/down pair for the NEXT schema change. Usage:
#   make migrate-create NAME=add_billing_webhook_table
# Requires the golang-migrate CLI (github.com/golang-migrate/migrate/v4/cmd/migrate).
migrate-create:
	@which migrate > /dev/null || (echo "install: go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest" && exit 1)
	migrate create -ext sql -dir $(MIGRATIONS_DIR) -seq $(NAME)

docker-up:
	docker compose up --build

docker-down:
	docker compose down -v
