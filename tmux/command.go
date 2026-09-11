package tmux

import (
	"bytes"
	"context"
	"io"
	"strings"

	"github.com/zigai/gotmux/internal/wire"
)

const (
	replyRaw replyMode = iota
	replyRecords
	replyEmpty
)

type replyMode uint8

// Command represents an immutable tmux command with literal string arguments.
// Arguments are safely escaped and not evaluated by a shell.
type (
	Command struct {
		name string
		args []string
	}

	// CommandSequence represents an immutable, validated sequence of tmux commands.
	CommandSequence struct{ commands []Command }

	// Result captures bounded output and termination status from a command execution.
	Result struct {
		// Stdout contains standard output bytes, capped by [Limits.OutputBytes].
		Stdout []byte

		// Stderr contains standard error bytes, capped by [Limits.OutputBytes].
		Stderr []byte

		// ExitCode is the process exit code, or -1 for control mode and un-signaled errors.
		ExitCode int
	}

	wireArg struct {
		text   string
		nested []wireNode
	}

	wireNode struct {
		name string
		args []wireArg
	}

	plan struct {
		nodes      []wireNode
		mode       replyMode
		allowStart bool
	}
)

// Sequence validates and constructs an immutable [CommandSequence].
// Invalid commands return an error; an empty sequence is valid.
func Sequence(commands ...Command) (CommandSequence, error) {
	out := make([]Command, len(commands))
	for i, c := range commands {
		if !c.Valid() {
			return CommandSequence{}, invalid("command sequence")
		}

		out[i], _ = NewCommand(c.name, c.args...)
	}

	return CommandSequence{commands: out}, nil
}

func (s CommandSequence) Commands() []Command {
	out := make([]Command, len(s.commands))
	for i, c := range s.commands {
		out[i], _ = NewCommand(c.name, c.args...)
	}

	return out
}

func (s CommandSequence) nodes() []wireNode {
	out := make([]wireNode, 0, len(s.commands))
	for _, c := range s.commands {
		out = append(out, leaf(c))
	}

	return out
}

// NewCommand validates and constructs an immutable [Command].
// Rejects empty names and arguments containing NUL bytes.
func NewCommand(name string, args ...string) (Command, error) {
	if !wire.ValidCommand(name) {
		return Command{}, invalid("command name")
	}

	for _, a := range args {
		if !wire.ValidString(a) {
			return Command{}, invalid("NUL argument")
		}
	}

	return Command{name: name, args: append([]string{}, args...)}, nil
}

// ParseSequence parses a semicolon-delimited compound tmux command string into a [CommandSequence].
func ParseSequence(text string) (CommandSequence, error) {
	parts, err := wire.SplitSequence(text)
	if err != nil {
		return CommandSequence{}, opError("ParseSequence", err)
	}

	var cmds []Command

	for _, part := range parts {
		words, err := wire.ParseWords(part)
		if err != nil {
			return CommandSequence{}, opError("ParseSequence", err)
		}

		if len(words) == 0 {
			continue
		}

		cmd, err := NewCommand(words[0], words[1:]...)
		if err != nil {
			return CommandSequence{}, opError("ParseSequence", err)
		}

		cmds = append(cmds, cmd)
	}

	return Sequence(cmds...)
}

// ParseCommandLine extracts global flags (-S, -L, -f) from a tmux command-line argv,
// returning a [Config] and the remaining [Command].
func ParseCommandLine(args []string) (Config, Command, error) {
	cfg, i := parseGlobalFlags(args)
	if i >= len(args) {
		return cfg, Command{}, invalid("no command found in argv")
	}

	cmd, err := NewCommand(args[i], args[i+1:]...)
	if err != nil {
		return cfg, Command{}, err
	}

	return cfg, cmd, nil
}

// Name returns the primary name of the tmux command (e.g. "new-session", "split-window").
func (c Command) Name() string { return c.name }

// Args returns an owned copy of the command's arguments.
func (c Command) Args() []string { return append([]string{}, c.args...) }

// Valid reports whether the command name is a valid tmux identifier and all arguments
// are free of NUL bytes.
func (c Command) Valid() bool {
	if !wire.ValidCommand(c.name) {
		return false
	}

	for _, a := range c.args {
		if !wire.ValidString(a) {
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

func commandStartsServer(name string) bool {
	return name == "new-session" || name == "start-server"
}

func plainPlan(c Command) plan {
	return plan{nodes: []wireNode{leaf(c)}, mode: replyRaw, allowStart: commandStartsServer(c.name)}
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
		if err := wire.Quoted(w, a.text); err != nil {
			return err //nolint:wrapcheck // wire.Quoted writes directly to w
		}

		return nil
	}

	if _, err := io.WriteString(w, `"`); err != nil {
		return err //nolint:wrapcheck // writeArg writes directly to io.Writer
	}

	if err := writeNodes(wire.QuoteWriter{W: w}, a.nested); err != nil {
		return err
	}

	if _, err := io.WriteString(w, `"`); err != nil {
		return err //nolint:wrapcheck // writeArg writes directly to io.Writer
	}

	return nil
}

func writeNode(w io.Writer, n wireNode) error {
	if !wire.ValidCommand(n.name) {
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
	c := wire.Counter{Limit: maxBytes, N: 0, Err: nil}
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

			out = append(out, wire.Argv(s))
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
// [Connection.AuxiliaryServer]; Run otherwise returns [ErrTransportUnsupported] before dispatch.
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

		if commandStartsServer(c.name) {
			p.allowStart = true
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

func parseFlagValue(args []string, i int, flag string) (string, int, bool) {
	arg := args[i]
	if arg == flag && i+1 < len(args) {
		return args[i+1], i + 1, true
	}

	if strings.HasPrefix(arg, flag) && len(arg) > len(flag) {
		return arg[len(flag):], i, true
	}

	return "", i, false
}

func tryParseConfigFlag(args []string, i int, cfg *Config) (int, bool) {
	if val, next, ok := parseFlagValue(args, i, "-S"); ok {
		cfg.SocketPath = val
		return next, true
	}

	if val, next, ok := parseFlagValue(args, i, "-L"); ok {
		cfg.SocketName = val
		return next, true
	}

	if val, next, ok := parseFlagValue(args, i, "-f"); ok {
		cfg.ConfigFile = val
		return next, true
	}

	return i, false
}

func isIgnoredGlobalFlag(arg string) bool {
	switch arg {
	case "-u", "-v", "-N", "-C":
		return true
	default:
		return strings.HasPrefix(arg, "-")
	}
}

func parseGlobalFlags(args []string) (Config, int) {
	var (
		cfg Config
		i   int
	)

	for i = 0; i < len(args); i++ {
		if next, ok := tryParseConfigFlag(args, i, &cfg); ok {
			i = next
			continue
		}

		if isIgnoredGlobalFlag(args[i]) {
			continue
		}

		break
	}

	return cfg, i
}
