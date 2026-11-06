# Build all packages with CGO disabled
build:
    CGO_ENABLED=0 go build ./...

# Run fast Go unit tests
[positional-arguments]
test *args:
    #!/usr/bin/env bash
    set -euo pipefail
    if (( $# )); then go test "$@"; else go test ./...; fi

# Run unit tests with race detector
race:
    go test -race ./...

# Run integration tests against a real tmux server
integration *args:
    TMUX_INTEGRATION_REQUIRED=1 go test -tags=integration -count=1 -timeout=5m {{ args }} ./...

# Run metamorphic relation tests against a real tmux server
metamorphic:
    TMUX_INTEGRATION_REQUIRED=1 go test -tags=integration -run=TestMetamorphic -count=1 ./...

# Run coverage-guided fuzzing (all targets for 10s each by default, or specific target/duration)
fuzz target="all" time="":
    #!/usr/bin/env bash
    set -euo pipefail

    tgt="{{ target }}"
    dur="{{ time }}"

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
        printf 'Starting fuzz sweep across all 13 targets (%s each)...\n' "$sweep_dur"
        for t in FuzzRecords FuzzWriteQuoted FuzzDecodeOctal; do
            run_target "$t" "./internal/wire" "$sweep_dur"
        done
        for t in FuzzControlFrames FuzzEmbeddedFrameDelimiters FuzzParseTmuxVars FuzzParseCommandLine FuzzParseSequence FuzzParseBinding FuzzParseBindingNote FuzzKeyValid FuzzParseOptionName FuzzDecodeEvent; do
            run_target "$t" "./tmux" "$sweep_dur"
        done
    else
        case "$tgt" in
            FuzzRecords|FuzzWriteQuoted|FuzzDecodeOctal)
                run_target "$tgt" "./internal/wire" "$dur"
                ;;
            FuzzControlFrames|FuzzEmbeddedFrameDelimiters|FuzzParseTmuxVars|FuzzParseCommandLine|FuzzParseSequence|FuzzParseBinding|FuzzParseBindingNote|FuzzKeyValid|FuzzParseOptionName|FuzzDecodeEvent)
                run_target "$tgt" "./tmux" "$dur"
                ;;
            *)
                printf 'Unknown fuzz target: %s\nRun "just fuzz" to sweep all targets, or pick: FuzzRecords, FuzzWriteQuoted, FuzzDecodeOctal, FuzzControlFrames, FuzzEmbeddedFrameDelimiters, FuzzParseTmuxVars, FuzzParseCommandLine, FuzzParseSequence, FuzzParseBinding, FuzzParseBindingNote, FuzzKeyValid, FuzzParseOptionName, FuzzDecodeEvent\n' "$tgt" >&2
                exit 1
                ;;
        esac
    fi

# Measure all local test suites and print coverage
coverage:
    #!/usr/bin/env bash
    set -euo pipefail
    rm -rf coverage
    mkdir -p coverage
    coverage_package=$(awk '$1 == "module" {gsub(/"/, "", $2); print $2; exit}' go.mod)
    go test -coverpkg=./... ./... -args -test.gocoverdir="$PWD/coverage"
    TMUX_INTEGRATION_REQUIRED=1 go test -coverpkg=./... -tags=integration -count=1 -timeout=5m ./... -args -test.gocoverdir="$PWD/coverage"
    go tool covdata percent -i coverage -pkg "$coverage_package/..."
    go tool covdata textfmt -i coverage -o coverage/profile.txt -pkg "$coverage_package/..."
    go tool cover -func coverage/profile.txt | awk '$1 == "total:"'

# Format every supported first-party file type
format:
    @golangci-lint run --allow-parallel-runners --fix --enable-only whitespace --issues-exit-code=0
    @golangci-lint fmt
    @git ls-files -co --exclude-standard -z | while IFS= read -r -d '' file; do if [[ -f $file ]]; then printf '%s\0' "$file"; fi; done | xargs -0 -r oxfmt --no-error-on-unmatched-pattern '!**/*.md'
    @git ls-files -co --exclude-standard -z | while IFS= read -r -d '' file; do if [[ -f $file ]]; then printf '%s\0' "$file"; fi; done | xargs -0 -r shfmt -f | xargs -r -d '\n' shfmt -w
    @git ls-files -co --exclude-standard -- Justfile justfile "*.just" | while IFS= read -r file; do just --justfile "$file" --fmt; done
    @just markdown fmt

# Apply safe lint autofixes, then format every supported file type
fix: && format
    golangci-lint run --fix

# Run every configured linter on the whole project, stopping on failure
lint:
    @golangci-lint run

# Run the commit checks
check: (run-checks "formatcheck" "lockcheck" "generated-check" "lint" "test")

# Regenerate scoped option accessors
generate:
    go generate ./tmux

# Remove generated output and caches
clean: (remove "dist" "coverage.out" "*.test" "*.prof" "coverage.html" "lint-report.json" "coverage")

# Check generated files without changing them
generated-check:
    go run ./internal/schema/generate -check

# Check the same files and tools without writing
formatcheck:
    @golangci-lint run --allow-parallel-runners --enable-only whitespace
    @golangci-lint fmt --diff
    @git ls-files -co --exclude-standard -z | while IFS= read -r -d '' file; do if [[ -f $file ]]; then printf '%s\0' "$file"; fi; done | xargs -0 -r oxfmt --check --no-error-on-unmatched-pattern '!**/*.md'
    @git ls-files -co --exclude-standard -z | while IFS= read -r -d '' file; do if [[ -f $file ]]; then printf '%s\0' "$file"; fi; done | xargs -0 -r shfmt -f | xargs -r -d '\n' shfmt -d
    @git ls-files -co --exclude-standard -- Justfile justfile "*.just" | while IFS= read -r file; do just --justfile "$file" --fmt --check; done
    @just markdown check
    @git ls-files -co --exclude-standard -z | while IFS= read -r -d '' file; do if [[ -f $file ]]; then printf '%s\0' "$file"; fi; done | xargs -0 -r editorconfig-checker -disable-indent-size -disable-indentation -disable-max-line-length -disable-charset

# Run the commit checks and slow suites
checkfull: (run-checks "formatcheck" "lockcheck" "generated-check" "lint" "test" "race" "integration -race" "fuzz")

# Synchronize dependency locks without upgrading dependencies
[no-exit-message]
lock:
    @go mod tidy

# Verify dependency manifests and locks without changing files
[no-exit-message]
lockcheck:
    @go mod tidy -diff

# Install repository dependencies
setup:
    @go mod download

# List available recipes
[default]
help:
    @just --list

alias cov := coverage
alias fmt := format

[positional-arguments]
[private]
remove +paths:
    #!/usr/bin/env bash
    set -euo pipefail
    shopt -s nullglob globstar
    size() { LC_ALL=C numfmt --from-unit=1024 --to=iec-i --suffix=B --format=%.1f "$1"; }
    rows=() total=0
    for pattern in "$@"; do
        found=()
        for path in $pattern; do
            if [[ -e $path || -L $path ]]; then found+=("$path"); fi
        done
        if (( ${#found[@]} == 0 )); then continue; fi
        kib=$(du -sck -- "${found[@]}" | tail -n1 | cut -f1)
        rm -rf -- "${found[@]}"
        label=$pattern
        if (( ${#found[@]} > 1 )); then label+=" (${#found[@]})"; fi
        rows+=("removed  $label|$(size "$kib")")
        total=$(( total + kib ))
    done
    if [[ -n ${CLEAN_TOTAL_FILE:-} ]]; then echo "$total" >> "$CLEAN_TOTAL_FILE"; fi
    if (( ${#rows[@]} == 0 )); then echo "nothing to clean"; exit 0; fi
    printf '%s\n' "${rows[@]}" "total freed|$(size "$total")" | column -t -s '|' -R 2

[positional-arguments]
[private]
run-checks +steps:
    #!/usr/bin/env bash
    set -uo pipefail
    LC_NUMERIC=C
    log_dir="${XDG_CACHE_HOME:-$HOME/.cache}/just-check"
    mkdir -p "$log_dir"
    log=$(mktemp "$log_dir/step.XXXXXX")
    trap 'rm -f "$log"' EXIT
    width=0 failed=()
    for step in "$@"; do (( ${#step} > width )) && width=${#step}; done
    for step in "$@"; do
        read -ra command <<< "$step"
        printf 'check  %-*s  ' "$width" "$step"
        start=$EPOCHREALTIME
        run=(just --justfile {{ quote(justfile()) }} "${command[@]}")
        if [[ -t 1 ]]; then run=(script -qefc "${run[*]@Q}" /dev/null); fi
        if "${run[@]}" < /dev/null > "$log" 2>&1; then status=ok; else status=FAIL; failed+=("$step"); fi
        printf '%-4s  %7.1fs\n' "$status" "$(LC_NUMERIC=C awk -v a="$start" -v b="$EPOCHREALTIME" 'BEGIN { print b - a }')"
        if [[ "$status" == FAIL ]]; then sed 's/\r$//; s/^/    /' "$log"; fi
    done
    if (( ${#failed[@]} )); then printf '%d of %d failed: %s\n' "${#failed[@]}" "$#" "${failed[*]}"; exit 1; fi
    printf 'all %d passed\n' "$#"

[private]
markdown *args:
    rumdl {{ args }} --disable MD001,MD013,MD024,MD025,MD026,MD031,MD032,MD033,MD034,MD036,MD041,MD058 --config 'MD060.enabled=true' --config 'MD060.style="aligned"' --config 'MD003.style="atx"' --config 'MD004.style="dash"' --config 'MD029.style="ordered"' --config 'MD046.style="fenced"' --config 'MD048.style="backtick"' --config 'MD049.style="asterisk"' --config 'MD050.style="asterisk"' --config 'MD055.style="leading_and_trailing"'
