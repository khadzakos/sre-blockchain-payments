COMPOSE ?= docker compose

.PHONY: up up-monitoring down lint format format-check minikube-up minikube-status minikube-ui minikube-ui-stop
up:
	$(COMPOSE) up --build -d
up-monitoring:
	OTEL_EXPORTER_OTLP_ENDPOINT=http://alloy:4318 $(COMPOSE) --profile monitoring up --build -d
down:
	$(COMPOSE) --profile monitoring down
minikube-up:
	sh deploy/minikube/local.sh up
minikube-status:
	sh deploy/minikube/local.sh status
minikube-ui:
	sh deploy/minikube/local.sh ui
minikube-ui-stop:
	sh deploy/minikube/local.sh ui-stop
lint:
	golangci-lint run ./...
	npm run format:check
format:
	golangci-lint fmt
	npm run format
format-check:
	golangci-lint fmt --diff
	npm run format:check
