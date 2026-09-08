package tmux

import (
	"context"
	"time"

	"example.com/tmux/internal/codec"
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
// cancelling this client cannot retract an already queued server-side waiter.
func (s *Server) WaitFor(ctx context.Context, channel string) error {
	if e := requireDeadline(ctx); e != nil {
		return opError("WaitFor", e)
	}
	if !validFormatName(channel) {
		return opError("WaitFor", invalid("channel"))
	}
	return s.endpointAction(ctx, "wait-for", "--", channel)
}
func (s *Server) Signal(ctx context.Context, channel string) error {
	if !validFormatName(channel) {
		return opError("Signal", invalid("channel"))
	}
	return s.endpointAction(ctx, "wait-for", "-S", "--", channel)
}
func (s *Server) Lock(ctx context.Context, channel string) error {
	if e := requireDeadline(ctx); e != nil {
		return opError("Lock", e)
	}
	if !validFormatName(channel) {
		return opError("Lock", invalid("channel"))
	}
	return s.endpointAction(ctx, "wait-for", "-L", "--", channel)
}
func (s *Server) Unlock(ctx context.Context, channel string) error {
	if !validFormatName(channel) {
		return opError("Unlock", invalid("channel"))
	}
	return s.endpointAction(ctx, "wait-for", "-U", "--", channel)
}

type RunShellOptions struct {
	Background bool
	Delay      time.Duration
}

// RunShell deliberately interprets a script on the daemon. Background success
// acknowledges scheduling, not script completion. Foreground results are raw.
func (s *Server) RunShell(ctx context.Context, script string, o RunShellOptions) (Result, error) {
	if !codec.ValidString(script) || o.Delay < 0 {
		return Result{ExitCode: -1}, opError("RunShell", invalid("script/delay"))
	}
	if o.Delay != 0 {
		return Result{ExitCode: -1}, opError("RunShell", &UnsupportedError{Feature: "run-shell delay requires the newer flag ledger"})
	}
	op, e := s.begin(ctx)
	if e != nil {
		return Result{ExitCode: -1}, opError("RunShell", e)
	}
	defer op.close()
	info, e := s.probe(op)
	if e != nil {
		return Result{ExitCode: -1}, opError("RunShell", e)
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
	r, e := s.execute(op, p, &guard{identity: info.Identity}, nil)
	return r, opError("RunShell", e)
}

type SourceOptions struct {
	QuietMissing bool
	ParseOnly    bool
	Verbose      bool
}

func (s *Server) SourceFile(ctx context.Context, path string, o SourceOptions) (Result, error) {
	if !codec.ValidString(path) || path == "" || path == "-" {
		return Result{ExitCode: -1}, opError("SourceFile", invalid("config path"))
	}
	op, e := s.begin(ctx)
	if e != nil {
		return Result{ExitCode: -1}, opError("SourceFile", e)
	}
	defer op.close()
	info, e := s.probe(op)
	if e != nil {
		return Result{ExitCode: -1}, opError("SourceFile", e)
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
	r, e := s.execute(op, plainPlan(command("source-file", args...)), &guard{identity: info.Identity}, nil)
	return r, opError("SourceFile", e)
}

// IfFormat uses tmux's synchronous format condition, not shell truthiness. The
// nested command sequence can deliberately run shell-capable commands.
func (s *Server) IfFormat(ctx context.Context, condition Format, yes, no CommandSequence) (Result, error) {
	if !codec.ValidString(string(condition)) || condition == "" || len(yes.commands) == 0 {
		return Result{ExitCode: -1}, opError("IfFormat", invalid("condition/commands"))
	}
	op, e := s.begin(ctx)
	if e != nil {
		return Result{ExitCode: -1}, opError("IfFormat", e)
	}
	defer op.close()
	info, e := s.probe(op)
	if e != nil {
		return Result{ExitCode: -1}, opError("IfFormat", e)
	}
	node := conditionNode(string(condition), yes.nodes(), no.nodes(), "")
	if len(no.commands) == 0 {
		node.args = node.args[:len(node.args)-1]
	}
	r, e := s.execute(op, plan{nodes: []wireNode{node}, mode: replyRaw, nested: true}, &guard{identity: info.Identity}, nil)
	return r, opError("IfFormat", e)
}

// KillIfIdentity kills only a daemon matching the supplied observed identity.
// It never signals a PID directly; isolated fixtures use this for safe cleanup.
func (s *Server) KillIfIdentity(ctx context.Context, id ServerIdentity) error {
	if s == nil || !id.valid() || id.Endpoint != s.endpoint {
		return opError("KillIfIdentity", ErrInvalidHandle)
	}
	op, e := s.begin(ctx)
	if e != nil {
		return opError("KillIfIdentity", e)
	}
	defer op.close()
	_, e = s.execute(op, emptyPlan(command("kill-server")), &guard{identity: id}, nil)
	return opError("KillIfIdentity", e)
}
