_:
    @just help

# List available commands
help:
    @just --list

# Build all packages with CGO disabled
build:
    CGO_ENABLED=0 go build ./...

# Run unit tests
test:
    go test ./...

# Run unit tests with race detector
race:
    go test -race ./...

# Run integration tests against a real tmux server
integration:
    TMUX_INTEGRATION_REQUIRED=1 go test -tags=integration -count=1 -timeout=5m ./...

# Run metamorphic relation tests against a real tmux server
metamorphic:
    TMUX_INTEGRATION_REQUIRED=1 go test -tags=integration -run=TestMetamorphic -count=1 ./...


# Run coverage-guided fuzzing (all targets for 10s each by default, or specific target/duration)
fuzz target="all" time="":
    #!/usr/bin/env bash
    set -euo pipefail

    tgt="{{target}}"
    dur="{{time}}"

    # If first argument looks like a duration (e.g. "30s", "1m", "5m"), treat as all targets for that time
    if [[ "$tgt" =~ ^[0-9]+[smh]$ ]] && [ -z "$dur" ]; then
        dur="$tgt"
        tgt="all"
    fi

    run_target() {
        local name="$1"
        local pkg="$2"
        local d="$3"
        local extra_flags=("-run=^$" "-fuzz=^${name}$" "-timeout=0")
        if [ -n "$d" ]; then
            extra_flags+=("-fuzztime=${d}")
            printf '==> Fuzzing %s in %s (%s)...\n' "$name" "$pkg" "$d"
        else
            printf '==> Fuzzing %s in %s indefinitely (Ctrl+C to stop)...\n' "$name" "$pkg"
        fi
        go test "${extra_flags[@]}" "$pkg"
    }

    if [ "$tgt" = "all" ]; then
        sweep_dur="${dur:-10s}"
        printf 'Starting fuzz sweep across all 9 targets (%s each)...\n' "$sweep_dur"
        for t in FuzzRecords FuzzQuoted FuzzOctal; do
            run_target "$t" "./internal/wire" "$sweep_dur"
        done
        for t in FuzzControlFrames FuzzEmbeddedFrameDelimiters FuzzParseEnvironment FuzzParseCommandLine FuzzParseSequence FuzzParseBinding; do
            run_target "$t" "./tmux" "$sweep_dur"
        done
    else
        case "$tgt" in
            FuzzRecords|FuzzQuoted|FuzzOctal)
                run_target "$tgt" "./internal/wire" "$dur"
                ;;
            FuzzControlFrames|FuzzEmbeddedFrameDelimiters|FuzzParseEnvironment|FuzzParseCommandLine|FuzzParseSequence|FuzzParseBinding)
                run_target "$tgt" "./tmux" "$dur"
                ;;
            *)
                printf 'Unknown fuzz target: %s\nRun "just fuzz" to sweep all targets, or pick: FuzzRecords, FuzzQuoted, FuzzOctal, FuzzControlFrames, FuzzEmbeddedFrameDelimiters, FuzzParseEnvironment, FuzzParseCommandLine, FuzzParseSequence, FuzzParseBinding\n' "$tgt" >&2
                exit 1
                ;;
        esac
    fi
# Run tests and display coverage
coverage:
    #!/usr/bin/env sh
    set -e
    coverage_file=$(mktemp)
    trap 'rm -f "$coverage_file"' EXIT
    TMUX_INTEGRATION_REQUIRED=1 go test -tags=integration -coverprofile="$coverage_file" ./...
    go tool cover -func="$coverage_file"
# Update Go module dependencies
tidy:
    go mod tidy

# Format Go source files
format:
    golangci-lint fmt

# Apply automatic fixes and format code
fix:
    golangci-lint run --fix
    golangci-lint fmt

# Run linters
lint:
    golangci-lint run

# Run all non-mutating quality checks
check:
    go mod tidy -diff
    go run ./internal/schema/generate -check
    golangci-lint run
    golangci-lint fmt --diff
    go test ./...

# Regenerate scoped option accessors
generate:
    go generate ./tmux

# Remove build and test artifacts
clean:
    rm -rf dist/ coverage.out coverage.html *.prof *.test lint-report.json

alias cov := coverage
alias fmt := format
