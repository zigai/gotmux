_:
    @just help

# List available commands
help:
    @just --list

# Run all tests
test:
    go test ./...

# Run tests with race detector
race:
    go test -race ./...

# Run go vet
vet:
    go vet ./...

# Run integration tests against a real tmux server
integration:
    TMUX_INTEGRATION_REQUIRED=1 go test -tags=integration -count=1 -timeout=5m ./...

# Run tests and display coverage
coverage:
    #!/usr/bin/env sh
    set -e
    coverage_file=$(mktemp)
    trap 'rm -f "$coverage_file"' EXIT
    go test -coverprofile="$coverage_file" ./...
    go tool cover -func="$coverage_file"

# Update Go module files
tidy:
    go mod tidy

# Check Go module files without modifying them
mod-check:
    go mod tidy -diff

# Format Go source files
format:
    golangci-lint fmt

# Apply automatic fixes and format code
fix:
    golangci-lint run --fix
    golangci-lint fmt

# Run golangci-lint without --fix
lint:
    golangci-lint run

# Run all non-mutating quality checks
check: mod-check check-generated test lint
    golangci-lint fmt --diff

# Regenerate scoped option accessors
generate:
    go generate ./tmux

# Verify generated code matches schema
check-generated:
    go run ./internal/schema/generate -check

# Build and run a clean external consumer without replace directives (requires zip)
consumer:
    sh scripts/test_consumer.sh

# Build all packages
build:
    CGO_ENABLED=0 go build ./...

# Remove build and test artifacts
clean:
    rm -rf dist/ coverage.out coverage.html *.prof *.test lint-report.json

alias cov := coverage
alias fmt := format
