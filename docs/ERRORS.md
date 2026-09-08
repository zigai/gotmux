# Errors and effect uncertainty

Use `errors.Is` for classification and `errors.As` for structured details.
No library operation logs captured contents, arguments, or environment values.
`CommandError.Result` contains bounded stdout/stderr explicitly; ordinary error
text does not print those diagnostics.

`OperationError.Outcome.Effect` is independent of the error kind:

| Effect | Meaning |
| --- | --- |
| `NotSent` | The requested action was not dispatched. A probe may already have run. |
| `Unknown` | An effect may have happened or partially happened; do not infer replay safety. |
| `Confirmed` | tmux acknowledged the action, but later decoding/inspection failed. |

There is no generic Retryable flag and no automatic mutation retry or rollback.
An acknowledged creation can leave a real object even when its full metadata
cannot be decoded. Recovered created IDs are retained separately; a nonzero
returned handle is usable only when `Valid()` is true. Sparse updates report
confirmed logical step indexes, the failed step's knowledge, and undispatched
remaining steps. Their successful prefix is not undone.

Important classifications include `ErrNoServer`, `ErrNotFound`,
`ErrNotInsideTmux`, `ErrInvalidHandle`, `ErrInvalidArgument`, `ErrServerChanged`,
`ErrLinkChanged`, `ErrClientChanged`, `ErrUnsupported`,
`ErrTransportUnsupported`, `ErrInputLimit`, `ErrOutputLimit`,
`ErrResourceLimit`, `ErrProtocol`, `ErrEventsLost`, `ErrClosed`,
`ErrShutdownIncomplete`, and `ErrInconsistent`.

Permission errors do not become empty listings or `ErrNoServer`. Unknown tmux
stderr remains a command failure; classification is conservative and still needs
more version-specific fixtures. Caller cancellation and deadlines are preserved
in the error chain. Captured command errors distinguish caller and library
timeouts where that provenance is available.

Raw/capture calls return bounded bytes already received together with an error.
List and snapshot calls do not expose transport/decode-failed partial collections.
Ordinary graph churn is instead a successful incomplete snapshot with missing
references. Control cancellation may lose the caller's ability to observe an
in-flight result; its late response is drained, never assigned to another call.
