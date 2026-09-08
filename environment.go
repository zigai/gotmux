package tmux

import (
	"bytes"
	"context"
	"strings"

	"example.com/tmux/internal/codec"
)

type EnvironmentScope struct{ target optionTarget }
type EnvironmentValue struct {
	Value  Value[string]
	Unset  bool
	Hidden bool
}

func (s *Server) Environment() EnvironmentScope {
	return EnvironmentScope{target: optionTarget{server: s, scope: GlobalSessionScope}}
}
func (s Session) Environment() EnvironmentScope {
	return EnvironmentScope{target: optionTarget{server: s.h.server, h: s.h, scope: SessionScope}}
}
func (e EnvironmentScope) base() []string {
	if e.target.scope == GlobalSessionScope {
		return []string{"-g"}
	}
	return []string{"-t", e.target.h.id}
}

// Get observes one exact variable, preserving embedded newlines and empty values.
// Environment listing is deliberately not parsed with unsafe line splitting.
func (v EnvironmentScope) Get(ctx context.Context, name string, hidden bool) (EnvironmentValue, error) {
	if !envName(name) {
		return EnvironmentValue{}, opError("Environment.Get", invalid("environment name"))
	}
	op, g, e := v.target.prepare(ctx)
	if e != nil {
		return EnvironmentValue{}, opError("Environment.Get", e)
	}
	defer op.close()
	args := v.base()
	if hidden {
		args = append(args, "-h")
	}
	args = append(args, "--", name)
	r, e := v.target.server.execute(op, plainPlan(command("show-environment", args...)), g, nil)
	if e != nil {
		if string(r.Stderr) == "unknown variable: "+name+"\n" {
			return EnvironmentValue{Value: UnavailableValue[string](), Hidden: hidden}, nil
		}
		return EnvironmentValue{}, opError("Environment.Get", e)
	}
	data := bytes.TrimSuffix(r.Stdout, []byte{'\n'})
	if len(r.Stdout) == 0 {
		return EnvironmentValue{Value: UnavailableValue[string](), Hidden: hidden}, nil
	}
	if string(data) == "-"+name {
		return EnvironmentValue{Value: UnavailableValue[string](), Unset: true, Hidden: hidden}, nil
	}
	prefix := name + "="
	if !strings.HasPrefix(string(data), prefix) {
		return EnvironmentValue{}, afterError("Environment.Get", ErrProtocol)
	}
	return EnvironmentValue{Value: PresentValue(string(data[len(prefix):])), Hidden: hidden}, nil
}
func (v EnvironmentScope) change(ctx context.Context, name, value, flag string) error {
	if !envName(name) || !codec.ValidString(value) {
		return opError("Environment.Set", invalid("environment entry"))
	}
	op, g, e := v.target.prepare(ctx)
	if e != nil {
		return opError("Environment.Set", e)
	}
	defer op.close()
	args := v.base()
	if flag != "" {
		args = append(args, flag)
	}
	args = append(args, "--", name)
	if flag != "-u" && flag != "-r" {
		args = append(args, value)
	}
	_, e = v.target.server.execute(op, emptyPlan(command("set-environment", args...)), g, nil)
	return opError("Environment.Set", e)
}
func (v EnvironmentScope) Set(ctx context.Context, name, value string) error {
	return v.change(ctx, name, value, "")
}
func (v EnvironmentScope) SetHidden(ctx context.Context, name, value string) error {
	return v.change(ctx, name, value, "-h")
}

// Unset removes the scoped entry; Remove marks it to be removed from new
// program environments. Those two tmux operations are not interchangeable.
func (v EnvironmentScope) Unset(ctx context.Context, name string) error {
	return v.change(ctx, name, "", "-u")
}
func (v EnvironmentScope) Remove(ctx context.Context, name string) error {
	return v.change(ctx, name, "", "-r")
}
