package tmux

import (
	"context"
	"strconv"
	"time"

	"github.com/zigai/gotmux/internal/wire"
)

type (
	// RunShellOptions configures execution of a shell script or tmux commands via run-shell.
	RunShellOptions struct {
		// Background executes the script in the background without blocking (-b flag).
		// When true, RunShell succeeds as soon as tmux schedules the command.
		Background bool

		// Delay specifies an optional execution delay (-d flag), sent to tmux in
		// fractional seconds. Must not be negative.
		Delay time.Duration

		// Deprecated: Cancel does not cancel a background job in tmux; native -C parses and
		// executes tmux commands instead. Setting this returns an error. Use TmuxCommands instead.
		Cancel bool

		// Deprecated: ClearEnvironment does not clear the environment in tmux; native -E enables
		// standard error output. Setting this returns an error. Use IncludeStderr instead.
		ClearEnvironment bool

		// TmuxCommands parses and executes script as tmux commands rather than a shell command (-C flag).
		TmuxCommands bool

		// IncludeStderr enables standard error output capture from the command (-E flag).
		IncludeStderr bool

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

// WaitFor blocks until tmux signals channel. A caller deadline is required and is
// the only bound: [Limits.CommandTimeout] does not apply. Canceling this client
// cannot retract an already queued server-side waiter.
//
// Fails with [ErrTransportUnsupported] on a control-bound server.
func (s *Server) WaitFor(ctx context.Context, channel string) error {
	if err := requireDeadline(ctx); err != nil {
		return opError("Server.WaitFor", err)
	}

	if s != nil && s.conn != nil {
		return opError("Server.WaitFor", ErrTransportUnsupported)
	}

	if !validFormatName(channel) {
		return opError("Server.WaitFor", invalid("channel"))
	}

	return s.endpointActionFrom(ctx, "Server.WaitFor", s.beginCallerBounded, "wait-for", "--", channel)
}

// Notify wakes all callers waiting on channel via [Server.WaitFor] (wait-for -S).
func (s *Server) Notify(ctx context.Context, channel string) error {
	if !validFormatName(channel) {
		return opError("Server.Notify", invalid("channel"))
	}

	return s.endpointAction(ctx, "Server.Notify", "wait-for", "-S", "--", channel)
}

// Lock acquires an exclusive named mutex lock on the server (wait-for -L).
// A caller deadline is required and is the only bound: [Limits.CommandTimeout]
// does not apply.
//
// Fails with [ErrTransportUnsupported] on a control-bound server.
func (s *Server) Lock(ctx context.Context, channel string) error {
	if err := requireDeadline(ctx); err != nil {
		return opError("Server.Lock", err)
	}

	if s != nil && s.conn != nil {
		return opError("Server.Lock", ErrTransportUnsupported)
	}

	if !validFormatName(channel) {
		return opError("Server.Lock", invalid("channel"))
	}

	return s.endpointActionFrom(ctx, "Server.Lock", s.beginCallerBounded, "wait-for", "-L", "--", channel)
}

// Unlock releases an exclusive named lock previously acquired via [Server.Lock] (wait-for -U).
func (s *Server) Unlock(ctx context.Context, channel string) error {
	if !validFormatName(channel) {
		return opError("Server.Unlock", invalid("channel"))
	}

	return s.endpointAction(ctx, "Server.Unlock", "wait-for", "-U", "--", channel)
}

func validateRunShell(script string, opts RunShellOptions) error {
	if opts.Cancel {
		return unsupported("RunShellOptions.Cancel is unsupported; cancellation requires explicit job tracking")
	}

	if opts.ClearEnvironment {
		return unsupported("RunShellOptions.ClearEnvironment is unsupported; native -E enables stderr output, not environment clearing")
	}

	if !wire.ValidString(script) || opts.Delay < 0 || !wire.ValidString(opts.Dir) || !wire.ValidString(opts.Target) {
		return invalid("run-shell options")
	}

	return nil
}

// runShellArgs builds and validates the command-line arguments for run-shell.
func runShellArgs(script string, opts RunShellOptions) ([]string, error) {
	if err := validateRunShell(script, opts); err != nil {
		return nil, err
	}

	var args []string

	if opts.Background {
		args = append(args, "-b")
	}

	if opts.TmuxCommands {
		args = append(args, "-C")
	}

	if opts.IncludeStderr {
		args = append(args, "-E")
	}

	if opts.Delay > 0 {
		args = append(args, "-d", strconv.FormatFloat(opts.Delay.Seconds(), 'f', -1, 64))
	}

	if opts.Dir != "" {
		args = append(args, "-c", opts.Dir)
	}

	if opts.Target != "" {
		args = append(args, "-t", opts.Target)
	}

	args = append(args, "--", script)

	return args, nil
}

// RunShell executes a shell script on the server host using tmux's run-shell command.
//
// When o.Background is true, it returns immediately after scheduling; foreground execution
// blocks until completion and captures standard output and standard error.
func (s *Server) RunShell(ctx context.Context, script string, opts RunShellOptions) (Result, error) {
	args, err := runShellArgs(script, opts)
	if err != nil {
		return failedResult(), opError("Server.RunShell", err)
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), opError("Server.RunShell", err)
	}
	defer op.close()

	info, err := s.verifiedInfo(opCtx, op)
	if err != nil {
		return failedResult(), opError("Server.RunShell", err)
	}

	p := plainPlan(command("run-shell", args...))
	if opts.Background {
		p.mode = replyEmpty
	}

	r, err := s.execute(opCtx, op, p, newGuard(info.Identity), nil)

	return r, opError("Server.RunShell", err)
}

// LockScreen locks all clients attached to the server using tmux's lock-server command.
// It is named LockScreen to avoid collision with the wait-for lock Server.Lock.
func (s *Server) LockScreen(ctx context.Context) error {
	return s.endpointAction(ctx, "Server.LockScreen", "lock-server")
}

// IfShell executes shellCommand using tmux's if-shell command and evaluates its exit status.
// If the shell command returns 0, the ifTrue sequence is executed; otherwise, the ifFalse
// sequence is executed (if non-empty).
// Unlike [Server.IfFormat], which evaluates a #{...} format expression, IfShell tests shell command exit status.
func (s *Server) IfShell(ctx context.Context, shellCommand string, ifTrue, ifFalse CommandSequence) error {
	if !wire.ValidString(shellCommand) || shellCommand == "" || len(ifTrue.commands) == 0 {
		return opError("Server.IfShell", invalid("shell command/commands"))
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return opError("Server.IfShell", err)
	}
	defer op.close()

	info, err := s.verifiedInfo(opCtx, op)
	if err != nil {
		return opError("Server.IfShell", err)
	}

	node := wireNode{name: "if-shell", args: []wireArg{
		{text: shellCommand, nested: nil},
		{text: "", nested: ifTrue.nodes()},
	}}
	if len(ifFalse.commands) > 0 {
		node.args = append(node.args, wireArg{text: "", nested: ifFalse.nodes()})
	}

	_, err = s.execute(opCtx, op, plan{nodes: []wireNode{node}, mode: replyRaw, allowStart: false}, newGuard(info.Identity), nil)

	return opError("Server.IfShell", err)
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

func (s *Server) source(ctx context.Context, opName, target string, input []byte, opts SourceOptions) (Result, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), opError(opName, err)
	}
	defer op.close()

	if input != nil && int64(len(input)) > op.input {
		return failedResult(), opError(opName, ErrInputLimit)
	}

	info, err := s.verifiedInfo(opCtx, op)
	if err != nil {
		return failedResult(), opError(opName, err)
	}

	args := append(opts.flags(), "--", target)
	r, err := s.execute(opCtx, op, plainPlan(command("source-file", args...)), newGuard(info.Identity), input)

	return r, opError(opName, err)
}

// SourceFile loads and executes tmux configuration commands from the specified path (source-file).
func (s *Server) SourceFile(ctx context.Context, path string, opts SourceOptions) (Result, error) {
	if !wire.ValidString(path) || path == "" || path == "-" {
		return failedResult(), opError("Server.SourceFile", invalid("config path"))
	}

	return s.source(ctx, "Server.SourceFile", path, nil, opts)
}

// SourceText loads and executes tmux configuration commands from a string via stdin (source-file -).
// Direct evaluation supports multiline commands, conditionals, braces, and escaped literals.
// Fails with [ErrTransportUnsupported] over control mode; use [Connection.SubprocessServer] instead.
func (s *Server) SourceText(ctx context.Context, text string, opts SourceOptions) (Result, error) {
	if s == nil || s.runner == nil {
		return failedResult(), opError("Server.SourceText", ErrInvalidHandle)
	}

	if s.conn != nil {
		return failedResult(), opError("Server.SourceText", unsupportedControl("streaming configuration text over control transport", ErrTransportUnsupported))
	}

	if !wire.ValidString(text) {
		return failedResult(), opError("Server.SourceText", invalid("config text"))
	}

	return s.source(ctx, "Server.SourceText", "-", []byte(text), opts)
}

// IfFormat uses tmux's synchronous format condition, not shell truthiness. The
// nested command sequence can deliberately run shell-capable commands.
func (s *Server) IfFormat(ctx context.Context, condition Format, yes, no CommandSequence) (Result, error) {
	if !wire.ValidString(string(condition)) || condition == "" || len(yes.commands) == 0 {
		return failedResult(), opError("Server.IfFormat", invalid("condition/commands"))
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), opError("Server.IfFormat", err)
	}
	defer op.close()

	info, err := s.verifiedInfo(opCtx, op)
	if err != nil {
		return failedResult(), opError("Server.IfFormat", err)
	}

	node := conditionNode(string(condition), yes.nodes(), no.nodes(), "")
	if len(no.commands) == 0 {
		node.args = node.args[:len(node.args)-1]
	}

	r, err := s.execute(opCtx, op, plan{nodes: []wireNode{node}, mode: replyRaw, allowStart: false}, newGuard(info.Identity), nil)

	return r, opError("Server.IfFormat", err)
}

// KillMatching kills only a daemon matching the supplied observed identity.
// It never signals a PID directly; isolated fixtures use this for safe cleanup.
func (s *Server) KillMatching(ctx context.Context, id ServerIdentity) error {
	if s == nil || !id.valid() || id.Endpoint != s.endpoint {
		return opError("Server.KillMatching", ErrInvalidHandle)
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return opError("Server.KillMatching", err)
	}
	defer op.close()

	_, err = s.execute(opCtx, op, emptyPlan(command("kill-server")), newGuard(id), nil)

	return opError("Server.KillMatching", err)
}
