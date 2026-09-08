# Ownership and execution contracts

## Servers and configuration

`New` resolves an executable and captures environment and working directory;
it never starts a process. Explicit socket path/name wins over captured TMUX.
TMUX is parsed from its two trailing comma-separated fields, so a comma in the
socket path is preserved. A default/named socket's effective TMUX_TMPDIR is frozen.
An explicitly empty environment is not changed into the process environment.

Ordinary server methods take a context. Each logical operation shares its input
and per-stream output budget across probes, queries, and decoding. Earlier caller
deadlines win. Input means encoded input, including nested command expansion.
Captured process cancellation kills the owned client, not a daemon PID or jobs
running inside panes. Startup permission is supplied explicitly; ordinary reads
use tmux's native no-start flag.

A raw server denotes an endpoint. A materialized handle also retains the daemon's
reported socket, PID, start time, and (when applicable) control generation. Handle
methods insert synchronous format guards and the requested commands in one tmux
queue. PID/start-time precision is finite, typically seconds: identity reuse can
collide. This is not a malicious-daemon or cryptographic security boundary.
The real-version proof gate for that mechanism is still open.

## Handles and records

IDs, `Valid`, `Equal`, `Identity`, `Info.Handle`, and known link parent accessors
are local operations. `Valid` checks provenance, not liveness. All fetched records
are data; they never refresh themselves. Changing a record's exported ID or name
does not change its private handle origin. Manually assembled records have no
provenance. Raw format expansions are returned as owned copies, not mutable maps.

A window is one shared object. Links are observed `(session, index, expected
window)` slots. A stale link returns an error instead of following renumbering.
Pane/window methods do not invent a unique parent session in a linked graph.

Snapshot accessors return copies, including nested client flags. Snapshot graph
churn is recorded as `Incomplete` with a bounded missing-reference list. A
consistent snapshot is still an observation over time, not an atomic transaction.
No per-pane query loop is used to collect a snapshot.

## Programs and input

`Exec` preserves executable/argv semantics; `Shell` deliberately invokes shell
syntax. A fixed positional launcher handles tmux's special one-operand behavior.
Environment launchers pass each validated assignment as one argv operand, never
as interpolated shell source. They do not modify daemon-global or session state.
Nonempty overrides with `Program{}` are explicitly rejected for now.

`SendText` sends literal data without Enter. `SendKeys` validates key names.
`Submit` is literal data followed by Enter in one ordered tmux command sequence;
it says nothing about shell completion. Capture returns bytes without trimming,
ANSI removal, title queries, or application-specific line budgets.

## Control and events

The context passed to `OpenControl` owns the connection lifetime. The connection
is a real attached tmux client with hooks and attached-client-count effects. Its
size is ignored and pane output is disabled by default. The library does not
claim this client is invisible to the daemon.

One owner reads and one owner writes. Only one logical request is in flight.
Canceled queued requests are not written. Once a request is dispatched, caller
cancellation cannot retract it: its reservations remain held while the dispatcher
drains through an independent completion marker. Recovery is bounded; loss of
synchronization fails the connection and pending calls. Command-number gaps and
nested blocks are not equated with Go call indexes.

`Close` is idempotent and concurrent-safe. It stops and reaps only the owned local
client, closes its pipes, resolves work, and waits within the shutdown budget.
A nil return means owned work has finished. A retained connection failure is not
erased by later Close calls. `Wait(ctx)` observes eventual termination.

A stream reserves its full byte capacity when created. Overflow terminates that
stream with persistent `ErrEventsLost`; it clears payloads and releases the
reservation. Other streams and command replies continue. Individual `Next`
cancellation leaves the stream usable and does not consume queued data. Explicit
stream closure yields EOF; lifetime cancellation and connection errors remain
terminal errors. No callbacks run on the reader.

No subscribe-plus-snapshot helper is labeled atomic. Applications must reconcile
races and refresh after loss. Terminal output events are not screen captures and
are not a terminal emulator.

## Terminal/UI paths

`PrepareAttach` returns an unstarted command using caller-supplied terminal files.
The caller owns execution, cancellation, and waiting. This advanced method is
endpoint-relative, not a protected handle operation. The library does not change
terminal state; tmux owns its setup and restoration. Guarded attachment is a
remaining v1 requirement, not a hidden downgrade.

UI builders target a materialized client. Popup/menu/prompt calls require a caller
deadline and reject control transport. Success only acknowledges the documented
tmux scheduling/completion behavior, never an invented selected value. Canceling
a local waiter does not guarantee dismissal of server-side UI or cancellation of
an application command selected from that UI.
