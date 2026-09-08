# Consumer migration guide

No consumer migration is claimed in this archive. These are concrete mapping
instructions for the next integration step, not records of changes already made.

## aht

Replace shared subprocess/format/capture helpers with one configured Server per
selected endpoint, batched `Panes`/`Snapshot` reads, `CurrentWithEnv`, and handle
`Capture`/`SendKeys(KeyCtrlC)`. Keep process discovery, registry policy, lifecycle
inference, capture depth, and any legacy fallback outside the library. A failed
live context read remains an error; aht must deliberately choose its fallback.

Acceptance still requires porting the actual AHT-1 through AHT-4 tests, testing
nontrivial names/paths on real tmux, and removing the replaced command codec and
process owner. This archive does not identify migrated consumer files because
none have been migrated.

## sesh

Use exact `FindSession`, stable-ID handles, `WindowLink` membership slots,
explicit index pointers, and lifecycle operations. Keep persistence, restore
plans, selection policy, history, remote protocol, autosave, and systemd in sesh.
Treat restore failures as partial progress: do not replay successful creations.
Refresh links after renumbering instead of trying to reuse old observed slots.

SESH-1 through SESH-5 remain consumer gates. Generation-protected terminal attach
is still missing; do not silently replace it with `PrepareAttach` and call that
equivalent. That method is an explicitly weaker advanced endpoint-relative API.

## anno

Use fetched selection/mode fields, exact capture bytes, keys, scoped options,
buffers, and explicit-client UI builders. Keep quote anchoring/confidence,
annotations, staging, persistence, and custom-provider parsing in anno.
Custom provider commands belong in an extension/adapter using `Run`; they are
not portable stock tmux commands.

ANNO-1 through ANNO-4 require the actual consumer tests. Inherited origin is
explicitly unavailable when not proven; do not replace that state with a guessed
scope. Control-only reads that need binary or unframed output require explicit
same-daemon auxiliary execution, not hidden fallback.

## Shared acceptance records

The machine-readable `internal/schema/acceptance.json` records every supplied
consumer gate as not migrated. Some library-side scenarios exist in
`integration_test.go`; those scenarios have not run against real tmux here and
are not a substitute for deletion/adaptation of consumer-owned code.

When migrating, retain a file/test mapping, the exact daemon version and platform,
and the removal or intentional policy adaptation of each previous responsibility.
Decide explicitly on old trimming, nil/empty results, missing-server behavior,
timeouts, and shell-completion waiting. A second command encoder is not a thin
policy adapter.
