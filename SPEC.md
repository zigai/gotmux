# Go tmux library specification

Status: proposed implementation contract, not an implemented API.
Date: 2026-09-08.
Working package name: `tmux`; repository/module name is deliberately undecided.
Initial consumers: aht, sesh, anno.
Language baseline: Go 1.27, using the latest available patch release.
Minimum tmux version: 3.6.

## 1. Purpose and completion criteria

Build a general-purpose Go library for controlling real tmux servers. It must make ordinary operations easy while preserving the identities, bytes, timing, and failure semantics needed by serious automation.
The library owns tmux protocol details. Applications own agent state, annotation policy, workspace persistence, session-selection policy, and user interfaces.
A complete v1 must provide:

- Typed coverage of the stock tmux command families in section 15, including operations used by all three consumers.
- Explicit server selection and stable object targeting.
- A consistent API over subprocess execution and an explicitly opened control connection, with documented transport-specific capabilities.
- Lossless output handling, bounded resources, and honest cancellation and partial-failure semantics.
- Published test support and compatibility evidence from real tmux.
- A supported raw-command path for custom commands and formats.
- Successful migration of the existing shared tmux responsibilities out of aht, sesh, and anno.

“Complete” does not imply a terminal emulator, shell completion detector, transactional tmux, or native Go reimplementation of tmux. The library has no cgo dependency, does not link tmux internals, and does not implement its private client/server socket protocol.
MUST is a release requirement. SHOULD admits a documented, tested exception. API fragments below specify signatures and behavior; they are not a compilable implementation.

## 2. Design rules

1. **Context first for external work.** Every blocking operation takes `context.Context` first. No `FooContext` duplicates, retained request contexts, implicit background refreshes, or hidden polling on property access.
2. **Concrete handles, explicit reads.** `Pane` identifies a pane; `PaneInfo` is a fetched record. `pane.ID()` performs no I/O. `pane.Info(ctx)` does.
3. **One vocabulary.** Use tmux's session, window, pane, client, option, hook, layout, and buffer terminology. `tmux.Client` means an attached tmux client; `tmux.Server` is the Go entry point.
4. **Small ordinary calls.** `pane.Kill(ctx)`, `pane.SendText(ctx, text)`, `session.Rename(ctx, name)`. Use option structs for operations with several independent settings.
5. **No implicit execution in constructors.** Creating a configured server handle does not start tmux or contact a daemon.
6. **No silent semantic changes.** Never drop unsupported flags, truncate successful results, switch servers, recreate missing sessions, or retry mutations automatically.
7. **Make execution domains distinct.** Literal text, keys, tmux commands, format expressions, and shell scripts have distinct APIs.
8. **Typed data with an escape hatch.** Core APIs do not return `map[string]any`. Raw access preserves information beyond the typed schema.
9. **Small, justified dependencies.** Prefer the standard library and the four selected dependencies in section 3 for their specific responsibilities. No logging framework, CLI parser, metrics stack, process scanner, or application models in the core module.
10. **Generics where they express a contract.** A presence-bearing value can be generic. Do not construct an operation framework or interface for every object type.

## 3. Package and dependency structure

One Go module, with the main API at its root:

```
<module>/                 package tmux: handles, records, commands, control API
<module>/tmuxtest/        isolated real-server fixtures for consumers
<module>/internal/exec/   process ownership and bounded execution
<module>/internal/codec/ argument, format, output, and control codecs
<module>/internal/schema/ checked-in compatibility metadata and generators
```

These are responsibility boundaries, not a requirement to create empty packages. Private control parsing may live beside its owner until extraction improves the implementation.
Session, window, and pane types stay in one public package: their relationships must not require circular imports or surrogate interfaces. Test support imports the core; the core never imports test support. Future CLI/MCP/workspace tools belong in separate consumer modules.
Applications can define small interfaces at their own boundaries. The library returns concrete handles and does not export a giant `Tmux` interface or mock framework.

### Selected third-party dependencies

These four libraries are approved for this design. Introduce each with the implementation or tests that use it; do not add unused imports or dependencies in advance.

| PackageScopeRequired role                                                       |                                                                |                                                                                                                        |
| ------------------------------------------------------------------------------- | -------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| [`golang.org/x/sync/semaphore`](https://pkg.go.dev/golang.org/x/sync/semaphore) | Runtime; module `golang.org/x/sync`                            | Context-aware weighted admission for retained request bytes and resource limits.                                       |
| [`golang.org/x/term`](https://pkg.go.dev/golang.org/x/term)                     | Runtime, when interactive attachment needs terminal operations | Terminal detection, dimensions, and restoration of terminal state changed by the library.                              |
| [`pgregory.net/rapid`](https://github.com/flyingmutant/rapid)                   | Test-only                                                      | Property and state-machine tests for command sequences, cancellation, closure, subscriptions, and resource accounting. |
| [`github.com/google/go-cmp/cmp`](https://github.com/google/go-cmp)              | Test-only; module `github.com/google/go-cmp`                   | Semantic comparisons and readable diffs for snapshots, linked-window graphs, parsed records, and event sequences.      |

Keep these types behind implementation/test boundaries; the public API continues to use library-owned and standard Go types. Test-only packages must not enter production import paths. Pin released versions compatible with Go 1.27 when implementing, after checking their module requirements and supported platforms; this specification selects packages, not unverified version numbers.
Use `sync.WaitGroup.Go`, channels, mutexes, and contexts for ordinary lifecycle ownership. `x/sync/errgroup` is available from the selected module only where tasks require shared failure cancellation. Weighted semaphores supply admission primitives, not the complete dispatcher: the library still owns coordinated byte/count reservations, acquisition order, cancellation cleanup, and release after draining.
Go's `testing` remains the test runner, and native `testing.F` remains the parser fuzzing tool. Rapid adds structured/model-based exploration; go-cmp adds semantic diffs. tmux codecs and protocol parsing remain library-owned. Logging, CLI/configuration frameworks, and application telemetry belong in consumer modules.

## 4. Server configuration and ownership

```
type Config struct {
    Binary     string
    SocketPath string
    SocketName string
    ConfigFile string
    Env        []string
    Dir        string
    Limits     Limits
}

type Limits struct {
    CommandTimeout time.Duration
    OutputBytes    int64
    InputBytes     int64
    Concurrent     int
}

func New(config Config) (*Server, error)
func (s *Server) Probe(ctx context.Context) (ServerInfo, error)
func (s *Server) Version(ctx context.Context) (Version, error)
func (s *Server) Capabilities(ctx context.Context) (Capabilities, error)
func (s *Server) Kill(ctx context.Context) error
```

`New` validates configuration and captures the effective environment, working directory, and executable selection once. Filesystem resolution is allowed; spawning a process is not. Caller-owned slices are copied. A subprocess `Server` owns no persistent process and has no `Close` method.
Socket selection is deterministic:

1. Explicit `SocketPath` or `SocketName`; supplying both is an error.
2. Otherwise the socket encoded in the captured `TMUX` environment.
3. Otherwise tmux's default socket namespace, with its effective temporary directory frozen.

An explicit path must be absolute. The library must not merge servers based on basename, resolve unrelated names heuristically, or probe another server when the selected socket fails. Format-reported socket spelling may be retained separately from the selection used to connect.
`Env == nil` means capture the current environment; a non-nil empty slice means an explicitly empty environment. Duplicate/malformed environment entries are rejected. `Dir == ""` captures the current working directory. `Binary == ""` selects `tmux` using the captured environment. The library never mutates process-wide environment or working directory.
`ConfigFile` is a startup input only; it does not imply reconfiguring an existing daemon. Querying an absent server does not create one. Starting a session is an explicit creation operation; an existing-only mode must use supported tmux mechanisms rather than a racy check-then-create sequence.

### Defaults

| ResourceDefaultSemantics        |                  |                                                     |
| ------------------------------- | ---------------- | --------------------------------------------------- |
| Captured-command timeout        | 5 seconds        | Includes admission, probes, execution, and decoding |
| Output                          | 4 MiB per stream | Entire logical operation, including internal calls  |
| Input                           | 1 MiB            | Includes encoded request expansion                  |
| Concurrent subprocesses         | 8 per server     | Shared by handles derived from that server          |
| Process/control shutdown budget | 5 seconds        | Failure to finish remains observable                |

Zero configuration fields select defaults; negative or nonsensical values are invalid. Callers may raise finite limits. Earlier caller deadlines win. Interactive attachment and subscriptions have caller-owned lifetimes instead of the captured-command timeout. No hidden unlimited mode.
Separate `Server` instances have separate admission budgets; there is no global registry. The library does not promise to stop tmux-server-side jobs merely by cancelling its own client process.

## 5. Identity, handles, and the tmux graph

```
type SessionID string
type WindowID string
type PaneID string
type ClientName string

func (s *Server) Session(ctx context.Context, id SessionID) (Session, error)
func (s *Server) Window(ctx context.Context, id WindowID) (Window, error)
func (s *Server) Pane(ctx context.Context, id PaneID) (Pane, error)
func (s *Server) FindSession(ctx context.Context, exactName string) (Session, error)

func (p Pane) ID() PaneID
func (p Pane) Info(ctx context.Context) (PaneInfo, error)
func (p Pane) Window(ctx context.Context) (Window, error)
func (w Window) Links(ctx context.Context) ([]WindowLink, error)
func (w Window) ActivePane(ctx context.Context) (Pane, error)
func (s Session) Windows(ctx context.Context) ([]WindowLink, error)
func (l WindowLink) Window() Window
func (l WindowLink) Session() Session
```

ID types validate their grammar at operation boundaries even if explicitly cast from a string. Client names use their own grammar; they are not pane IDs. Lookups by ID and exact name are separate. Ordinary APIs never interpret a name as a prefix, wildcard, index, or current-target expression.
Handles are small values with private identity and a reference to server execution state. Copies are safe. Zero handles return `ErrInvalidHandle`; they never select the current pane. Constructors/queries/lifecycle operations produce valid handles. Comparison uses documented `Equal` methods, not incidental struct equality.
**Windows form a graph.** A window can be linked into multiple sessions. `Window` represents window identity; `WindowLink` represents an observed membership slot: daemon identity, session ID, index, and expected window ID. Its `Info(ctx)` fetches current state only after validating that association; access to its known `Window()` and `Session()` performs no I/O. A link does not silently follow renumbering. Link-specific selection, moving, and unlinking must resolve and validate the expected association in the same daemon/queue as the action. Replaced or renumbered slots return `ErrLinkChanged`; the caller obtains new links through `Session.Windows` or `Window.Links`. There is no ambiguous `window.Session()` or `pane.Session()` choosing an arbitrary parent.
`Current(ctx) (CurrentInfo, error)` is the explicit environment-reading convenience API. `CurrentWithEnv(ctx, env Environment)` accepts captured `TMUX` and `TMUX_PANE` for deterministic use and testing. A malformed environment is an error, absence returns `ErrNotInsideTmux`, and a failed live query is not silently replaced by a successful partial record. `ParseEnvironment` is the separate pure operation for callers that want hints without verification. Parse the documented trailing TMUX fields without assuming the socket path contains no commas. The live pane determines its containing window/session context; do not trust an old session ID from the environment after a pane moves. Client/link context can be unavailable or ambiguous and has explicit presence information.

### Daemon replacement

Socket paths and object IDs can be reused. Materialized handles retain a `ServerIdentity` containing selected endpoint, reported socket, PID, and start time. A control-bound handle also retains its connection generation.

- A normal `Server` selects an endpoint; new lookups may discover a replacement daemon.
- An existing object handle must never silently rebind to that replacement.
- Every handle-based operation, including reads, capture, control attachment, and auxiliary execution, validates its origin against the same answering daemon as the operation. Mutations and target selection must place the identity check in the same tmux command queue entry as the action. A client-side preflight followed by an independent invocation is insufficient.
- On detectable replacement, return `ErrServerChanged`; do not refresh and retry.
- PID/start-time identity is limited by tmux's exposed precision. Document that limitation; it is not a cryptographic identity or a defense against a malicious daemon. An owned control connection supplies stronger lifetime binding.
- Client names may be reused within one daemon. Retain available creation identity and document weaker guarantees when tmux does not expose a sufficiently precise client identity.

Implementing and proving guarded handle operations on every supported version is a release gate. If this cannot be delivered on a particular execution path, reject that path with `ErrTransportUnsupported`; do not silently weaken an existing handle's guarantee. The explicitly endpoint-relative raw API remains available. A handle from another endpoint or daemon is rejected before an operation such as `OpenControl` can attach to a coincidentally equal session ID.

## 6. Reads, records, and snapshots

```
func (s *Server) Sessions(ctx context.Context) ([]SessionInfo, error)
func (s *Server) Windows(ctx context.Context) ([]WindowInfo, error)
func (s *Server) Panes(ctx context.Context) ([]PaneInfo, error)
func (s *Server) Clients(ctx context.Context) ([]ClientInfo, error)
func (s *Server) Snapshot(ctx context.Context) (Snapshot, error)

func (v Snapshot) Sessions() []SessionInfo
func (v Snapshot) Windows() []WindowInfo
func (v Snapshot) Links() []WindowLinkInfo
func (v Snapshot) Panes() []PaneInfo
func (v Snapshot) Clients() []ClientInfo
func (v Snapshot) Pane(id PaneID) (PaneInfo, bool)
func (v Snapshot) ResolvePane(id PaneID) (Pane, bool)
```

`Info` structs are caller-owned data with exported fields: IDs, names, paths, dimensions, commands, timestamps, and relevant state. They do not run commands or update themselves. Slices/maps returned from retained snapshots are copied deeply enough to prevent caller mutation of library state. Go strings may hold non-UTF-8 bytes; the library does not normalize them.
An optional field uses `Value[T]` with `Get() (T, bool)` and `State() ValueState`, where states are `Present`, `Unsupported`, and `Unavailable`. A missing/unsupported field differs from a present empty string, false, or zero. Malformed typed data returns `DecodeError`; it never becomes a zero value or a silently absent value. Unknown fields remain available through immutable raw format data.
Representative record shape:

```
type PaneInfo struct {
    ID             PaneID
    WindowID       WindowID
    Title          string
    CurrentPath    string
    CurrentCommand string
    PID            int
    TTY            string
    Width          int
    Height         int
    Active         bool
    HistorySize    int
    Alternate      bool
    Mode           Value[string]
    Selection      Value[SelectionInfo]
}
```

The checked-in schema defines the remaining fields and each field's presence semantics. Core identity fields must be present. Selection coordinates retain the coordinate system reported by tmux; capture-relative conversion stays an explicit operation. Each record provides `Raw(name) ([]byte, bool)` for copied raw expansions, with absence distinct from empty.
A `Snapshot` contains unique object records plus separate window links. Every fetched record has a pure `Handle()` conversion to its corresponding handle type: session, window, pane, client, or window link. It retains a private copy of the identity captured by the query; editing the record's exported fields never retargets that handle. Manually assembled records have no provenance and return an invalid zero handle. Handles expose `Valid() bool` as a local provenance check, never a liveness query. This conversion must not perform a fresh ID lookup that could bind to a replacement daemon.
`Snapshot.ResolvePane` is a convenience over the same conversion; the pattern applies consistently to other indexed kinds. Records can disappear immediately afterward; subsequent operations can return `ErrNotFound`.
Snapshots report collection start/end and daemon identity. They are bounded observations, not atomic transactions. Daemon replacement during collection is an error. Ordinary graph churn returns a `Consistency` status and bounded missing-reference details; never synthesize relationships or call an incomplete graph complete. Do not automatically retry a continuously changing graph.
Results use a documented stable order: numeric object IDs and, for links, session ID plus index. Clients use their exact name as the sort key. Successful list calls return a non-nil empty slice when empty. An absent server returns `ErrNoServer`; it is not a successful empty listing. Permission and decode failures remain errors.
Local filtering uses ordinary Go loops and `slices` helpers. Live query filtering accepts a distinctly named `Format` expression and executes through tmux. A general query DSL is not required for v1. Iterators, if added for snapshots, must be purely local and not hide subprocesses.

## 7. Creation and ordinary operations

```
type NewSessionOptions struct {
    Name    string
    Dir     string
    Window  string
    Program Program
    Env     map[string]string
    Size    Size
    Start   StartPolicy
}

type NewWindowOptions struct {
    Name    string
    Dir     string
    Program Program
    Env     map[string]string
    Index   *int
    Select  bool
}

type SplitOptions struct {
    Direction Direction
    Size      SplitSize
    Dir       string
    Program   Program
    Env       map[string]string
    Select    bool
}

func (s *Server) NewSession(ctx context.Context, opts NewSessionOptions) (Session, error)
func (s Session) NewWindow(ctx context.Context, opts NewWindowOptions) (WindowLink, error)
func (p Pane) Split(ctx context.Context, opts SplitOptions) (Pane, error)
func (s Session) Rename(ctx context.Context, name string) error
func (p Pane) Kill(ctx context.Context) error
func (w Window) Kill(ctx context.Context) error
func (l WindowLink) Unlink(ctx context.Context) error
func (l WindowLink) Select(ctx context.Context) error
func (w Window) SelectLayout(ctx context.Context, layout Layout) error
```

Creation is detached and does not change the user's selection by default. Selection is an explicit option. `StartPolicy` has `AllowStart` as its zero value and `ExistingOnly` to prohibit daemon startup for `NewSession`. Handle-based window/pane creation always requires its original existing daemon. If a requested startup policy cannot be guaranteed, return `ErrUnsupported` before the requested action.
Use tmux's creation output to return IDs; never discover a new object afterward by name. If creation succeeds but a later decode/inspection fails, report confirmed creation and any known identity through a structured operation error. Never automatically delete the object or repeat creation. On creation errors, a nonzero returned handle is usable only when its complete provenance was verified (`Valid()` is true); otherwise return a zero handle and put any partially known created identity in the error's outcome.
`Window.Kill` affects the shared window; `WindowLink.Unlink` affects one membership. These effects must be prominent in godoc and examples.
Option structs use plain fields when zero is unambiguously the default. Use pointers only when omitted and explicit zero/false differ. `SplitSize` distinguishes cells from percentages and rejects mutually exclusive settings. Optional indexes retain explicit zero. Numeric input is validated before encoding.
Names are literal by default. When tmux would rewrite an unrepresentable name, reject it or return explicitly documented normalized identity; the default typed API must not pretend a different name was preserved. Format expansion is opt-in via a separate `Format` input.

### Programs versus scripts

```
func Exec(name string, args ...string) Program
func Shell(script string) Program
```

`Program{}` requests tmux's default shell. `Exec` means argv semantics; `Shell` deliberately means shell interpretation. Copy program arguments and validate before submission.
The implementation must account for tmux's special treatment of a single program argument. It must not add a dummy argument, silently interpret it as shell code, or reject all ordinary zero-argument executables. For commands/versions that require a shell launcher, use a fixed, reviewed `exec` launcher with positional arguments and preserve argv exactly. Test empty arguments, unusual executable paths, leading dashes, and metacharacters. If exact execution is impossible on a supported path, return `ErrUnsupported` before dispatch.
Library-side execution and tmux-server-side program environments are different. `Config.Env` controls the local client process. Each creation option's `Env` contains overrides applied only to that new session/window/pane's program. Nil and empty maps both mean no overrides; an entry with an empty string explicitly sets an empty value. Copy the map, reject invalid names/NULs, and never mutate the daemon's global environment to emulate a per-program override. Per-program unsetting is outside this initial shape; scoped environment APIs still provide explicit unset/remove operations. Unsupported per-program overrides fail before creation.

### Canonical application flow

This illustrates the intended ergonomics. `tmux` denotes the eventual module's root package; the function uses an already configured server and an existing session. Every external read/write is visible in the code.

```
func openLogs(ctx context.Context, server *tmux.Server) ([]byte, error) {
    session, err := server.FindSession(ctx, "work")
    if err != nil {
        return nil, err
    }
    link, err := session.NewWindow(ctx, tmux.NewWindowOptions{
        Name:    "logs",
        Program: tmux.Exec("tail", "-f", "/var/log/app.log"),
    })
    if err != nil {
        return nil, err
    }
    pane, err := link.Window().ActivePane(ctx)
    if err != nil {
        return nil, err
    }
    return pane.Capture(ctx, tmux.CaptureOptions{JoinWrapped: true})
}
```

This captures what is available now; it does not wait for `tail` to produce output. Capturing failure leaves the explicitly created window in place, as required by the no-implicit-rollback rule.

## 8. Pane input and capture

```
func (p Pane) SendText(ctx context.Context, text string) error
func (p Pane) SendKeys(ctx context.Context, keys ...Key) error
func (p Pane) Submit(ctx context.Context, text string) error
func (p Pane) Capture(ctx context.Context, opts CaptureOptions) ([]byte, error)

type CaptureOptions struct {
    Start          *int
    End            *int
    EntireHistory  bool
    JoinWrapped    bool
    IncludeEscapes bool
    PreserveSpaces bool
    Screen         CaptureScreen
    MaxBytes       int64
}
```

- `SendText` sends literal text and never appends Enter. Reject NUL if the selected mechanism cannot represent it; binary paste is a separate buffer API.
- `SendKeys` sends validated key names, including constants such as `KeyEnter` and `KeyCtrlC`. Empty input is a successful no-op.
- `Submit` sends literal text followed by Enter as one ordered tmux command sequence. It means input submission, not shell-command completion. Multiline text retains its literal newlines and then receives the final Enter.
- Cancellation/overflow during input can leave a partial effect; report uncertainty instead of retrying. Large text must not be silently chunked with undefined ordering.
- `Capture` returns owned bytes. It never trims, decodes, removes ANSI sequences, inserts a title query, or imposes an application-specific line count.
- Zero capture options request the visible active screen. `Start`/`End` retain explicit zero; `EntireHistory` conflicts with `Start` and requests the beginning of available history. Negative coordinates refer to history. Screen choices include current, alternate, and mode screen where supported.
- ANSI preservation, wrapped-line joining, and space preservation are explicit. Unsupported combinations fail before dispatch. Byte limits apply to the result even when a line count is specified. `MaxBytes == 0` inherits the configured limit; a positive value can tighten it. Raising the capture ceiling requires configuring the server's limit.
- Capture is a screen/history observation; event output is a stream of terminal bytes. Neither implies the other can be reconstructed without a terminal model.

anno computes selection bounds and quote confidence outside this package. aht chooses its capture depth outside this package. sesh decides whether a submitted shell command should be awaited outside this package.

## 9. Formats, codecs, and raw commands

Three distinct codecs are required: process argv, tmux command text, and tmux format/output. Shell quoting is a fourth domain, used only by explicitly shell-capable operations.
Typed APIs must correctly represent literal semicolons, backslashes, quotes, newlines, dollar signs, leading dashes, and `#{...}` without turning data into commands or unintended expansions. A direct argv call avoids an intervening shell but still encounters tmux's parser.
Metadata queries use one versioned codec. Do not assume a tab, unit separator, or fixed printable marker cannot occur in a field. Use tmux-supported escaping with a parser proved against real output; preserve record boundaries, empty fields, raw bytes, and unknown fields. Each supported version must pass round-trip fixtures for the selected dialect. No permanent “try several parsers until one works” fallback.

```
type Command struct { /* immutable name and literal arguments */ }
type Result struct {
    Stdout   []byte
    Stderr   []byte
    ExitCode int // -1 if no process exit status exists
}

func NewCommand(name string, args ...string) (Command, error)
func (s *Server) Run(ctx context.Context, cmd Command) (Result, error)
func (p Pane) Format(ctx context.Context, expr Format) ([]byte, error)
```

`Command` is one tmux command with arguments, not a shell line or a batch. The command name must be one nonempty command token, not a global flag. Server arguments cannot be injected into it. Arguments are encoded as literal tmux arguments; commands such as `run-shell`, `if-shell`, and `source-file` can still deliberately interpret their operands according to tmux semantics.
`Run` preserves bounded stdout/stderr on failure and returns an error for nonzero process status or a tmux control `%error`. A control response has no OS exit status for the command; never invent exit code 1 to conceal that distinction. Raw commands are endpoint-relative and cannot provide inferred object-generation protection. Handle-based typed APIs provide the stronger contract in section 5.
Custom anno provider commands use `Run` and a separate extension package. The core must not claim undocumented fork commands are portable stock tmux features.

## 10. Options, hooks, buffers, and bindings

Provide generated, scope-specific APIs for known options, plus raw user-option access. Example shape:

```
func (s Session) Options() SessionOptions
func (s *Server) GlobalSessionOptions() SessionOptions
func (w Window) Options() WindowOptions
func (p Pane) Options() PaneOptions

func (o SessionOptions) HistoryLimit(ctx context.Context) (OptionValue[int], error)
func (o SessionOptions) SetHistoryLimit(ctx context.Context, value int) error
func (o PaneOptions) User(ctx context.Context, name string) (OptionValue[string], error)
func (o PaneOptions) SetUser(ctx context.Context, name, value string) error
func (o PaneOptions) UnsetUser(ctx context.Context, name string) error
```

The value contract is explicit:

```
type OptionValue[T any] struct {
    Local     Value[T]
    Effective Value[T]
    Origin    Value[Scope]
}
```

For an inherited value, `Local` is unavailable and `Effective` is present. For an absent user option, both are unavailable. For an unsupported known option, both are unsupported. `Origin` is unavailable when it cannot be established; never guess. An empty value is not unset. Separate queries used for local/effective/origin are observational, not an atomic read; a contradictory result must be marked inconclusive or returned as a consistency error, never silently reconciled.
Require `UnsetHistoryLimit` and corresponding typed unset methods wherever tmux supports restoring inheritance/defaults. All scopes have explicit accessors: `Server.Options`, `GlobalSessionOptions`, `GlobalWindowOptions`, `Session.Options`, `Window.Options`, and `Pane.Options`. Resolve inheritance using actual tmux scope rules; do not infer it from a guessed prefix. Known option enums preserve unknown read values while typed setters reject unsupported values.
Sparse arrays retain indexes and holes. Updating several entries returns confirmed partial progress on failure and does not promise rollback. Hooks retain scope, indexes, and parsed command payloads; setting a hook must not execute it eagerly.
Bindings use a key-table type, a key, and typed command sequences. Read APIs preserve commands the library cannot semantically decode. The library does not install application key bindings automatically.
Buffer APIs distinguish named buffers and automatic buffers. Read/write accept bytes, including NUL where tmux permits it. Binary input uses a bounded stdin-capable subprocess path; a control-bound operation must either use an explicitly selected, same-daemon auxiliary process with verified semantics or return `ErrTransportUnsupported`. Never silently submit binary data as key names or shell syntax.
Clipboard integration with the host OS belongs to the application. tmux's own buffer/clipboard commands remain available.

## 11. Persistent control connections

```
type ControlOptions struct {
    PaneOutput  bool
    QueueDepth  int
    QueuedBytes int64
    FrameBytes  int64
    EventBytes  int64
    MaxStreams  int
}

func (s *Server) OpenControl(ctx context.Context, session Session, opts ControlOptions) (*Connection, error)
func (c *Connection) Server() *Server
func (c *Connection) Close() error
func (c *Connection) Wait(ctx context.Context) error
```

`OpenControl` attaches to an explicitly identified existing session; it never creates a scratch session, detaches another client, or reconnects automatically. Its context owns the connection lifetime, including startup. Calls through `Connection.Server()` use that exact connection. Methods on the original server continue using subprocesses.
A connection is an actual tmux client. Default to ignoring its terminal size and not receiving pane output; do not claim it is invisible to attached-client counts or hooks. Requesting pane output is explicit. Only operations supported on that transport are available; unsupported UI/stdin operations return `ErrTransportUnsupported` unless the caller explicitly chooses a supported auxiliary path.

### Dispatcher contract

- One owner writes the control stream; one owner reads it. Callers do not write pipes.
- Maintain a bounded admission queue: default 64 operations and 8 MiB of total retained encoded request bytes, including the in-flight/draining operation. Admission waits observe caller cancellation. Do not allocate or retain an expanded library-owned request before it has byte and count reservations. Caller-owned command values are outside that internal queue budget. Only one externally submitted logical operation is in flight in the initial implementation; future pipelining requires independent proof.
- Decode the startup handshake before resolving user requests. Match full response framing; do not assume command numbers start at zero or increase by one per Go call.
- Model commands that insert nested commands, produce multiple blocks, or exit the client. Known typed operations declare their framing/completion policy. Raw commands with ambiguous control framing are rejected before writing with `ErrTransportUnsupported`; subprocess execution remains available explicitly.
- Each operation's budget includes queue wait and execution. A result's successful completion means tmux acknowledged it, not that a program launched inside a pane finished.
- Unknown event types are preserved as bounded `UnknownEvent` values. A decoded frame has a separate 4 MiB default ceiling; bound encoded overhead as well. Invalid framing, malformed escapes, or oversized frames fail the connection rather than guessing the next boundary. Each supported output-producing command must pass adversarial delimiter-shaped-output tests proving unambiguous byte-preserving framing. Where that is impossible, require an explicitly selected auxiliary path or `ErrTransportUnsupported`; one request in flight alone does not prove frame safety.
- A failed connection fails all pending calls. It never sends uncertain mutations to a replacement process.

### Cancellation and shutdown

Before dispatch, cancellation reports `NotSent`. After dispatch, cancellation stops waiting but cannot retract the command. Its slot remains reserved while the reader drains the response under a bounded recovery budget; if synchronization cannot be restored, close the connection and fail pending operations. A late response cannot satisfy a different request.
`Close` is concurrency-safe and idempotent. It rejects new work, terminates the owned control client, closes pipes, resolves waiters, and waits for owned goroutines/process reaping within five seconds. It never directly signals the daemon or sends destructive server/session/pane commands. Ordinary client departure may still trigger the user's configured detach hooks or automatic lifecycle policies; the library does not guarantee those programs survive arbitrary server configuration. If cleanup is incomplete, return `ErrShutdownIncomplete`; `Wait(ctx)` remains available to observe eventual completion. A nil result from `Close` guarantees owned work is finished. Preserve independent teardown errors.

## 12. Events and subscriptions

```
type EventOptions struct {
    MaxBytes int64
    MaxCount int
    Overflow OverflowPolicy
}

func (c *Connection) Events(ctx context.Context, opts EventOptions) (*EventStream, error)
func (s *EventStream) Next(ctx context.Context) (Event, error)
func (s *EventStream) Close() error
```

`Event` is a closed interface implemented by concrete event values: pane output, layout change, session/window changes, client changes, subscription updates, and unknown notifications. The raw event name remains available. Event objects own immutable/caller-owned payloads; they do not alias a reused parser buffer.
Each stream has bounded count and byte capacity, default 256 events and 4 MiB. `EventOptions` controls only that stream's queue. `ControlOptions.EventBytes` is a connection-wide reservation ceiling, default 32 MiB, and `MaxStreams` defaults to eight. Opening a stream reserves its full queue budget; a reservation that cannot fit fails immediately with `ErrResourceLimit`. Release reservations on terminal closure. Parser frame limits are independent; one subscriber cannot increase them.
`Next` supports one reader at a time; concurrent reads are rejected. Cancelling one `Next` does not consume an event. The stream's creation context or `Close` ends its lifetime. Stream closure never closes the connection. After explicit normal closure, `Next` returns `io.EOF`. After overflow, connection failure, or creation-context cancellation, every subsequent `Next` returns the retained terminal error; clearing payloads does not erase the failure. A cancelled individual read returns its context error and leaves the stream usable.
The default overflow policy terminates the affected stream with `ErrEventsLost` and clears its queued payloads. This failure remains observable even if no later event arrives. Other streams and command responses continue. An explicit lossy mode may retain recent events, but MUST expose loss counters and a gap notification; silently dropping state changes is forbidden.
No callbacks execute on the protocol reader. Pane output is delivered as bytes. Notifications are ordered as observed on that connection; there is no promised total order across connections, subprocess reads, or snapshots. Format subscriptions expose their tmux-defined cadence, not a promise to see every intermediate value.
A helper claiming a synchronized initial snapshot plus events must define its race reconciliation algorithm and pass adversarial tests. v1 does not label a simple subscribe-then-snapshot sequence atomic or lossless. Consumers invalidate and refresh after gaps.

## 13. Errors and effect uncertainty

Use ordinary Go errors with `errors.Is`, `errors.As`, and `Unwrap`. No logging side effects. Error text is useful to a developer but does not automatically include argument values, captured contents, or environment values; bounded raw diagnostics are available explicitly.
Required classifications:

| ErrorMeaning                              |                                                                        |
| ----------------------------------------- | ---------------------------------------------------------------------- |
| `ErrNotInsideTmux`                        | No environment context exists for an explicit current-context lookup   |
| `ErrNoServer`                             | Selected endpoint has no answering tmux daemon                         |
| `ErrNotFound`                             | Requested object is absent on the answering daemon                     |
| `ErrAmbiguousTarget`                      | A requested lookup cannot select exactly one entity                    |
| `ErrInvalidHandle` / `ErrInvalidArgument` | Invalid local input; no requested effect dispatched                    |
| `ErrServerChanged`                        | Materialized daemon identity no longer matches                         |
| `ErrLinkChanged`                          | Observed session/window membership slot was replaced or renumbered     |
| `ErrUnsupported`                          | Connected version cannot perform the requested semantics               |
| `ErrTransportUnsupported`                 | Selected execution path cannot perform them                            |
| `ErrOutputLimit` / `ErrInputLimit`        | A finite byte budget was exceeded                                      |
| `ErrResourceLimit`                        | A requested queue/stream reservation cannot fit its configured ceiling |
| `ErrProtocol`                             | Invalid framing or data that prevents safe interpretation              |
| `ErrEventsLost`                           | A subscription cannot claim uninterrupted delivery                     |
| `ErrClosed`                               | Connection or stream no longer accepts work                            |
| `ErrShutdownIncomplete`                   | Owned work did not finish within shutdown budget                       |

Permission failures must not become `ErrNoServer`. Classification of tmux stderr is command/version-aware and conservative: unrecognized failures remain `CommandError`. Preserve caller cancellation/deadlines through the error chain. Record whether a timeout came from the caller or library budget.
`CommandError`, `DecodeError`, `UnsupportedError`, and `OperationError` carry structured details. Effect knowledge is independent of error kind:

- `NotSent`: the requested action was never dispatched.
- `Unknown`: it may have run or partially run; acknowledgement is insufficient or missing.
- `Confirmed`: tmux acknowledged completion; later decoding/inspection failed.

Every failed typed operation exposes an `OperationError` with an `Outcome`; raw execution failures expose the same outcome through `CommandError`. Wrapping preserves the original validation, context, command, and decode classifications.

```
type Outcome struct {
    Effect   Effect
    Steps    []StepOutcome
    Created  []CreatedObject
}

type StepOutcome struct {
    Index  int
    Effect Effect
}

type OperationError struct {
    Operation string
    Outcome   Outcome
    Err       error
}
```

`CreatedObject` retains kind, raw ID, and any verified daemon/link identity, with explicit availability for fields not recovered. `Steps` is populated only where individual acknowledgements are actually known, using indexes in the documented logical operation order. The aggregate may be `Unknown` while earlier steps are `Confirmed` and later undispatched steps are `NotSent`. Example: setting three sparse-array entries may confirm entries 0 and 1, lose acknowledgement for entry 2, and report that precise prefix. A creation acknowledged before its metadata fails decoding reports `Confirmed` plus the recovered ID; it does not report `NotSent` merely because the returned handle is invalid.
On error, capture/raw APIs return any bounded bytes already obtained together with the error; no partial buffer is a successful full result. List/snapshot APIs return no usable partial data for transport or decode failure, while ordinary graph churn follows the successful snapshot consistency contract. Multi-step APIs with confirmed partial effects document their partial result fields explicitly.
No generic `Retryable` boolean. Whether replay is acceptable depends on the application and operation. Killing a local tmux client, exceeding output bounds, or losing a connection does not prove the server did nothing. Probe commands can run before a requested action remains `NotSent`.

## 14. Command sequences and interactive operations

Ordinary Go calls are the primary API. Calls from different goroutines have no guaranteed relative order. The library may serialize a control connection internally but does not expose incidental goroutine scheduling as an ordering contract.
Two explicitly distinct composition mechanisms are supported:

1. `RunSequence(ctx, []Command)` uses one tmux command sequence and returns combined output. It does not promise per-command results, exclusivity against other clients, or rollback. On failure, report uncertainty for the sequence when tmux does not identify the failed step.
2. A future typed `Plan` may record dependent operations and preserve per-step results. It is not a prerequisite for the initial migration. If delivered, it must share command builders with direct methods, report confirmed/unknown/skipped steps, and never call itself a transaction.

`Submit` is a narrow, required sequence with fixed semantics. Hooks, bindings, menus, and conditional commands need a structured command-sequence encoder even if a general public planner is deferred.
Interactive attach has an explicit terminal contract: caller-supplied `*os.File` terminal streams, caller context, no default five-second operation timeout, and no arbitrary uninterruptible `io.Reader` hidden behind a goroutine. Use `golang.org/x/term` for terminal detection, dimensions, and state restoration where the library needs those operations. Let tmux own terminal setup where it already does; the library restores only state it changes. Expose a prepared `*exec.Cmd` only through a clearly named advanced method whose caller owns execution, cancellation, and waiting.
Distinguish attaching the caller's terminal from switching an existing tmux client. UI operations target an explicit `Client` or verified current client context. Do not pick an arbitrary attached client when none is identified.
Popup/menu/prompt APIs state whether return means scheduled, displayed, dismissed, or completed. A plain successful command result never claims a user made a choice. Waiting UI operations require a caller deadline and have cancellation behavior documented for both the local waiter and any remaining server-side UI. Do not use control mode for an interaction that requires a terminal-facing client.

## 15. Coverage and compatibility

Support stock tmux 3.6 and above. Initial compatibility fixtures: 3.6, 3.6a, 3.6b, and 3.7c. Test the exact 3.6 floor as well as subsequent releases; passing on a patched release alone does not establish support for 3.6. Add newer stable releases to the tested matrix as they become available. This is a proposed support commitment, not a claim that tests already pass. [Official releases](https://github.com/tmux/tmux/releases)
Recognized versions below 3.6 return `ErrUnsupported` before executing the requested operation; discovery/version probes may run to establish that result. Do not implement compatibility shims, parser dialects, or flag fallbacks solely for pre-3.6 releases. Version gates within the supported range remain necessary for features introduced after 3.6.
The release ledger must enumerate commands and flags from the pinned release manuals and `list-commands`, not from an unversioned web manual alone. Each entry records public symbol, minimum version, execution path, completion semantics, test, and any deliberate raw-only support. Missing ledger entries fail generation/checks.

| FamilyRequired typed scope |                                                                                      |
| -------------------------- | ------------------------------------------------------------------------------------ |
| Server                     | Selection, probe/version/capabilities, lifecycle, environment, config loading        |
| Sessions                   | Create/list/find/rename/kill, groups, attach/switch, session options                 |
| Windows and links          | Create/list/rename/link/unlink/move/swap/kill, index handling, layouts               |
| Panes                      | Split/join/break/move/swap/resize/select/respawn/kill, titles, capture, pipe         |
| Input and modes            | Literal text, key names, copy-mode entry/actions, selection information              |
| Clients                    | List/inspect/switch/detach, size, relevant flags and refresh                         |
| Options                    | Server/global/session/window/pane scopes, inheritance, user options, arrays          |
| Hooks and bindings         | Scope, indexed hooks, key tables, bind/unbind/list                                   |
| Buffers                    | Named/automatic buffers, load/save/read/write/delete/paste, byte semantics           |
| UI                         | Message, popup, menu, prompts, choose/display modes, explicit completion model       |
| Coordination               | Wait-for/signalling/locking, ordered command sequences                               |
| Control                    | Connection ownership, notifications, pane output, format subscriptions, flow control |
| Advanced                   | Raw commands/formats; explicit fork adapters; version-gated newly added primitives   |

For each family, “typed scope” requires meaningful option/flag coverage, not one convenience method. Newer features such as floating panes are included only where the pinned release ledger proves availability. Interactive or deeply diagnostic commands may be raw-only only with a stated reason; required migration operations cannot use that exemption.
Version parsing preserves raw strings and vendor suffixes. Unknown development/vendor versions are not automatically greater than every stable release. Prefer non-mutating feature discovery where available; never mutate a user's server to probe capability. Cache capabilities by daemon identity, not merely executable path or socket name. Unknown support fails explicitly for typed operations that depend on it; raw access remains available.
Linux and macOS are release platforms. FreeBSD is a supported target only after its real-tmux jobs exist. Other platforms may compile but must return a clear unsupported-platform error for unavailable operations. WSL follows the Linux environment it runs in. `CGO_ENABLED=0` builds must succeed.

## 16. Tests and measurable quality gates

### Public contract tests

- External-package tests exercise the API a consumer imports.
- Examples become compiled `Example` tests as soon as implementation starts. No documentation code that silently drifts from exported signatures.
- Compare semantic results for equivalent subprocess/control operations. Use `github.com/google/go-cmp/cmp` for nontrivial record, graph, and event comparisons with readable diffs. Compare bytes exactly for capture and buffers; comparison options must not conceal differences in identity, ordering, presence, or byte contents.
- Verify no-server versus denied socket, exact targets, linked windows, explicit index zero, and unavailable versus empty/zero fields.
- Verify recognized pre-3.6 versions are rejected before the requested operation, while tmux 3.6 passes the baseline contracts.
- Replace a daemon on the same socket between lookup and mutation; assert the replacement's pane is untouched. Exercise identity collision limitations separately.
- Kill objects during snapshot collection and prove incomplete graphs are reported accurately.
- Verify failed creation follow-up preserves known created identity without duplicate creation or automatic deletion.

### Parser and process tests

- Fuzz argv/control encoding and format/event decoding with bounds, using native `testing.F`.
- Use `pgregory.net/rapid` to generate structured command sequences and model dispatcher/subscription state across admission, dispatch, cancellation, late responses, overflow, and closure. Assert byte/count conservation, correct response ownership, and terminal-state invariants. Keep model tests deterministic and bounded; retain minimized failures as named regressions.
- Include tabs, LF/CRLF, quotes, backslashes, dollar signs, semicolons, braces, format tokens, empty fields, invalid UTF-8, and buffer NULs.
- A stand-in executable records argv/environment and exercises timeout, large stdout/stderr, partial reads, startup failures, and inherited-pipe teardown.
- Control tests cover startup frames, unrelated notifications, fragmented reads, multiple/nested responses, command-number gaps, late replies, malformed blocks, queue exhaustion, and a silent stalled peer.
- After cancellation, prove the next command cannot receive the previous command's result.
- Under slow event consumption, command completion stays responsive and overflow is observable without needing another event.
- Race tests cover concurrent calls/close, subscription closure, repeated shutdown, and callers abandoning reads.

### Real tmux fixtures

`tmuxtest.NewServer(t testing.TB)` starts an owned server on a short absolute private `-S` path with a minimal explicit configuration. It returns a usable server handle and registers cleanup immediately. Fixture startup errors fail the test; a missing tmux executable can be skipped locally but MUST fail a required integration CI job.
Each test owns its daemon; no global environment changes or shared server. Close clients before killing the daemon. Cleanup uses a fresh finite context, waits for owned processes, and reports retained artifacts on failure. Never signal an unverified, potentially reused PID. Never touch the developer's current socket. Integrations verify empty configuration, environmental isolation, and cleanup itself.

### CI and performance

Required commands include `go test ./...`, `go test -race ./...`, `go test -tags=integration ./...`, `go vet ./...`, generated-file checks, and clean consumer builds with `GOWORK=off`. Run real integration/race subsets on Linux and macOS and the pinned tmux matrix. A declared support lane cannot pass by skipping every relevant test.
Use `b.Loop` benchmarks for decoding, 1/100/1000-pane snapshots, repeated captures, control request throughput, cancellation under load, and event saturation. Record allocations, peak retained memory, subprocess count, and latency distributions. Benchmarks verify successful behavior before measuring it. Compare repeated runs; do not publish an unsupported universal speedup claim.
Structural budgets are release gates: one snapshot must not spawn one process per pane; control operations must not spawn a process per supported command; queue/event memory remains within declared caps; close completes or explicitly reports incomplete shutdown. Large-server queries must request needed fields in batches.

## 17. Documentation, release policy, and migration

Godoc must answer: does this method perform I/O, what does it target, can it change user focus, what does success acknowledge, what survives cancellation, and who owns returned data/resources? Include executable examples for inspect/capture, create/split, options, events, and explicit raw fork commands.
Ship a command-to-Go index, compatibility ledger, error guide, ownership guide, and a compact migration guide. Avoid copying the entire tmux manual into generated comments; link each operation to its command and explain the Go-specific contract.
Alpha releases may change the API with migration notes. v1 freezes observable contracts: defaults, targeting, byte preservation, error identities, and ownership, in addition to exported symbols. Publish tagged dependencies; released consumers must not require local `replace` directives. No plugin system, global registry, or daemon is required to use the library.
Migration proceeds in vertical slices:

1. **Foundation:** execution, identity, inspection, codecs, capture, and isolated test server. Migrate one real path from each consumer and port its regressions.
2. **Shared operations:** sesh lifecycle/navigation/targets and anno keys/options/buffers/UI. Remove replaced execution paths rather than retaining parallel implementations.
3. **Control:** connection/event implementation with protocol and shutdown gates; migrate only consumers that benefit from live events or repeated commands.
4. **Completeness:** finish typed coverage ledger, cross-version/platform tests, and public docs. Reach v1 only after all gates pass.

| ConsumerLibrary responsibilityApplication responsibility retained |                                                                                        |                                                                                                            |
| ----------------------------------------------------------------- | -------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| aht                                                               | Per-server pane queries, context, capture, interrupt, process execution                | Server discovery strategy/process inspection, agent registry and lifecycle inference, capture-depth policy |
| sesh                                                              | Targets, lifecycle, graph records, navigation primitives, options, attach, capture     | Snapshot persistence/restore plans, remote protocol, autosave/systemd, attention/history policy            |
| anno                                                              | Context fields, mode information, capture primitives, keys/options/buffers/UI commands | Selection anchoring, quote matching/confidence, annotations, staging, custom provider semantics            |

Keep existing public aht contracts through adapters during migration. Distinguish intentionally changed semantics from accidental changes: old trimming, timeout, nil/empty results, and missing-server behavior need explicit consumer decisions and tests. Moving code alone is not completion; each migrated concern has one implementation owner.

## 18. Initial implementation ledger and consumer acceptance

This table fixes the initial typed surface at the command-family level. Before a slice is implemented, its ledger must expand each listed command into the flags present in the pinned manuals, with exact option field, supported versions/transports, completion contract, and test IDs. That checked-in expansion is reviewed as an implementation artifact; the prose below is not a claim of exhaustive flag parity. Commands in the required consumer flows cannot be downgraded to raw-only to declare the migration finished.

| tmux command(s)Go owner / operationRequired slice                                                          |                                                          |                   |
| ---------------------------------------------------------------------------------------------------------- | -------------------------------------------------------- | ----------------- |
| `display-message -p`, `list-sessions`, `list-windows`, `list-panes`, `list-clients`                        | `Info`, `Format`, lists, `Snapshot`, `Current`           | Foundation        |
| `capture-pane`                                                                                             | `Pane.Capture`                                           | Foundation        |
| `send-keys`                                                                                                | `Pane.SendText`, `SendKeys`, `Submit`, mode actions      | Foundation/shared |
| `new-session`, `has-session`, `rename-session`, `kill-session`                                             | `Server.NewSession`, exact lookup, `Session.Rename/Kill` | Shared            |
| `new-window`, `rename-window`, `kill-window`                                                               | `Session.NewWindow`, `Window.Rename/Kill`                | Shared            |
| `link-window`, `unlink-window`, `move-window`, `swap-window`                                               | `Window.Link`, `WindowLink.Unlink/Move/Swap`             | Shared            |
| `split-window`, `join-pane`, `break-pane`, `swap-pane`, `kill-pane`                                        | `Pane.Split/Join/Break/Swap/Kill`                        | Shared            |
| `select-pane`, `select-window`, `last-pane`, `last-window`                                                 | Explicit pane/link/client selection                      | Shared            |
| `resize-pane`, `resize-window`, `select-layout`                                                            | Pane/window sizing and layout methods                    | Shared            |
| `respawn-pane`, `respawn-window`                                                                           | `Respawn` with explicit `Program`                        | Shared            |
| `show-options`, `set-option`, `show-window-options`, `set-window-option`                                   | Scoped typed/raw options and inheritance                 | Shared            |
| `show-environment`, `set-environment`                                                                      | Scoped environment APIs                                  | Shared            |
| `list-buffers`, `load-buffer`, `save-buffer`, `show-buffer`, `set-buffer`, `delete-buffer`, `paste-buffer` | Server buffers and pane paste                            | Shared            |
| `copy-mode`, `send-keys -X`                                                                                | Pane mode and selection operations                       | Shared            |
| `display-popup`, `display-menu`, `command-prompt`, `display-message`                                       | Explicit client UI operations                            | Shared            |
| `attach-session`, `switch-client`, `detach-client`                                                         | Terminal attachment versus existing-client methods       | Shared            |
| `set-hook`, `show-hooks`, `bind-key`, `unbind-key`, `list-keys`                                            | Scoped hooks/key-table APIs                              | Completeness      |
| `wait-for`, `run-shell`, `if-shell`, `source-file`, `pipe-pane`                                            | Coordination, explicit scripts/config/pipes              | Completeness      |
| `refresh-client`, control attach and notifications                                                         | Owned `Connection`, events, format watches               | Control           |
| Remaining commands/flags in supported releases                                                             | Named typed operation or justified raw-only ledger entry | Completeness      |

Consumer gates are observable scenarios, ported from the current implementations and their tests:

| IDConsumer and scenarioRequired result |                                                                     |                                                                                         |
| -------------------------------------- | ------------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| AHT-1                                  | Two servers containing the same pane ID                             | Inspection/capture/interrupt reach only the selected server                             |
| AHT-2                                  | Names/paths containing tabs, quotes, dollar signs and line breaks   | Metadata round-trips without shifting columns or records                                |
| AHT-3                                  | Missing live context query with environment hints present           | Library reports the live failure; aht deliberately applies its existing fallback policy |
| AHT-4                                  | Capture with a caller-selected line budget and interrupted command  | Capture policy remains in aht; cancellation/limits are preserved                        |
| SESH-1                                 | Exact session name versus a matching prefix and explicit index zero | Intended session/window is selected without fuzzy resolution                            |
| SESH-2                                 | A window linked into two sessions, then renumbered                  | Snapshot records both links; stale slot operations cannot act on a replacement          |
| SESH-3                                 | Create/split/layout/send during restore, failing midway             | No repeated mutations; confirmed progress and surviving objects are observable          |
| SESH-4                                 | Absent, denied, and malformed server socket                         | Distinct outcomes; only the application decides whether to create a session             |
| SESH-5                                 | Popup selection, switch existing client, and terminal attach        | Correct client targeted; completion semantics and terminal ownership preserved          |
| ANNO-1                                 | Selection inside scrollback, alternate screen, or copy mode         | Accurate mode/context fields and capture bytes; quote policy stays in anno              |
| ANNO-2                                 | Global/window/pane user-option inheritance and explicit empty value | Origin/presence distinguished; unsetting restores the intended inheritance              |
| ANNO-3                                 | Literal paste containing tmux/shell metacharacters                  | Same literal text, no unintended key or command interpretation                          |
| ANNO-4                                 | Custom provider command with unknown response fields                | Raw invocation reaches the selected endpoint; provider parsing stays outside core       |
| ALL-1                                  | Daemon restarted at the same socket before a handle operation       | Detectable replacement is rejected and replacement objects remain untouched             |
| ALL-2                                  | Cancelled in-flight control request followed by a new request       | Late output cannot be attributed to the new request                                     |
| ALL-3                                  | Saturated event subscription during normal command traffic          | Bounded memory, explicit subscriber loss, responsive command replies                    |

The implementation records the exact migrated file/test for each gate. A gate passes only with executable evidence and deletion or intentional adaptation of the replaced responsibility. Existing application behavior may be preserved through thin policy adapters; a second command encoder/parser is not such an adapter.

## 19. Research and design provenance

These sources inform the design; their claims do not substitute for this library's acceptance tests.

- [tmux command and protocol manual](https://man.openbsd.org/tmux.1): authoritative command behavior; use version-pinned source manuals for implementation.
- [tmux control-mode documentation](https://github.com/tmux/tmux/wiki/Control-Mode): existing command language plus framed results and notifications.
- [Python libtmux architecture](https://libtmux.git-pull.com/topics/architecture/): approachable object vocabulary and stable-ID navigation.
- [Python libtmux testing](https://libtmux.git-pull.com/api/testing/pytest-plugin/): test fixtures as part of a usable library.
- [Rust tmux\_interface](https://docs.rs/tmux_interface/latest/tmux_interface/): command construction independent of execution.
- [libtmux-go design](https://github.com/libtmux/libtmux-go/blob/master/DESIGN.md): explicit fetched state, relationships, and compatibility evidence.
- [gotmucks](https://github.com/counterflow/gotmucks): argument/output edge cases and bounded event delivery.
- [gotmuxcc](https://github.com/atomicstack/gotmuxcc): separate command building, response routing, and transport ownership.

The deliberate choices here are a handle/Info split, explicit transport selection, one initial in-flight control operation, strict list errors, finite resource budgets, and comprehensive typed coverage driven by three applications. They are proposed contracts, not claims that any cited library implements this exact combination.