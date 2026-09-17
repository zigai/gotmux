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

func TestIntegrationHookScopeExtensions(t *testing.T) {
	_, session, ctx := apiFixture(t)

	cmd1 := testCommand(t, "display-message", "first")
	cmd2 := testCommand(t, "display-message", "second")
	cmd3 := testCommand(t, "display-message", "replaced")

	// 1. SetWhole sets whole hook without index
	if err := session.Hooks().SetWhole(ctx, "after-new-window", testSequence(t, cmd1)); err != nil {
		t.Fatalf("SetWhole failed: %v", err)
	}

	// 2. Append appends to the hook with -a
	if err := session.Hooks().Append(ctx, "after-new-window", testSequence(t, cmd2)); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Verify both entries exist (index 0 and index 1)
	hooks, err := session.Hooks().List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	var count int
	for _, h := range hooks {
		if h.Name == "after-new-window" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 hooks for after-new-window after append, got %d", count)
	}

	// 3. ListFiltered returns only matching hooks
	filtered, err := session.Hooks().ListFiltered(ctx, "after-new-window")
	if err != nil {
		t.Fatalf("ListFiltered failed: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered hooks, got %d", len(filtered))
	}
	for _, h := range filtered {
		if h.Name != "after-new-window" {
			t.Fatalf("ListFiltered returned non-matching hook: %+v", h)
		}
	}

	// 4. Run-now (-R) executes without error
	if err := session.Hooks().Run(ctx, "after-new-window"); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// 5. SetWhole replaces the whole hook (replaces slot 0 and removes slot 1)
	if err := session.Hooks().SetWhole(ctx, "after-new-window", testSequence(t, cmd3)); err != nil {
		t.Fatalf("SetWhole replace failed: %v", err)
	}
	hooksReplaced, err := session.Hooks().ListFiltered(ctx, "after-new-window")
	if err != nil || len(hooksReplaced) != 1 {
		t.Fatalf("expected 1 hook after SetWhole replace, got %d (err=%v)", len(hooksReplaced), err)
	}

	// 6. Remove deletes all slots for the hook
	if err := session.Hooks().Remove(ctx, "after-new-window"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	hooksRemoved, err := session.Hooks().ListFiltered(ctx, "after-new-window")
	if err != nil || len(hooksRemoved) != 0 {
		t.Fatalf("expected 0 hooks after Remove, got %d (err=%v)", len(hooksRemoved), err)
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

	// Query with BindingsWith filtering by table and key
	bindings, err := server.BindingsWith(ctx, tmux.BindingsOptions{
		Table: "root",
		Key:   "F12",
	})
	if err != nil || len(bindings) != 1 {
		t.Fatalf("BindingsWith failed: %v, len=%d", err, len(bindings))
	}
	if bindings[0].Key != "F12" || bindings[0].Repeat {
		t.Fatalf("unexpected binding: %+v", bindings[0])
	}

	// 2. Commandless edit: update note and repeat without replacing command
	var emptySeq tmux.CommandSequence
	if err := server.Bind(ctx, "root", "F12", emptySeq, tmux.BindOptions{
		Repeat: true,
		Note:   "Updated Note",
	}); err != nil {
		t.Fatalf("commandless Bind failed: %v", err)
	}

	// Verify command was preserved and repeat is true
	bindingsAfter, err := server.BindingsWith(ctx, tmux.BindingsOptions{
		Table: "root",
		Key:   "F12",
	})
	if err != nil || len(bindingsAfter) != 1 {
		t.Fatalf("BindingsWith after edit failed: %v", err)
	}
	if !bindingsAfter[0].Repeat {
		t.Fatalf("expected Repeat=true after commandless edit, got %+v", bindingsAfter[0])
	}
	cmds, ok := bindingsAfter[0].Payload.Commands()
	if !ok || len(cmds) != 1 || cmds[0].Name() != "display-message" {
		t.Fatalf("command was not preserved: %+v", bindingsAfter[0].Payload)
	}

	// 3. Query BindingNotes
	notes, err := server.BindingNotes(ctx, tmux.BindingsOptions{
		Table: "root",
		Key:   "F12",
	})
	if err != nil || len(notes) == 0 {
		t.Fatalf("BindingNotes failed: %v, len=%d", err, len(notes))
	}
	if notes[0].Note != "Updated Note" {
		t.Fatalf("expected note 'Updated Note', got %q", notes[0].Note)
	}

	// 4. Commandless edit: clear note with ClearNote
	if err := server.Bind(ctx, "root", "F12", emptySeq, tmux.BindOptions{
		ClearNote: true,
	}); err != nil {
		t.Fatalf("ClearNote failed: %v", err)
	}

	// 5. UnbindWith specific key
	if err := server.UnbindWith(ctx, "root", "F12", tmux.UnbindOptions{Quiet: true}); err != nil {
		t.Fatalf("UnbindWith failed: %v", err)
	}

	// 6. Native mouse and User key bindings
	if err := server.Bind(ctx, "root", "MouseDown1Pane", testSequence(t, cmd), tmux.BindOptions{}); err != nil {
		t.Fatalf("failed to bind MouseDown1Pane: %v", err)
	}
	if err := server.Bind(ctx, "root", "User0", testSequence(t, cmd), tmux.BindOptions{}); err != nil {
		t.Fatalf("failed to bind User0: %v", err)
	}

	mouseBindings, err := server.BindingsWith(ctx, tmux.BindingsOptions{
		Table: "root",
		Key:   "MouseDown1Pane",
	})
	if err != nil || len(mouseBindings) != 1 {
		t.Fatalf("failed to query MouseDown1Pane binding: %v, len=%d", err, len(mouseBindings))
	}
	if mouseBindings[0].Key != "MouseDown1Pane" {
		t.Fatalf("unexpected mouse key: %q", mouseBindings[0].Key)
	}

	userBindings, err := server.BindingsWith(ctx, tmux.BindingsOptions{
		Table: "root",
		Key:   "User0",
	})
	if err != nil || len(userBindings) != 1 {
		t.Fatalf("failed to query User0 binding: %v, len=%d", err, len(userBindings))
	}
	if userBindings[0].Key != "User0" {
		t.Fatalf("unexpected user key: %q", userBindings[0].Key)
	}

	// 7. UnbindWith all on custom table
	customTable := tmux.KeyTable("custom_table")
	if err := server.Bind(ctx, customTable, "F1", testSequence(t, cmd), tmux.BindOptions{}); err != nil {
		t.Fatalf("failed to bind in custom table: %v", err)
	}
	if err := server.Bind(ctx, customTable, "F2", testSequence(t, cmd), tmux.BindOptions{}); err != nil {
		t.Fatalf("failed to bind in custom table: %v", err)
	}
	customBindings, err := server.Bindings(ctx, customTable)
	if err != nil || len(customBindings) != 2 {
		t.Fatalf("expected 2 custom bindings, got %d, err=%v", len(customBindings), err)
	}
	if err := server.UnbindWith(ctx, customTable, "", tmux.UnbindOptions{All: true, Quiet: true}); err != nil {
		t.Fatalf("UnbindWith all failed: %v", err)
	}
	afterAll, err := server.Bindings(ctx, customTable)
	if err != nil || len(afterAll) != 0 {
		t.Fatalf("expected 0 bindings after UnbindWith all, got %d, err=%v", len(afterAll), err)
	}
}
