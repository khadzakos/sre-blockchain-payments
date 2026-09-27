COMPOSE ?= docker compose

.PHONY: up down lint format format-check
up:
	$(COMPOSE) up --build -d
down:
	$(COMPOSE) down
lint:
	golangci-lint run ./...
	npm run format:check
format:
	golangci-lint fmt
	npm run format
format-check:
	golangci-lint fmt --diff
	npm run format:check
