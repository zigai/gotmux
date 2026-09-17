package tmux

import (
	"context"
	"math"
	"strconv"

	"github.com/zigai/gotmux/internal/wire"
)

type (
	// RunShellOptions configures execution of a shell script on the tmux server.
	RunShellOptions struct {
		// Background executes the script in the background without blocking (-b flag).
		// When true, RunShell succeeds as soon as tmux schedules the command.
		Background bool

		// Delay specifies an optional execution delay in seconds (-d flag).
		// Supports fractional seconds (e.g. 0.5). Must be non-negative.
		Delay float64

		// Cancel cancels an existing background shell command (-C flag).
		Cancel bool

		// ClearEnvironment prevents default TMUX environment variables from being set (-E flag).
		ClearEnvironment bool

		// Dir specifies the working directory for the shell command (-c flag).
		Dir string

		// Target specifies the target pane for the command (-t flag).
		Target string
	}

	// SourceOptions configures loading tmux configuration commands from a file or text string.
	SourceOptions struct {
		// QuietMissing ignores missing file errors silently (-q flag).
		QuietMissing bool

		// ParseOnly validates configuration file syntax without executing commands (-n flag).
		ParseOnly bool

		// Verbose includes executed commands in the captured output (-v flag).
		Verbose bool
	}
)

func requireDeadline(ctx context.Context) error {
	if ctx == nil {
		return invalid("nil context")
	}

	if _, ok := ctx.Deadline(); !ok {
		return invalid("an explicit caller deadline is required")
	}

	return nil
}

// WaitFor blocks until tmux signals channel. A caller deadline is required;
// canceling this client cannot retract an already queued server-side waiter.
func (s *Server) WaitFor(ctx context.Context, channel string) error {
	if err := requireDeadline(ctx); err != nil {
		return opError("WaitFor", err)
	}

	if !validFormatName(channel) {
		return opError("WaitFor", invalid("channel"))
	}

	return s.endpointAction(ctx, "wait-for", "--", channel)
}

// Signal wakes all callers waiting on channel via [Server.WaitFor] (wait-for -S).
func (s *Server) Signal(ctx context.Context, channel string) error {
	if !validFormatName(channel) {
		return opError("Signal", invalid("channel"))
	}

	return s.endpointAction(ctx, "wait-for", "-S", "--", channel)
}

// Lock acquires an exclusive named mutex lock on the server (wait-for -L).
// A caller deadline is required.
func (s *Server) Lock(ctx context.Context, channel string) error {
	if err := requireDeadline(ctx); err != nil {
		return opError("Lock", err)
	}

	if !validFormatName(channel) {
		return opError("Lock", invalid("channel"))
	}

	return s.endpointAction(ctx, "wait-for", "-L", "--", channel)
}

// Unlock releases an exclusive named lock previously acquired via [Server.Lock] (wait-for -U).
func (s *Server) Unlock(ctx context.Context, channel string) error {
	if !validFormatName(channel) {
		return opError("Unlock", invalid("channel"))
	}

	return s.endpointAction(ctx, "wait-for", "-U", "--", channel)
}

func validateRunShell(script string, o RunShellOptions) error {
	if !wire.ValidString(script) || o.Delay < 0 || math.IsNaN(o.Delay) || math.IsInf(o.Delay, 0) || !wire.ValidString(o.Dir) || !wire.ValidString(o.Target) {
		return invalid("run-shell options")
	}

	return nil
}

// runShellArgs builds and validates the command-line arguments for run-shell.
func runShellArgs(script string, o RunShellOptions) ([]string, error) {
	if err := validateRunShell(script, o); err != nil {
		return nil, err
	}

	var args []string

	if o.Background {
		args = append(args, "-b")
	}

	if o.Cancel {
		args = append(args, "-C")
	}

	if o.ClearEnvironment {
		args = append(args, "-E")
	}

	if o.Delay > 0 {
		args = append(args, "-d", strconv.FormatFloat(o.Delay, 'f', -1, 64))
	}

	if o.Dir != "" {
		args = append(args, "-c", o.Dir)
	}

	if o.Target != "" {
		args = append(args, "-t", o.Target)
	}

	args = append(args, "--", script)

	return args, nil
}

// RunShell executes a shell script on the server host using tmux's run-shell command.
//
// When o.Background is true, it returns immediately after scheduling; foreground execution
// blocks until completion and captures standard output and standard error.
func (s *Server) RunShell(ctx context.Context, script string, o RunShellOptions) (Result, error) {
	args, err := runShellArgs(script, o)
	if err != nil {
		return failedResult(), opError("RunShell", err)
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), opError("RunShell", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return failedResult(), opError("RunShell", err)
	}

	p := plainPlan(command("run-shell", args...))
	if o.Background {
		p.mode = replyEmpty
	}

	r, err := s.execute(opCtx, op, p, newGuard(info.Identity), nil)

	return r, opError("RunShell", err)
}

// LockScreen locks all clients attached to the server using tmux's lock-server command.
// It is named LockScreen to avoid collision with the wait-for lock Server.Lock.
func (s *Server) LockScreen(ctx context.Context) error {
	return s.endpointAction(ctx, "lock-server")
}

// IfShell executes shellCommand using tmux's if-shell command and evaluates its exit status.
// If the shell command returns 0, the ifTrue sequence is executed; otherwise, the ifFalse
// sequence is executed (if non-empty).
// Unlike [Server.IfFormat], which evaluates a #{...} format expression, IfShell tests shell command exit status.
func (s *Server) IfShell(ctx context.Context, shellCommand string, ifTrue, ifFalse CommandSequence) error {
	if !wire.ValidString(shellCommand) || shellCommand == "" || len(ifTrue.commands) == 0 {
		return opError("IfShell", invalid("shell command/commands"))
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return opError("IfShell", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return opError("IfShell", err)
	}

	node := wireNode{name: "if-shell", args: []wireArg{
		{text: shellCommand, nested: nil},
		{text: "", nested: ifTrue.nodes()},
	}}
	if len(ifFalse.commands) > 0 {
		node.args = append(node.args, wireArg{text: "", nested: ifFalse.nodes()})
	}

	_, err = s.execute(opCtx, op, plan{nodes: []wireNode{node}, mode: replyRaw, allowStart: false}, newGuard(info.Identity), nil)

	return opError("IfShell", err)
}

func (o SourceOptions) flags() []string {
	var args []string
	if o.QuietMissing {
		args = append(args, "-q")
	}

	if o.ParseOnly {
		args = append(args, "-n")
	}

	if o.Verbose {
		args = append(args, "-v")
	}

	return args
}

func (s *Server) source(ctx context.Context, opName, target string, input []byte, o SourceOptions) (Result, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), opError(opName, err)
	}
	defer op.close()

	if input != nil && int64(len(input)) > op.input {
		return failedResult(), opError(opName, ErrInputLimit)
	}

	info, err := s.probe(opCtx, op)
	if err != nil {
		return failedResult(), opError(opName, err)
	}

	args := append(o.flags(), "--", target)
	r, err := s.execute(opCtx, op, plainPlan(command("source-file", args...)), newGuard(info.Identity), input)

	return r, opError(opName, err)
}

// SourceFile loads and executes tmux configuration commands from the specified path (source-file).
func (s *Server) SourceFile(ctx context.Context, path string, o SourceOptions) (Result, error) {
	if !wire.ValidString(path) || path == "" || path == "-" {
		return failedResult(), opError("SourceFile", invalid("config path"))
	}

	return s.source(ctx, "SourceFile", path, nil, o)
}

// SourceText loads and executes tmux configuration commands from a string via stdin (source-file -).
//
// Like [Server.SourceFile], tmux evaluates the configuration directly, supporting multiline commands,
// %if/%elif/%else/%endif conditionals, braces, and escaped literals without Go-side reparsing.
//
// Fails with [ErrTransportUnsupported] over control mode because control mode cannot frame standard input.
// Callers must use [Connection.AuxiliaryServer] explicitly for streaming configuration text.
func (s *Server) SourceText(ctx context.Context, text string, o SourceOptions) (Result, error) {
	if s.conn != nil {
		return failedResult(), opError("SourceText", unsupportedControl("streaming configuration text over control transport", ErrTransportUnsupported))
	}

	if !wire.ValidString(text) {
		return failedResult(), opError("SourceText", invalid("config text"))
	}

	return s.source(ctx, "SourceText", "-", []byte(text), o)
}

// IfFormat uses tmux's synchronous format condition, not shell truthiness. The
// nested command sequence can deliberately run shell-capable commands.
func (s *Server) IfFormat(ctx context.Context, condition Format, yes, no CommandSequence) (Result, error) {
	if !wire.ValidString(string(condition)) || condition == "" || len(yes.commands) == 0 {
		return failedResult(), opError("IfFormat", invalid("condition/commands"))
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), opError("IfFormat", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return failedResult(), opError("IfFormat", err)
	}

	node := conditionNode(string(condition), yes.nodes(), no.nodes(), "")
	if len(no.commands) == 0 {
		node.args = node.args[:len(node.args)-1]
	}

	r, err := s.execute(opCtx, op, plan{nodes: []wireNode{node}, mode: replyRaw, allowStart: false}, newGuard(info.Identity), nil)

	return r, opError("IfFormat", err)
}

// KillIfIdentity kills only a daemon matching the supplied observed identity.
// It never signals a PID directly; isolated fixtures use this for safe cleanup.
func (s *Server) KillIfIdentity(ctx context.Context, id ServerIdentity) error {
	if s == nil || !id.valid() || id.Endpoint != s.endpoint {
		return opError("KillIfIdentity", ErrInvalidHandle)
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return opError("KillIfIdentity", err)
	}
	defer op.close()

	_, err = s.execute(opCtx, op, emptyPlan(command("kill-server")), newGuard(id), nil)

	return opError("KillIfIdentity", err)
}
