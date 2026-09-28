//go:build integration

package tmux_test

import (
	"context"
	"slices"
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

	c1 := testCommand(t, "set-option", "-t", string(session.ID()), "@e1", "1")
	c2 := testCommand(t, "set-option", "-t", string(session.ID()), "@e2", "2")

	key := tmux.Key("F12")
	if err := server.Bind(ctx, "root", key, testSequence(t, c1, c2), tmux.BindOptions{Note: "multicmd"}); err != nil {
		t.Fatal(err)
	}

	found := findBinding(t, ctx, server, "root", key)

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

func findBinding(t *testing.T, ctx context.Context, server *tmux.Server, table tmux.KeyTable, key tmux.Key) tmux.BindingInfo {
	t.Helper()

	bindings, err := server.Bindings(ctx, table)
	if err != nil {
		t.Fatal(err)
	}

	for _, binding := range bindings {
		if binding.Key == key {
			return binding
		}
	}

	t.Fatalf("binding %v not found in %+v", key, bindings)

	return tmux.BindingInfo{}
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

func TestIntegrationHookScopeExtensions(t *testing.T) {
	_, session, ctx := apiFixture(t)

	const hook = "after-new-window"

	cmd1 := testCommand(t, "display-message", "first")
	cmd2 := testCommand(t, "display-message", "second")
	cmd3 := testCommand(t, "display-message", "replaced")

	// 1. SetWhole sets whole hook without index
	if err := session.Hooks().SetWhole(ctx, hook, testSequence(t, cmd1)); err != nil {
		t.Fatalf("SetWhole failed: %v", err)
	}

	// 2. Append appends to the hook with -a
	if err := session.Hooks().Append(ctx, hook, testSequence(t, cmd2)); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Verify both entries exist (index 0 and index 1)
	hooks, err := session.Hooks().List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if count := len(slices.DeleteFunc(hooks, func(h tmux.HookInfo) bool { return h.Name != hook })); count != 2 {
		t.Fatalf("expected 2 hooks for %s after append, got %d", hook, count)
	}

	// 3. ListFiltered returns only matching hooks
	assertFilteredHooks(t, ctx, session, hook, 2)

	// 4. Run-now (-R) executes without error
	if err := session.Hooks().Run(ctx, hook); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// 5. SetWhole replaces the whole hook (replaces slot 0 and removes slot 1)
	if err := session.Hooks().SetWhole(ctx, hook, testSequence(t, cmd3)); err != nil {
		t.Fatalf("SetWhole replace failed: %v", err)
	}

	assertFilteredHooks(t, ctx, session, hook, 1)

	// 6. Remove deletes all slots for the hook
	if err := session.Hooks().Remove(ctx, hook); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	assertFilteredHooks(t, ctx, session, hook, 0)
}

func assertFilteredHooks(t *testing.T, ctx context.Context, session tmux.Session, name string, want int) {
	t.Helper()

	filtered, err := session.Hooks().ListFiltered(ctx, name)
	if err != nil {
		t.Fatalf("ListFiltered failed: %v", err)
	}

	if len(filtered) != want {
		t.Fatalf("ListFiltered(%s) = %d hooks, want %d: %+v", name, len(filtered), want, filtered)
	}

	for _, h := range filtered {
		if h.Name != name {
			t.Fatalf("ListFiltered returned non-matching hook: %+v", h)
		}
	}
}

func TestIntegrationKeyBindingsExtensions(t *testing.T) {
	server, _, ctx := apiFixture(t)

	cmd := testCommand(t, "display-message", "pressed")

	// 1. Bind key with note and repeat
	if err := server.Bind(ctx, "root", "F12", testSequence(t, cmd), tmux.BindOptions{
		Repeat: false,
		Note:   "Initial Note",
	}); err != nil {
		t.Fatalf("Bind failed: %v", err)
	}

	if binding := soleBinding(t, ctx, server, "F12"); binding.Repeat {
		t.Fatalf("unexpected binding: %+v", binding)
	}

	// 2-3. Commandless edit
	assertCommandlessEdit(t, ctx, server, "F12")

	// 4. Commandless edit: clear note with ClearNote, then 5. UnbindWith the key
	var emptySeq tmux.CommandSequence
	if err := server.Bind(ctx, "root", "F12", emptySeq, tmux.BindOptions{
		ClearNote: true,
	}); err != nil {
		t.Fatalf("ClearNote failed: %v", err)
	}

	if err := server.UnbindWith(ctx, "root", "F12", tmux.UnbindOptions{Quiet: true}); err != nil {
		t.Fatalf("UnbindWith failed: %v", err)
	}

	// 6. Native mouse and User key bindings
	bindKeys(t, ctx, server, "root", testSequence(t, cmd), "MouseDown1Pane", "User0")
	soleBinding(t, ctx, server, "MouseDown1Pane")
	soleBinding(t, ctx, server, "User0")

	// 7. UnbindWith all on custom table
	assertUnbindAll(t, ctx, server, "custom_table", testSequence(t, cmd))
}

func assertCommandlessEdit(t *testing.T, ctx context.Context, server *tmux.Server, key tmux.Key) {
	t.Helper()

	var emptySeq tmux.CommandSequence
	if err := server.Bind(ctx, "root", key, emptySeq, tmux.BindOptions{
		Repeat: true,
		Note:   "Updated Note",
	}); err != nil {
		t.Fatalf("commandless Bind failed: %v", err)
	}

	edited := soleBinding(t, ctx, server, key)
	if !edited.Repeat {
		t.Fatalf("expected Repeat=true after commandless edit, got %+v", edited)
	}

	cmds, ok := edited.Payload.Commands()
	if !ok || len(cmds) != 1 || cmds[0].Name() != "display-message" {
		t.Fatalf("command was not preserved: %+v", edited.Payload)
	}

	notes, err := server.BindingNotes(ctx, tmux.BindingsOptions{
		Table: "root",
		Key:   key,
	})
	if err != nil || len(notes) == 0 || notes[0].Note != "Updated Note" {
		t.Fatalf("BindingNotes = %+v, %v; want the note \"Updated Note\"", notes, err)
	}
}

func assertUnbindAll(t *testing.T, ctx context.Context, server *tmux.Server, table tmux.KeyTable, commands tmux.CommandSequence) {
	t.Helper()

	bindKeys(t, ctx, server, table, commands, "F1", "F2")

	bindings, err := server.Bindings(ctx, table)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("expected 2 bindings in %s, got %d, err=%v", table, len(bindings), err)
	}

	if err := server.UnbindWith(ctx, table, "", tmux.UnbindOptions{All: true, Quiet: true}); err != nil {
		t.Fatalf("UnbindWith all failed: %v", err)
	}

	afterAll, err := server.Bindings(ctx, table)
	if err != nil || len(afterAll) != 0 {
		t.Fatalf("expected 0 bindings after UnbindWith all, got %d, err=%v", len(afterAll), err)
	}
}

func soleBinding(t *testing.T, ctx context.Context, server *tmux.Server, key tmux.Key) tmux.BindingInfo {
	t.Helper()

	bindings, err := server.BindingsWith(ctx, tmux.BindingsOptions{Table: "root", Key: key})
	if err != nil || len(bindings) != 1 || bindings[0].Key != key {
		t.Fatalf("BindingsWith(root, %s) = %+v, %v; want one binding for the key", key, bindings, err)
	}

	return bindings[0]
}

func bindKeys(t *testing.T, ctx context.Context, server *tmux.Server, table tmux.KeyTable, commands tmux.CommandSequence, keys ...tmux.Key) {
	t.Helper()

	for _, key := range keys {
		if err := server.Bind(ctx, table, key, commands, tmux.BindOptions{}); err != nil {
			t.Fatalf("failed to bind %s in %s: %v", key, table, err)
		}
	}
}
