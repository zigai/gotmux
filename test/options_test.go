//go:build integration

package tmux_test

import (
	"context"
	"slices"
	"testing"

	tmux "github.com/zigai/gotmux/tmux"
)

func TestIntegrationTypedOptionInteger(t *testing.T) {
	server, _, ctx := apiFixture(t)
	for _, value := range []int{0, 2147483647} {
		if err := server.Options().SetEscapeTime(ctx, value); err != nil {
			t.Fatal(err)
		}

		got, err := server.Options().EscapeTime(ctx)
		if actual, ok := got.Effective.Get(); err != nil || !ok || actual != value {
			t.Fatalf("integer: %+v, %v", got, err)
		}
	}
}

func TestIntegrationTypedOptionBoolean(t *testing.T) {
	_, session, ctx := apiFixture(t)
	for _, value := range []bool{true, false} {
		if err := session.Options().SetMouse(ctx, value); err != nil {
			t.Fatal(err)
		}

		got, err := session.Options().Mouse(ctx)
		if actual, ok := got.Local.Get(); err != nil || !ok || actual != value {
			t.Fatalf("boolean: %+v, %v", got, err)
		}
	}
}

func TestIntegrationTypedOptionEnum(t *testing.T) {
	server, _, ctx := apiFixture(t)
	for _, value := range []tmux.ClipboardMode{tmux.ClipboardOff, tmux.ClipboardExternal, tmux.ClipboardOn} {
		if err := server.Options().SetClipboard(ctx, value); err != nil {
			t.Fatal(err)
		}

		got, err := server.Options().Clipboard(ctx)
		if actual, ok := got.Effective.Get(); err != nil || !ok || actual != value {
			t.Fatalf("enum: %+v, %v", got, err)
		}
	}
}

func TestIntegrationTypedOptionKey(t *testing.T) {
	_, session, ctx := apiFixture(t)
	if err := session.Options().SetPrefix(ctx, tmux.Key("C-a")); err != nil {
		t.Fatal(err)
	}

	key, err := session.Options().Prefix(ctx)
	if actual, ok := key.Local.Get(); err != nil || !ok || actual != tmux.Key("C-a") {
		t.Fatalf("key: %+v, %v", key, err)
	}

	if err := session.Options().UnsetPrefix(ctx); err != nil {
		t.Fatal(err)
	}

	key, err = session.Options().Prefix(ctx)
	if err != nil || key.Local.State() != tmux.ValueStateUnavailable {
		t.Fatalf("unset key: %+v, %v", key, err)
	}
}

func TestIntegrationArrayOptions_ArbitraryAndSparse(t *testing.T) {
	server, _, ctx := apiFixture(t)

	// Set sparse array entries via indexed Set
	for name, value := range map[string]string{"codepoint-widths[100]": "2", "codepoint-widths[500]": "1"} {
		if err := server.Options().Set(ctx, name, value); err != nil {
			t.Fatalf("failed to set sparse array entry %s: %v", name, err)
		}
	}

	// Read single entry via indexed Get
	val100, err := server.Options().Get(ctx, "codepoint-widths[100]")
	if v, ok := val100.Effective.Get(); err != nil || !ok || v != "2" {
		t.Fatalf("unexpected value for codepoint-widths[100]: %+v, err: %v", val100, err)
	}

	// Read full sparse array via Array
	if entries := arrayEntries(t, ctx, server, "codepoint-widths"); entries[100] != "2" || entries[500] != "1" {
		t.Fatalf("expected indices 100 and 500 in array, got entries: %+v", entries)
	}

	// Update array using UpdateArray
	updateRes, err := server.Options().UpdateArray(ctx, "codepoint-widths", []tmux.ArrayUpdate{
		{Index: 200, Value: "2", Unset: false},
		{Index: 100, Value: "", Unset: true},
	})
	if err != nil {
		t.Fatalf("UpdateArray failed: %v", err)
	}

	if len(updateRes.Applied) != 2 {
		t.Fatalf("expected 2 applied updates, got %v", updateRes.Applied)
	}

	// Verify index 100 was unset
	after := arrayEntries(t, ctx, server, "codepoint-widths")
	if _, ok := after[100]; ok {
		t.Fatalf("index 100 should have been unset, still found: %+v", after)
	}

	// Calling Array on a scalar option must fail with not an array error
	if _, err := server.Options().Array(ctx, "escape-time"); err == nil {
		t.Fatal("expected error calling Array on scalar option escape-time, got nil")
	}
}

func arrayEntries(t *testing.T, ctx context.Context, server *tmux.Server, name string) map[int]string {
	t.Helper()

	entries, err := server.Options().Array(ctx, name)
	if err != nil {
		t.Fatalf("failed to read array %s: %v", name, err)
	}

	values := make(map[int]string, len(entries))
	for _, e := range entries {
		values[e.Index] = e.Value
	}

	return values
}

func TestIntegrationScalarMutationFlags(t *testing.T) {
	_, session, ctx := apiFixture(t)

	// 1. Native toggle: omitting Value toggles flag options
	if err := session.Options().Set(ctx, "monitor-activity", "on"); err != nil {
		t.Fatalf("failed to set monitor-activity: %v", err)
	}

	got, err := session.Options().Get(ctx, "monitor-activity")
	assertLocal(t, "monitor-activity", got, err, "on")

	// Toggle to off via SetWith with absent Value, then back to on
	for _, want := range []string{"off", "on"} {
		if err := session.Options().SetWith(ctx, "monitor-activity", tmux.SetOptionOptions{}); err != nil {
			t.Fatalf("failed to toggle monitor-activity: %v", err)
		}

		got, err = session.Options().Get(ctx, "monitor-activity")
		assertLocal(t, "toggled monitor-activity", got, err, want)
	}

	// 2. Format expansion (-F)
	if err := session.Options().SetWith(ctx, "@test_format", tmux.SetOptionOptions{
		Value:        tmux.PresentValue("#{session_windows}"),
		ExpandFormat: true,
	}); err != nil {
		t.Fatalf("failed to set option with format expansion: %v", err)
	}

	got, err = session.Options().User(ctx, "@test_format")
	assertLocal(t, "expanded format user option", got, err, "1")

	// 3. Append (-a)
	if err := session.Options().Set(ctx, "@test_append", "hello"); err != nil {
		t.Fatalf("failed to set @test_append: %v", err)
	}

	if err := session.Options().SetWith(ctx, "@test_append", tmux.SetOptionOptions{
		Value:  tmux.PresentValue(" world"),
		Append: true,
	}); err != nil {
		t.Fatalf("failed to append to @test_append: %v", err)
	}
	// 4. OnlyIfUnset (-o): succeeds on unset option, errors and prevents overwrite on already set option
	if err := session.Options().SetWith(ctx, "@test_nounset", tmux.SetOptionOptions{
		Value:       tmux.PresentValue("first"),
		OnlyIfUnset: true,
	}); err != nil {
		t.Fatalf("failed to set unset option with OnlyIfUnset: %v", err)
	}

	got, err = session.Options().User(ctx, "@test_nounset")
	assertLocal(t, "@test_nounset", got, err, "first")

	// Attempting to set an already set option with OnlyIfUnset fails in tmux and prevents overwrite
	if err := session.Options().SetWith(ctx, "@test_nounset", tmux.SetOptionOptions{
		Value:       tmux.PresentValue("second"),
		OnlyIfUnset: true,
	}); err == nil {
		t.Fatal("expected error setting already set option with OnlyIfUnset, got nil")
	}

	got, err = session.Options().User(ctx, "@test_nounset")
	assertLocal(t, "@test_nounset after OnlyIfUnset", got, err, "first")

	// 5. Cascade unset (-U)
	if err := session.Options().UnsetWith(ctx, "monitor-activity", tmux.UnsetOptionOptions{Cascade: true}); err != nil {
		t.Fatalf("UnsetWith cascade failed: %v", err)
	}
}

func assertLocal(t *testing.T, option string, got tmux.OptionValue[string], err error, want string) {
	t.Helper()

	if v, ok := got.Local.Get(); err != nil || !ok || v != want {
		t.Fatalf("%s: got %+v, %v; want local %q", option, got, err, want)
	}
}

func TestIntegrationOptionsList(t *testing.T) {
	server, session, ctx := apiFixture(t)

	// Server options list
	serverOpts, err := server.Options().List(ctx)
	if err != nil || len(serverOpts) == 0 {
		t.Fatalf("expected server options list, got %v, err: %v", len(serverOpts), err)
	}

	// Session options with inherited (-A)
	sessionOpts, err := session.Options().ListWith(ctx, tmux.ListOptionOptions{Inherited: true})
	if err != nil || len(sessionOpts) == 0 {
		t.Fatalf("expected session options with inherited, got %v, err: %v", len(sessionOpts), err)
	}

	var hasInherited bool

	for _, opt := range sessionOpts {
		if opt.Inherited {
			hasInherited = true
			break
		}
	}

	if !hasInherited {
		t.Fatal("expected at least one inherited option with -A")
	}

	// List with hooks (-H)
	hooksList, err := server.Options().ListWith(ctx, tmux.ListOptionOptions{Hooks: true})
	if err != nil || len(hooksList) == 0 {
		t.Fatalf("expected options list with hooks, got %v, err: %v", len(hooksList), err)
	}
}

func TestIntegrationEnvironmentListPreservesMultilineValues(t *testing.T) {
	server, session, ctx := apiFixture(t)

	value := "\n    --color=hl:red\nGOTMUX_TEST_FORGED=1\n-GOTMUX_TEST_FORGED_REMOVAL\nquote \" dollar $ tick ` slash \\ end"
	if err := session.Environment().Set(ctx, "GOTMUX_TEST_MULTILINE", value); err != nil {
		t.Fatal(err)
	}

	if err := server.Environment().SetHidden(ctx, "GOTMUX_TEST_HIDDEN_MULTILINE", value); err != nil {
		t.Fatal(err)
	}

	for _, listing := range []struct {
		name   string
		scope  tmux.EnvironmentScope
		hidden bool
		entry  string
	}{
		{name: "session", scope: session.Environment(), hidden: false, entry: "GOTMUX_TEST_MULTILINE"},
		{name: "global hidden", scope: server.Environment(), hidden: true, entry: "GOTMUX_TEST_HIDDEN_MULTILINE"},
	} {
		entries, err := listing.scope.ListWith(ctx, tmux.ListEnvironmentOptions{Hidden: listing.hidden})
		if err != nil {
			t.Fatalf("%s Environment.ListWith: %v", listing.name, err)
		}

		assertEnvironmentEntries(t, listing.name, entries, map[string]tmux.EnvironmentValue{
			listing.entry: {Value: tmux.PresentValue(value), Unset: false, Hidden: listing.hidden},
		})

		// Lines of the value must not be parsed as entries of their own.
		for _, entry := range entries {
			if slices.Contains([]string{"GOTMUX_TEST_FORGED", "GOTMUX_TEST_FORGED_REMOVAL", "", "    --color"}, entry.Name) {
				t.Fatalf("%s listing parsed value continuation as entry %q", listing.name, entry.Name)
			}
		}
	}
}

func assertEnvironmentEntries(t *testing.T, listing string, entries []tmux.EnvironmentEntry, want map[string]tmux.EnvironmentValue) {
	t.Helper()

	got := map[string]tmux.EnvironmentValue{}

	for _, entry := range entries {
		if _, wanted := want[entry.Name]; wanted {
			got[entry.Name] = entry.Value
		}
	}

	for name, value := range want {
		if actual, listed := got[name]; !listed || actual != value {
			t.Errorf("%s listing: %s = %+v (listed %v), want %+v", listing, name, actual, listed, value)
		}
	}
}

func TestIntegrationEnvironmentListInheritedNames(t *testing.T) {
	server, _, ctx := apiFixture(t)

	// Exported bash functions reach tmux's environment under names like BASH_FUNC_name%%.
	command, err := tmux.NewCommand("set-environment", "-g", "BASH_FUNC_gotmux%%", "() { :; }")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := server.Run(ctx, command); err != nil {
		t.Fatal(err)
	}

	entries, err := server.Environment().List(ctx)
	if err != nil {
		t.Fatalf("Environment.List: %v", err)
	}

	for _, entry := range entries {
		if entry.Name == "BASH_FUNC_gotmux%%" {
			if v, ok := entry.Value.Value.Get(); !ok || v != "() { :; }" {
				t.Fatalf("inherited entry = %+v", entry)
			}

			return
		}
	}

	t.Fatal("listing is missing BASH_FUNC_gotmux%%")
}

func TestIntegrationEnvironmentList(t *testing.T) {
	server, session, ctx := apiFixture(t)

	// Set regular variable, empty variable, removed variable, hidden variable
	if err := session.Environment().Set(ctx, "GOTMUX_TEST_REGULAR", "val123"); err != nil {
		t.Fatalf("failed to set regular env: %v", err)
	}

	if err := session.Environment().Set(ctx, "GOTMUX_TEST_EMPTY", ""); err != nil {
		t.Fatalf("failed to set empty env: %v", err)
	}

	if err := session.Environment().Remove(ctx, "GOTMUX_TEST_REMOVED"); err != nil {
		t.Fatalf("failed to remove env: %v", err)
	}

	if err := server.Environment().SetHidden(ctx, "GOTMUX_TEST_HIDDEN", "secret"); err != nil {
		t.Fatalf("failed to set hidden env: %v", err)
	}

	entries, err := session.Environment().List(ctx)
	if err != nil {
		t.Fatalf("Environment.List failed: %v", err)
	}

	assertEnvironmentEntries(t, "session", entries, map[string]tmux.EnvironmentValue{
		"GOTMUX_TEST_REGULAR": {Value: tmux.PresentValue("val123"), Unset: false, Hidden: false},
		"GOTMUX_TEST_EMPTY":   {Value: tmux.PresentValue(""), Unset: false, Hidden: false},
		"GOTMUX_TEST_REMOVED": {Value: tmux.UnavailableValue[string](), Unset: true, Hidden: false},
	})

	hiddenEntries, err := server.Environment().ListWith(ctx, tmux.ListEnvironmentOptions{Hidden: true})
	if err != nil {
		t.Fatalf("Environment.ListWith(Hidden) failed: %v", err)
	}

	assertEnvironmentEntries(t, "hidden", hiddenEntries, map[string]tmux.EnvironmentValue{
		"GOTMUX_TEST_HIDDEN": {Value: tmux.PresentValue("secret"), Unset: false, Hidden: true},
	})
}
