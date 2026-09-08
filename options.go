package tmux

import (
	"bytes"
	"context"
	"strconv"
	"strings"

	"example.com/tmux/internal/codec"
)

type optionTarget struct {
	server *Server
	h      handle
	scope  Scope
}
type ServerOptions struct{ target optionTarget }
type SessionOptions struct{ target optionTarget }
type WindowOptions struct{ target optionTarget }
type PaneOptions struct{ target optionTarget }

func (s *Server) Options() ServerOptions {
	return ServerOptions{optionTarget{server: s, scope: ServerScope}}
}
func (s *Server) GlobalSessionOptions() SessionOptions {
	return SessionOptions{optionTarget{server: s, scope: GlobalSessionScope}}
}
func (s *Server) GlobalWindowOptions() WindowOptions {
	return WindowOptions{optionTarget{server: s, scope: GlobalWindowScope}}
}
func (s Session) Options() SessionOptions {
	return SessionOptions{optionTarget{server: s.h.server, h: s.h, scope: SessionScope}}
}
func (w Window) Options() WindowOptions {
	return WindowOptions{optionTarget{server: w.h.server, h: w.h, scope: WindowScope}}
}
func (p Pane) Options() PaneOptions {
	return PaneOptions{optionTarget{server: p.h.server, h: p.h, scope: PaneScope}}
}
func (t optionTarget) prepare(ctx context.Context) (*operation, *guard, error) {
	if t.h.server != nil {
		if e := t.h.check(); e != nil {
			return nil, nil, e
		}
	} else if t.scope == SessionScope || t.scope == WindowScope || t.scope == PaneScope {
		return nil, nil, ErrInvalidHandle
	}
	op, e := t.server.begin(ctx)
	if e != nil {
		return nil, nil, e
	}
	if t.h.server != nil {
		return op, t.h.guard(), nil
	}
	info, e := t.server.probe(op)
	if e != nil {
		op.close()
		return nil, nil, e
	}
	return op, &guard{identity: info.Identity}, nil
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

// readScalar relies only on the final command-added LF, not on line splitting:
// a present empty string is one LF; an absent local option is zero bytes.
func (t optionTarget) readScalar(op *operation, g *guard, name string, inherited bool) (Value[string], error) {
	args := append(t.args(), "-v", "-q")
	if inherited {
		args = append(args, "-A")
	}
	args = append(args, "--", name)
	r, e := t.server.execute(op, plainPlan(command("show-options", args...)), g, nil)
	if e != nil {
		return Value[string]{}, e
	}
	if len(r.Stdout) == 0 {
		return UnavailableValue[string](), nil
	}
	if r.Stdout[len(r.Stdout)-1] != '\n' {
		return Value[string]{}, afterError("Options", decodeError("option", name, ErrProtocol))
	}
	return PresentValue(string(r.Stdout[:len(r.Stdout)-1])), nil
}
func (t optionTarget) read(ctx context.Context, name string) (OptionValue[string], error) {
	if !validOptionName(name) {
		return OptionValue[string]{}, opError("Options", invalid("option name"))
	}
	op, g, e := t.prepare(ctx)
	if e != nil {
		return OptionValue[string]{}, opError("Options", e)
	}
	defer op.close()
	local, e := t.readScalar(op, g, name, false)
	if e != nil {
		return OptionValue[string]{}, opError("Options", e)
	}
	effective, e := t.readScalar(op, g, name, true)
	if e != nil {
		return OptionValue[string]{}, opError("Options", e)
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
	if !validOptionName(name) || !codec.ValidString(value) {
		return opError("SetOption", invalid("option name/value"))
	}
	op, g, e := t.prepare(ctx)
	if e != nil {
		return opError("SetOption", e)
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
	_, e = t.server.execute(op, emptyPlan(command("set-option", args...)), g, nil)
	return opError("SetOption", e)
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
		x, e := parse(s)
		if e != nil {
			return Value[T]{}, e
		}
		return PresentValue(x), nil
	}
	local, e := cv(v.Local)
	if e != nil {
		return OptionValue[T]{}, e
	}
	effective, e := cv(v.Effective)
	if e != nil {
		return OptionValue[T]{}, e
	}
	return OptionValue[T]{Local: local, Effective: effective, Origin: v.Origin}, nil
}
func parseOptionInt(s string) (int, error) {
	n, e := strconv.Atoi(s)
	if e != nil {
		return 0, decodeError("option", "integer", e)
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

// ArrayEntry retains explicit indexes, including zero and holes. Value is
// tmux's serialized value, not a shell line to execute.
type ArrayEntry struct {
	Index int
	Value string
}
type ArrayUpdate struct {
	Index int
	Value string
	Unset bool
}
type ArrayUpdateResult struct{ Applied []int }

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
	op, g, e := t.prepare(ctx)
	if e != nil {
		return nil, opError("Array", e)
	}
	defer op.close()
	args := append(t.args(), "--", name)
	r, e := t.server.execute(op, plainPlan(command("show-options", args...)), g, nil)
	if e != nil {
		return nil, opError("Array", e)
	}
	out := []ArrayEntry{}
	for _, line := range bytes.Split(bytes.TrimSuffix(r.Stdout, []byte{'\n'}), []byte{'\n'}) {
		if len(line) == 0 || string(line) == name {
			continue
		}
		key, value, ok := strings.Cut(string(line), " ")
		if !ok {
			return nil, afterError("Array", ErrProtocol)
		}
		index, e := arrayIndex(key, name)
		if e != nil {
			return nil, afterError("Array", e)
		}
		words, e := codec.ParseWords(value)
		if e != nil || len(words) != 1 {
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
	n, e := strconv.Atoi(key[len(name)+1 : len(key)-1])
	if e != nil || n < 0 {
		return 0, ErrProtocol
	}
	return n, nil
}
func (t optionTarget) updateArray(ctx context.Context, name string, updates []ArrayUpdate) (ArrayUpdateResult, error) {
	result := ArrayUpdateResult{Applied: []int{}}
	if !arrayName(name) {
		return result, opError("UpdateArray", invalid("known array name"))
	}
	owned := append([]ArrayUpdate{}, updates...)
	for _, u := range owned {
		if u.Index < 0 || u.Index > 1<<30 || !codec.ValidString(u.Value) {
			return result, opError("UpdateArray", invalid("array entry"))
		}
	}
	op, g, e := t.prepare(ctx)
	if e != nil {
		return result, opError("UpdateArray", e)
	}
	defer op.close()
	steps := make([]StepOutcome, len(owned))
	for i := range steps {
		steps[i] = StepOutcome{Index: i, Effect: NotSent}
	}
	for i, u := range owned {
		args := t.args()
		if u.Unset {
			args = append(args, "-u")
		}
		args = append(args, "--", name+"["+strconv.Itoa(u.Index)+"]")
		if !u.Unset {
			args = append(args, u.Value)
		}
		_, e = t.server.execute(op, emptyPlan(command("set-option", args...)), g, nil)
		if e != nil {
			outcome := outcomeOf(e)
			steps[i].Effect = outcome.Effect
			aggregate := outcome.Effect
			if i > 0 && aggregate == NotSent {
				aggregate = Unknown
			}
			return result, &OperationError{Operation: "UpdateArray", Outcome: Outcome{Effect: aggregate, Steps: steps}, Err: e}
		}
		steps[i].Effect = Confirmed
		result.Applied = append(result.Applied, i)
	}
	return result, nil
}
