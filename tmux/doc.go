// Package tmux controls explicitly selected tmux servers using bounded subprocesses
// or an explicitly opened control connection. New performs no process execution.
//
// Handles identify objects on a particular daemon lifetime. Info methods perform
// I/O; ID, Valid, Equal, record Handle methods, and snapshot accessors do not.
// Multi-handle operations require the same endpoint and connection lifetime.
// Unprobed handles are resolved within the operation budget, retaining any
// verified participant's daemon identity rather than rebinding a stale handle.
// All captured calls include admission, discovery, and decoding in a finite
// operation budget. Cancellation of a client never promises cancellation of
// server-side effects. Inspect OperationError.Outcome before deciding how to
// recover; mutations are never automatically retried.
//
// Window denotes shared identity; WindowLink denotes an observed session slot.
// Killing a Window affects every link. Unlinking a WindowLink affects one slot.
//
// This is an alpha implementation.
package tmux
