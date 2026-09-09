package tmux

import (
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/zigai/gotmux/internal/codec"
)

const (
	replyRaw replyMode = iota
	replyRecords
	replyEmpty
)

type replyMode uint8

// Command represents a single immutable tmux command and its literal arguments.
//
// Unlike shell scripts, arguments in a Command are passed literally at the tmux parser layer:
// they are escaped to prevent accidental command splitting or format expansion.
// Operand semantics (for example, the shell command passed to "run-shell") are interpreted
// by tmux according to that specific command's specification.
type Command struct {
	name string
	args []string
}

// Result captures the bounded output and termination status of a command execution.
type Result struct {
	// Stdout contains bytes captured from standard output, capped by [Limits.OutputBytes].
	Stdout []byte

	// Stderr contains bytes captured from standard error, capped by [Limits.OutputBytes].
	Stderr []byte

	// ExitCode is the process exit code returned by tmux.
	// It is -1 for commands executed over a control connection or when a subprocess
	// is terminated by a signal without a normal exit code.
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

type plan struct {
	nodes      []wireNode
	mode       replyMode
	allowStart bool
}

// NewCommand validates and constructs an immutable [Command].
// It verifies that the command name is non-empty and valid, and that none of the
// argument strings contain embedded NUL bytes (which cannot be represented in tmux argv).
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

// Name returns the primary name of the tmux command (e.g. "new-session", "split-window").
func (c Command) Name() string { return c.name }

// Args returns an owned copy of the command's arguments.
func (c Command) Args() []string { return append([]string{}, c.args...) }

// Valid reports whether the command name is a valid tmux identifier and all arguments
// are free of NUL bytes.
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

func leaf(c Command) wireNode {
	a := make([]wireArg, len(c.args))
	for i, s := range c.args {
		a[i] = wireArg{text: s, nested: nil}
	}

	return wireNode{name: c.name, args: a}
}

func plainPlan(c Command) plan {
	return plan{nodes: []wireNode{leaf(c)}, mode: replyRaw, allowStart: false}
}

func recordsPlan(c Command) plan {
	p := plainPlan(c)
	p.mode = replyRecords

	return p
}

func emptyPlan(c Command) plan {
	p := plainPlan(c)
	p.mode = replyEmpty

	return p
}

func writeArg(w io.Writer, a wireArg) error {
	if a.nested == nil {
		if err := codec.Quoted(w, a.text); err != nil {
			return err //nolint:wrapcheck // codec.Quoted writes directly to w
		}

		return nil
	}

	if _, err := io.WriteString(w, `"`); err != nil {
		return err //nolint:wrapcheck // writeArg writes directly to io.Writer
	}

	if err := writeNodes(codec.QuoteWriter{W: w}, a.nested); err != nil {
		return err
	}

	if _, err := io.WriteString(w, `"`); err != nil {
		return err //nolint:wrapcheck // writeArg writes directly to io.Writer
	}

	return nil
}

func writeNode(w io.Writer, n wireNode) error {
	if !codec.ValidCommand(n.name) {
		return invalid("command name")
	}

	if _, err := io.WriteString(w, n.name); err != nil {
		return err //nolint:wrapcheck // writeNode writes directly to io.Writer
	}

	for _, a := range n.args {
		if _, err := io.WriteString(w, " "); err != nil {
			return err //nolint:wrapcheck // writeNode writes directly to io.Writer
		}

		if err := writeArg(w, a); err != nil {
			return err
		}
	}

	return nil
}

func writeNodes(w io.Writer, nodes []wireNode) error {
	for i, n := range nodes {
		if i > 0 {
			if _, err := io.WriteString(w, " ; "); err != nil {
				return err //nolint:wrapcheck // writeNodes writes directly to io.Writer and propagates underlying writer errors
			}
		}

		if err := writeNode(w, n); err != nil {
			return err
		}
	}

	return nil
}

func (p plan) size(maxBytes int64) (int64, error) {
	c := codec.Counter{Limit: maxBytes, N: 0, Err: nil}
	if err := writeNodes(&c, p.nodes); err != nil {
		return 0, ErrInputLimit
	}

	if c.N >= maxBytes {
		return 0, ErrInputLimit
	}

	return c.N + 1, nil
}

func (p plan) text() (string, error) {
	var b strings.Builder

	err := writeNodes(&b, p.nodes)
	if err != nil {
		return "", err
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
				if err := writeNodes(&b, a.nested); err != nil {
					return nil, err
				}

				s = b.String()
			}

			out = append(out, codec.Argv(s))
		}
	}

	return out, nil
}

// Run executes one raw tmux command against the server and returns its captured output.
//
// Unlike typed handle methods, Run is endpoint-relative: it does not assert daemon identity
// or window link guards, and targets are resolved by tmux according to its standard rules.
//
// Raw commands require subprocess execution. On a control-bound server, use
// [Server.UsingSubprocess]; Run otherwise returns [ErrTransportUnsupported] before dispatch.
func (s *Server) Run(ctx context.Context, c Command) (Result, error) {
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), &CommandError{Command: c.name, Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: err}
	}
	defer op.close()

	if !c.Valid() {
		return failedResult(), &CommandError{Command: c.name, Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: invalid("command")}
	}

	return s.execute(opCtx, op, plainPlan(c), nil, nil)
}

// RunSequence executes an ordered list of commands in a single round-trip joined by semicolons.
//
// Execution is sequential within this sequence, but tmux does NOT provide transactional rollback:
// if command N fails, changes made by commands 0 through N-1 remain in effect.
// Stdout and Stderr contain the combined output of all executed commands.
func (s *Server) RunSequence(ctx context.Context, sequence CommandSequence) (Result, error) {
	commands := sequence.commands

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), &CommandError{Command: "sequence", Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: err}
	}
	defer op.close()

	if len(commands) == 0 {
		return Result{Stdout: []byte{}, Stderr: []byte{}, ExitCode: -1}, nil
	}

	p := plan{nodes: nil, mode: replyRaw, allowStart: false}

	for _, c := range commands {
		if !c.Valid() {
			return failedResult(), &CommandError{Command: "sequence", Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: invalid("command")}
		}

		p.nodes = append(p.nodes, leaf(c))
	}

	return s.execute(opCtx, op, p, nil, nil)
}

// cloneResult never aliases retained connection state.
func cloneResult(r Result) Result {
	r.Stdout = bytes.Clone(r.Stdout)
	r.Stderr = bytes.Clone(r.Stderr)

	return r
}
