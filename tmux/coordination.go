package tmux

import (
	"context"
	"time"

	"github.com/zigai/gotmux/internal/codec"
)

type (
	// RunShellOptions configures execution of a shell script on the tmux server.
	RunShellOptions struct {
		// Background executes the script in the background without blocking (-b flag).
		// When true, RunShell succeeds as soon as tmux schedules the command.
		Background bool

		// Delay specifies an optional execution delay.
		Delay time.Duration
	}

	// SourceOptions configures loading a tmux configuration file.
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

// RunShell executes a shell script on the server host using tmux's run-shell command.
//
// When o.Background is true, it returns immediately after scheduling; foreground execution
// blocks until completion and captures standard output and standard error.
func (s *Server) RunShell(ctx context.Context, script string, o RunShellOptions) (Result, error) {
	if !codec.ValidString(script) || o.Delay < 0 {
		return failedResult(), opError("RunShell", invalid("script/delay"))
	}

	if o.Delay != 0 {
		return failedResult(), opError("RunShell", unsupported("run-shell delay requires the newer flag ledger"))
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

	args := []string{}
	if o.Background {
		args = append(args, "-b")
	}

	args = append(args, "--", script)

	p := plainPlan(command("run-shell", args...))
	if o.Background {
		p.mode = replyEmpty
	}

	r, err := s.execute(opCtx, op, p, newGuard(info.Identity), nil)

	return r, opError("RunShell", err)
}

// SourceFile loads and executes tmux configuration commands from the specified path (source-file).
func (s *Server) SourceFile(ctx context.Context, path string, o SourceOptions) (Result, error) {
	if !codec.ValidString(path) || path == "" || path == "-" {
		return failedResult(), opError("SourceFile", invalid("config path"))
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), opError("SourceFile", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return failedResult(), opError("SourceFile", err)
	}

	args := []string{}
	if o.QuietMissing {
		args = append(args, "-q")
	}

	if o.ParseOnly {
		args = append(args, "-n")
	}

	if o.Verbose {
		args = append(args, "-v")
	}

	args = append(args, "--", path)
	r, err := s.execute(opCtx, op, plainPlan(command("source-file", args...)), newGuard(info.Identity), nil)

	return r, opError("SourceFile", err)
}

// IfFormat uses tmux's synchronous format condition, not shell truthiness. The
// nested command sequence can deliberately run shell-capable commands.
func (s *Server) IfFormat(ctx context.Context, condition Format, yes, no CommandSequence) (Result, error) {
	if !codec.ValidString(string(condition)) || condition == "" || len(yes.commands) == 0 {
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
