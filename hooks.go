package tmux

import (
	"bytes"
	"context"
	"strconv"
	"strings"

	"example.com/tmux/internal/codec"
)

// CommandSequence is immutable and represents tmux commands, not shell text.
type CommandSequence struct{ commands []Command }

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

// CommandPayload retains the exact serialized payload even when its syntax is
// richer than the non-evaluating parser supports. No command is run by parsing.
type CommandPayload struct {
	raw      string
	sequence CommandSequence
	parsed   bool
}

func (p CommandPayload) Raw() string                 { return p.raw }
func (p CommandPayload) Commands() ([]Command, bool) { return p.sequence.Commands(), p.parsed }
func parsePayload(raw string) CommandPayload {
	out := CommandPayload{raw: raw}
	parts, e := codec.SplitSequence(raw)
	if e != nil {
		return out
	}
	commands := []Command{}
	for _, part := range parts {
		words, e := codec.ParseWords(part)
		if e != nil || len(words) == 0 {
			return out
		}
		c, e := NewCommand(words[0], words[1:]...)
		if e != nil {
			return out
		}
		commands = append(commands, c)
	}
	out.sequence = CommandSequence{commands: commands}
	out.parsed = true
	return out
}

type HookScope struct{ target optionTarget }
type HookInfo struct {
	Name    string
	Index   int
	Scope   Scope
	Payload CommandPayload
}

func (s *Server) GlobalHooks() HookScope {
	return HookScope{target: optionTarget{server: s, scope: GlobalSessionScope}}
}
func (s Session) Hooks() HookScope { return HookScope{target: s.Options().target} }
func (w Window) Hooks() HookScope  { return HookScope{target: w.Options().target} }
func (p Pane) Hooks() HookScope    { return HookScope{target: p.Options().target} }
func (h HookScope) Set(ctx context.Context, name string, index int, commands CommandSequence) error {
	if !validFormatName(name) || index < 0 || index > 1<<30 || len(commands.commands) == 0 {
		return opError("SetHook", invalid("hook name/index/commands"))
	}
	op, g, e := h.target.prepare(ctx)
	if e != nil {
		return opError("SetHook", e)
	}
	defer op.close()
	node := leaf(command("set-hook", append(h.target.args(), "--", name+"["+strconv.Itoa(index)+"]")...))
	node.args = append(node.args, wireArg{nested: commands.nodes()})
	_, e = h.target.server.execute(op, plan{nodes: []wireNode{node}, mode: replyEmpty}, g, nil)
	return opError("SetHook", e)
}
func (h HookScope) Unset(ctx context.Context, name string, index int) error {
	if !validFormatName(name) || index < 0 || index > 1<<30 {
		return opError("UnsetHook", invalid("hook name/index"))
	}
	op, g, e := h.target.prepare(ctx)
	if e != nil {
		return opError("UnsetHook", e)
	}
	defer op.close()
	args := append(h.target.args(), "-u", "--", name+"["+strconv.Itoa(index)+"]")
	_, e = h.target.server.execute(op, emptyPlan(command("set-hook", args...)), g, nil)
	return opError("UnsetHook", e)
}
func (h HookScope) List(ctx context.Context) ([]HookInfo, error) {
	op, g, e := h.target.prepare(ctx)
	if e != nil {
		return nil, opError("Hooks", e)
	}
	defer op.close()
	r, e := h.target.server.execute(op, plainPlan(command("show-hooks", h.target.args()...)), g, nil)
	if e != nil {
		return nil, opError("Hooks", e)
	}
	out := []HookInfo{}
	for _, line := range bytes.Split(bytes.TrimSuffix(r.Stdout, []byte{'\n'}), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		key, value, ok := strings.Cut(string(line), " ")
		if !ok {
			continue
		}
		i := strings.LastIndexByte(key, '[')
		if i < 1 {
			return nil, afterError("Hooks", ErrProtocol)
		}
		name := key[:i]
		index, e := arrayIndex(key, name)
		if e != nil {
			return nil, afterError("Hooks", e)
		}
		out = append(out, HookInfo{Name: name, Index: index, Scope: h.target.scope, Payload: parsePayload(value)})
	}
	return out, nil
}

type KeyTable string

const (
	RootTable       KeyTable = "root"
	PrefixTable     KeyTable = "prefix"
	CopyModeTable   KeyTable = "copy-mode"
	CopyModeViTable KeyTable = "copy-mode-vi"
)

func (t KeyTable) Valid() bool { return validFormatName(string(t)) }

type BindOptions struct {
	Repeat bool
	Note   string
}
type BindingInfo struct {
	Table   KeyTable
	Key     Key
	Repeat  bool
	Payload CommandPayload
	raw     string
	Parsed  bool
}

func (b BindingInfo) Raw() string { return b.raw }
func (s *Server) Bind(ctx context.Context, table KeyTable, key Key, commands CommandSequence, o BindOptions) error {
	if !table.Valid() || !key.Valid() || len(commands.commands) == 0 || !codec.ValidString(o.Note) {
		return opError("Bind", invalid("binding"))
	}
	op, e := s.begin(ctx)
	if e != nil {
		return opError("Bind", e)
	}
	defer op.close()
	info, e := s.probe(op)
	if e != nil {
		return opError("Bind", e)
	}
	args := []string{"-T", string(table)}
	if o.Repeat {
		args = append(args, "-r")
	}
	if o.Note != "" {
		args = append(args, "-N", o.Note)
	}
	args = append(args, "--", string(key))
	node := leaf(command("bind-key", args...))
	node.args = append(node.args, wireArg{nested: commands.nodes()})
	_, e = s.execute(op, plan{nodes: []wireNode{node}, mode: replyEmpty}, &guard{identity: info.Identity}, nil)
	return opError("Bind", e)
}
func (s *Server) Unbind(ctx context.Context, table KeyTable, key Key) error {
	if !table.Valid() || !key.Valid() {
		return opError("Unbind", invalid("binding"))
	}
	return s.endpointAction(ctx, "unbind-key", "-T", string(table), "--", string(key))
}

// Bindings returns caller-owned serialized bindings. Parsed is false when the
// retained command payload uses syntax outside the non-evaluating parser.
func (s *Server) Bindings(ctx context.Context, table KeyTable) ([]BindingInfo, error) {
	if !table.Valid() {
		return nil, opError("Bindings", invalid("key table"))
	}
	op, err := s.begin(ctx)
	if err != nil {
		return nil, opError("Bindings", err)
	}
	defer op.close()
	info, err := s.probe(op)
	if err != nil {
		return nil, opError("Bindings", err)
	}
	r, err := s.execute(op, plainPlan(command("list-keys", "-T", string(table))), &guard{identity: info.Identity}, nil)
	if err != nil {
		return nil, opError("Bindings", err)
	}
	out := []BindingInfo{}
	for _, line := range bytes.Split(bytes.TrimSuffix(r.Stdout, []byte{'\n'}), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		out = append(out, parseBinding(string(line), table))
	}
	return out, nil
}

func parseBinding(raw string, table KeyTable) BindingInfo {
	b := BindingInfo{Table: table, raw: raw, Payload: CommandPayload{raw: raw}}
	parts, err := codec.SplitSequence(raw)
	if err != nil || len(parts) == 0 {
		return b
	}
	words, err := codec.ParseWords(parts[0])
	if err != nil || len(words) < 5 || words[0] != "bind-key" {
		return b
	}
	i := 1
	for i < len(words) {
		switch words[i] {
		case "-r":
			b.Repeat = true
			i++
		case "-T":
			if i+1 >= len(words) {
				return b
			}
			b.Table = KeyTable(words[i+1])
			i += 2
		case "-N":
			if i+1 >= len(words) {
				return b
			}
			i += 2 // note remains in Raw
		case "--":
			i++
			goto key
		default:
			goto key
		}
	}
key:
	if i+1 >= len(words) {
		return b
	}
	b.Key = Key(words[i])
	i++
	if !b.Table.Valid() || !b.Key.Valid() {
		return b
	}
	first, err := NewCommand(words[i], words[i+1:]...)
	if err != nil {
		return b
	}
	commands := []Command{first}
	for _, part := range parts[1:] {
		w, err := codec.ParseWords(part)
		if err != nil || len(w) == 0 {
			return b
		}
		c, err := NewCommand(w[0], w[1:]...)
		if err != nil {
			return b
		}
		commands = append(commands, c)
	}
	b.Parsed = true
	b.Payload = CommandPayload{raw: raw, sequence: CommandSequence{commands: commands}, parsed: true}
	return b
}
