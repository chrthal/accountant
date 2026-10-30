# Container runtime is auto-detected so neither the developer nor CI has to
# care whether docker or podman is installed (ADR 0005).
CONTAINER ?= $(shell command -v docker 2>/dev/null || command -v podman 2>/dev/null)
COMPOSE   ?= $(CONTAINER) compose
IMAGE     ?= ghcr.io/chrthal/accountant
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)

# Host port for the app. Change it when 8080 is taken, e.g. by a `make dev`
# running beside `make up`:  make up PORT=8081
PORT      ?= 8080
PROJECT   ?= $(or $(COMPOSE_PROJECT_NAME),$(notdir $(CURDIR)))
export BOOKKEEPER_PORT := $(PORT)

# Postgres is the primary engine (ADR 0006); these are for `make test-postgres`
# and `make dev-postgres`, which start a throwaway server in a container.
PGPORT     ?= 55432
PG_DSN     ?= postgres://bookkeeper:bookkeeper@localhost:$(PGPORT)/bookkeeper?sslmode=disable
PG_IMAGE   ?= docker.io/library/postgres:17-alpine

.PHONY: up-llm llm-pull backup backup-compose help dev dev-postgres build test test-postgres test-container image run-image \
        up up-sqlite down logs clean fmt pg-up pg-down

help: ## Show this help
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  %-15s %s\n", $$1, $$2}'

dev: ## Run locally on SQLite (fastest inner loop, no container, no server)
	go run -ldflags "$(LDFLAGS)" ./cmd/bookkeeper -addr :$(PORT) -dsn sqlite://bookkeeper.db

dev-postgres: pg-up ## Run locally against a throwaway Postgres container
	go run -ldflags "$(LDFLAGS)" ./cmd/bookkeeper -addr :$(PORT) -dsn "$(PG_DSN)"

pg-up: ## Start the throwaway Postgres used by dev-postgres / test-postgres
	@$(CONTAINER) inspect bookkeeper-pg >/dev/null 2>&1 || \
	  $(CONTAINER) run -d --rm --name bookkeeper-pg \
	    -e POSTGRES_USER=bookkeeper -e POSTGRES_PASSWORD=bookkeeper -e POSTGRES_DB=bookkeeper \
	    -p $(PGPORT):5432 $(PG_IMAGE) >/dev/null
	@for i in $$(seq 1 30); do \
	  $(CONTAINER) exec bookkeeper-pg pg_isready -U bookkeeper >/dev/null 2>&1 && break; \
	  sleep 1; \
	done

pg-down: ## Stop the throwaway Postgres
	-$(CONTAINER) rm -f -v bookkeeper-pg

build: ## Build the binary into ./dist
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/bookkeeper ./cmd/bookkeeper

fmt: ## Format and tidy
	go fmt ./...
	go mod tidy

test: ## Run vet + tests on the host (SQLite only; Postgres cases skip)
	go vet ./...
	go test ./...

test-postgres: pg-up ## Run the full suite against both engines, like CI does
	TEST_POSTGRES_DSN="$(PG_DSN)" go test ./...
	@$(MAKE) pg-down

test-container: ## Run vet + tests inside the builder image (same as CI)
	$(COMPOSE) -f compose.yaml -f compose.test.yaml run --rm --build test

image: ## Build the container image
	$(CONTAINER) build --build-arg VERSION=$(VERSION) -t $(IMAGE):dev .

run-image: image ## Run the built image directly, no compose (podman fallback)
	$(CONTAINER) run --rm -p $(PORT):8080 -v bookkeeper-data:/data -e TZ=Europe/Vienna $(IMAGE):dev

up: ## Start app + Postgres via compose (the production-shaped path)
	$(COMPOSE) up --build -d
	@echo "http://localhost:$(PORT)"

up-sqlite: ## Start the app on SQLite via compose, no database server
	# --no-deps is what actually keeps Postgres from starting: an override
	# cannot clear compose.yaml's depends_on list.
	$(COMPOSE) -f compose.yaml -f compose.sqlite.yaml up --build -d --no-deps app
	@echo "http://localhost:$(PORT)"

up-llm: ## Start app + Postgres + the local model for booking suggestions (ADR 0020)
	BOOKKEEPER_LLM_URL=http://ollama:11434 $(COMPOSE) --profile llm up --build -d
	@echo "http://localhost:$(PORT)  (first time: make llm-pull)"

llm-pull: ## Download the suggestion model into the ollama volume (~2 GB, once)
	$(COMPOSE) --profile llm exec ollama ollama pull $${BOOKKEEPER_LLM_MODEL:-qwen2.5:3b}

down: ## Stop the app (named volume is kept)
	$(COMPOSE) --profile llm down

logs: ## Follow app logs
	$(COMPOSE) logs -f app

backup: ## Back up the dev database (bookkeeper.db) into ./backups, verified
	@mkdir -p backups
	go run ./cmd/bookkeeper backup -dsn sqlite://bookkeeper.db -o backups

backup-compose: ## Back up the compose app's database into ./backups, verified
	@mkdir -p backups
	$(COMPOSE) exec -T app bookkeeper backup -o - > backups/bookkeeper-compose-$$(date +%Y-%m-%d-%H%M%S).backup.gz
	go run ./cmd/bookkeeper verify "$$(ls -t backups/bookkeeper-compose-*.backup.gz | head -1)"

# Not `down -v`: that would also delete the backups volume, the one thing that
# should outlive a wiped database.
clean: ## Stop and delete the database volumes — destroys the local books, keeps backups
	$(COMPOSE) --profile llm down
	-$(CONTAINER) volume rm $(PROJECT)_bookkeeper-db $(PROJECT)_bookkeeper-data
	rm -rf dist bookkeeper.db bookkeeper.db-shm bookkeeper.db-wal
