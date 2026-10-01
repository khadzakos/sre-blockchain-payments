COMPOSE ?= docker compose

.PHONY: up up-monitoring down lint format format-check
up:
	$(COMPOSE) up --build -d
up-monitoring:
	OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy:4318 $(COMPOSE) --profile monitoring up --build -d
down:
	$(COMPOSE) --profile monitoring down
lint:
	golangci-lint run ./...
	npm run format:check
format:
	golangci-lint fmt
	npm run format
format-check:
	golangci-lint fmt --diff
	npm run format:check
