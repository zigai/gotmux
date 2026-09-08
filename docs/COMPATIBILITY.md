# Compatibility, codec, and dependency ledger

The intended floor is tmux 3.6. The source-derived matrix is 3.6, 3.6a, 3.6b,
and 3.7c. **None of those real-server lanes ran in the delivery container.**
The implementation does not claim FreeBSD support. Unsupported operating-system
operations return `ErrUnsupported`; cgo-free builds are intended.

Stable version parsing retains raw text and vendor suffixes. Unknown/vendor
versions do not automatically compare greater than stable releases. Typed probes
reject unsupported/unknown versions. Startup performs an executable probe and a
queue-local stable-version condition against the actual answering daemon, so a
concurrent start cannot silently substitute a recognized older server. Raw
execution currently has a documented version-gate exception in STATUS.md.

## Wire dialect

Metadata uses `TGO1:<field-count>:<length>:<value>,...\n`. Each field length is
emitted using tmux's byte-counting `n:` modifier. The decoder reads exact byte
lengths, not tabs, unit separators, or delimiter-shaped lines. It preserves empty
fields, CR/LF, invalid UTF-8, and extra requested fields. Oversized lengths,
noncanonical lengths, excessive field counts, and malformed records fail closed.

The same record framing is used inside control response blocks, so a metadata
value containing `%end`, `%begin`, or completion-looking text is not a response
boundary. Ordinary raw output is not presumed safe merely because only one call
is in flight. Unsupported output-producing paths reject control transport.

Process argv handling is separate from tmux command-text encoding. The trailing
semicolon rule in tmux's argv parser is handled explicitly. Command-text operands
are quoted with fixed octal escapes for special bytes; nested command sequences
are structurally encoded. Format literals use a literal hash expansion rather
than assuming shell quoting or `##` handles every style-prefix case.

Control headers match the complete `(timestamp, command-number, flags)` triple.
Malformed framing, invalid output escapes, and oversized frames fail the whole
connection. Unrecognized notifications remain bounded unknown events when they
can be represented unambiguously by the stock line protocol.

## Runtime dependencies

| Module | Pin | Role | Declared upstream Go floor checked during implementation |
| --- | --- | --- | --- |
| `golang.org/x/sync` | v0.17.0 | Weighted count/byte admission | 1.24 |
| `golang.org/x/term` | v0.35.0 | Terminal validation and dimensions | 1.24 |
| `golang.org/x/sys` | v0.36.0 | Indirect platform dependency of x/term | 1.24 |
| `pgregory.net/rapid` | v1.2.0 | Test-only event state machine | 1.18 |
| `github.com/google/go-cmp` | v0.7.0 | Test-only semantic diffs | 1.21 |

The public API does not expose these dependency types. Rapid and go-cmp do not
appear in production imports. No dependency source or local replaces are shipped.
The native Go 1.27 dependency/build lane remains unexecuted.

See REFERENCES.md for version-pinned source locations. The generated metadata
and command ledger are implementation artifacts, not upstream test certificates.
