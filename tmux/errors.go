package tmux

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	// NotSent indicates the command was never dispatched to tmux; no server state changed.
	NotSent Effect = iota

	// Unknown indicates the command was sent or partially processed, but an error
	// occurred before confirmation. The mutation MAY have taken effect on the server;
	// callers must NOT blindly retry.
	Unknown
	// Confirmed indicates tmux processed the command, but subsequent inspection failed.
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
	NoTimeout TimeoutSource = iota
	CallerTimeout
	LibraryTimeout
)

var (
	// ErrNotInsideTmux indicates $TMUX is missing from the environment.
	ErrNotInsideTmux = errors.New("tmux: not inside tmux")

	// ErrNoServer indicates no tmux daemon is listening on the selected socket.
	ErrNoServer = errors.New("tmux: no server on selected endpoint")

	// ErrNotFound indicates the target session, window, pane, client, or buffer does not exist.
	ErrNotFound = errors.New("tmux: object not found")

	// ErrAmbiguousTarget indicates an unqualified target matched multiple objects.
	ErrAmbiguousTarget = errors.New("tmux: ambiguous target")

	// ErrInvalidHandle indicates a handle lacks provenance or refers to an invalid ID.
	ErrInvalidHandle = errors.New("tmux: invalid handle")

	// ErrInvalidArgument indicates an invalid parameter (out of range, NUL byte, etc.).
	ErrInvalidArgument = errors.New("tmux: invalid argument")

	// ErrServerChanged indicates the daemon PID, start time, or socket changed since handle creation.
	ErrServerChanged = errors.New("tmux: daemon identity changed")

	// ErrLinkChanged indicates the window at the target session slot index changed or was unlinked.
	ErrLinkChanged = errors.New("tmux: window link changed")

	// ErrClientChanged indicates the target client terminal disconnected or changed identity.
	ErrClientChanged = errors.New("tmux: client identity changed")

	// ErrUnsupported indicates a feature or flag is not supported by this tmux version.
	ErrUnsupported = errors.New("tmux: unsupported feature or version")

	// ErrTransportUnsupported indicates the operation cannot run over the active transport.
	ErrTransportUnsupported = errors.New("tmux: unsupported execution transport")

	// ErrOutputLimit indicates stdout/stderr exceeded [Limits.OutputBytes].
	ErrOutputLimit = errors.New("tmux: output byte limit exceeded")

	// ErrInputLimit indicates payload sent to tmux exceeded [Limits.InputBytes].
	ErrInputLimit = errors.New("tmux: input byte limit exceeded")

	// ErrResourceLimit indicates queue or reservation limits would be exceeded.
	ErrResourceLimit = errors.New("tmux: resource reservation cannot fit")

	// ErrProtocol indicates malformed control framing or an unexpected wire response.
	ErrProtocol = errors.New("tmux: invalid control protocol")

	// ErrEventsLost indicates an event subscription overflowed its buffer capacity and was dropped.
	ErrEventsLost = errors.New("tmux: subscription events lost")

	// ErrClosed indicates the connection or event stream is closed.
	ErrClosed = errors.New("tmux: closed")

	// ErrShutdownIncomplete indicates connection teardown timed out before background workers exited.
	ErrShutdownIncomplete = errors.New("tmux: shutdown incomplete")

	// ErrConcurrentRead indicates multiple goroutines attempted concurrent reads on an [EventStream].
	ErrConcurrentRead = errors.New("tmux: concurrent event reads")

	// ErrInconsistent indicates contradictory observation state during snapshot or option queries.
	ErrInconsistent = errors.New("tmux: inconsistent observation")
)

// Effect describes whether a mutation was dispatched or acknowledged.
type (
	Effect uint8

	// ObjectKind classifies a tmux object type.
	ObjectKind string

	// StepOutcome records the outcome of an individual step in a batch operation.
	StepOutcome struct {
		Index  int
		Effect Effect
	}

	// CreatedObject records identity information recovered for a newly created object.
	CreatedObject struct {
		Kind        ObjectKind
		RawID       string
		Identity    Value[ServerIdentity]
		SessionID   Value[SessionID]
		WindowIndex Value[int]
	}

	// Outcome details the side-effect knowledge of an operation.
	Outcome struct {
		Effect  Effect
		Steps   []StepOutcome
		Created []CreatedObject
	}

	// TimeoutSource indicates whether a timeout originated from the caller or library limits.
	TimeoutSource uint8

	// OperationError wraps every failing typed I/O operation. Err is safe to inspect
	// with [errors.Is] or [errors.As]. The Error message deliberately excludes captured
	// text and arguments to avoid leaking sensitive information into application logs.
	OperationError struct {
		Operation string
		Outcome   Outcome
		Err       error
	}

	// CommandError provides low-level diagnostics, including bounded stdout/stderr copies.
	// ExitCode is -1 for control-mode commands and for processes terminated without exit status.
	CommandError struct {
		Command string
		Result  Result
		Outcome Outcome
		Timeout TimeoutSource
		Err     error
	}
	// DecodeError records a failure decoding a structured record field.
	DecodeError struct {
		Record string
		Field  string
		Offset int
		Err    error
	}

	// UnsupportedError describes a feature incompatible with the current daemon or transport.
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
