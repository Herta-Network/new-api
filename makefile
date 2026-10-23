WEB_DIR = ./web
API_DIR = .
DEV_WEB_PORT ?= 5173
DEV_COMPOSE_FILE = docker-compose.dev.yml
DEV_POSTGRES_SERVICE = postgres
DEV_API_SERVICE = new-api
DEV_POSTGRES_DB = new-api
DEV_POSTGRES_USER = root
TEST_DB_CONTAINER = new-api-test-postgres
TEST_DB_PORT ?= 55432

.PHONY: all build-web build-all-web start-api dev dev-api dev-api-rebuild dev-web reset-setup test test-db migration

all: build-all-web start-api

build-web:
	@echo "Building web frontend..."
	@cd $(WEB_DIR) && bun install --frozen-lockfile
	@cd $(WEB_DIR) && DISABLE_ESLINT_PLUGIN='true' VITE_REACT_APP_VERSION=$$(cat ../VERSION) bun run build

build-all-web: build-web

start-api:
	@echo "Starting api dev server..."
	@cd $(API_DIR) && go run main.go &

dev-api:
	@echo "Starting api services (docker)..."
	@docker compose -f $(DEV_COMPOSE_FILE) up -d

dev-api-rebuild:
	@echo "Rebuilding and starting api service (docker)..."
	@docker compose -f $(DEV_COMPOSE_FILE) up -d --build $(DEV_API_SERVICE)

dev-web:
	@echo "Starting web frontend dev server..."
	@echo "Web frontend: http://localhost:$(DEV_WEB_PORT)"
	@cd $(WEB_DIR) && bun install
	@cd $(WEB_DIR) && bun run dev -- --host 0.0.0.0 --port $(DEV_WEB_PORT)

dev: dev-api dev-web

# The main package embeds the ignored web/dist output and is covered after build-web.
# Database tests need a PostgreSQL 18 server: run `make test-db` and export the
# TEST_POSTGRES_DSN it prints, or point it at any UTF8 database you can create
# schemas in.
test:
	@if [ -z "$$TEST_POSTGRES_DSN" ]; then \
		echo "TEST_POSTGRES_DSN is not set. Run 'make test-db' and export the DSN it prints."; \
		exit 1; \
	fi
	@echo "Testing root Go module..."
	@root_module=$$(GOWORK=off go list -m); \
		root_packages=$$(GOWORK=off go list -e ./... | grep -vxF "$$root_module"); \
		GOWORK=off go test -count=1 $$root_packages
	@echo "Testing relaykit Go module..."
	@cd relaykit && GOWORK=off go test -count=1 ./...
	@echo "Testing tokenkit Go module..."
	@cd tokenkit && GOWORK=off go test -count=1 ./...

test-db:
	@if ! docker ps --format '{{.Names}}' | grep -qx "$(TEST_DB_CONTAINER)"; then \
		docker run -d --rm --name $(TEST_DB_CONTAINER) \
			-e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=newapi_test \
			-p 127.0.0.1:$(TEST_DB_PORT):5432 postgres:18-alpine >/dev/null; \
		until docker exec $(TEST_DB_CONTAINER) pg_isready -U postgres -d newapi_test >/dev/null 2>&1; do sleep 1; done; \
	fi
	@echo "export TEST_POSTGRES_DSN='postgres://postgres:postgres@127.0.0.1:$(TEST_DB_PORT)/newapi_test?sslmode=disable'"

# Creates the next versioned migration of the main (default) or log schema:
# make migration NAME=add_user_locale [SET=log]
MIGRATION_SET ?= $(or $(SET),main)
migration:
	@if ! printf '%s' "$(NAME)" | grep -Eqx '[a-z0-9_]+'; then \
		echo "Usage: make migration NAME=add_user_locale [SET=main|log]"; \
		exit 1; \
	fi
	@dir=model/migrations/$(MIGRATION_SET); \
		if [ ! -d "$$dir" ]; then echo "No migration set $$dir"; exit 1; fi; \
		last=$$(ls "$$dir" | sed -n 's/^\([0-9]\{6\}\)_.*\.up\.sql$$/\1/p' | sort | tail -1); \
		file=$$dir/$$(printf '%06d' $$(expr "$${last:-0}" + 1))_$(NAME).up.sql; \
		printf -- '-- %s\n' "$(NAME)" > "$$file"; \
		echo "Created $$file"

reset-setup:
	@echo "Resetting local setup wizard state..."
	@if docker compose -f $(DEV_COMPOSE_FILE) ps --services --status running | grep -qx "$(DEV_POSTGRES_SERVICE)"; then \
		echo "Detected running docker dev PostgreSQL. Removing setup record and root users..."; \
		docker compose -f $(DEV_COMPOSE_FILE) exec -T $(DEV_POSTGRES_SERVICE) \
			psql -U $(DEV_POSTGRES_USER) -d $(DEV_POSTGRES_DB) \
			-c 'DELETE FROM setups;' \
			-c 'DELETE FROM users WHERE role = 100;' \
			-c "DELETE FROM options WHERE key IN ('SelfUseModeEnabled', 'DemoSiteEnabled');"; \
		echo "Restarting docker dev api so setup status is recalculated..."; \
		docker compose -f $(DEV_COMPOSE_FILE) restart $(DEV_API_SERVICE); \
	else \
		echo "No running docker dev PostgreSQL found. Start the dev stack with 'make dev-api'."; \
		exit 1; \
	fi
