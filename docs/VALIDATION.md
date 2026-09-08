# Validation report

Recorded: 2026-09-08T07:55:58+00:00.

## What was and was not tested

The deliverable is an alpha implementation, **not a v1 conformance claim**. The
available compiler was **Go 1.23.2 on Linux/amd64**, and **no tmux executable was
installed**. Direct toolchain/dependency downloads were unavailable. Consequently,
the unmodified Go 1.27 module and the real-tmux release matrix were not run.

For meaningful local checks, an isolated copy of the source was tested with a
Go 1.23 module file and actual `golang.org/x/sync`, `golang.org/x/term`, and
`golang.org/x/sys` source already vendored by the installed Go toolchain. This was
not a substitute semaphore or terminal implementation. Only that temporary copy
used local module replacements; **the delivered module contains none**. The
fixture's coverage-environment correction was copied to both workspaces before
the final unit and race runs. The published dependency versions remain untested
in this environment.

The Go 1.27 Rapid/go-cmp tests and the Go 1.24+ `b.Loop` benchmarks were excluded
by their explicit build constraints under Go 1.23. `internal/lifecycle` used its
Go 1.23 compatibility implementation; the `sync.WaitGroup.Go` implementation was
not executed. This report does not imply those excluded files passed compilation.

## Executed checks

| Check | Result | Evidence |
| --- | --- | --- |
| Unit tests, offline compatibility copy | PASS: 64 top-level Test functions, 1 executable Example, 10 fuzz seed subtests | `validation/unit-json.log` |
| `go test -race -count=1 ./...`, offline copy | PASS, repeated after the fixture correction | `validation/race.log` |
| `go vet ./...`, offline copy | PASS | `validation/vet.log` |
| `CGO_ENABLED=0 go build ./...`, offline copy | PASS | `validation/cgo-free.log` |
| `go test -cover -count=1 ./...`, offline copy | PASS after test-fixture correction | `validation/coverage.log` |
| Five native fuzz smoke targets, offline copy | PASS, 113,682 generated executions reported collectively | `validation/fuzz-*.log` |
| Integration-tag compilation and local invocation | COMPILED; 7 real-tmux tests SKIPPED, not passed | `validation/integration.log` |
| Required integration without installed tmux | EXPECTED FAILURE; missing executable cannot silently skip this lane | `validation/integration-required.log` |
| Unmodified module, `GOTOOLCHAIN=local go test ./...` | BLOCKED: installed Go 1.23.2 is below the required Go 1.27 | `validation/native-baseline.log` |
| Generated option code check | PASS | `validation/generated.log` |
| Alpha command-ledger structural check | PASS; does not prove upstream command/flag parity | `validation/ledger.log` |
| `gofmt -l .` | PASS, no reported files | `validation/gofmt.log` |
| Full release gate | EXPECTED FAILURE; not v1-ready | `validation/release-gates.log` |

The unit JSON log contains 91 successful test/example/fuzz events including
subtests. Counting all of those as distinct top-level tests would inflate the
number; the table above separates top-level tests, the executable example, and
seed subtests. Other examples compile but deliberately do not execute without
an explicit tmux server.

### Native fuzz smoke results

| Target | Reported executions |
| --- | ---: |
| `FuzzControlFrames` | 9,080 |
| `FuzzEmbeddedFrameDelimiters` | 4,610 |
| `FuzzOctal` | 32,877 |
| `FuzzQuoted` | 31,349 |
| `FuzzRecords` | 35,766 |

Codec targets ran with `-fuzztime=2s`, control targets with `-fuzztime=1s`, and
`GOMAXPROCS=2`. These are short smoke runs, not long-duration fuzzing evidence.
No generated failures were reported. Seed corpora are checked into the test code.

### Coverage context

Root-package statement coverage was **24.4%**, internal codec coverage **73.5%**,
and internal process runner coverage **92.2%** in this unit-only offline run.
Unexercised typed command methods require real-tmux integration. Those values
are not full release acceptance or cross-platform coverage.

The first coverage run failed because the instrumented subprocess test helper
emitted a `GOCOVERDIR` warning into stderr. The fixture now supplies a private
coverage destination without changing process-global environment. The initial
failure log is retained as `coverage-initial-fixture-warning.log`; the final
coverage and race logs are successful. The production runner was not changed
or taught to suppress stderr.

## Integration tests not executed against tmux

- `TestIntegrationInspectCreateAndCapture`
- `TestIntegrationBinaryBufferAndLiteralInput`
- `TestIntegrationOptionsInheritanceAndEmpty`
- `TestIntegrationLinkedGraphAndStaleIndex`
- `TestIntegrationTwoServersAndReplacement`
- `TestIntegrationControlParityAndExplicitAuxiliary`
- `TestIntegrationNoServerAndExistingOnly`

The synthetic pipe peer in dispatcher tests exercises the library's ownership,
cancellation, draining, and queue accounting. It is deliberately not described
as a real-tmux compatibility fixture. Hand-crafted protocol fixtures and codec
round trips cannot prove tmux's actual output dialect or queue-local guards.

## Still required before release

Run the unmodified module on Go 1.27 with the actual pinned dependencies; execute
all real-server and race scenarios against tmux 3.6, 3.6a, 3.6b, and 3.7c on Linux
and macOS; run Rapid/go-cmp model tests and benchmarks; review full command/flag
and option parity against pinned inventories; complete guarded terminal attach
and all remaining semantics in `STATUS.md`; then migrate and test aht, sesh, and
anno. None of those applications was modified in this delivery.

The supplied CI workflow and clean-consumer-build script have not themselves
been run here. `go.sum` is intentionally not invented: a network-enabled
`go mod tidy` obtains and authenticates the selected modules before normal use.

See `STATUS.md` for implementation gaps distinct from unexecuted verification.
