package tmux

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrNotInsideTmux        = errors.New("tmux: not inside tmux")
	ErrNoServer             = errors.New("tmux: no server on selected endpoint")
	ErrNotFound             = errors.New("tmux: object not found")
	ErrAmbiguousTarget      = errors.New("tmux: ambiguous target")
	ErrInvalidHandle        = errors.New("tmux: invalid handle")
	ErrInvalidArgument      = errors.New("tmux: invalid argument")
	ErrServerChanged        = errors.New("tmux: daemon identity changed")
	ErrLinkChanged          = errors.New("tmux: window link changed")
	ErrClientChanged        = errors.New("tmux: client identity changed")
	ErrUnsupported          = errors.New("tmux: unsupported feature or version")
	ErrTransportUnsupported = errors.New("tmux: unsupported execution transport")
	ErrOutputLimit          = errors.New("tmux: output byte limit exceeded")
	ErrInputLimit           = errors.New("tmux: input byte limit exceeded")
	ErrResourceLimit        = errors.New("tmux: resource reservation cannot fit")
	ErrProtocol             = errors.New("tmux: invalid control protocol")
	ErrEventsLost           = errors.New("tmux: subscription events lost")
	ErrClosed               = errors.New("tmux: closed")
	ErrShutdownIncomplete   = errors.New("tmux: shutdown incomplete")
	ErrConcurrentRead       = errors.New("tmux: concurrent event reads")
	ErrInconsistent         = errors.New("tmux: inconsistent observation")
)

// Effect describes knowledge, not whether replay would be safe.
type Effect uint8

const (
	NotSent Effect = iota
	Unknown
	Confirmed
)

func (e Effect) String() string {
	switch e {
	case NotSent:
		return "not-sent"
	case Unknown:
		return "unknown"
	case Confirmed:
		return "confirmed"
	}
	return "invalid"
}

type ObjectKind string

const (
	SessionKind ObjectKind = "session"
	WindowKind  ObjectKind = "window"
	PaneKind    ObjectKind = "pane"
	ClientKind  ObjectKind = "client"
	LinkKind    ObjectKind = "window-link"
)

type StepOutcome struct {
	Index  int
	Effect Effect
}
type CreatedObject struct {
	Kind        ObjectKind
	RawID       string
	Identity    Value[ServerIdentity]
	SessionID   Value[SessionID]
	WindowIndex Value[int]
}
type Outcome struct {
	Effect  Effect
	Steps   []StepOutcome
	Created []CreatedObject
}

type TimeoutSource uint8

const (
	NoTimeout TimeoutSource = iota
	CallerTimeout
	LibraryTimeout
)

// OperationError wraps every failing typed I/O operation. Err is safe to inspect
// with errors.Is/As. Error deliberately excludes captured text and arguments.
type OperationError struct {
	Operation string
	Outcome   Outcome
	Err       error
}

func (e *OperationError) Error() string { return "tmux: " + e.Operation + ": " + e.Err.Error() }
func (e *OperationError) Unwrap() error { return e.Err }

// CommandError keeps bounded, caller-owned diagnostics explicitly accessible.
// ExitCode is -1 for control-mode commands and for processes without exit status.
type CommandError struct {
	Command string
	Result  Result
	Outcome Outcome
	Timeout TimeoutSource
	Err     error
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("tmux: %s failed (effect %s, exit %d)", e.Command, e.Outcome.Effect, e.Result.ExitCode)
}
func (e *CommandError) Unwrap() error { return e.Err }

type DecodeError struct {
	Record string
	Field  string
	Offset int
	Err    error
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("tmux: decode %s field %s at byte %d", e.Record, e.Field, e.Offset)
}
func (e *DecodeError) Unwrap() error { return e.Err }

type UnsupportedError struct {
	Feature   string
	Version   Version
	Transport Transport
	Err       error
}

func (e *UnsupportedError) Error() string { return "tmux: unsupported " + e.Feature }
func (e *UnsupportedError) Unwrap() error {
	if e.Err != nil {
		return e.Err
	}
	return ErrUnsupported
}

func invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalidArgument, field) }
func decodeError(kind, field string, err error) error {
	return &DecodeError{Record: kind, Field: field, Err: err}
}

// discoveryError marks work performed before the caller's requested action.
type discoveryError struct{ Err error }

func (e *discoveryError) Error() string { return e.Err.Error() }
func (e *discoveryError) Unwrap() error { return e.Err }

func opError(name string, err error) error {
	if err == nil {
		return nil
	}
	var discovery *discoveryError
	if name != "Probe" && errors.As(err, &discovery) {
		return &OperationError{Operation: name, Outcome: Outcome{Effect: NotSent}, Err: err}
	}
	var op *OperationError
	if errors.As(err, &op) {
		return &OperationError{Operation: name, Outcome: op.Outcome, Err: err}
	}
	var cmd *CommandError
	if errors.As(err, &cmd) {
		return &OperationError{Operation: name, Outcome: cmd.Outcome, Err: err}
	}
	return &OperationError{Operation: name, Outcome: Outcome{Effect: NotSent}, Err: err}
}
func afterError(name string, err error, created ...CreatedObject) error {
	if err == nil {
		return nil
	}
	return &OperationError{Operation: name, Outcome: Outcome{Effect: Confirmed, Created: created}, Err: err}
}
func outcomeOf(err error) Outcome {
	var op *OperationError
	if errors.As(err, &op) {
		return op.Outcome
	}
	var ce *CommandError
	if errors.As(err, &ce) {
		return ce.Outcome
	}
	return Outcome{Effect: NotSent}
}
func contextSource(caller context.Context, err error) TimeoutSource {
	if !errors.Is(err, context.DeadlineExceeded) {
		return NoTimeout
	}
	if errors.Is(caller.Err(), context.DeadlineExceeded) {
		return CallerTimeout
	}
	return LibraryTimeout
}
