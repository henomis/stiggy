.PHONY: all build test lint fmt vet clean coverage help license tidy fix check guard examples examples-live

# Variables
GO := go
GOFLAGS := -v
MODULE := $(shell $(GO) list -m)
PKGS := $(shell $(GO) list ./... | grep -v /examples/)
GOLANGCI_LINT := golangci-lint
BIN_DIR := bin

# Default target
all: test lint

## help: Display this help message
help:
	@echo "Available targets:"
	@echo "  make build       - Build the stiggy binary into bin/ (honors GOOS/GOARCH)"
	@echo "  make test        - Run all tests"
	@echo "  make guard       - Run only the no-direct-NATS architecture guard"
	@echo "  make lint        - Run golangci-lint"
	@echo "  make fmt         - Format all Go files"
	@echo "  make fix         - Run go fix on all packages"
	@echo "  make vet         - Run go vet"
	@echo "  make tidy        - Tidy and verify go modules"
	@echo "  make coverage    - Generate test coverage report"
	@echo "  make clean       - Clean build artifacts and cache"
	@echo "  make license     - Add license headers to all Go files"
	@echo "  make check       - Run test + lint + vet"
	@echo "  make examples    - Run the examples with their scripted models"
	@echo "  make examples-live - Run the examples against a real model (OLLAMA_MODEL=gemma4:cloud by default)"
	@echo "  make help        - Display this help message"

## build: Build the stiggy binary (static; honors GOOS/GOARCH)
build:
	@echo "Building $(BIN_DIR)/stiggy..."
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w" -o $(BIN_DIR)/stiggy ./cmd/stiggy

## test: Run all tests (excluding examples)
test:
	@echo "Running tests..."
	$(GO) test $(GOFLAGS) -race -timeout 5m $(PKGS)

## guard: Run the architecture guard (stiggy never touches NATS directly)
guard:
	$(GO) test -run TestNoDirectNATS ./internal/archguard/

## coverage: Generate test coverage report
coverage:
	@echo "Generating coverage report..."
	$(GO) test -race -timeout 5m -coverprofile=coverage.out -covermode=atomic $(PKGS)
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

## lint: Run golangci-lint
lint:
	@echo "Running linters..."
	$(GOLANGCI_LINT) run ./...

## fmt: Format all Go files
fmt:
	@echo "Formatting code..."
	$(GO) fmt ./...
	gofumpt -l -w .
	gci write --skip-generated -s standard -s default -s "prefix($(MODULE))" .

## fix: Run go fix on all packages
fix:
	$(GO) fix ./...

## vet: Run go vet
vet:
	@echo "Running go vet..."
	$(GO) vet ./...

## tidy: Tidy go modules
tidy:
	$(GO) mod tidy
	$(GO) mod verify

## clean: Clean build artifacts and cache
clean:
	rm -f coverage.out coverage.html
	rm -rf $(BIN_DIR)

## license: Add Apache license headers to all Go files
license:
	find . -type f -name '*.go' -not -path './internal/archguard/testdata/*' \
		-exec addlicense -c "Simone Vellei" -l apache {} +

## examples: Run every example on an embedded server with scripted models
examples:
	$(GO) test -count=1 ./examples/...

## examples-live: Run every example against a real model and log the output
OLLAMA_MODEL ?= gemma4:cloud
examples-live:
	STIGGY_EXAMPLE_LIVE=1 OLLAMA_MODEL=$(OLLAMA_MODEL) $(GO) test -count=1 -v -timeout 30m ./examples/...

## check: Run all checks (test + lint + vet)
check: test examples lint vet
	@echo "All checks passed!"
