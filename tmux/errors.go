package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	// NotSent indicates the command or mutation was never dispatched to tmux
	// (for example, due to argument validation failure or pre-dispatch context cancellation).
	// Callers can safely assume no daemon state was modified.
	NotSent Effect = iota

	// Unknown indicates the command was sent or partially processed, but an error
	// occurred before confirmation (e.g. process timeout or connection drop).
	// The mutation MAY have taken effect on the server; callers must NOT blindly retry.
	Unknown

	// Confirmed indicates tmux successfully processed the command, but subsequent
	// inspection or decoding failed (e.g. failure to parse the created object's ID).
	// The server state HAS been modified.
	Confirmed
)

const (
	SessionKind ObjectKind = "session"
	WindowKind  ObjectKind = "window"
	PaneKind    ObjectKind = "pane"
	ClientKind  ObjectKind = "client"
	LinkKind    ObjectKind = "window-link"
)

const (
	// NoTimeout indicates the error was not caused by a timeout.
	NoTimeout TimeoutSource = iota

	// CallerTimeout indicates the caller's context deadline expired while waiting.
	CallerTimeout

	// LibraryTimeout indicates the library's internal CommandTimeout expired.
	LibraryTimeout
)

var (
	// ErrNotInsideTmux indicates that ambient tmux environment variables ($TMUX)
	// were not found in the current process environment.
	ErrNotInsideTmux = errors.New("tmux: not inside tmux")

	// ErrNoServer indicates that no tmux server daemon is listening on the target socket,
	// and automatic startup was not permitted (via AllowStart or -N).
	ErrNoServer = errors.New("tmux: no server on selected endpoint")

	// ErrNotFound indicates that the requested session, window, pane, client, or buffer does not exist.
	ErrNotFound = errors.New("tmux: object not found")

	// ErrAmbiguousTarget indicates that tmux found multiple matching targets for an unqualified name.
	ErrAmbiguousTarget = errors.New("tmux: ambiguous target")

	// ErrInvalidHandle indicates that a handle lacks required provenance, refers to an invalid ID,
	// or was used with a server from a different daemon lifetime or connection generation.
	ErrInvalidHandle = errors.New("tmux: invalid handle")

	// ErrInvalidArgument indicates that an input parameter violated validation rules
	// (e.g. negative dimensions, out-of-range indices, NUL bytes, or invalid option names).
	ErrInvalidArgument = errors.New("tmux: invalid argument")

	// ErrServerChanged indicates that the answering daemon's PID, start time, or reported socket
	// changed between handle creation and command execution, indicating a server crash/restart.
	ErrServerChanged = errors.New("tmux: daemon identity changed")

	// ErrLinkChanged indicates that the window at the target session slot index changed or was unlinked.
	ErrLinkChanged = errors.New("tmux: window link changed")

	// ErrClientChanged indicates that the target client terminal disconnected or changed identity.
	ErrClientChanged = errors.New("tmux: client identity changed")

	// ErrUnsupported indicates that a requested command, flag, or option is not supported
	// by the running tmux binary or daemon version.
	ErrUnsupported = errors.New("tmux: unsupported feature or version")

	// ErrTransportUnsupported indicates that the requested operation cannot be executed over
	// the current transport (for example, running interactive UI or raw commands over control mode).
	ErrTransportUnsupported = errors.New("tmux: unsupported execution transport")

	// ErrOutputLimit indicates that stdout/stderr output from tmux exceeded [Limits.OutputBytes].
	ErrOutputLimit = errors.New("tmux: output byte limit exceeded")

	// ErrInputLimit indicates that input sent via stdin or command arguments exceeded [Limits.InputBytes].
	ErrInputLimit = errors.New("tmux: input byte limit exceeded")

	// ErrResourceLimit indicates that control queue depth or event buffer byte reservations
	// would exceed configured capacity limits.
	ErrResourceLimit = errors.New("tmux: resource reservation cannot fit")

	// ErrProtocol indicates that tmux emitted malformed control framing or an unexpected wire response.
	ErrProtocol = errors.New("tmux: invalid control protocol")

	// ErrEventsLost indicates that an event subscription overflowed its buffer capacity and was terminated.
	ErrEventsLost = errors.New("tmux: subscription events lost")

	// ErrClosed indicates that the target control connection or event stream is closed.
	ErrClosed = errors.New("tmux: closed")

	// ErrShutdownIncomplete indicates that connection teardown timed out before background workers exited.
	ErrShutdownIncomplete = errors.New("tmux: shutdown incomplete")

	// ErrConcurrentRead indicates that multiple goroutines attempted concurrent reads on a single [EventStream].
	ErrConcurrentRead = errors.New("tmux: concurrent event reads")

	// ErrInconsistent indicates that an observation detected an inconsistent or contradictory state
	// during snapshot analysis or option hierarchy queries.
	ErrInconsistent = errors.New("tmux: inconsistent observation")
)

// Effect describes our knowledge of whether a mutation was dispatched or acknowledged.
// It does NOT indicate whether replaying the mutation would be safe or idempotent.
type (
	Effect uint8

	// ObjectKind classifies a tmux object type (session, window, pane, client, or link).
	ObjectKind string

	// StepOutcome records the outcome of a single step within a multi-step operation.
	StepOutcome struct {
		Index  int
		Effect Effect
	}

	// CreatedObject records identity information recovered for an object created during an operation,
	// even if a subsequent post-creation step (like inspection) failed.
	CreatedObject struct {
		Kind        ObjectKind
		RawID       string
		Identity    Value[ServerIdentity]
		SessionID   Value[SessionID]
		WindowIndex Value[int]
	}

	// Outcome details the side-effect knowledge of an operation, including partial step
	// progress and any objects known to have been created on the daemon.
	Outcome struct {
		Effect  Effect
		Steps   []StepOutcome
		Created []CreatedObject
	}

	// TimeoutSource distinguishes whether an operation timed out because the caller's
	// context deadline was reached or because the library's internal CommandTimeout expired.
	TimeoutSource uint8

	// OperationError wraps every failing typed I/O operation. Err is safe to inspect
	// with [errors.Is] or [errors.As]. The Error message deliberately excludes captured
	// text and arguments to avoid leaking sensitive information into application logs.
	OperationError struct {
		Operation string
		Outcome   Outcome
		Err       error
	}

	// CommandError provides low-level diagnostic details for command execution failures.
	// Result contains bounded stdout/stderr copies. ExitCode is -1 for control-mode
	// commands and for processes terminated by signals without an exit status.
	CommandError struct {
		Command string
		Result  Result
		Outcome Outcome
		Timeout TimeoutSource
		Err     error
	}

	// DecodeError records a failure while decoding a structured tmux record field.
	DecodeError struct {
		Record string
		Field  string
		Offset int
		Err    error
	}

	// UnsupportedError describes a feature, command, or version incompatible with the
	// current daemon or execution transport.
	UnsupportedError struct {
		Feature   string
		Version   Version
		Transport Transport
		Err       error
	}

	discoveryError struct{ Err error }
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

func (oe *OperationError) Error() string {
	return fmt.Sprintf("tmux: %s: %s", oe.Operation, strings.TrimPrefix(oe.Err.Error(), "tmux: "))
}
func (oe *OperationError) Unwrap() error { return oe.Err }

func (ce *CommandError) Error() string {
	return fmt.Sprintf("tmux: %s failed (effect %s, exit %d)", ce.Command, ce.Outcome.Effect, ce.Result.ExitCode)
}
func (ce *CommandError) Unwrap() error { return ce.Err }

func (de *DecodeError) Error() string {
	return fmt.Sprintf("tmux: decode %s field %s at byte %d", de.Record, de.Field, de.Offset)
}
func (de *DecodeError) Unwrap() error { return de.Err }

func (ue *UnsupportedError) Error() string { return "tmux: unsupported " + ue.Feature }
func (ue *UnsupportedError) Unwrap() error {
	if ue.Err != nil {
		return ue.Err
	}

	return ErrUnsupported
}

func (de *discoveryError) Error() string { return de.Err.Error() }
func (de *discoveryError) Unwrap() error { return de.Err }
func invalid(field string) error         { return fmt.Errorf("%w: %s", ErrInvalidArgument, field) }
func decodeError(kind, field string, err error) error {
	return &DecodeError{Record: kind, Field: field, Offset: 0, Err: err}
}

func failedResult() Result {
	return Result{Stdout: nil, Stderr: nil, ExitCode: -1}
}

func notSentOutcome() Outcome {
	return Outcome{Effect: NotSent, Steps: nil, Created: nil}
}

func unsupportedVersion(feature string, v Version) *UnsupportedError {
	return &UnsupportedError{
		Feature:   feature,
		Version:   v,
		Transport: Subprocess,
		Err:       nil,
	}
}

func unsupported(feature string) *UnsupportedError {
	return &UnsupportedError{
		Feature:   feature,
		Version:   Version{Raw: "", Major: 0, Minor: 0, Patch: "", Suffix: "", Recognized: false},
		Transport: Subprocess,
		Err:       nil,
	}
}

func unsupportedTransport(feature string, t Transport, err error) *UnsupportedError {
	return &UnsupportedError{
		Feature:   feature,
		Version:   Version{Raw: "", Major: 0, Minor: 0, Patch: "", Suffix: "", Recognized: false},
		Transport: t,
		Err:       err,
	}
}

func opError(name string, err error) error {
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*discoveryError](err); ok && name != "Probe" {
		return &OperationError{Operation: name, Outcome: Outcome{Effect: NotSent, Steps: nil, Created: nil}, Err: err}
	}

	if op, ok := errors.AsType[*OperationError](err); ok {
		return &OperationError{Operation: name, Outcome: op.Outcome, Err: err}
	}

	if cmd, ok := errors.AsType[*CommandError](err); ok {
		return &OperationError{Operation: name, Outcome: cmd.Outcome, Err: err}
	}

	return &OperationError{Operation: name, Outcome: Outcome{Effect: NotSent, Steps: nil, Created: nil}, Err: err}
}

func afterError(name string, err error, created ...CreatedObject) error {
	if err == nil {
		return nil
	}

	return &OperationError{Operation: name, Outcome: Outcome{Effect: Confirmed, Steps: nil, Created: created}, Err: err}
}

func outcomeOf(err error) Outcome {
	if op, ok := errors.AsType[*OperationError](err); ok {
		return op.Outcome
	}

	if ce, ok := errors.AsType[*CommandError](err); ok {
		return ce.Outcome
	}

	return Outcome{Effect: NotSent, Steps: nil, Created: nil}
}

func contextSource(callerDone <-chan struct{}, err error) TimeoutSource {
	if !errors.Is(err, context.DeadlineExceeded) {
		return NoTimeout
	}

	select {
	case <-callerDone:
		return CallerTimeout
	default:
		return LibraryTimeout
	}
}
