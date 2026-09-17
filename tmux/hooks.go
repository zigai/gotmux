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

	// BindOptions configures key binding registration and modification.
	BindOptions struct {
		// Repeat allows the bound key to be pressed multiple times without re-entering prefix (-r flag).
		Repeat bool

		// Note is an optional descriptive note for this key binding displayed in list-keys (-N flag).
		Note string

		// ClearNote clears any existing note attached to this key binding (-N "" flag).
		ClearNote bool
	}

	// UnbindOptions configures key binding removal behavior.
	UnbindOptions struct {
		// All removes all key bindings in the specified table (-a flag).
		// When All is true, key may be empty ("").
		All bool

		// Quiet prevents errors from being returned if the key is not bound (-q flag).
		Quiet bool
	}

	// BindingsOptions configures key binding listing and filtering queries.
	BindingsOptions struct {
		// Table optionally restricts listing to a specific key table (-T flag).
		// If Table is empty, all key tables are listed.
		Table KeyTable

		// Key optionally restricts listing to a specific key combination.
		Key Key

		// FirstMatch limits output to the first matching key binding (-1 flag).
		FirstMatch bool

		// NotesOnly queries bindings with attached notes (-N flag).
		NotesOnly bool

		// Prefix specifies a prefix string printed before each key in notes view (-P flag).
		Prefix string
	}

	// BindingNote captures a key binding and its note from list-keys -N.
	BindingNote struct {
		// Raw is the exact output line from list-keys -N.
		Raw string

		// Key is the key combination string as reported in notes view (e.g. "C-b Tab", "MYPREFIX Tab").
		Key string

		// Note is the descriptive note text attached to the key.
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

// SetWhole replaces all commands on the named hook event in this scope.
func (h HookScope) SetWhole(ctx context.Context, name string, commands CommandSequence) error {
	if !validFormatName(name) || len(commands.commands) == 0 {
		return opError("SetHook", invalid("hook name/commands"))
	}

	opCtx, op, g, err := h.target.prepare(ctx)
	if err != nil {
		return opError("SetHook", err)
	}
	defer op.close()

	node := leaf(command("set-hook", append(h.target.args(), "--", name)...))
	node.args = append(node.args, wireArg{text: "", nested: commands.nodes()})
	_, err = h.target.server.execute(opCtx, op, plan{nodes: []wireNode{node}, mode: replyEmpty, allowStart: false}, g, nil)

	return opError("SetHook", err)
}

// Append appends a command sequence to the named hook event in this scope (-a flag).
func (h HookScope) Append(ctx context.Context, name string, commands CommandSequence) error {
	if !validFormatName(name) || len(commands.commands) == 0 {
		return opError("SetHook", invalid("hook name/commands"))
	}

	opCtx, op, g, err := h.target.prepare(ctx)
	if err != nil {
		return opError("SetHook", err)
	}
	defer op.close()

	node := leaf(command("set-hook", append(h.target.args(), "-a", "--", name)...))
	node.args = append(node.args, wireArg{text: "", nested: commands.nodes()})
	_, err = h.target.server.execute(opCtx, op, plan{nodes: []wireNode{node}, mode: replyEmpty, allowStart: false}, g, nil)

	return opError("SetHook", err)
}

// Run executes the named hook event immediately in this scope (-R flag).
func (h HookScope) Run(ctx context.Context, name string) error {
	if !validFormatName(name) {
		return opError("RunHook", invalid("hook name"))
	}

	opCtx, op, g, err := h.target.prepare(ctx)
	if err != nil {
		return opError("RunHook", err)
	}
	defer op.close()

	args := append(h.target.args(), "-R", "--", name)
	_, err = h.target.server.execute(opCtx, op, emptyPlan(command("set-hook", args...)), g, nil)

	return opError("RunHook", err)
}

// Remove deletes all command slots registered under the named hook event in this scope.
func (h HookScope) Remove(ctx context.Context, name string) error {
	if !validFormatName(name) {
		return opError("UnsetHook", invalid("hook name"))
	}

	opCtx, op, g, err := h.target.prepare(ctx)
	if err != nil {
		return opError("UnsetHook", err)
	}
	defer op.close()

	args := append(h.target.args(), "-u", "--", name)
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

	return h.parseHooksOutput(r.Stdout)
}

// ListFiltered queries hooks registered in this scope filtered to the specified event name.
func (h HookScope) ListFiltered(ctx context.Context, name string) ([]HookInfo, error) {
	if !validFormatName(name) {
		return nil, opError("Hooks", invalid("hook name"))
	}

	opCtx, op, g, err := h.target.prepare(ctx)
	if err != nil {
		return nil, opError("Hooks", err)
	}
	defer op.close()

	args := append(h.target.args(), "--", name)

	r, err := h.target.server.execute(opCtx, op, plainPlan(command("show-hooks", args...)), g, nil)
	if err != nil {
		return nil, opError("Hooks", err)
	}

	return h.parseHooksOutput(r.Stdout)
}

func (h HookScope) parseHooksOutput(stdout []byte) ([]HookInfo, error) {
	out := []HookInfo{}

	for line := range bytes.SplitSeq(bytes.TrimSuffix(stdout, []byte{'\n'}), []byte{'\n'}) {
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

func validateBind(table KeyTable, key Key, commands CommandSequence, o BindOptions) error {
	isCommandlessEdit := len(commands.commands) == 0 && (o.Note != "" || o.ClearNote || o.Repeat)
	if !table.Valid() || !key.Valid() || (!isCommandlessEdit && len(commands.commands) == 0) || !wire.ValidString(o.Note) {
		return invalid("binding")
	}

	return nil
}

func bindArgs(table KeyTable, key Key, o BindOptions) []string {
	args := []string{"-T", string(table)}
	if o.Repeat {
		args = append(args, "-r")
	}

	if o.Note != "" {
		args = append(args, "-N", o.Note)
	} else if o.ClearNote {
		args = append(args, "-N", "")
	}

	return append(args, "--", string(key))
}

// Bind registers a key binding in the specified key table, executing commands when pressed.
//
// If commands is empty, [BindOptions.Note], [BindOptions.ClearNote], or [BindOptions.Repeat]
// can be used to update an existing binding's note or repeat flag without replacing its command.
func (s *Server) Bind(ctx context.Context, table KeyTable, key Key, commands CommandSequence, o BindOptions) error {
	if err := validateBind(table, key, commands, o); err != nil {
		return opError("Bind", err)
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

	args := bindArgs(table, key, o)
	if len(commands.commands) == 0 {
		_, err = s.execute(opCtx, op, emptyPlan(command("bind-key", args...)), newGuard(info.Identity), nil)
		return opError("Bind", err)
	}

	node := leaf(command("bind-key", args...))
	node.args = append(node.args, wireArg{text: "", nested: commands.nodes()})
	_, err = s.execute(opCtx, op, plan{nodes: []wireNode{node}, mode: replyEmpty, allowStart: false}, newGuard(info.Identity), nil)

	return opError("Bind", err)
}

// Unbind removes a key binding from the specified key table.
func (s *Server) Unbind(ctx context.Context, table KeyTable, key Key) error {
	return s.UnbindWith(ctx, table, key, UnbindOptions{All: false, Quiet: false})
}

// UnbindWith removes key bindings with options such as -a (remove all bindings) and -q (quiet).
func (s *Server) UnbindWith(ctx context.Context, table KeyTable, key Key, o UnbindOptions) error {
	if !o.All && !key.Valid() {
		return opError("Unbind", invalid("key"))
	}

	if table != "" && !table.Valid() {
		return opError("Unbind", invalid("key table"))
	}

	var args []string

	if o.All {
		args = append(args, "-a")
	}

	if o.Quiet {
		args = append(args, "-q")
	}

	if table != "" {
		args = append(args, "-T", string(table))
	}

	if !o.All {
		args = append(args, "--", string(key))
	}

	return s.endpointAction(ctx, "unbind-key", args...)
}

// Bindings queries and returns all key bindings currently registered in the specified key table.
//
// If a binding's command text uses complex shell or tmux syntax outside what our non-evaluating
// parser supports, [BindingInfo.Parsed] will be false and [BindingInfo.Raw] provides the verbatim text.
func (s *Server) Bindings(ctx context.Context, table KeyTable) ([]BindingInfo, error) {
	return s.BindingsWith(ctx, BindingsOptions{
		Table:      table,
		Key:        "",
		FirstMatch: false,
		NotesOnly:  false,
		Prefix:     "",
	})
}

func listKeysArgs(o BindingsOptions) []string {
	var args []string

	if o.FirstMatch {
		args = append(args, "-1")
	}

	if o.Table != "" {
		args = append(args, "-T", string(o.Table))
	}

	if o.Key != "" {
		args = append(args, string(o.Key))
	}

	return args
}

func validateBindingsOptions(o BindingsOptions) error {
	if o.Table != "" && !o.Table.Valid() {
		return invalid("key table")
	}

	if o.Key != "" && !o.Key.Valid() {
		return invalid("key")
	}

	return nil
}

// BindingsWith queries key bindings matching custom options such as table, key filter, or first match.
func (s *Server) BindingsWith(ctx context.Context, o BindingsOptions) ([]BindingInfo, error) {
	if err := validateBindingsOptions(o); err != nil {
		return nil, opError("Bindings", err)
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

	r, err := s.execute(opCtx, op, plainPlan(command("list-keys", listKeysArgs(o)...)), newGuard(info.Identity), nil)
	if err != nil {
		if o.Table != "" && strings.Contains(string(r.Stderr), "doesn't exist") {
			return []BindingInfo{}, nil
		}

		return nil, opError("Bindings", err)
	}

	out := parseBindingsLines(r.Stdout, o.Table, o.Table)
	if o.Key != "" {
		out = filterBindingsByKey(out, o.Key)
	}

	if len(out) == 0 && o.Table != "" && !o.FirstMatch {
		out = s.fallbackBindings(opCtx, op, info, o.Table, o.Key)
	}

	return out, nil
}

// BindingNotes queries key bindings that have attached notes (tmux list-keys -N).
func (s *Server) BindingNotes(ctx context.Context, o BindingsOptions) ([]BindingNote, error) {
	if err := validateBindingsOptions(o); err != nil {
		return nil, opError("BindingNotes", err)
	}

	if !wire.ValidString(o.Prefix) {
		return nil, opError("BindingNotes", invalid("prefix"))
	}

	opCtx, op, err := s.begin(ctx)
	if err != nil {
		return nil, opError("BindingNotes", err)
	}
	defer op.close()

	info, err := s.probe(opCtx, op)
	if err != nil {
		return nil, opError("BindingNotes", err)
	}

	r, err := s.execute(opCtx, op, plainPlan(command("list-keys", bindingNotesArgs(o)...)), newGuard(info.Identity), nil)
	if err != nil {
		if o.Table != "" && strings.Contains(string(r.Stderr), "doesn't exist") {
			return []BindingNote{}, nil
		}

		return nil, opError("BindingNotes", err)
	}

	notes := parseNotesOutput(r.Stdout, o)
	if len(notes) == 0 && o.Key != "" && o.Table != "" {
		notes = s.fallbackBindingNotes(opCtx, op, info, o)
	}

	return notes, nil
}

func bindingNotesArgs(o BindingsOptions) []string {
	args := append([]string{"-N"}, listKeysArgs(o)...)
	if o.Prefix != "" {
		args = append(args, "-P", o.Prefix)
	}

	return args
}

func parseNotesOutput(stdout []byte, o BindingsOptions) []BindingNote {
	var notes []BindingNote

	for line := range bytes.SplitSeq(bytes.TrimSuffix(stdout, []byte{'\n'}), []byte{'\n'}) {
		trimmed := strings.TrimRight(string(line), "\r\n")
		if len(trimmed) == 0 {
			continue
		}

		notes = append(notes, parseBindingNote(trimmed, o))
	}

	return notes
}

func (s *Server) fallbackBindingNotes(ctx context.Context, op *operation, info ServerInfo, o BindingsOptions) []BindingNote {
	fallbackArgs := []string{"-a"}
	if o.Table != "" {
		fallbackArgs = append(fallbackArgs, "-T", string(o.Table))
	}

	if o.Prefix != "" {
		fallbackArgs = append(fallbackArgs, "-P", o.Prefix)
	}

	fallbackArgs = append(fallbackArgs, "-F", "#{?key_note,#{key_string}\t#{key_note},}")

	rAll, errAll := s.execute(ctx, op, plainPlan(command("list-keys", fallbackArgs...)), newGuard(info.Identity), nil)
	if errAll != nil || len(rAll.Stdout) == 0 {
		return nil
	}

	var notes []BindingNote

	for line := range bytes.SplitSeq(bytes.TrimSuffix(rAll.Stdout, []byte{'\n'}), []byte{'\n'}) {
		trimmed := strings.TrimRight(string(line), "\r\n")
		if len(trimmed) == 0 {
			continue
		}

		if keyPart, notePart, ok := strings.Cut(trimmed, "\t"); ok {
			k := strings.TrimSpace(keyPart)
			noteText := strings.TrimSpace(notePart)

			if o.Key != "" && k != string(o.Key) {
				continue
			}

			notes = append(notes, BindingNote{
				Raw:  k + "  " + noteText,
				Key:  k,
				Note: noteText,
			})
		}
	}

	return notes
}

func parseBindingNote(trimmed string, o BindingsOptions) BindingNote {
	if i := strings.Index(trimmed, "  "); i >= 0 {
		return BindingNote{
			Raw:  trimmed,
			Key:  strings.TrimSpace(trimmed[:i]),
			Note: strings.TrimSpace(trimmed[i:]),
		}
	}

	if o.Key != "" && strings.Contains(trimmed, string(o.Key)) {
		idx := strings.Index(trimmed, string(o.Key))

		return BindingNote{
			Raw:  trimmed,
			Key:  strings.TrimSpace(trimmed[:idx+len(o.Key)]),
			Note: strings.TrimSpace(trimmed[idx+len(o.Key):]),
		}
	}

	if o.Prefix != "" && strings.HasPrefix(trimmed, o.Prefix) {
		rest := strings.TrimLeft(strings.TrimPrefix(trimmed, o.Prefix), " ")
		k, n, _ := strings.Cut(rest, " ")

		return BindingNote{
			Raw:  trimmed,
			Key:  strings.TrimSpace(o.Prefix + k),
			Note: strings.TrimSpace(n),
		}
	}

	first, rest, hasRest := strings.Cut(trimmed, " ")
	if !hasRest {
		return BindingNote{Raw: trimmed, Key: trimmed, Note: ""}
	}

	second, note, hasNote := strings.Cut(rest, " ")
	if hasNote {
		return BindingNote{
			Raw:  trimmed,
			Key:  first + " " + second,
			Note: note,
		}
	}

	return BindingNote{Raw: trimmed, Key: first, Note: second}
}

func parseBindingsLines(stdout []byte, defaultTable, filterTable KeyTable) []BindingInfo {
	var out []BindingInfo

	for line := range bytes.SplitSeq(bytes.TrimSuffix(stdout, []byte{'\n'}), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}

		b := parseBinding(string(line), defaultTable)
		if (filterTable == "" || b.Table == filterTable) && b.Key != "" {
			out = append(out, b)
		}
	}

	return out
}

func (s *Server) fallbackBindings(ctx context.Context, op *operation, info ServerInfo, table KeyTable, key Key) []BindingInfo {
	args := []string{}
	if table != "" {
		args = append(args, "-T", string(table))
	}

	defaultTable := table

	r, err := s.execute(ctx, op, plainPlan(command("list-keys", args...)), newGuard(info.Identity), nil)
	if err != nil || len(r.Stdout) == 0 {
		rAll, errAll := s.execute(ctx, op, plainPlan(command("list-keys")), newGuard(info.Identity), nil)
		if errAll != nil {
			return nil
		}

		r = rAll
		defaultTable = PrefixTable
	}

	out := parseBindingsLines(r.Stdout, defaultTable, table)
	if key != "" {
		out = filterBindingsByKey(out, key)
	}

	return out
}

func filterBindingsByKey(bindings []BindingInfo, key Key) []BindingInfo {
	var filtered []BindingInfo

	for _, b := range bindings {
		if b.Key == key {
			filtered = append(filtered, b)
		}
	}

	return filtered
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

	if b.Table == "" {
		b.Table = PrefixTable
	}

	if !b.Table.Valid() || !b.Key.Valid() {
		return b
	}

	commands, ok := parseBindingCommands(parts, words, i)
	if !ok {
		return b
	}

	b.Parsed = true
	b.Payload = CommandPayload{raw: raw, sequence: CommandSequence{commands: commands}, parsed: true}

	return b
}

func parseBindingCommands(parts []string, words []string, i int) ([]Command, bool) {
	first, err := NewCommand(words[i], words[i+1:]...)
	if err != nil {
		return nil, false
	}

	return parseBindingParts(parts[1:], first)
}

func parseBindingFlags(words []string, b *BindingInfo) (int, bool) {
	i := 1
	for i < len(words) {
		switch words[i] {
		case "-r":
			b.Repeat = true
			i++
		case "-n":
			b.Table = "root"
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
