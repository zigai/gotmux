package tmux

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/zigai/gotmux/internal/wire"
)

const (
	minDebugLogVerbosity  = 2
	minNoEchoControlCount = 2
	approxArgsPerNode     = 3
)

const (
	replyRaw replyMode = iota
	replyRecords
	replyEmpty
)

const (
	// ActionDefault represents tmux invoked without a command or root action flag (runs default client action).
	ActionDefault RootActionKind = iota

	// ActionCommand represents tmux invoked with one or more subcommands.
	ActionCommand

	// ActionShell represents tmux invoked with -c shell-command.
	ActionShell

	// ActionForeground represents tmux daemon run in the foreground (-D flag).
	ActionForeground

	// ActionHelp represents the usage query flag (-h flag).
	ActionHelp

	// ActionVersion represents the version query flag (-V flag).
	ActionVersion

	// ActionControl represents control mode (-C or -CC flag).
	ActionControl
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

	// RunOptions configures raw command execution against a tmux server endpoint.
	RunOptions struct {
		// Start controls whether tmux is permitted to spawn a new server daemon if none is listening.
		// When [AllowStart], the -N flag is omitted, allowing tmux to auto-spawn a daemon.
		// When [ExistingOnly], the -N flag is emitted to prevent daemon auto-spawn.
		// Bound auxiliary servers ([Connection.AuxiliaryServer]) always forbid daemon auto-spawn regardless of this setting.
		Start StartPolicy

		// Input supplies bounded standard input bytes for the tmux command or sequence execution.
		//
		// Ownership: The caller retains ownership of the slice. The library does not modify or retain
		// the slice after execution completes. Callers should snapshot or clone the slice before concurrent
		// or asynchronous reuse.
		//
		// Absence vs. zero value:
		// A nil slice means no standard input is connected (stdin is nil/detached).
		// An empty non-nil slice ([]byte{}) connects standard input and immediately sends EOF.
		//
		// Accounting: Input bytes count against the operation's [Limits.InputBytes] limit alongside
		// command arguments. If the combined size of command arguments and input exceeds the limit,
		// execution fails with [ErrInputLimit].
		//
		// Sequences: Native stdin is a single shared stream delivered to the tmux process; it is not
		// duplicated per command in a sequence. Typically, only the first command in the sequence that
		// reads stdin will consume these bytes.
		Input []byte
	}

	// RootActionKind specifies the kind of root action requested in a parsed command line.
	RootActionKind uint8

	// ParsedCommandLine captures faithfully parsed root configuration settings,
	// action kind, and command sequence from native tmux command-line arguments.
	ParsedCommandLine struct {
		// Config contains the parsed root configuration (socket, config file, UTF-8, colors, features, logging, login shell).
		Config Config

		// Action indicates the kind of root action requested.
		Action RootActionKind

		// Commands contains the parsed command sequence when Action is ActionCommand or ActionControl.
		Commands CommandSequence

		// ShellCommand contains the shell script when Action is ActionShell (-c flag).
		ShellCommand string

		// ControlNoEcho indicates whether -CC (control mode with echo disabled) was specified.
		ControlNoEcho bool

		// StartPolicy reflects whether -N was specified on the command line ([ExistingOnly]) or omitted ([AllowStart]).
		StartPolicy StartPolicy
	}

	rawParseState struct {
		cfg          Config
		action       RootActionKind
		shellCmd     string
		controlCount int
		verboseCount int
		hasN         bool
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

// Command returns the single parsed command if Action is ActionCommand and exactly
// one command was parsed. Returns false if Action is not ActionCommand or if multiple
// or zero commands were parsed.
func (p ParsedCommandLine) Command() (Command, bool) {
	if p.Action == ActionCommand && len(p.Commands.commands) == 1 {
		c := p.Commands.commands[0]

		return Command{name: c.name, args: slices.Clone(c.args)}, true
	}

	return Command{name: "", args: nil}, false
}

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

// Commands returns a copy of the validated commands in this sequence.
func (s CommandSequence) Commands() []Command {
	out := make([]Command, len(s.commands))
	for i, c := range s.commands {
		out[i] = Command{name: c.name, args: slices.Clone(c.args)}
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

// ParseCommandLine extracts native root flags, action kind, and command sequence from a tmux argv slice.
// Returns a [ParsedCommandLine] retaining all parsed root configuration settings and commands.
func ParseCommandLine(args []string) (ParsedCommandLine, error) {
	s := rawParseState{
		cfg: Config{
			Binary:           "",
			SocketPath:       "",
			SocketName:       "",
			ConfigFile:       "",
			Env:              nil,
			Dir:              "",
			Limits:           DefaultLimits(),
			UTF8:             UTF8Default,
			Colors256:        false,
			TerminalFeatures: nil,
			LogLevel:         LogNone,
			LoginShell:       false,
		},
		action:       ActionDefault,
		shellCmd:     "",
		controlCount: 0,
		verboseCount: 0,
		hasN:         false,
	}

	var cmdArgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			cmdArgs = args[i+1:]
			break
		}

		if arg == "-" || !strings.HasPrefix(arg, "-") {
			cmdArgs = args[i:]
			break
		}

		nextI, stop, err := parseFlagBundle(arg, args, i, &s)
		if err != nil {
			return ParsedCommandLine{}, err
		}

		i = nextI
		if stop {
			cmdArgs = args[i:]
			break
		}
	}

	return finishCommandLineParse(&s, cmdArgs)
}

func applySimpleFlag(ch rune, s *rawParseState) bool {
	switch ch {
	case '2':
		s.cfg.Colors256 = true
		return true
	case 'u':
		s.cfg.UTF8 = UTF8Force
		return true
	case 'l':
		s.cfg.LoginShell = true
		return true
	case 'v':
		s.verboseCount++
		return true
	case 'N':
		s.hasN = true
		return true
	case 'D':
		s.action = ActionForeground
		return true
	case 'h':
		s.action = ActionHelp
		return true
	case 'V':
		s.action = ActionVersion
		return true
	case 'C':
		s.controlCount++
		return true
	default:
		return false
	}
}

func isValuedFlag(ch rune) bool {
	return ch == 'c' || ch == 'f' || ch == 'L' || ch == 'S' || ch == 'T'
}

func parseFlagBundle(arg string, args []string, i int, s *rawParseState) (int, bool, error) {
	runes := []rune(arg[1:])
	if len(runes) == 0 {
		return i, true, nil
	}

	for j := range runes {
		ch := runes[j]
		if applySimpleFlag(ch, s) {
			continue
		}

		if isValuedFlag(ch) {
			nextI, err := applyValuedFlag(ch, runes[j+1:], args, i, s)
			if err != nil {
				return i, false, err
			}

			return nextI, false, nil
		}

		return i, false, invalid(fmt.Sprintf("unknown option: -%c", ch))
	}

	return i, false, nil
}

func extractFlagVal(ch rune, remaining []rune, args []string, i int) (string, int, error) {
	var (
		val   string
		nextI = i
	)

	switch {
	case len(remaining) > 0:
		val = string(remaining)
	case i+1 < len(args):
		nextI++
		val = args[nextI]
	default:
		return "", i, invalid(fmt.Sprintf("flag requires an argument: -%c", ch))
	}

	if val == "" {
		return "", i, invalid(fmt.Sprintf("flag requires an argument: -%c", ch))
	}

	return val, nextI, nil
}

func applyValuedFlag(ch rune, remaining []rune, args []string, i int, s *rawParseState) (int, error) {
	val, nextI, err := extractFlagVal(ch, remaining, args, i)
	if err != nil {
		return i, err
	}

	switch ch {
	case 'c':
		s.action = ActionShell
		s.shellCmd = val
	case 'f':
		s.cfg.ConfigFile = val
	case 'L':
		if s.cfg.SocketPath != "" {
			return i, invalid("choose SocketPath or SocketName")
		}

		s.cfg.SocketName = val
	case 'S':
		if s.cfg.SocketName != "" {
			return i, invalid("choose SocketPath or SocketName")
		}

		s.cfg.SocketPath = val
	case 'T':
		return nextI, appendTerminalFeatures(val, &s.cfg)
	}

	return nextI, nil
}

func appendTerminalFeatures(val string, cfg *Config) error {
	for p := range strings.SplitSeq(val, ",") {
		if p == "" || !wire.ValidString(p) || strings.ContainsAny(p, " \t\r\n,\x00") {
			return invalid("terminal feature")
		}

		cfg.TerminalFeatures = append(cfg.TerminalFeatures, p)
	}

	return nil
}

func applyVerbosityAndPolicy(s *rawParseState) {
	switch {
	case s.verboseCount >= minDebugLogVerbosity:
		s.cfg.LogLevel = LogDebug
	case s.verboseCount == 1:
		s.cfg.LogLevel = LogVerbose
	default:
		s.cfg.LogLevel = LogNone
	}

	if s.controlCount > 0 && s.action != ActionHelp && s.action != ActionVersion {
		s.action = ActionControl
	}
}

func validateCommandArgs(s *rawParseState, cmdArgs []string) error {
	if s.action == ActionForeground && len(cmdArgs) > 0 {
		return invalid("-D does not accept command arguments")
	}

	if s.action == ActionShell && len(cmdArgs) > 0 {
		return invalid("-c does not accept command arguments")
	}

	if s.controlCount > 0 && s.action == ActionForeground {
		return invalid("-D and -C are mutually exclusive")
	}

	if s.controlCount > 0 && s.action == ActionShell {
		return invalid("-c and -C are mutually exclusive")
	}

	return nil
}

func finishCommandLineParse(s *rawParseState, cmdArgs []string) (ParsedCommandLine, error) {
	if err := validateCommandArgs(s, cmdArgs); err != nil {
		return ParsedCommandLine{}, err
	}

	applyVerbosityAndPolicy(s)

	if len(cmdArgs) > 0 && len(splitArgvCommands(cmdArgs)) == 0 {
		return ParsedCommandLine{}, invalid("no command given")
	}

	seq, err := parseCommandSequenceArgv(cmdArgs, s.action)
	if err != nil {
		return ParsedCommandLine{}, err
	}

	if len(cmdArgs) > 0 && s.action == ActionDefault {
		s.action = ActionCommand
	}

	startPolicy := AllowStart
	if s.hasN {
		startPolicy = ExistingOnly
	}

	return ParsedCommandLine{
		Config:        s.cfg,
		Action:        s.action,
		Commands:      seq,
		ShellCommand:  s.shellCmd,
		ControlNoEcho: s.controlCount >= minNoEchoControlCount,
		StartPolicy:   startPolicy,
	}, nil
}

func parseCommandSequenceArgv(cmdArgs []string, action RootActionKind) (CommandSequence, error) {
	if len(cmdArgs) == 0 || action == ActionHelp || action == ActionVersion {
		return CommandSequence{commands: nil}, nil
	}

	rawCmds := splitArgvCommands(cmdArgs)
	if len(rawCmds) == 0 {
		return CommandSequence{commands: nil}, nil
	}

	cmdList := make([]Command, 0, len(rawCmds))
	for _, grp := range rawCmds {
		if len(grp) == 0 {
			continue
		}

		cmd, err := NewCommand(grp[0], grp[1:]...)
		if err != nil {
			return CommandSequence{}, err
		}

		cmdList = append(cmdList, cmd)
	}

	return Sequence(cmdList...)
}

func splitArgvCommands(args []string) [][]string {
	if len(args) == 0 {
		return nil
	}

	var cmds [][]string
	var current []string

	for _, a := range args {
		end := false
		arg := a
		if len(arg) > 0 && arg[len(arg)-1] == ';' {
			arg = arg[:len(arg)-1]
			if len(arg) > 0 && arg[len(arg)-1] == '\\' {
				arg = arg[:len(arg)-1] + ";"
			} else {
				end = true
			}
		}

		if !end || len(arg) > 0 {
			current = append(current, arg)
		}

		if end {
			if len(current) > 0 {
				cmds = append(cmds, current)
				current = nil
			}
		}
	}

	if len(current) > 0 {
		cmds = append(cmds, current)
	}

	return cmds
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
	out := make([]string, 0, len(p.nodes)*approxArgsPerNode)
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
	if s == nil || s.runner == nil {
		return failedResult(), &CommandError{Command: c.name, Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: ErrInvalidHandle}
	}

	if s.conn != nil {
		return failedResult(), &CommandError{Command: c.name, Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: unsupportedControl("raw execution over control transport", ErrTransportUnsupported)}
	}
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
	if s == nil || s.runner == nil {
		return failedResult(), &CommandError{Command: "sequence", Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: ErrInvalidHandle}
	}

	if s.conn != nil {
		return failedResult(), &CommandError{Command: "sequence", Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: unsupportedControl("raw execution over control transport", ErrTransportUnsupported)}
	}
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), &CommandError{Command: "sequence", Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: err}
	}
	defer op.close()

	commands := sequence.commands
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

func (s *Server) rawAllowStart(start StartPolicy) (bool, error) {
	if start > ExistingOnly {
		return false, invalid("start policy")
	}

	if s.bound != nil {
		return false, nil
	}

	return start == AllowStart, nil
}

// RunWith executes one raw tmux command against the server with custom execution options.
//
// Start policy is controlled explicitly by o.Start rather than inferred from command names.
// Fails with [ErrTransportUnsupported] over control mode.
func (s *Server) RunWith(ctx context.Context, c Command, o RunOptions) (Result, error) {
	if s == nil || s.runner == nil {
		return failedResult(), &CommandError{Command: c.name, Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: ErrInvalidHandle}
	}

	if s.conn != nil {
		return failedResult(), &CommandError{Command: c.name, Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: unsupportedControl("raw execution over control transport", ErrTransportUnsupported)}
	}
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), &CommandError{Command: c.name, Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: err}
	}
	defer op.close()

	if !c.Valid() {
		return failedResult(), &CommandError{Command: c.name, Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: invalid("command")}
	}

	allowStart, err := s.rawAllowStart(o.Start)
	if err != nil {
		return failedResult(), &CommandError{Command: c.name, Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: err}
	}

	p := plan{nodes: []wireNode{leaf(c)}, mode: replyRaw, allowStart: allowStart}

	return s.execute(opCtx, op, p, nil, o.Input)
}

// RunSequenceWith executes an ordered list of commands in a single round-trip with custom execution options.
//
// Start policy is controlled explicitly by o.Start rather than inferred from command names.
// Fails with [ErrTransportUnsupported] over control mode.
func (s *Server) RunSequenceWith(ctx context.Context, sequence CommandSequence, o RunOptions) (Result, error) {
	if s == nil || s.runner == nil {
		return failedResult(), &CommandError{Command: "sequence", Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: ErrInvalidHandle}
	}

	if s.conn != nil {
		return failedResult(), &CommandError{Command: "sequence", Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: unsupportedControl("raw execution over control transport", ErrTransportUnsupported)}
	}
	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return failedResult(), &CommandError{Command: "sequence", Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: err}
	}
	defer op.close()

	commands := sequence.commands
	if len(commands) == 0 {
		return Result{Stdout: []byte{}, Stderr: []byte{}, ExitCode: -1}, nil
	}

	allowStart, err := s.rawAllowStart(o.Start)
	if err != nil {
		return failedResult(), &CommandError{Command: "sequence", Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: err}
	}

	p := plan{nodes: nil, mode: replyRaw, allowStart: allowStart}

	for _, c := range commands {
		if !c.Valid() {
			return failedResult(), &CommandError{Command: "sequence", Result: failedResult(), Outcome: notSentOutcome(), Timeout: NoTimeout, Err: invalid("command")}
		}

		p.nodes = append(p.nodes, leaf(c))
	}

	return s.execute(opCtx, op, p, nil, o.Input)
}

// cloneResult never aliases retained connection state.
func cloneResult(r Result) Result {
	r.Stdout = bytes.Clone(r.Stdout)
	r.Stderr = bytes.Clone(r.Stderr)

	return r
}
