.PHONY: build test clean fmt lint help

BINARY_NAME=gitunstuck

.DEFAULT_GOAL := build

help: ## Display this help message
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the gitunstuck binary
	go build -o $(BINARY_NAME) ./cmd/gitunstuck

test: ## Run the go test suite
	go test -v ./...

clean: ## Remove the built binary and clean go cache
	rm -f $(BINARY_NAME)
	go clean

fmt: ## Format all go source files
	go fmt ./...

lint: ## Run golangci-lint if available
	if command -v golangci-lint > /dev/null; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not found, skipping lint"; \
	fi

all: fmt lint build test ## Run fmt, lint, build and test
