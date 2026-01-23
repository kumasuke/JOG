.PHONY: build test test-s3compat test-coverage lint clean run deps docker-build docker-up docker-down
.PHONY: benchmark benchmark-env benchmark-warp benchmark-custom benchmark-report benchmark-clean

# Binary name
BINARY_NAME=jog
BUILD_DIR=bin

# Version info (set from git tags)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt
GOLINT=golangci-lint

# Build flags
LDFLAGS=-ldflags "-s -w \
  -X github.com/kumasuke/jog/internal/cli.Version=$(VERSION) \
  -X github.com/kumasuke/jog/internal/cli.Commit=$(COMMIT)"

# Default target
all: deps lint test build

# Install dependencies
deps:
	$(GOMOD) download
	$(GOMOD) tidy

# Build the binary
build:
	mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/jog

# Run the server
run: build
	./$(BUILD_DIR)/$(BINARY_NAME) server

# Run all tests
test:
	$(GOTEST) -v ./...

# Run unit tests only (internal packages)
test-unit:
	$(GOTEST) -v ./internal/...

# Run S3 compatibility tests
test-s3compat:
	$(GOTEST) -v ./test/s3compat/...

# Run tests with coverage
test-coverage:
	$(GOTEST) -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

# Run S3 compatibility tests with coverage
test-s3compat-coverage:
	$(GOTEST) -coverprofile=coverage-s3compat.out ./test/s3compat/...
	$(GOCMD) tool cover -html=coverage-s3compat.out -o coverage-s3compat.html
	@echo "S3 compatibility coverage report generated: coverage-s3compat.html"

# Format code
fmt:
	$(GOFMT) -s -w .

# Run linter
lint:
	@if command -v $(GOLINT) > /dev/null; then \
		$(GOLINT) run ./...; \
	else \
		echo "golangci-lint not installed, skipping lint"; \
	fi

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html
	rm -f coverage-s3compat.out coverage-s3compat.html

# Install development tools
tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Docker targets
docker-build:
	docker compose build

docker-up:
	docker compose up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f

# Benchmark targets
benchmark: benchmark-env benchmark-warp benchmark-report
	@echo "Benchmark completed. Report generated at benchmark/results/REPORT.md"

benchmark-env:
	@echo "Starting benchmark environment..."
	docker compose -f benchmark/docker-compose.benchmark.yml up -d
	@echo "Waiting for services to be healthy..."
	@sleep 5
	@docker compose -f benchmark/docker-compose.benchmark.yml ps

benchmark-warp:
	@echo "Running Warp benchmarks..."
	./benchmark/scripts/run-warp.sh both throughput

benchmark-warp-all:
	@echo "Running all Warp benchmarks..."
	./benchmark/scripts/run-warp.sh both all

benchmark-custom:
	@echo "Running custom Go benchmarks..."
	./benchmark/scripts/run-custom.sh jog
	./benchmark/scripts/run-custom.sh minio

benchmark-report:
	@echo "Generating benchmark report..."
	./benchmark/scripts/generate-report.sh
	@echo "Report generated at benchmark/results/REPORT.md"

benchmark-clean:
	@echo "Stopping benchmark environment..."
	docker compose -f benchmark/docker-compose.benchmark.yml down -v
	rm -rf benchmark/data/jog/* benchmark/data/minio/*
	@echo "Benchmark environment cleaned"

# Help
help:
	@echo "Available targets:"
	@echo "  make build           - Build the binary"
	@echo "  make run             - Build and run the server"
	@echo "  make test            - Run all tests"
	@echo "  make test-unit       - Run unit tests only"
	@echo "  make test-s3compat   - Run S3 compatibility tests"
	@echo "  make test-coverage   - Run tests with coverage report"
	@echo "  make lint            - Run linter"
	@echo "  make fmt             - Format code"
	@echo "  make clean           - Clean build artifacts"
	@echo "  make deps            - Download dependencies"
	@echo "  make tools           - Install development tools"
	@echo "  make docker-build    - Build Docker image"
	@echo "  make docker-up       - Start Docker containers"
	@echo "  make docker-down     - Stop Docker containers"
	@echo "  make docker-logs     - View Docker container logs"
	@echo ""
	@echo "Benchmark targets:"
	@echo "  make benchmark       - Run full benchmark suite"
	@echo "  make benchmark-env   - Start JOG and MinIO containers"
	@echo "  make benchmark-warp  - Run Warp throughput benchmarks"
	@echo "  make benchmark-warp-all - Run all Warp benchmarks"
	@echo "  make benchmark-custom - Run custom Go benchmarks"
	@echo "  make benchmark-report - Generate benchmark report"
	@echo "  make benchmark-clean - Stop and clean benchmark environment"
