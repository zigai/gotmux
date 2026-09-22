package tmux

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/zigai/gotmux/internal/wire"

	"golang.org/x/term"
)

const (
	// DetachModeNone leaves existing attached clients undisturbed (default native behavior).
	DetachModeNone DetachMode = iota

	// DetachModeOtherClients detaches any other clients attached to the session (-d flag).
	DetachModeOtherClients

	// DetachModeParentSignal detaches any other clients attached to the session and sends
	// SIGHUP to their parent processes (-x flag).
	DetachModeParentSignal
)

const (
	// DetachNone, DetachOtherClients, and DetachParentSignal are retained for backward compatibility.
	DetachNone         = DetachModeNone
	DetachOtherClients = DetachModeOtherClients
	DetachParentSignal = DetachModeParentSignal
)

type (
	// DetachMode specifies how other attached clients should be handled during session attachment.
	DetachMode uint8

	// TerminalStreams supplies the file descriptors for an interactive terminal attachment.
	// In and Out must refer to real terminal devices (validated via [term.IsTerminal]).
	TerminalStreams struct {
		In *os.File

		Out *os.File

		Err *os.File
	}

	// AttachOptions configures interactive terminal attachment to a session.
	AttachOptions struct {
		// ReadOnly attaches the client in read-only mode (-r flag), preventing input transmission.
		ReadOnly bool

		// PreserveEnvironment prevents tmux from updating client environment variables (-E flag).
		PreserveEnvironment bool

		// Detach controls whether other attached clients are detached or signaled (-d, -x flags).
		Detach DetachMode

		// Dir sets the session working directory used for new windows (-c flag).
		Dir string
		// Flags specifies client flags to apply (-f flag), such as [ClientReadOnly], [ClientIgnoreSize], etc.
		Flags []ClientFlag
	}

	// CommandOptions configures prepared subprocess command execution.
	CommandOptions struct {
		// Start controls whether tmux is permitted to spawn a new server daemon if none is listening.
		// When [AllowStart] (the zero value), daemon auto-spawn is permitted.
		// When [ExistingOnly], the -N flag is emitted to prevent daemon auto-spawn.
		Start StartPolicy
	}

	// Streams supplies caller-owned operating system file descriptors for standard input,
	// standard output, and standard error of a prepared subprocess command.
	//
	// Unlike [TerminalStreams], the streams do not need to refer to real terminal devices,
	// permitting ordinary files, pipes, and sockets.
	// Any stream field left nil is detached (connected to /dev/null).
	Streams struct {
		// In is connected to the child process's standard input.
		In *os.File

		// Out is connected to the child process's standard output.
		Out *os.File

		// Err is connected to the child process's standard error.
		Err *os.File
	}
	// TerminalOptions configures interactive terminal command preparation.
	TerminalOptions struct {
		// Start controls whether tmux is permitted to spawn a new server daemon if none is listening.
		// When [AllowStart] (the zero value), daemon auto-spawn is permitted.
		// When [ExistingOnly], the -N flag is emitted to prevent daemon auto-spawn.
		Start StartPolicy
	}

	// RootShellOptions configures root shell compatibility command execution (-c flag).
	RootShellOptions struct {
		// Start controls whether tmux is permitted to spawn a new server daemon if none is listening.
		// When [AllowStart] (the zero value), daemon auto-spawn is permitted.
		// When [ExistingOnly], the -N flag is emitted to prevent daemon auto-spawn.
		Start StartPolicy
	}
)

func validateAttachOptions(opts AttachOptions) error {
	if opts.Detach > DetachParentSignal {
		return invalid("detach mode")
	}

	if opts.Dir != "" && !wire.ValidString(opts.Dir) {
		return invalid("working directory")
	}

	for _, f := range opts.Flags {
		if !f.Valid() {
			return invalid("client flag")
		}
	}

	return nil
}

func attachFlags(opts AttachOptions) []string {
	var flags []string

	switch opts.Detach {
	case DetachOtherClients:
		flags = append(flags, "-d")
	case DetachParentSignal:
		flags = append(flags, "-x")
	case DetachNone:
	}

	if opts.ReadOnly {
		flags = append(flags, "-r")
	}

	if opts.PreserveEnvironment {
		flags = append(flags, "-E")
	}

	if opts.Dir != "" {
		flags = append(flags, "-c", opts.Dir)
	}

	if len(opts.Flags) > 0 {
		flags = append(flags, "-f", formatClientFlags(opts.Flags))
	}

	return flags
}

func attachArgs(target string, opts AttachOptions) ([]string, error) {
	if err := validateAttachOptions(opts); err != nil {
		return nil, err
	}

	args := []string{"attach-session"}
	if target != "" {
		args = append(args, "-t", target)
	}

	return append(args, attachFlags(opts)...), nil
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
func (s *Server) PrepareAttach(ctx context.Context, session SessionID, streams TerminalStreams, opts AttachOptions) (*exec.Cmd, error) {
	if !session.Valid() {
		return nil, opError("PrepareAttach", invalid("session"))
	}

	return s.PrepareAttachTarget(ctx, string(session), streams, opts)
}

// PrepareAttachTarget returns an unstarted [exec.Cmd] for interactive terminal attachment to target
// (such as a session name, session ID, or empty string to attach to the most recently used unattached session).
// It validates streams; the caller manages Start, Wait, signals, cancellation, and exit codes.
//
// Fails with [ErrTransportUnsupported] if invoked on a control-bound server.
func (s *Server) PrepareAttachTarget(ctx context.Context, target string, streams TerminalStreams, opts AttachOptions) (*exec.Cmd, error) {
	if !wire.ValidString(target) {
		return nil, opError("PrepareAttachTarget", invalid("target"))
	}

	subArgs, err := attachArgs(target, opts)
	if err != nil {
		return nil, opError("PrepareAttachTarget", err)
	}

	argv := append(s.baseArgs(false), subArgs...)

	return s.prepareTerminalArgs(ctx, "PrepareAttachTarget", argv, streams)
}

// PrepareAttach returns an unstarted [exec.Cmd] for interactive terminal attachment to this session.
func (s Session) PrepareAttach(ctx context.Context, streams TerminalStreams, opts AttachOptions) (*exec.Cmd, error) {
	if err := s.h.check(); err != nil {
		return nil, opError("Session.PrepareAttach", err)
	}

	return s.h.server.PrepareAttachTarget(ctx, s.h.id, streams, opts)
}

// Attach attaches the caller's terminal until detach or context cancellation.
// The daemon identity is checked in the same command queue as attachment.
// Tmux owns terminal setup; the original terminal state is restored on return,
// including when cancellation terminates the local client. No command timeout applies.
// Control-bound sessions must explicitly select UsingSubprocess first.
func (s Session) Attach(ctx context.Context, streams TerminalStreams, opts AttachOptions) (err error) {
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

	argv, err := s.attachmentArgs(opts)
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

func (s Session) attachmentArgs(opts AttachOptions) ([]string, error) {
	subArgs, err := attachArgs(s.h.id, opts)
	if err != nil {
		return nil, err
	}

	// Successful attachment discards queued command output. Only the rejection
	// branch emits a marker; it never attaches.
	p := plan{nodes: []wireNode{conditionNode(s.h.origin.condition(),
		[]wireNode{leaf(command(subArgs[0], subArgs[1:]...))},
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
	cmd := s.h.server.runner.command(child, argv)
	// The tmux server renders directly to the terminal supplied on stdin.
	// Stdout/stderr carry command diagnostics and the identity-rejection marker.
	overflow := func() { cancel(ErrOutputLimit) }
	stdout := newBuffer(s.h.server.config.Limits.OutputBytes, overflow)
	stderr := newBuffer(s.h.server.config.Limits.OutputBytes, overflow)
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

func (s *Server) prepareSubprocessCheck(ctx context.Context, opName string) error {
	if s == nil || s.runner == nil {
		return opError(opName, ErrInvalidHandle)
	}

	if ctx == nil {
		return opError(opName, invalid("nil context"))
	}

	if s.conn != nil || s.bound != nil {
		return opError(opName, ErrTransportUnsupported)
	}

	return nil
}

func (s *Server) prepareTerminalArgs(ctx context.Context, opName string, argv []string, streams TerminalStreams) (*exec.Cmd, error) {
	if err := s.prepareSubprocessCheck(ctx, opName); err != nil {
		return nil, err
	}

	if err := validateTerminals(streams); err != nil {
		return nil, opError(opName, err)
	}

	cmd := s.runner.command(ctx, argv)
	cmd.Stdin = streams.In
	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err

	return cmd, nil
}

func (s *Server) prepareRawArgv(opName string, commands []Command, start StartPolicy) ([]string, error) {
	allowStart, err := s.rawAllowStart(start)
	if err != nil {
		return nil, opError(opName, err)
	}

	p := plan{nodes: make([]wireNode, 0, len(commands)), mode: replyRaw, allowStart: allowStart}
	for _, c := range commands {
		p.nodes = append(p.nodes, leaf(c))
	}

	if _, err := p.size(s.config.Limits.InputBytes); err != nil {
		return nil, opError(opName, err)
	}

	args, err := p.argv()
	if err != nil {
		return nil, opError(opName, err)
	}

	return append(s.baseArgs(allowStart), args...), nil
}

func (s *Server) rootShellArgv(opName string, shellCommand string, start StartPolicy) ([]string, error) {
	allowStart, err := s.rawAllowStart(start)
	if err != nil {
		return nil, opError(opName, err)
	}

	return append(s.baseArgs(allowStart), "-c", shellCommand), nil
}

func applyStreams(cmd *exec.Cmd, streams Streams) {
	cmd.Stdin = streams.In
	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err
}

func validateSequenceCommands(commands []Command) error {
	if len(commands) == 0 {
		return invalid("empty sequence")
	}

	for _, c := range commands {
		if !c.Valid() {
			return invalid("command")
		}
	}

	return nil
}

// PrepareTerminal returns an unstarted [exec.Cmd] for executing a single tmux command
// in an interactive terminal (such as new-session -A, attach-session, or custom window creation).
// It validates that streams refer to real terminal devices; the caller owns the process lifecycle.
//
// Fails with [ErrTransportUnsupported] on control-bound or auxiliary servers.
func (s *Server) PrepareTerminal(ctx context.Context, c Command, streams TerminalStreams, opts TerminalOptions) (*exec.Cmd, error) {
	if !c.Valid() {
		return nil, opError("PrepareTerminal", invalid("command"))
	}

	argv, err := s.prepareRawArgv("PrepareTerminal", []Command{c}, opts.Start)
	if err != nil {
		return nil, err
	}

	return s.prepareTerminalArgs(ctx, "PrepareTerminal", argv, streams)
}

// PrepareTerminalSequence returns an unstarted [exec.Cmd] for executing an ordered sequence of
// tmux commands in an interactive terminal.
// It validates that streams refer to real terminal devices; the caller owns the process lifecycle.
//
// Fails with [ErrTransportUnsupported] on control-bound or auxiliary servers.
func (s *Server) PrepareTerminalSequence(ctx context.Context, seq CommandSequence, streams TerminalStreams, opts TerminalOptions) (*exec.Cmd, error) {
	commands := seq.commands
	if err := validateSequenceCommands(commands); err != nil {
		return nil, opError("PrepareTerminalSequence", err)
	}

	argv, err := s.prepareRawArgv("PrepareTerminalSequence", commands, opts.Start)
	if err != nil {
		return nil, err
	}

	return s.prepareTerminalArgs(ctx, "PrepareTerminalSequence", argv, streams)
}

// PrepareDefaultTerminal returns an unstarted [exec.Cmd] for invoking tmux with no commands
// in an interactive terminal (ROOT.default invocation).
//
// Tmux honors the native default-client-command option (typically new-session), creating or
// attaching to a session. The server handle remains side-effect free; the caller owns the process lifecycle.
//
// Fails with [ErrTransportUnsupported] on control-bound or auxiliary servers.
func (s *Server) PrepareDefaultTerminal(ctx context.Context, streams TerminalStreams, opts TerminalOptions) (*exec.Cmd, error) {
	allowStart, err := s.rawAllowStart(opts.Start)
	if err != nil {
		return nil, opError("PrepareDefaultTerminal", err)
	}

	argv := s.baseArgs(allowStart)

	return s.prepareTerminalArgs(ctx, "PrepareDefaultTerminal", argv, streams)
}

// PrepareCommand returns an unstarted [exec.Cmd] for executing a single tmux command
// with caller-owned file descriptor streams and custom execution options.
//
// Unlike [Server.Run] and [Server.RunWith], execution is neither buffered into memory
// nor subject to [Limits.CommandTimeout]. The caller owns the process lifecycle (calling
// Start, Wait, managing signals, and closing the supplied stream files).
//
// Fails with [ErrTransportUnsupported] on control-bound or auxiliary servers.
func (s *Server) PrepareCommand(ctx context.Context, c Command, streams Streams, opts CommandOptions) (*exec.Cmd, error) {
	if err := s.prepareSubprocessCheck(ctx, "PrepareCommand"); err != nil {
		return nil, err
	}

	if !c.Valid() {
		return nil, opError("PrepareCommand", invalid("command"))
	}

	argv, err := s.prepareRawArgv("PrepareCommand", []Command{c}, opts.Start)
	if err != nil {
		return nil, err
	}

	cmd := s.runner.command(ctx, argv)
	applyStreams(cmd, streams)

	return cmd, nil
}

// PrepareSequence returns an unstarted [exec.Cmd] for executing an ordered sequence of tmux commands
// with caller-owned file descriptor streams and custom execution options.
//
// Unlike [Server.RunSequence] and [Server.RunSequenceWith], execution is neither buffered into memory
// nor subject to [Limits.CommandTimeout]. The caller owns the process lifecycle.
//
// Fails with [ErrTransportUnsupported] on control-bound or auxiliary servers.
func (s *Server) PrepareSequence(ctx context.Context, seq CommandSequence, streams Streams, opts CommandOptions) (*exec.Cmd, error) {
	if err := s.prepareSubprocessCheck(ctx, "PrepareSequence"); err != nil {
		return nil, err
	}

	commands := seq.commands
	if err := validateSequenceCommands(commands); err != nil {
		return nil, opError("PrepareSequence", err)
	}

	argv, err := s.prepareRawArgv("PrepareSequence", commands, opts.Start)
	if err != nil {
		return nil, err
	}

	cmd := s.runner.command(ctx, argv)
	applyStreams(cmd, streams)

	return cmd, nil
}

// PrepareForegroundServer returns an unstarted [exec.Cmd] for running the tmux server in the
// foreground without daemonizing (-D flag).
//
// Commands may not be specified with -D per native tmux semantics. The caller owns the process
// lifecycle, standard streams, signals, and termination. No command timeout applies, and no
// automatic daemon termination occurs outside the caller's process.
//
// Fails with [ErrTransportUnsupported] on control-bound or auxiliary servers.
func (s *Server) PrepareForegroundServer(ctx context.Context) (*exec.Cmd, error) {
	if err := s.prepareSubprocessCheck(ctx, "PrepareForegroundServer"); err != nil {
		return nil, err
	}

	argv := append(s.baseArgs(true), "-D")

	return s.runner.command(ctx, argv), nil
}

// PrepareRootShell returns an unstarted [exec.Cmd] for executing a shell command via tmux's
// root shell compatibility path (-c shell-command) with caller-owned file descriptor streams.
//
// Unlike [Server.RunShell] (which executes run-shell inside tmux), this uses native tmux -c,
// honoring default-shell semantics and start policy without rewriting to a tmux command.
//
// Fails with [ErrTransportUnsupported] on control-bound or auxiliary servers.
func (s *Server) PrepareRootShell(ctx context.Context, shellCommand string, streams Streams, opts RootShellOptions) (*exec.Cmd, error) {
	if err := s.prepareSubprocessCheck(ctx, "PrepareRootShell"); err != nil {
		return nil, err
	}

	if !wire.ValidString(shellCommand) {
		return nil, opError("PrepareRootShell", invalid("shell command"))
	}

	argv, err := s.rootShellArgv("PrepareRootShell", shellCommand, opts.Start)
	if err != nil {
		return nil, err
	}

	cmd := s.runner.command(ctx, argv)
	applyStreams(cmd, streams)

	return cmd, nil
}

// RunRootShell executes a shell command via tmux's root shell compatibility path (-c shell-command),
// capturing standard output and standard error into a bounded [Result].
//
// Fails with [ErrTransportUnsupported] on control-bound or auxiliary servers.
func (s *Server) RunRootShell(ctx context.Context, shellCommand string, opts RootShellOptions) (Result, error) {
	if err := s.prepareSubprocessCheck(ctx, "RunRootShell"); err != nil {
		return failedResult(), err
	}

	if !wire.ValidString(shellCommand) {
		return failedResult(), opError("RunRootShell", invalid("shell command"))
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), opError("RunRootShell", err)
	}
	defer op.close()

	argv, err := s.rootShellArgv("RunRootShell", shellCommand, opts.Start)
	if err != nil {
		return failedResult(), err
	}

	res, started, err := s.runner.run(opCtx, argv, nil, max(op.output, 0), max(op.stderr, 0))
	if err != nil {
		effect := NotSent
		if started {
			effect = Unknown
		}

		err = errors.Join(err, classifyStderr(res.Stderr))

		return res, opError("RunRootShell", &CommandError{
			Command: "root-shell",
			Result:  res,
			Outcome: Outcome{Effect: effect, Steps: nil, Created: nil},
			Timeout: contextSource(op.callerDone, err),
			Err:     err,
		})
	}

	return res, nil
}
