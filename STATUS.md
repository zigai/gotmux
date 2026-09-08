# Implementation status and release gates

**This delivery is an alpha implementation, not full completion of SPEC.md.**
Code presence, synthetic tests, real-tmux compatibility, and consumer acceptance
are separate facts. This document intentionally does not mark one as another.

## Implemented code

The tree contains configured subprocess execution, distinct command codecs,
length-prefixed metadata, fetched records and private-provenance handles,
daemon/link/client guards, batched snapshots, ordinary creation and graph
operations, capture/input/modes, generated option accessors, sparse updates,
hooks/bindings, scoped environments, binary buffers, explicit-client UI builders,
control connection ownership, serial request dispatch with post-cancellation
draining, bounded event streams, and public real-server fixtures.

Unit tests exercise codecs, process ownership and limits, provenance, graph
consistency, partial creation identity recovery, literal argv/environment
launchers, request cancellation/response ownership, and subscription terminal
states. Native fuzz targets are present. Go 1.27-only tests use Rapid and go-cmp;
these could not be executed with the available Go 1.23 toolchain.

## Known implementation restrictions / unfinished surface

1. **Guarded interactive terminal attachment is not delivered.** `PrepareAttach`
   is an advanced endpoint-relative, caller-owned `*exec.Cmd` escape hatch. It is
   explicitly not a generation-protected handle attachment. This is a v1 blocker.
2. **The typed command/flag and option catalog is not exhaustive.** There are
   meaningful builders across the required families, but not every flag in every
   pinned manual has a reviewed Go mapping. Newer floating-pane primitives and
   several diagnostic/navigation commands remain raw-only. The current ledger
   makes those omissions visible; it is not the exhaustive release ledger.
3. **Per-program environment overrides require explicit `Exec` or `Shell`.**
   `Program{}` plus a nonempty override map returns `ErrUnsupported` before
   creation. Native `new-session -e` was not used because it would persist values
   in session state, and tmux may override native PATH/SHELL values afterward.
4. **Link/Move require an explicit destination index.** This avoids guessing a
   returned slot after mutation. Automatic destination-slot discovery is not
   implemented. Window/link follow-up-error outcomes need broader real-server
   fault injection; sparse-array prefix outcomes have a dedicated implementation.
5. **Control support is conservative.** All raw commands, capture, binary stdin,
   option/environment/hook/binding reads, and other unframed-output paths reject
   control execution. Explicit same-daemon subprocess access is provided instead.
   This is not an automatic fallback. Arbitrary native notifications that contain
   ambiguous raw line breaks fail the connection rather than being guessed.
6. **Inherited option origin can be unavailable.** Local/effective presence and
   explicit empty values are retained. The library does not claim an exact parent
   origin where separate observations cannot prove it. The full ANNO-2 migration
   scenario and all array/hook serialization dialects remain unverified.
7. **Zero-byte buffer replacement is explicitly unsupported.** Stock tmux's load
   and set commands treat empty data as a no-op. Returning successful replacement
   would be false; the library rejects it instead of leaving a stale value silently.
8. **Not every lifecycle edge has an executable proof.** Examples include
   inherited-pipe teardown, real UI cancellation, all nested after-hook effects,
   all stale-client identity cases, adversarial mutation follow-up failures, and
   full operation-wide accounting of temporary typed-builder allocations before
   control queue admission. The encoded wire itself is reserved before expansion.
9. **Raw commands remain endpoint-relative and do not currently enforce the full
   typed version gate.** Typed discovery and creation gate recognized versions;
   raw access can reach a daemon outside the typed support range. This differs
   from a strict reading of section 15 and must be reconciled before v1.

## Unmet release evidence

| Gate | Status |
| --- | --- |
| Native Go 1.27.1 build with all four pinned dependencies | Not run; downloads unavailable |
| Runtime unit/race checks in Go 1.23 compatibility workspace | Run; see VALIDATION.md for exact scope |
| Go 1.27 Rapid and go-cmp tests | Written, not run |
| Real tmux 3.6 / 3.6a / 3.6b / 3.7c | Integration suite and CI written, not run |
| Linux/macOS real-tmux race jobs | Not run |
| Full daemon-guard and delimiter safety evidence on each release | Synthetic regressions pass; real release gate open |
| Exhaustive pinned command/flag and field presence ledger | Not complete |
| Performance baselines, peak memory, latency distributions | Benchmarks provided; results not published |
| Clean external-consumer build with no local replace directives | Harness provided; native dependency lane not run |
| aht, sesh, anno migrations and removal of duplicate ownership | Not performed |
| Final module identity, checksums, publication policy | Module undecided; dependency checksum bootstrap required |

`python3 scripts/check_release.py` intentionally fails while these release gates
remain open. This prevents the presence of a workflow or test name from being
mistaken for completed acceptance evidence.
