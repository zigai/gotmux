package tmux

import (
	"bytes"
	"context"
	"io"
	"strings"

	"example.com/tmux/internal/codec"
)

// Command is one immutable tmux command, not a shell script. Arguments are
// literal at the command parser layer. Operand semantics (for example run-shell)
// are still those of the selected tmux command.
type Command struct {
	name string
	args []string
}

func NewCommand(name string, args ...string) (Command, error) {
	if !codec.ValidCommand(name) {
		return Command{}, invalid("command name")
	}
	for _, a := range args {
		if !codec.ValidString(a) {
			return Command{}, invalid("NUL argument")
		}
	}
	return Command{name: name, args: append([]string{}, args...)}, nil
}
func (c Command) Name() string   { return c.name }
func (c Command) Args() []string { return append([]string{}, c.args...) }
func (c Command) Valid() bool {
	if !codec.ValidCommand(c.name) {
		return false
	}
	for _, a := range c.args {
		if !codec.ValidString(a) {
			return false
		}
	}
	return true
}
func command(name string, args ...string) Command { return Command{name: name, args: args} }

type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type wireArg struct {
	text   string
	nested []wireNode
}
type wireNode struct {
	name string
	args []wireArg
}
type replyMode uint8

const (
	replyRaw replyMode = iota
	replyRecords
	replyEmpty
	replyTokens
)

type plan struct {
	nodes      []wireNode
	mode       replyMode
	allowStart bool
	nested     bool
}

func leaf(c Command) wireNode {
	a := make([]wireArg, len(c.args))
	for i, s := range c.args {
		a[i] = wireArg{text: s}
	}
	return wireNode{name: c.name, args: a}
}
func plainPlan(c Command) plan   { return plan{nodes: []wireNode{leaf(c)}, mode: replyRaw} }
func recordsPlan(c Command) plan { p := plainPlan(c); p.mode = replyRecords; return p }
func emptyPlan(c Command) plan   { p := plainPlan(c); p.mode = replyEmpty; return p }
func writeNodes(w io.Writer, nodes []wireNode) error {
	for i, n := range nodes {
		if i > 0 {
			if _, e := io.WriteString(w, " ; "); e != nil {
				return e
			}
		}
		if !codec.ValidCommand(n.name) {
			return invalid("command name")
		}
		if _, e := io.WriteString(w, n.name); e != nil {
			return e
		}
		for _, a := range n.args {
			if _, e := io.WriteString(w, " "); e != nil {
				return e
			}
			if a.nested == nil {
				if e := codec.Quoted(w, a.text); e != nil {
					return e
				}
			} else {
				if _, e := io.WriteString(w, `"`); e != nil {
					return e
				}
				if e := writeNodes(codec.QuoteWriter{W: w}, a.nested); e != nil {
					return e
				}
				if _, e := io.WriteString(w, `"`); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func (p plan) size(max int64) (int64, error) {
	c := codec.Counter{Limit: max}
	if e := writeNodes(&c, p.nodes); e != nil {
		return 0, ErrInputLimit
	}
	if c.N >= max {
		return 0, ErrInputLimit
	}
	return c.N + 1, nil
}
func (p plan) text() (string, error) {
	var b strings.Builder
	e := writeNodes(&b, p.nodes)
	if e != nil {
		return "", e
	}
	b.WriteByte('\n')
	return b.String(), nil
}
func (p plan) argv() ([]string, error) {
	out := []string{}
	for i, n := range p.nodes {
		if i > 0 {
			out = append(out, ";")
		}
		out = append(out, n.name)
		for _, a := range n.args {
			s := a.text
			if a.nested != nil {
				var b strings.Builder
				if e := writeNodes(&b, a.nested); e != nil {
					return nil, e
				}
				s = b.String()
			}
			out = append(out, codec.Argv(s))
		}
	}
	return out, nil
}

// Run executes one endpoint-relative raw command. It does not infer handle
// generation safety. Output-producing/ambiguous raw control commands fail before
// dispatch; use the original subprocess server for those commands.
func (s *Server) Run(ctx context.Context, c Command) (Result, error) {
	op, e := s.begin(ctx)
	if e != nil {
		return Result{ExitCode: -1}, &CommandError{Command: c.name, Result: Result{ExitCode: -1}, Err: e}
	}
	defer op.close()
	if !c.Valid() {
		return Result{ExitCode: -1}, &CommandError{Command: c.name, Result: Result{ExitCode: -1}, Err: invalid("command")}
	}
	return s.execute(op, plainPlan(c), nil, nil)
}

// RunSequence returns combined output and never promises rollback or per-command
// success. Order within this sequence is explicit; order across goroutines isn't.
func (s *Server) RunSequence(ctx context.Context, commands []Command) (Result, error) {
	op, e := s.begin(ctx)
	if e != nil {
		return Result{ExitCode: -1}, &CommandError{Command: "sequence", Result: Result{ExitCode: -1}, Err: e}
	}
	defer op.close()
	if len(commands) == 0 {
		return Result{Stdout: []byte{}, Stderr: []byte{}, ExitCode: -1}, nil
	}
	p := plan{mode: replyRaw}
	for _, c := range commands {
		if !c.Valid() {
			return Result{ExitCode: -1}, &CommandError{Command: "sequence", Result: Result{ExitCode: -1}, Err: invalid("command")}
		}
		p.nodes = append(p.nodes, leaf(c))
	}
	return s.execute(op, p, nil, nil)
}

// cloneResult never aliases retained connection state.
func cloneResult(r Result) Result {
	r.Stdout = bytes.Clone(r.Stdout)
	r.Stderr = bytes.Clone(r.Stderr)
	return r
}
