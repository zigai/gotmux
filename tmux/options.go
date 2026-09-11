//go:generate go run ../internal/schema/generate -root ..

package tmux

import (
	"bytes"
	"context"
	"strconv"
	"strings"

	"github.com/zigai/gotmux/internal/wire"
)

type optionTarget struct {
	server *Server
	h      handle
	scope  Scope
}
type (
	// ServerOptions provides typed and untyped accessors for server-scoped options (-s flag).
	ServerOptions struct{ target optionTarget }

	// SessionOptions provides accessors for session options (global -g or session-specific).
	SessionOptions struct{ target optionTarget }

	// WindowOptions provides accessors for window options (global -g -w or window-specific -w).
	WindowOptions struct{ target optionTarget }

	// PaneOptions provides accessors for pane-scoped options (-p flag).
	PaneOptions struct{ target optionTarget }

	// ArrayEntry represents an element in a tmux array option (e.g. terminal-features, status-format).
	// In tmux, option arrays can be sparse (containing gaps/holes), so the explicit Index is preserved.
	ArrayEntry struct {
		Index int
		Value string
	}

	// ArrayUpdate specifies a single modification step for an array option.
	ArrayUpdate struct {
		Index int
		Value string
		Unset bool
	}

	// ArrayUpdateResult records the indices of array update steps successfully confirmed by tmux.
	ArrayUpdateResult struct{ Applied []int }
)

// Options returns an accessor for server-wide configuration options (tmux set-option -s).
func (s *Server) Options() ServerOptions {
	return ServerOptions{optionTarget{server: s, h: zeroHandle, scope: ServerScope}}
}

// GlobalSessionOptions returns an accessor for default session options inherited by
// newly created sessions (tmux set-option -g).
func (s *Server) GlobalSessionOptions() SessionOptions {
	return SessionOptions{optionTarget{server: s, h: zeroHandle, scope: GlobalSessionScope}}
}

// GlobalWindowOptions returns an accessor for default window options inherited by
// newly created windows (tmux set-option -g -w).
func (s *Server) GlobalWindowOptions() WindowOptions {
	return WindowOptions{optionTarget{server: s, h: zeroHandle, scope: GlobalWindowScope}}
}

// Options returns an accessor for configuration options scoped to this specific session.
func (s Session) Options() SessionOptions {
	return SessionOptions{optionTarget{server: s.h.server, h: s.h, scope: SessionScope}}
}

// Options returns an accessor for configuration options scoped to this specific window.
func (w Window) Options() WindowOptions {
	return WindowOptions{optionTarget{server: w.h.server, h: w.h, scope: WindowScope}}
}

// Options returns an accessor for configuration options scoped to this specific pane.
func (p Pane) Options() PaneOptions {
	return PaneOptions{optionTarget{server: p.h.server, h: p.h, scope: PaneScope}}
}

func (t optionTarget) prepare(ctx context.Context) (context.Context, *operation, *guard, error) {
	if t.h.server != nil {
		if err := t.h.check(); err != nil {
			return nil, nil, nil, err
		}
	} else if t.scope == SessionScope || t.scope == WindowScope || t.scope == PaneScope {
		return nil, nil, nil, ErrInvalidHandle
	}

	opCtx, op, err := t.server.begin(ctx)
	if err != nil {
		return nil, nil, nil, err
	}

	if t.h.server != nil {
		return opCtx, op, t.h.guard(), nil
	}

	info, err := t.server.probe(opCtx, op)
	if err != nil {
		op.close()
		return nil, nil, nil, err
	}

	return opCtx, op, newGuard(info.Identity), nil
}

func (t optionTarget) args() []string {
	switch t.scope {
	case ServerScope:
		return []string{"-s"}
	case GlobalSessionScope:
		return []string{"-g"}
	case SessionScope:
		return []string{"-t", t.h.id}
	case GlobalWindowScope:
		return []string{"-g", "-w"}
	case WindowScope:
		return []string{"-w", "-t", t.h.id}
	case PaneScope:
		return []string{"-p", "-t", t.h.id}
	}

	return nil
}
func validOptionName(name string) bool { return validFormatName(name) && name != "@" }
func userOptionName(name string) bool {
	return strings.HasPrefix(name, "@") && len(name) > 1 && validOptionName(name)
}

// Named output distinguishes scalars from arrays and rejects abbreviated names.
// Tmux quotes string values; ParseWords preserves their bytes without evaluation.
func (t optionTarget) readScalar(ctx context.Context, op *operation, g *guard, name string, inherited bool) (Value[string], error) {
	args := append(t.args(), "-q")
	if inherited {
		args = append(args, "-A")
	}

	args = append(args, "--", name)

	r, err := t.server.execute(ctx, op, plainPlan(command("show-options", args...)), g, nil)
	if err != nil {
		return Value[string]{}, err
	}

	if len(r.Stdout) == 0 {
		return UnavailableValue[string](), nil
	}

	if r.Stdout[len(r.Stdout)-1] != '\n' {
		return Value[string]{}, afterError("Options", decodeError("option", name, ErrProtocol))
	}

	if bytes.HasPrefix(r.Stdout, []byte(name+"[")) {
		return Value[string]{}, invalid("array option; use Array")
	}

	words, err := wire.ParseWords(string(r.Stdout[:len(r.Stdout)-1]))
	if err != nil {
		return Value[string]{}, afterError("Options", decodeError("option", name, err))
	}

	if len(words) != 2 || strings.TrimSuffix(words[0], "*") != name {
		return Value[string]{}, invalid("expected exact scalar option name")
	}

	return PresentValue(words[1]), nil
}

func (t optionTarget) read(ctx context.Context, name string) (OptionValue[string], error) {
	if !validOptionName(name) {
		return OptionValue[string]{}, opError("Options", invalid("option name"))
	}

	opCtx, op, g, err := t.prepare(ctx)
	if err != nil {
		return OptionValue[string]{}, opError("Options", err)
	}
	defer op.close()

	local, err := t.readScalar(opCtx, op, g, name, false)
	if err != nil {
		return OptionValue[string]{}, opError("Options", err)
	}

	effective, err := t.readScalar(opCtx, op, g, name, true)
	if err != nil {
		return OptionValue[string]{}, opError("Options", err)
	}

	result := OptionValue[string]{Local: local, Effective: effective, Origin: UnavailableValue[Scope]()}
	if a, ok := local.Get(); ok {
		if b, found := effective.Get(); !found || a != b {
			return OptionValue[string]{}, afterError("Options", ErrInconsistent)
		}

		result.Origin = PresentValue(t.scope)
	}
	// Parent values are observable, but a separate local/effective query cannot
	// prove exactly which parent currently owns the value. Origin stays unavailable
	// rather than guessing from an option-name prefix or comparing equal strings.
	return result, nil
}

func (t optionTarget) set(ctx context.Context, name, value string, unset bool) error {
	if !validOptionName(name) || !wire.ValidString(value) {
		return opError("SetOption", invalid("option name/value"))
	}

	opCtx, op, g, err := t.prepare(ctx)
	if err != nil {
		return opError("SetOption", err)
	}
	defer op.close()

	args := t.args()
	if unset {
		args = append(args, "-u")
	}

	args = append(args, "--", name)
	if !unset {
		args = append(args, value)
	}

	_, err = t.server.execute(opCtx, op, emptyPlan(command("set-option", args...)), g, nil)

	return opError("SetOption", err)
}

func userGet(ctx context.Context, t optionTarget, name string) (OptionValue[string], error) {
	if !userOptionName(name) {
		return OptionValue[string]{}, opError("UserOption", invalid("user option name"))
	}

	return t.read(ctx, name)
}

func userSet(ctx context.Context, t optionTarget, name, value string, unset bool) error {
	if !userOptionName(name) {
		return opError("UserOption", invalid("user option name"))
	}

	return t.set(ctx, name, value, unset)
}

func convertOption[T any](v OptionValue[string], parse func(string) (T, error)) (OptionValue[T], error) {
	cv := func(value Value[string]) (Value[T], error) {
		s, ok := value.Get()
		if !ok {
			if value.State() == Unsupported {
				return UnsupportedValue[T](), nil
			}

			return UnavailableValue[T](), nil
		}

		x, err := parse(s)
		if err != nil {
			return Value[T]{}, err
		}

		return PresentValue(x), nil
	}

	local, err := cv(v.Local)
	if err != nil {
		return OptionValue[T]{}, err
	}

	effective, err := cv(v.Effective)
	if err != nil {
		return OptionValue[T]{}, err
	}

	return OptionValue[T]{Local: local, Effective: effective, Origin: v.Origin}, nil
}

func parseOptionInt(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, decodeError("option", "integer", err)
	}

	return n, nil
}

func parseOptionBool(s string) (bool, error) {
	switch s {
	case "on", "1":
		return true, nil
	case "off", "0":
		return false, nil
	}

	return false, decodeError("option", "boolean", ErrProtocol)
}

func boolOption(v bool) string {
	if v {
		return "on"
	}

	return "off"
}

func arrayName(name string) bool {
	switch name {
	case "update-environment", "terminal-features", "terminal-overrides", "command-alias", "status-format", "user-keys":
		return true
	}

	return false
}

func (t optionTarget) array(ctx context.Context, name string) ([]ArrayEntry, error) {
	if !arrayName(name) {
		return nil, opError("Array", invalid("known array name"))
	}

	opCtx, op, g, err := t.prepare(ctx)
	if err != nil {
		return nil, opError("Array", err)
	}
	defer op.close()

	args := append(t.args(), "--", name)

	r, err := t.server.execute(opCtx, op, plainPlan(command("show-options", args...)), g, nil)
	if err != nil {
		return nil, opError("Array", err)
	}

	out := []ArrayEntry{}

	for line := range bytes.SplitSeq(bytes.TrimSuffix(r.Stdout, []byte{'\n'}), []byte{'\n'}) {
		if len(line) == 0 || string(line) == name {
			continue
		}

		key, value, ok := strings.Cut(string(line), " ")
		if !ok {
			return nil, afterError("Array", ErrProtocol)
		}

		index, err := arrayIndex(key, name)
		if err != nil {
			return nil, afterError("Array", err)
		}

		words, err := wire.ParseWords(value)
		if err != nil || len(words) != 1 {
			return nil, afterError("Array", ErrProtocol)
		}

		out = append(out, ArrayEntry{Index: index, Value: words[0]})
	}

	return out, nil
}

func arrayIndex(key, name string) (int, error) {
	if !strings.HasPrefix(key, name+"[") || !strings.HasSuffix(key, "]") {
		return 0, ErrProtocol
	}

	n, err := strconv.Atoi(key[len(name)+1 : len(key)-1])
	if err != nil || n < 0 {
		return 0, ErrProtocol
	}

	return n, nil
}

func (t optionTarget) updateArray(ctx context.Context, name string, updates []ArrayUpdate) (ArrayUpdateResult, error) {
	result := ArrayUpdateResult{Applied: []int{}}
	if !arrayName(name) {
		return result, opError("UpdateArray", invalid("known array name"))
	}

	if err := validateArrayUpdates(updates); err != nil {
		return result, opError("UpdateArray", err)
	}

	owned := append([]ArrayUpdate{}, updates...)

	opCtx, op, g, err := t.prepare(ctx)
	if err != nil {
		return result, opError("UpdateArray", err)
	}
	defer op.close()

	steps := make([]StepOutcome, len(owned))
	for i := range steps {
		steps[i] = StepOutcome{Index: i, Effect: NotSent}
	}

	for i, u := range owned {
		args := t.updateArrayStepArgs(name, u)

		_, err = t.server.execute(opCtx, op, emptyPlan(command("set-option", args...)), g, nil)
		if err != nil {
			return result, arrayUpdateError(err, i, steps)
		}

		steps[i].Effect = Confirmed
		result.Applied = append(result.Applied, i)
	}

	return result, nil
}

func validateArrayUpdates(updates []ArrayUpdate) error {
	for _, u := range updates {
		if u.Index < 0 || u.Index > 1<<30 || !wire.ValidString(u.Value) {
			return invalid("array entry")
		}
	}

	return nil
}

func (t optionTarget) updateArrayStepArgs(name string, u ArrayUpdate) []string {
	args := t.args()
	if u.Unset {
		args = append(args, "-u")
	}

	args = append(args, "--", name+"["+strconv.Itoa(u.Index)+"]")
	if !u.Unset {
		args = append(args, u.Value)
	}

	return args
}

func arrayUpdateError(err error, i int, steps []StepOutcome) error {
	outcome := outcomeOf(err)
	steps[i].Effect = outcome.Effect

	aggregate := outcome.Effect
	if i > 0 && aggregate == NotSent {
		aggregate = Unknown
	}

	return &OperationError{
		Operation: "UpdateArray",
		Outcome:   Outcome{Effect: aggregate, Steps: steps, Created: nil},
		Err:       err,
	}
}
