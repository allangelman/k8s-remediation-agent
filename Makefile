.DEFAULT_GOAL := help

.PHONY: help build test

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build the agent binary into bin/
	go build -o bin/agent ./cmd/agent

test: ## Run Go tests
	go test ./...
