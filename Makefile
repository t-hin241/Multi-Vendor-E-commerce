-include .env
export

# Migrations run inside a throwaway container on the compose network,
# addressing Postgres by its service name (postgres:5432). A host machine
# often already has something else bound to host port 5432 (e.g. a native
# Postgres install), which makes a host-side "localhost:5432" connection
# ambiguous — going through the compose network sidesteps that entirely.
MIGRATE_IMAGE := migrate/migrate:v4.19.1
COMPOSE_NETWORK ?= shopee_shopee
POSTGRES_USER ?= shopee
# Git Bash (MSYS) rewrites leading-/ arguments like /migrations into a
# Windows path before docker ever sees them; this opts the recipes below
# out of that rewriting. Harmless on native Linux/macOS shells.
export MSYS_NO_PATHCONV=1

.PHONY: up down restart logs ps build fmt lint test backend-fmt backend-lint backend-test frontend-fmt frontend-lint frontend-test migrate-up migrate-down migrate-up-all

## Start the full stack (builds images as needed).
up:
	docker compose up -d --build

## Stop and remove the stack's containers.
down:
	docker compose down

restart: down up

logs:
	docker compose logs -f

ps:
	docker compose ps

fmt: backend-fmt frontend-fmt

lint: backend-lint frontend-lint

test: backend-test frontend-test

backend-fmt:
	cd backend && gofmt -l .

backend-lint:
	cd backend/pkg && golangci-lint run ./...
	cd backend/gateway && golangci-lint run ./...
	@for svc in identity vendor catalog inventory cart order payment shipment admin notification; do \
		echo "--- golangci-lint services/$$svc ---"; \
		(cd backend/services/$$svc && golangci-lint run ./...) || exit 1; \
	done

backend-test:
	cd backend && go test ./pkg/... ./gateway/...
	@for svc in identity vendor catalog inventory cart order payment shipment admin notification; do \
		echo "--- go test services/$$svc ---"; \
		(cd backend && go test ./services/$$svc/...) || exit 1; \
	done

frontend-fmt:
	cd frontend && npm run format

frontend-lint:
	cd frontend && npm run lint

frontend-test:
	cd frontend && npm test

## Run pending migrations for one service: make migrate-up SERVICE=identity
migrate-up:
	@test -n "$(SERVICE)" || (echo "SERVICE is required, e.g. make migrate-up SERVICE=identity" && exit 1)
	@test -n "$(POSTGRES_PASSWORD)" || (echo "POSTGRES_PASSWORD is not set (check your .env)" && exit 1)
	docker run --rm --network $(COMPOSE_NETWORK) \
		-v "$(CURDIR)/backend/services/$(SERVICE)/migrations:/migrations:ro" \
		$(MIGRATE_IMAGE) \
		-path=/migrations \
		-database="postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@postgres:5432/$(SERVICE)_db?sslmode=disable" \
		up

## Roll back the last migration for one service: make migrate-down SERVICE=identity
migrate-down:
	@test -n "$(SERVICE)" || (echo "SERVICE is required, e.g. make migrate-down SERVICE=identity" && exit 1)
	@test -n "$(POSTGRES_PASSWORD)" || (echo "POSTGRES_PASSWORD is not set (check your .env)" && exit 1)
	docker run --rm --network $(COMPOSE_NETWORK) \
		-v "$(CURDIR)/backend/services/$(SERVICE)/migrations:/migrations:ro" \
		$(MIGRATE_IMAGE) \
		-path=/migrations \
		-database="postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@postgres:5432/$(SERVICE)_db?sslmode=disable" \
		down 1

## Run pending migrations for every service in one shot: make migrate-up-all
migrate-up-all:
	@test -n "$(POSTGRES_PASSWORD)" || (echo "POSTGRES_PASSWORD is not set (check your .env)" && exit 1)
	@for svc in identity vendor catalog inventory cart order payment shipment admin notification; do \
		echo "--- migrate up $$svc ---"; \
		docker run --rm --network $(COMPOSE_NETWORK) \
			-v "$(CURDIR)/backend/services/$$svc/migrations:/migrations:ro" \
			$(MIGRATE_IMAGE) \
			-path=/migrations \
			-database="postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@postgres:5432/$${svc}_db?sslmode=disable" \
			up || exit 1; \
	done
