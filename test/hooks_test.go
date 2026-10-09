//go:build integration

package test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/gotmux/tmux"
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

		if hook.Scope != tmux.ScopeSession {
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

func TestIntegrationBindingQueriesMatchNativeKeys(t *testing.T) {
	server, _, ctx := apiFixture(t)

	const table tmux.KeyTable = "query-keys"

	command := testCommand(t, "display-message", "pressed")
	sequence := testSequence(t, command)

	for _, key := range []tmux.Key{"Enter", "PPage", "IC", "C-Up"} {
		if err := server.Bind(ctx, table, key, sequence, tmux.BindOptions{Note: "note"}); err != nil {
			t.Fatal(err)
		}
	}

	for _, key := range []tmux.Key{"enter", "ENTER", "PgUp", "PageUp", "ppage", "Insert", "ic", "c-up"} {
		t.Run(string(key), func(t *testing.T) {
			got, err := server.FindBindings(ctx, tmux.BindingsOptions{Table: table, Key: key})
			if err != nil || len(got) != 1 {
				t.Fatalf("key %s: %+v, %v", key, got, err)
			}

			assertPayload(t, got[0].Payload, command)
		})
	}
}

func TestIntegrationBindingQueriesEmptyAndErrors(t *testing.T) {
	server, _, ctx := apiFixture(t)

	const table tmux.KeyTable = "query-empty"

	command := testCommand(t, "display-message", "pressed")
	if err := server.Bind(ctx, table, "x", testSequence(t, command), tmux.BindOptions{}); err != nil {
		t.Fatal(err)
	}

	for _, options := range []tmux.BindingsOptions{{Table: table, Key: "z"}, {Table: "absent-table"}, {Table: table, Key: "x", NotesOnly: true}} {
		got, err := server.FindBindings(ctx, options)
		if err != nil || len(got) != 0 {
			t.Fatalf("no match %+v: %+v, %v", options, got, err)
		}
	}

	notes, err := server.BindingNotes(ctx, tmux.BindingsOptions{Table: table, Key: "z"})
	if err != nil || len(notes) != 0 {
		t.Fatalf("unbound notes: %+v, %v", notes, err)
	}

	if _, err := server.FindBindings(ctx, tmux.BindingsOptions{Table: table, Key: "unsupported-key"}); !errors.Is(err, tmux.ErrInvalidArgument) {
		t.Fatalf("invalid key error = %v", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := server.FindBindings(ctx, tmux.BindingsOptions{Table: table, Key: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query error = %v", err)
	}
}

func TestIntegrationBindingsNotesOnlyAndFirstMatch(t *testing.T) {
	server, _, ctx := apiFixture(t)

	const table tmux.KeyTable = "query-notes"

	command := testCommand(t, "display-message", "pressed")
	sequence := testSequence(t, command)

	for _, binding := range []struct {
		key  tmux.Key
		note string
	}{{key: "a"}, {key: "b", note: "first note"}, {key: "c", note: "second note"}} {
		if err := server.Bind(ctx, table, binding.key, sequence, tmux.BindOptions{Repeat: true, Note: binding.note}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := server.FindBindings(ctx, tmux.BindingsOptions{Table: table, NotesOnly: true})
	if err != nil || len(got) != 2 || got[0].Key != "b" || got[1].Key != "c" {
		t.Fatalf("noted bindings: %+v, %v", got, err)
	}

	for _, binding := range got {
		assertBinding(t, binding, table, command)
	}

	got, err = server.FindBindings(ctx, tmux.BindingsOptions{Table: table, NotesOnly: true, FirstMatch: true})
	if err != nil || len(got) != 1 || got[0].Key != "b" {
		t.Fatalf("first noted binding: %+v, %v", got, err)
	}
}

func TestIntegrationBindingNotesKeyAndPrefix(t *testing.T) {
	server, _, ctx := apiFixture(t)

	const table tmux.KeyTable = "query-prefix"

	command := testCommand(t, "display-message", "pressed")
	if err := server.Bind(ctx, table, "Enter", testSequence(t, command), tmux.BindOptions{Note: "accept input"}); err != nil {
		t.Fatal(err)
	}

	got, err := server.BindingNotes(ctx, tmux.BindingsOptions{Table: table, Key: "enter", Prefix: "PRE "})
	if err != nil || len(got) != 1 || got[0].Key != "PRE Enter" || got[0].Note != "accept input" {
		t.Fatalf("key plus prefix notes: %+v, %v", got, err)
	}
}

func TestIntegrationBindingQueriesSingletons(t *testing.T) {
	server, _, ctx := apiFixture(t)

	const table tmux.KeyTable = "query-singleton"

	command := testCommand(t, "display-message", "pressed")
	sequence := testSequence(t, command)

	if err := server.Bind(ctx, table, "Enter", sequence, tmux.BindOptions{Note: "accept input"}); err != nil {
		t.Fatal(err)
	}

	for _, options := range []tmux.BindingsOptions{
		{Table: table},
		{Table: table, Key: "enter"},
		{Table: table, FirstMatch: true},
		{Table: table, NotesOnly: true},
		{Table: table, NotesOnly: true, FirstMatch: true},
	} {
		assertSingleBindingQuery(t, ctx, server, options, "Enter")
	}

	if err := server.Bind(ctx, table, "x", sequence, tmux.BindOptions{}); err != nil {
		t.Fatal(err)
	}

	for _, options := range []tmux.BindingsOptions{
		{Table: table, NotesOnly: true},
		{Table: table, NotesOnly: true, FirstMatch: true},
	} {
		assertSingleBindingQuery(t, ctx, server, options, "Enter")
	}

	for _, options := range []tmux.BindingsOptions{
		{Table: table, Prefix: "PRE "},
		{Table: table, Key: "enter", Prefix: "PRE "},
		{Table: table, FirstMatch: true, Prefix: "PRE "},
	} {
		got, err := server.BindingNotes(ctx, options)
		if err != nil || len(got) != 1 || got[0].Note != "accept input" {
			t.Errorf("single note %+v: %+v, %v", options, got, err)
		}
	}
}

func TestIntegrationBindingQueriesGlobalSingleton(t *testing.T) {
	server, _, ctx := apiFixture(t)

	bindings, err := server.Bindings(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	tables := make(map[tmux.KeyTable]bool)
	for _, binding := range bindings {
		tables[binding.Table] = true
	}

	for table := range tables {
		if err := server.UnbindWith(ctx, table, "", tmux.UnbindOptions{All: true}); err != nil {
			t.Fatal(err)
		}
	}

	command := testCommand(t, "display-message", "only binding")
	if err := server.Bind(ctx, "root", "Enter", testSequence(t, command), tmux.BindOptions{Note: "only note"}); err != nil {
		t.Fatal(err)
	}

	for _, options := range []tmux.BindingsOptions{
		{},
		{Key: "enter"},
		{Table: "root"},
		{NotesOnly: true},
		{FirstMatch: true},
	} {
		assertSingleBindingQuery(t, ctx, server, options, "Enter")
	}

	got, err := server.BindingNotes(ctx, tmux.BindingsOptions{Table: "root", Prefix: "PRE "})
	if err != nil || len(got) != 1 || got[0].Note != "only note" {
		t.Errorf("global singleton note: %+v, %v", got, err)
	}
}

func assertSingleBindingQuery(t *testing.T, ctx context.Context, server *tmux.Server, options tmux.BindingsOptions, key tmux.Key) {
	t.Helper()

	got, err := server.FindBindings(ctx, options)
	if err != nil || len(got) != 1 || got[0].Key != key {
		t.Errorf("singleton binding %+v: %+v, %v", options, got, err)
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

	sub, err := server.ViaSubprocess()
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

	if err := session.Hooks().SetWhole(ctx, hook, testSequence(t, cmd1)); err != nil {
		t.Fatalf("SetWhole failed: %v", err)
	}

	if err := session.Hooks().Append(ctx, hook, testSequence(t, cmd2)); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	hooks, err := session.Hooks().List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if count := len(slices.DeleteFunc(hooks, func(h tmux.HookInfo) bool { return h.Name != hook })); count != 2 {
		t.Fatalf("expected 2 hooks for %s after append, got %d", hook, count)
	}

	assertFilteredHooks(t, ctx, session, hook, 2)

	if err := session.Hooks().Run(ctx, hook); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if err := session.Hooks().SetWhole(ctx, hook, testSequence(t, cmd3)); err != nil {
		t.Fatalf("SetWhole replace failed: %v", err)
	}

	assertFilteredHooks(t, ctx, session, hook, 1)

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

	if err := server.Bind(ctx, "root", "F12", testSequence(t, cmd), tmux.BindOptions{
		Repeat: false,
		Note:   "Initial Note",
	}); err != nil {
		t.Fatalf("Bind failed: %v", err)
	}

	if binding := soleBinding(t, ctx, server, "F12"); binding.Repeat {
		t.Fatalf("unexpected binding: %+v", binding)
	}

	assertCommandlessEdit(t, ctx, server, "F12")

	var emptySeq tmux.CommandSequence
	if err := server.Bind(ctx, "root", "F12", emptySeq, tmux.BindOptions{
		ClearNote: true,
	}); err != nil {
		t.Fatalf("ClearNote failed: %v", err)
	}

	if err := server.UnbindWith(ctx, "root", "F12", tmux.UnbindOptions{Quiet: true}); err != nil {
		t.Fatalf("UnbindWith failed: %v", err)
	}

	bindKeys(t, ctx, server, "root", testSequence(t, cmd), "MouseDown1Pane", "User0")
	soleBinding(t, ctx, server, "MouseDown1Pane")
	soleBinding(t, ctx, server, "User0")

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

	bindings, err := server.FindBindings(ctx, tmux.BindingsOptions{Table: "root", Key: key})
	if err != nil || len(bindings) != 1 || bindings[0].Key != key {
		t.Fatalf("FindBindings(root, %s) = %+v, %v; want one binding for the key", key, bindings, err)
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
