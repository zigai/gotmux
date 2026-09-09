package tmux

import (
	"bytes"
	"context"
	"strings"

	"github.com/zigai/gotmux/internal/codec"
)

// EnvironmentScope represents a scoped tmux environment variable table (either global
// server-wide or scoped to a specific session).
type (
	EnvironmentScope struct{ target optionTarget }

	// EnvironmentValue captures the state of an environment variable in tmux.
	EnvironmentValue struct {
		// Value is the string content of the variable if set, or Unavailable if absent.
		Value Value[string]

		// Unset is true if the variable was explicitly marked for removal (-r flag).
		// tmux represents removed variables with a leading minus sign (e.g. "-VAR").
		Unset bool

		// Hidden indicates whether the queried variable is in tmux's hidden environment table (-h flag).
		Hidden bool
	}
)

// Environment returns the global server environment scope (set-environment -g).
// Variables set here are inherited by all newly created sessions.
func (s *Server) Environment() EnvironmentScope {
	var zero handle
	return EnvironmentScope{target: optionTarget{server: s, h: zero, scope: GlobalSessionScope}}
}

// Environment returns the environment scope for this specific session.
// Variables set here override global environment variables for panes created in this session.
func (s Session) Environment() EnvironmentScope {
	return EnvironmentScope{target: optionTarget{server: s.h.server, h: s.h, scope: SessionScope}}
}

// Get retrieves one variable by name, preserving embedded newlines and empty values.
func (scope EnvironmentScope) Get(ctx context.Context, name string, hidden bool) (EnvironmentValue, error) {
	if !envName(name) {
		return EnvironmentValue{}, opError("Environment.Get", invalid("environment name"))
	}

	opCtx, op, g, err := scope.target.prepare(ctx)
	if err != nil {
		return EnvironmentValue{}, opError("Environment.Get", err)
	}
	defer op.close()

	args := scope.base()

	if hidden {
		args = append(args, "-h")
	}

	args = append(args, "--", name)

	r, err := scope.target.server.execute(opCtx, op, plainPlan(command("show-environment", args...)), g, nil)
	if err != nil {
		if string(r.Stderr) == "unknown variable: "+name+"\n" {
			return EnvironmentValue{Value: UnavailableValue[string](), Unset: false, Hidden: hidden}, nil
		}

		return EnvironmentValue{}, opError("Environment.Get", err)
	}

	data := bytes.TrimSuffix(r.Stdout, []byte{'\n'})
	if len(r.Stdout) == 0 {
		return EnvironmentValue{Value: UnavailableValue[string](), Unset: false, Hidden: hidden}, nil
	}

	if bytes.Equal(data, []byte("-"+name)) {
		return EnvironmentValue{Value: UnavailableValue[string](), Unset: true, Hidden: hidden}, nil
	}

	prefix := name + "="
	if !strings.HasPrefix(string(data), prefix) {
		return EnvironmentValue{}, afterError("Environment.Get", ErrProtocol)
	}

	return EnvironmentValue{Value: PresentValue(string(data[len(prefix):])), Unset: false, Hidden: hidden}, nil
}

// Set assigns a value to an environment variable in this scope.
func (scope EnvironmentScope) Set(ctx context.Context, name, value string) error {
	return scope.change(ctx, name, value, "")
}

// SetHidden assigns a value to a hidden environment variable (-h flag).
// Hidden variables are stored in tmux but are not automatically exported to child processes.
func (scope EnvironmentScope) SetHidden(ctx context.Context, name, value string) error {
	return scope.change(ctx, name, value, "-h")
}

// Unset deletes the specified variable from tmux's environment table (-u flag).
// Contrast with [EnvironmentScope.Remove]: Unset removes the variable from tmux, whereas
// Remove explicitly instructs tmux to strip the variable from child process environments.
func (scope EnvironmentScope) Unset(ctx context.Context, name string) error {
	return scope.change(ctx, name, "", "-u")
}

// Remove marks the variable to be stripped from new program environments (-r flag).
func (scope EnvironmentScope) Remove(ctx context.Context, name string) error {
	return scope.change(ctx, name, "", "-r")
}

func (scope EnvironmentScope) change(ctx context.Context, name, value, flag string) error {
	if !envName(name) || !codec.ValidString(value) {
		return opError("Environment.Set", invalid("environment entry"))
	}

	opCtx, op, g, err := scope.target.prepare(ctx)
	if err != nil {
		return opError("Environment.Set", err)
	}
	defer op.close()

	args := scope.base()

	if flag != "" {
		args = append(args, flag)
	}

	args = append(args, "--", name)
	if flag != "-u" && flag != "-r" {
		args = append(args, value)
	}

	_, err = scope.target.server.execute(opCtx, op, emptyPlan(command("set-environment", args...)), g, nil)

	return opError("Environment.Set", err)
}

func (scope EnvironmentScope) base() []string {
	if scope.target.scope == GlobalSessionScope {
		return []string{"-g"}
	}

	return []string{"-t", scope.target.h.id}
}
