# tmux — Go library implementation

A standard-library-oriented Go library for explicit tmux server selection,
daemon-bound object handles, batched inspection, lifecycle operations, literal
input, capture, scoped resources, and persistent control connections.

**Status: substantial alpha implementation, not a completed or certified v1.**
The supplied contract is preserved in [`SPEC.md`](SPEC.md). The remaining
implementation and release gates are listed in [`STATUS.md`](STATUS.md). In
particular, the code has not been exercised against real tmux in the delivery
environment. Do not interpret the integration tests or CI configuration as a
claim that those jobs have passed.

## Requirements and bootstrap

The declared baseline is **Go 1.27**, with **Go 1.27.1** selected as the toolchain.
The intended tmux baseline is **3.6**. Linux and macOS are the intended release
platforms; the library does not advertise FreeBSD support yet. There is no cgo
requirement.

The module path is deliberately provisional: `example.com/tmux`. Before
publishing, choose a real repository/module path:

```sh
python3 scripts/set_module.py github.com/your-account/your-repository
```

Bootstrap dependency checksums with the normal Go module verification mechanism:

```sh
go mod tidy
GOWORK=off go test ./...
GOWORK=off go test -race ./...
GOWORK=off go vet ./...
CGO_ENABLED=0 GOWORK=off go build ./...
python3 internal/schema/generate.py --check
python3 scripts/check_ledger.py
```

Dependency versions are pinned in `go.mod`. A `go.sum` is intentionally not
fabricated: direct downloads were unavailable during delivery. The bootstrap
step obtains real sums from the configured Go module infrastructure. No
third-party source, local `replace` directives, or private dependencies are
shipped in the module.

## Ordinary usage

```go
package main

import (
    "context"
    "fmt"
    "time"

    tmux "example.com/tmux"
)

func main() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    server, err := tmux.New(tmux.Config{SocketName: "work"})
    if err != nil { panic(err) }
    session, err := server.FindSession(ctx, "work") // exact name, not prefix
    if err != nil { panic(err) }
    link, err := session.NewWindow(ctx, tmux.NewWindowOptions{
        Name: "logs",
        Program: tmux.Exec("tail", "-f", "/var/log/app.log"),
    })
    if err != nil { panic(err) }
    pane, err := link.Window().ActivePane(ctx)
    if err != nil { panic(err) }
    data, err := pane.Capture(ctx, tmux.CaptureOptions{JoinWrapped: true})
    if err != nil { panic(err) }
    fmt.Printf("%s", data)
}
```

Creation is detached unless selection is explicitly requested. Capture observes
what exists now; neither `Submit` nor capture waits for a pane program to finish.
`example_test.go` contains compiled examples for inspection, creation, options,
events, presence-bearing values, and a deliberately explicit raw fork command.

## Main surface

| Area | Entry points |
| --- | --- |
| Configuration and discovery | `New`, `Probe`, `Version`, `Capabilities`, `CurrentWithEnv`, `ParseEnvironment` |
| Inspection | `Sessions`, `Windows`, `Panes`, `Clients`, `Snapshot`, handle `Info` methods |
| Graph and provenance | private-origin handles, `Info.Handle`, `Window.Links`, `Session.Windows`, guarded `WindowLink` operations |
| Lifecycle | session/window creation; split, join, break, move, swap, select, resize, respawn, rename, kill, layouts |
| Input and modes | `SendText`, `SendKeys`, `Submit`, `CopyMode`, mode actions, selection records |
| Resources | generated scoped options, user options, sparse arrays, environment entries, hooks, key bindings, binary buffers |
| Clients and UI | client switch/detach/refresh, messages, popup, menu, prompt, choose modes; advanced prepared attach |
| Control | `OpenControl`, connection-bound `Server`, explicit `AuxiliaryServer`, bounded `Events`, format subscriptions and output flow control |
| Escape hatch | `NewCommand`, `Run`, `RunSequence`, `Pane.Format`, `Shell` |

The command index and machine-readable coverage ledger are in
[`docs/COMMANDS.md`](docs/COMMANDS.md) and `internal/schema/commands.json`.
That ledger inventories the implemented surface; it is **not** a claim of
exhaustive per-release flag parity.

## Resource and transport rules

A configured subprocess server owns no persistent process and has no `Close`.
Defaults are five seconds per captured logical operation, 4 MiB per output
stream, 1 MiB encoded input, and eight concurrent subprocesses per server.
Handles derived from the same server share that budget. There is no unlimited
mode and no global server registry.

An explicitly opened control connection owns one local tmux client. Close it.
It does not reconnect, create a scratch session, or detach another client.
`conn.Server()` uses that connection; the original server still uses subprocesses.
Control defaults: 64 admitted operations, 8 MiB retained encoded request bytes,
4 MiB frames, one in-flight operation, eight event streams, and 32 MiB of total
reserved stream capacity. Each stream defaults to 256 events and 4 MiB.

Raw output, capture, binary stdin, and unframed resource reads are deliberately
not silently tunneled through control mode. Choose an explicit auxiliary server
or `pane.UsingSubprocess()` for those paths. An auxiliary handle retains the
connection's original daemon identity and cannot outlive that connection.

`Window.Kill` destroys the shared window in every session. `WindowLink.Unlink`
removes one observed membership. A link never follows a changed index silently.

See [`docs/OWNERSHIP.md`](docs/OWNERSHIP.md), [`docs/ERRORS.md`](docs/ERRORS.md), and
[`docs/COMPATIBILITY.md`](docs/COMPATIBILITY.md) before integrating.

## Tests and release status

```sh
# Requires a real supported tmux. Missing executable is a failure here.
TMUX_INTEGRATION_REQUIRED=1 go test -tags=integration -count=1 ./...
TMUX_INTEGRATION_REQUIRED=1 go test -race -tags=integration -count=1 ./...

# Select an exact release without changing your ordinary PATH.
TMUX_TEST_BINARY=/opt/tmux-3.6/bin/tmux \
  TMUX_INTEGRATION_REQUIRED=1 go test -tags=integration ./...

# Parser fuzzing and real-server benchmarks.
go test ./internal/codec -run '^$' -fuzz '^FuzzRecords$' -fuzztime=30s
go test . -run '^$' -fuzz '^FuzzControlFrames$' -fuzztime=30s
TMUX_INTEGRATION_REQUIRED=1 go test -tags=integration -run '^$' -bench . -benchmem ./...
```

The shipped CI defines Linux/macOS lanes for 3.6, 3.6a, 3.6b, and 3.7c and a
separate prerequisite audit. It has not been run in this delivery. The exact
local evidence, including the modified offline compatibility workspace, is in
[`docs/VALIDATION.md`](docs/VALIDATION.md) and `docs/validation/`.

This archive does **not** claim to have migrated aht, sesh, or anno. No consumer
repository has been modified. The migration guide identifies the library-side
replacements and the consumer gates still requiring executable evidence.
