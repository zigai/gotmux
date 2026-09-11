//go:build integration

package tmux_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"

	tmux "github.com/zigai/gotmux/tmux"
)

func testCommand(t *testing.T, name string, args ...string) tmux.Command {
	t.Helper()

	command, err := tmux.NewCommand(name, args...)
	if err != nil {
		t.Fatal(err)
	}

	return command
}

func testSequence(t *testing.T, commands ...tmux.Command) tmux.CommandSequence {
	t.Helper()

	sequence, err := tmux.Sequence(commands...)
	if err != nil {
		t.Fatal(err)
	}

	return sequence
}

func assertPayload(t *testing.T, payload tmux.CommandPayload, want tmux.Command) {
	t.Helper()

	commands, ok := payload.Commands()
	if !ok || len(commands) != 1 {
		t.Fatalf("unparsed payload %q: %v", payload.Raw(), commands)
	}

	if commands[0].Name() != want.Name() {
		t.Fatalf("command = %s, want %s", commands[0].Name(), want.Name())
	}

	if diff := cmp.Diff(want.Args(), commands[0].Args()); diff != "" {
		t.Fatal(diff)
	}
}

func assertHook(t *testing.T, ctx context.Context, session tmux.Session, name string, present bool, command tmux.Command) {
	t.Helper()

	hooks, err := session.Hooks().List(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, hook := range hooks {
		if hook.Name != name || hook.Index != 0 {
			continue
		}

		if !present {
			t.Fatalf("removed hook remains: %+v", hook)
		}

		if hook.Scope != tmux.SessionScope {
			t.Fatalf("scope = %v", hook.Scope)
		}

		assertPayload(t, hook.Payload, command)

		return
	}

	if present {
		t.Fatalf("missing hook %s: %+v", name, hooks)
	}
}

func TestIntegrationHooksLifecycle(t *testing.T) {
	_, session, ctx := apiFixture(t)

	const value = "literal ; #{pane_id} $() '"

	command := testCommand(t, "set-option", "-t", string(session.ID()), "@hook-effect", value)
	sequence := testSequence(t, command)

	const hook = "after-new-window"
	if err := session.Hooks().Set(ctx, hook, 0, sequence); err != nil {
		t.Fatal(err)
	}

	assertHook(t, ctx, session, hook, true, command)
	graphWindow(t, ctx, session)
	assertUserOption(t, ctx, session, "@hook-effect", value)

	if err := session.Hooks().Unset(ctx, hook, 0); err != nil {
		t.Fatal(err)
	}

	assertHook(t, ctx, session, hook, false, command)

	if err := session.Options().SetUser(ctx, "@hook-effect", "sentinel"); err != nil {
		t.Fatal(err)
	}

	graphWindow(t, ctx, session)
	assertUserOption(t, ctx, session, "@hook-effect", "sentinel")
}

func assertUserOption(t *testing.T, ctx context.Context, session tmux.Session, name, value string) {
	t.Helper()

	got, err := session.Options().User(ctx, name)
	if err != nil {
		t.Fatal(err)
	}

	if actual, ok := got.Local.Get(); !ok || actual != value {
		t.Fatalf("%s = %q present=%v, want %q", name, actual, ok, value)
	}
}

func TestIntegrationBindingsLifecycle(t *testing.T) {
	server, session, ctx := apiFixture(t)

	const table tmux.KeyTable = "tgo-test"

	command := testCommand(t, "set-option", "-t", string(session.ID()), "@binding-effect", "literal ; #{pane_id} '")
	sequence := testSequence(t, command)

	options := tmux.BindOptions{Repeat: true, Note: "test note"}
	for _, key := range []tmux.Key{"x", "y"} {
		if err := server.Bind(ctx, table, key, sequence, options); err != nil {
			t.Fatal(err)
		}
	}

	bindings, err := server.Bindings(ctx, table)
	if err != nil {
		t.Fatal(err)
	}

	if len(bindings) != 2 {
		t.Fatalf("bindings: %+v", bindings)
	}

	for _, binding := range bindings {
		assertBinding(t, binding, table, command)
	}

	if err := server.Unbind(ctx, table, "x"); err != nil {
		t.Fatal(err)
	}

	bindings, err = server.Bindings(ctx, table)
	if err != nil || len(bindings) != 1 || bindings[0].Key != "y" {
		t.Fatalf("unbind changed sentinel: %+v, %v", bindings, err)
	}
}

func assertBinding(t *testing.T, binding tmux.BindingInfo, table tmux.KeyTable, command tmux.Command) {
	t.Helper()

	if binding.Table != table || !binding.Repeat || !binding.Parsed {
		t.Fatalf("binding: %+v", binding)
	}

	assertPayload(t, binding.Payload, command)
}

func TestMultiCommandBinding(t *testing.T) {
	server, session, ctx := apiFixture(t)

	c1, err := tmux.NewCommand("set-option", "-t", string(session.ID()), "@e1", "1")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := tmux.NewCommand("set-option", "-t", string(session.ID()), "@e2", "2")
	if err != nil {
		t.Fatal(err)
	}
	seq, err := tmux.Sequence(c1, c2)
	if err != nil {
		t.Fatal(err)
	}

	key := tmux.Key("F12")
	if err := server.Bind(ctx, "root", key, seq, tmux.BindOptions{Note: "multicmd"}); err != nil {
		t.Fatal(err)
	}

	bindings, err := server.Bindings(ctx, "root")
	if err != nil {
		t.Fatal(err)
	}

	var found *tmux.BindingInfo
	for i := range bindings {
		if bindings[i].Key == key {
			found = &bindings[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("binding %v not found", key)
	}

	commands, ok := found.Payload.Commands()
	if !ok {
		t.Fatalf("payload not parsed: raw=%q", found.Payload.Raw())
	}
	if len(commands) != 2 {
		t.Fatalf("expected 2 commands in binding sequence, got %d (commands=%+v)", len(commands), commands)
	}
	if commands[0].Name() != "set-option" || commands[1].Name() != "set-option" {
		t.Fatalf("unexpected commands: %+v", commands)
	}
}

func TestUnindexedHookList(t *testing.T) {
	server, session, ctx := apiFixture(t)

	cmd, err := tmux.NewCommand("set-hook", "-t", string(session.ID()), "after-new-window", "display-message hello")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := server.UsingSubprocess()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Run(ctx, cmd); err != nil {
		t.Fatal(err)
	}

	hooks, err := session.Hooks().List(ctx)
	if err != nil {
		t.Fatalf("session.Hooks().List failed on unindexed hook: %v", err)
	}

	var found *tmux.HookInfo
	for i := range hooks {
		if hooks[i].Name == "after-new-window" {
			found = &hooks[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("unindexed hook after-new-window not found in %+v", hooks)
	}
	if found.Index != 0 {
		t.Fatalf("expected Index=0 for unindexed hook, got %d", found.Index)
	}
}
