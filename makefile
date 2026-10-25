API_DIR = .
DEV_COMPOSE_FILE = docker-compose.dev.yml
DEV_POSTGRES_SERVICE = postgres
DEV_API_SERVICE = new-api
DEV_POSTGRES_DB = new-api
DEV_POSTGRES_USER = root
TEST_DB_CONTAINER = new-api-test-postgres
TEST_DB_PORT ?= 55432

# The web console lives in the new-api-frontend repository; this repository
# builds and runs the backend only.
.PHONY: all start-api dev dev-api dev-api-rebuild reset-setup test test-db migration openapi lint-plugins format-plugins format-plugins-check

all: start-api

start-api:
	@echo "Starting api dev server..."
	@cd $(API_DIR) && go run main.go &

dev-api:
	@echo "Starting api services (docker)..."
	@docker compose -f $(DEV_COMPOSE_FILE) up -d

dev-api-rebuild:
	@echo "Rebuilding and starting api service (docker)..."
	@docker compose -f $(DEV_COMPOSE_FILE) up -d --build $(DEV_API_SERVICE)

dev: dev-api

# JavaScript task plugins are linted and formatted with the oxlint and oxfmt
# versions new-api-frontend locks.
OXLINT_VERSION = 1.74.0
OXFMT_VERSION = 0.72.0

lint-plugins:
	@cd plugins && bunx oxlint@$(OXLINT_VERSION) -c .oxlintrc.json .

format-plugins:
	@cd plugins && bunx oxfmt@$(OXFMT_VERSION) -c .oxfmtrc.json --write .

format-plugins-check:
	@cd plugins && bunx oxfmt@$(OXFMT_VERSION) -c .oxfmtrc.json --check .

# Database tests need a PostgreSQL 18 server: run `make test-db` and export the
# TEST_POSTGRES_DSN it prints, or point it at any UTF8 database you can create
# schemas in.
test:
	@if [ -z "$$TEST_POSTGRES_DSN" ]; then \
		echo "TEST_POSTGRES_DSN is not set. Run 'make test-db' and export the DSN it prints."; \
		exit 1; \
	fi
	@echo "Testing root Go module..."
	@GOWORK=off go test -count=1 ./...
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

# Regenerates docs/openapi/api.json and relay.json from the swag annotations on
# the handlers. The router/apidoc test, part of `make test`, fails when the
# files are out of date or a registered route has no annotation.
openapi:
	@GOWORK=off go test ./router/apidoc -run '^TestOpenAPIDocuments$$' -count=1 -update

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
