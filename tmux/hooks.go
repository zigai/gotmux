package tmux

import (
	"bytes"
	"context"
	"strconv"
	"strings"

	"github.com/zigai/gotmux/internal/wire"
)

const (
	// PrefixTable is the standard key table active after pressing the prefix key (default C-b).
	PrefixTable KeyTable = "prefix"

	// CopyModeTable is the key table active when emacs-style copy mode is entered.
	CopyModeTable KeyTable = "copy-mode"

	// CopyModeViTable is the key table active when vi-style copy mode is entered.
	CopyModeViTable KeyTable = "copy-mode-vi"
)

type (
	// KeyTable identifies a named tmux key binding table (such as "prefix" or "root").
	KeyTable string

	// BindOptions configures key binding registration.
	BindOptions struct {
		// Repeat allows the bound key to be pressed multiple times without re-entering prefix (-r flag).
		Repeat bool

		// Note is an optional descriptive note for this key binding displayed in list-keys (-N flag).
		Note string
	}

	// BindingInfo captures a point-in-time snapshot of a registered key binding.
	BindingInfo struct {
		// Table is the key table where the binding is registered.
		Table KeyTable

		// Key is the key combination triggering this binding.
		Key Key

		// Repeat indicates whether this key can repeat without re-entering the prefix.
		Repeat bool

		// Payload is the command sequence executed when this key is pressed.
		Payload CommandPayload

		raw string

		// Parsed indicates whether the payload was successfully parsed into typed [Command] structs.
		Parsed bool
	}

	// CommandPayload contains the raw serialized command string of a hook or key binding,
	// along with any typed commands parsed from it.
	CommandPayload struct {
		raw      string
		sequence CommandSequence
		parsed   bool
	}

	// HookScope provides accessors for registering, removing, and listing hooks
	// within a specific scope (global, session, window, or pane).
	HookScope struct{ target optionTarget }

	// HookInfo captures a point-in-time snapshot of a registered tmux hook.
	HookInfo struct {
		// Name is the event name triggering the hook (e.g. "after-new-session", "pane-focus-in").
		Name string

		// Index is the slot index for this hook within the hook array (e.g. hook[0]).
		Index int

		// Scope indicates the target scope where this hook is installed.
		Scope Scope

		// Payload contains the command sequence executed when the hook fires.
		Payload CommandPayload
	}
)

// Valid reports whether this key table name is a non-empty, valid tmux format identifier.
func (t KeyTable) Valid() bool { return validFormatName(string(t)) }

// Raw returns the exact serialized payload even when its syntax is
// richer than the non-evaluating parser supports. No command is run by parsing.
func (p CommandPayload) Raw() string                 { return p.raw }
func (p CommandPayload) Commands() ([]Command, bool) { return p.sequence.Commands(), p.parsed }

func parsePayload(raw string) CommandPayload {
	out := CommandPayload{raw: raw, sequence: CommandSequence{commands: nil}, parsed: false}

	parts, err := wire.SplitSequence(raw)
	if err != nil {
		return out
	}

	commands := []Command{}

	for _, part := range parts {
		words, err := wire.ParseWords(part)
		if err != nil || len(words) == 0 {
			return out
		}

		c, err := NewCommand(words[0], words[1:]...)
		if err != nil {
			return out
		}

		commands = append(commands, c)
	}

	out.sequence = CommandSequence{commands: commands}
	out.parsed = true

	return out
}

// GlobalHooks returns a [HookScope] targeting global server-wide hooks.
func (s *Server) GlobalHooks() HookScope {
	return HookScope{target: optionTarget{server: s, scope: GlobalSessionScope, h: zeroHandle}}
}

// Hooks returns a [HookScope] targeting hooks installed on this specific session.
func (s Session) Hooks() HookScope { return HookScope{target: s.Options().target} }

// Hooks returns a [HookScope] targeting hooks installed on this specific window.
func (w Window) Hooks() HookScope { return HookScope{target: w.Options().target} }

// Hooks returns a [HookScope] targeting hooks installed on this specific pane.
func (p Pane) Hooks() HookScope { return HookScope{target: p.Options().target} }

// Set registers a command sequence to execute when the named hook event triggers at index.
func (h HookScope) Set(ctx context.Context, name string, index int, commands CommandSequence) error {
	if !validFormatName(name) || index < 0 || index > 1<<30 || len(commands.commands) == 0 {
		return opError("SetHook", invalid("hook name/index/commands"))
	}

	opCtx, op, g, err := h.target.prepare(ctx)
	if err != nil {
		return opError("SetHook", err)
	}
	defer op.close()

	node := leaf(command("set-hook", append(h.target.args(), "--", name+"["+strconv.Itoa(index)+"]")...))
	node.args = append(node.args, wireArg{text: "", nested: commands.nodes()})
	_, err = h.target.server.execute(opCtx, op, plan{nodes: []wireNode{node}, mode: replyEmpty, allowStart: false}, g, nil)

	return opError("SetHook", err)
}

// Unset removes the hook installed at the specified event name and index slot.
func (h HookScope) Unset(ctx context.Context, name string, index int) error {
	if !validFormatName(name) || index < 0 || index > 1<<30 {
		return opError("UnsetHook", invalid("hook name/index"))
	}

	opCtx, op, g, err := h.target.prepare(ctx)
	if err != nil {
		return opError("UnsetHook", err)
	}
	defer op.close()

	args := append(h.target.args(), "-u", "--", name+"["+strconv.Itoa(index)+"]")
	_, err = h.target.server.execute(opCtx, op, emptyPlan(command("set-hook", args...)), g, nil)

	return opError("UnsetHook", err)
}

// List queries all hooks currently registered in this scope.
func (h HookScope) List(ctx context.Context) ([]HookInfo, error) {
	opCtx, op, g, err := h.target.prepare(ctx)
	if err != nil {
		return nil, opError("Hooks", err)
	}
	defer op.close()

	r, err := h.target.server.execute(opCtx, op, plainPlan(command("show-hooks", h.target.args()...)), g, nil)
	if err != nil {
		return nil, opError("Hooks", err)
	}

	out := []HookInfo{}

	for line := range bytes.SplitSeq(bytes.TrimSuffix(r.Stdout, []byte{'\n'}), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}

		key, value, ok := strings.Cut(string(line), " ")
		if !ok {
			continue
		}

		name := key
		index := 0

		if i := strings.LastIndexByte(key, '['); i >= 0 {
			name = key[:i]

			idx, err := arrayIndex(key, name)
			if err != nil {
				return nil, afterError("Hooks", err)
			}

			index = idx
		}

		out = append(out, HookInfo{Name: name, Index: index, Scope: h.target.scope, Payload: parsePayload(value)})
	}

	return out, nil
}

func (b BindingInfo) Raw() string { return b.raw }

// Bind registers a key binding in the specified key table, executing commands when pressed.
func (s *Server) Bind(ctx context.Context, table KeyTable, key Key, commands CommandSequence, o BindOptions) error {
	if !table.Valid() || !key.Valid() || len(commands.commands) == 0 || !wire.ValidString(o.Note) {
		return opError("Bind", invalid("binding"))
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return opError("Bind", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return opError("Bind", err)
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
	node.args = append(node.args, wireArg{text: "", nested: commands.nodes()})
	_, err = s.execute(opCtx, op, plan{nodes: []wireNode{node}, mode: replyEmpty, allowStart: false}, newGuard(info.Identity), nil)

	return opError("Bind", err)
}

// Unbind removes a key binding from the specified key table.
func (s *Server) Unbind(ctx context.Context, table KeyTable, key Key) error {
	if !table.Valid() || !key.Valid() {
		return opError("Unbind", invalid("binding"))
	}

	return s.endpointAction(ctx, "unbind-key", "-T", string(table), "--", string(key))
}

// Bindings queries and returns all key bindings currently registered in the specified key table.
//
// If a binding's command text uses complex shell or tmux syntax outside what our non-evaluating
// parser supports, [BindingInfo.Parsed] will be false and [BindingInfo.Raw] provides the verbatim text.
func (s *Server) Bindings(ctx context.Context, table KeyTable) ([]BindingInfo, error) {
	if !table.Valid() {
		return nil, opError("Bindings", invalid("key table"))
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return nil, opError("Bindings", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return nil, opError("Bindings", err)
	}

	r, err := s.execute(opCtx, op, plainPlan(command("list-keys", "-T", string(table))), newGuard(info.Identity), nil)
	if err != nil {
		return nil, opError("Bindings", err)
	}

	out := parseBindingsLines(r.Stdout, table, "")
	if len(out) == 0 {
		out = s.fallbackBindings(opCtx, op, info, table)
	}

	return out, nil
}

func parseBindingsLines(stdout []byte, defaultTable, filterTable KeyTable) []BindingInfo {
	var out []BindingInfo

	for line := range bytes.SplitSeq(bytes.TrimSuffix(stdout, []byte{'\n'}), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}

		b := parseBinding(string(line), defaultTable)
		if filterTable == "" || b.Table == filterTable {
			out = append(out, b)
		}
	}

	return out
}

func (s *Server) fallbackBindings(ctx context.Context, op *operation, info ServerInfo, table KeyTable) []BindingInfo {
	rAll, err := s.execute(ctx, op, plainPlan(command("list-keys")), newGuard(info.Identity), nil)
	if err != nil {
		return nil
	}

	return parseBindingsLines(rAll.Stdout, "", table)
}

func parseBinding(raw string, table KeyTable) BindingInfo {
	b := BindingInfo{
		Table:   table,
		Key:     "",
		Repeat:  false,
		Parsed:  false,
		raw:     raw,
		Payload: CommandPayload{raw: raw, sequence: CommandSequence{commands: nil}, parsed: false},
	}

	parts, err := wire.SplitSequence(raw)
	if err != nil || len(parts) == 0 {
		return b
	}

	words, err := wire.ParseWords(parts[0])
	if err != nil || len(words) < 5 || words[0] != "bind-key" {
		return b
	}

	i, ok := parseBindingFlags(words, &b)
	if !ok || i+1 >= len(words) {
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

	commands, ok := parseBindingParts(parts[1:], first)
	if !ok {
		return b
	}

	b.Parsed = true
	b.Payload = CommandPayload{raw: raw, sequence: CommandSequence{commands: commands}, parsed: true}

	return b
}

func parseBindingFlags(words []string, b *BindingInfo) (int, bool) {
	i := 1
	for i < len(words) {
		switch words[i] {
		case "-r":
			b.Repeat = true
			i++
		case "-T":
			if i+1 >= len(words) {
				return 0, false
			}

			b.Table = KeyTable(words[i+1])
			i += 2
		case "-N":
			if i+1 >= len(words) {
				return 0, false
			}

			i += 2 // note remains in Raw
		case "--":
			return i + 1, true
		default:
			return i, true
		}
	}

	return i, true
}

func parseBindingParts(parts []string, first Command) ([]Command, bool) {
	commands := []Command{first}

	for _, part := range parts {
		w, err := wire.ParseWords(part)
		if err != nil || len(w) == 0 {
			return nil, false
		}

		c, err := NewCommand(w[0], w[1:]...)
		if err != nil {
			return nil, false
		}

		commands = append(commands, c)
	}

	return commands, true
}
