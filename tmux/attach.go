package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	process "github.com/zigai/gotmux/internal/exec"

	"golang.org/x/term"
)

// TerminalStreams supplies the file descriptors for an interactive terminal attachment.
// In and Out must refer to real terminal devices (validated via [term.IsTerminal]).
type TerminalStreams struct {
	In *os.File

	Out *os.File

	Err *os.File
}

// AttachOptions configures interactive terminal attachment to a session.
type AttachOptions struct {
	// ReadOnly attaches the client in read-only mode (-r flag), preventing input transmission.
	ReadOnly bool

	// PreserveEnvironment prevents tmux from updating client environment variables (-E flag).
	PreserveEnvironment bool
}

func validateTerminals(t TerminalStreams) error {
	if t.In == nil || t.Out == nil || t.Err == nil {
		return invalid("terminal streams")
	}

	if !term.IsTerminal(int(t.In.Fd())) || !term.IsTerminal(int(t.Out.Fd())) {
		return invalid("stdin/stdout must be terminal files")
	}

	if _, _, err := term.GetSize(int(t.Out.Fd())); err != nil {
		return fmt.Errorf("terminal size: %w", err)
	}

	return nil
}

// PrepareAttach returns an unstarted [exec.Cmd] for interactive session attachment.
// It validates streams; the caller manages Start, Wait, signals, cancellation, and exit codes.
//
// The session ID is resolved at the endpoint without daemon identity or control-generation
// checks. Tmux manages terminal mode setup and restoration.
//
// Fails with [ErrTransportUnsupported] if invoked on a control-bound server.
func (s *Server) PrepareAttach(ctx context.Context, session SessionID, streams TerminalStreams, o AttachOptions) (*exec.Cmd, error) {
	if s == nil || s.runner == nil {
		return nil, opError("PrepareAttach", ErrInvalidHandle)
	}

	if ctx == nil || !session.Valid() {
		return nil, opError("PrepareAttach", invalid("context/session"))
	}

	if s.conn != nil || s.bound != nil {
		return nil, opError("PrepareAttach", ErrTransportUnsupported)
	}

	if err := validateTerminals(streams); err != nil {
		return nil, opError("PrepareAttach", err)
	}

	args := append(s.baseArgs(false), "attach-session", "-t", string(session))
	if o.ReadOnly {
		args = append(args, "-r")
	}

	if o.PreserveEnvironment {
		args = append(args, "-E")
	}

	cmd := s.runner.Command(ctx, args)
	cmd.Stdin = streams.In
	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err

	return cmd, nil
}

// Attach attaches the caller's terminal until detach or context cancellation.
// The daemon identity is checked in the same command queue as attachment.
// Tmux owns terminal setup; the original terminal state is restored on return,
// including when cancellation terminates the local client. No command timeout applies.
// Control-bound sessions must explicitly select UsingSubprocess first.
func (s Session) Attach(ctx context.Context, streams TerminalStreams, o AttachOptions) (err error) {
	if err = s.h.check(); err != nil {
		return opError("Attach", err)
	}

	if ctx == nil {
		return opError("Attach", invalid("nil context"))
	}

	if s.h.server.conn != nil {
		return opError("Attach", ErrTransportUnsupported)
	}

	if err = validateTerminals(streams); err != nil {
		return opError("Attach", err)
	}

	argv, err := s.attachmentArgs(o)
	if err != nil {
		return opError("Attach", err)
	}

	state, err := term.GetState(int(streams.In.Fd()))
	if err != nil {
		return opError("Attach", err)
	}
	defer func() { err = errors.Join(err, opError("Attach", term.Restore(int(streams.In.Fd()), state))) }()

	child, closeLifetime := attachmentContext(ctx, s.h.server.lifetime)
	defer closeLifetime()

	return opError("Attach", s.runAttachment(child, streams.In, argv))
}

func (s Session) attachmentArgs(o AttachOptions) ([]string, error) {
	args := []string{"-t", s.h.id}
	if o.ReadOnly {
		args = append(args, "-r")
	}

	if o.PreserveEnvironment {
		args = append(args, "-E")
	}
	// Successful attachment discards queued command output. Only the rejection
	// branch emits a marker; it never attaches.
	p := plan{nodes: []wireNode{conditionNode(s.h.origin.condition(),
		[]wireNode{leaf(command("attach-session", args...))},
		[]wireNode{markerNode(guardServerChanged)}, "")}, mode: replyEmpty, allowStart: false}
	if _, err := p.size(s.h.server.config.Limits.InputBytes); err != nil {
		return nil, err
	}

	argv, err := p.argv()
	if err != nil {
		return nil, err
	}

	return append(s.h.server.baseArgs(false), argv...), nil
}

// The returned cleanup cancels and joins the connection-lifetime watcher.
func attachmentContext(ctx context.Context, lifetime *Connection) (context.Context, context.CancelFunc) {
	child, cancel := context.WithCancel(ctx)
	if lifetime == nil {
		return child, cancel
	}

	done := make(chan struct{})
	go func() {
		defer close(done)

		select {
		case <-lifetime.stopCh:
			cancel()
		case <-child.Done():
		}
	}()

	return child, func() { cancel(); <-done }
}

func (s Session) runAttachment(ctx context.Context, terminal *os.File, argv []string) error {
	child, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	cmd := s.h.server.runner.Command(child, argv)
	// The tmux server renders directly to the terminal supplied on stdin.
	// Stdout/stderr carry command diagnostics and the identity-rejection marker.
	overflow := func() { cancel(ErrOutputLimit) }
	stdout := process.NewBuffer(s.h.server.config.Limits.OutputBytes, overflow)
	stderr := process.NewBuffer(s.h.server.config.Limits.OutputBytes, overflow)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = terminal, stdout, stderr
	err := cmd.Run()
	result := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: -1}
	effect := NotSent

	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
		effect = Unknown
	}

	err = errors.Join(err, context.Cause(child), classifyStderr(result.Stderr))
	if errors.Is(err, exec.ErrWaitDelay) {
		err = errors.Join(err, ErrShutdownIncomplete)
	}

	if bytes.HasPrefix(result.Stdout, []byte(guardServerChanged)) {
		_, err = s.h.guard().unwrap(result, err)
		return err
	}

	if err == nil {
		return nil
	}

	return &CommandError{
		Command: "attach-session", Result: result,
		Outcome: Outcome{Effect: effect, Steps: nil, Created: nil},
		Timeout: contextSource(ctx.Done(), err), Err: err,
	}
}
