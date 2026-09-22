// Package tmux controls explicitly selected tmux servers using bounded subprocesses
// or an explicitly opened control mode connection. New performs no process execution.
//
// Handles (Server, Session, WindowLink, Window, Pane, Client) represent objects bound
// to a verified daemon lifetime. Info methods perform I/O; ID, Valid, and Equal do not.
// Multi-handle operations require matching endpoints and connection lifetimes.
//
// Window represents a shared window object across sessions, while WindowLink represents
// an observed slot within a specific session. Killing a Window affects every link;
// unlinking a WindowLink affects only that session slot.
//
// Operations are bounded by configured limits. When an operation fails, inspect
// OperationError.Outcome to determine server-side side effects.
//
// This is an alpha implementation.
package tmux
