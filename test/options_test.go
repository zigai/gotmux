//go:build integration

package tmux_test

import (
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
	if err != nil || key.Local.State() != tmux.Unavailable {
		t.Fatalf("unset key: %+v, %v", key, err)
	}
}

func TestIntegrationArrayOptions_ArbitraryAndSparse(t *testing.T) {
	server, _, ctx := apiFixture(t)

	// Set sparse array entries via indexed Set
	if err := server.Options().Set(ctx, "codepoint-widths[100]", "2"); err != nil {
		t.Fatalf("failed to set sparse array entry: %v", err)
	}
	if err := server.Options().Set(ctx, "codepoint-widths[500]", "1"); err != nil {
		t.Fatalf("failed to set sparse array entry: %v", err)
	}

	// Read single entry via indexed Get
	val100, err := server.Options().Get(ctx, "codepoint-widths[100]")
	if v, ok := val100.Effective.Get(); err != nil || !ok || v != "2" {
		t.Fatalf("unexpected value for codepoint-widths[100]: %+v, err: %v", val100, err)
	}
	// Read full sparse array via Array
	entries, err := server.Options().Array(ctx, "codepoint-widths")
	if err != nil {
		t.Fatalf("failed to read array: %v", err)
	}

	var found100, found500 bool
	for _, e := range entries {
		if e.Index == 100 && e.Value == "2" {
			found100 = true
		}
		if e.Index == 500 && e.Value == "1" {
			found500 = true
		}
	}
	if !found100 || !found500 {
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

	// Verify index 100 was unset and index 200 was set
	entriesAfter, err := server.Options().Array(ctx, "codepoint-widths")
	if err != nil {
		t.Fatalf("failed to read array after update: %v", err)
	}
	for _, e := range entriesAfter {
		if e.Index == 100 {
			t.Fatalf("index 100 should have been unset, still found: %+v", e)
		}
	}

	// Calling Array on a scalar option must fail with not an array error
	if _, err := server.Options().Array(ctx, "escape-time"); err == nil {
		t.Fatal("expected error calling Array on scalar option escape-time, got nil")
	}
}

func TestIntegrationScalarMutationFlags(t *testing.T) {
	_, session, ctx := apiFixture(t)

	// 1. Native toggle: omitting Value toggles flag options
	if err := session.Options().Set(ctx, "monitor-activity", "on"); err != nil {
		t.Fatalf("failed to set monitor-activity: %v", err)
	}
	got1, err := session.Options().Get(ctx, "monitor-activity")
	if v, ok := got1.Local.Get(); err != nil || !ok || v != "on" {
		t.Fatalf("expected on, got %+v, %v", got1, err)
	}

	// Toggle to off via SetWith with absent Value
	if err := session.Options().SetWith(ctx, "monitor-activity", tmux.SetOptionOptions{}); err != nil {
		t.Fatalf("failed to toggle monitor-activity: %v", err)
	}
	got2, err := session.Options().Get(ctx, "monitor-activity")
	if v, ok := got2.Local.Get(); err != nil || !ok || v != "off" {
		t.Fatalf("expected off after toggle, got %+v, %v", got2, err)
	}

	// Toggle back to on
	if err := session.Options().SetWith(ctx, "monitor-activity", tmux.SetOptionOptions{}); err != nil {
		t.Fatalf("failed to toggle monitor-activity back: %v", err)
	}
	got3, err := session.Options().Get(ctx, "monitor-activity")
	if v, ok := got3.Local.Get(); err != nil || !ok || v != "on" {
		t.Fatalf("expected on after second toggle, got %+v, %v", got3, err)
	}

	// 2. Format expansion (-F)
	if err := session.Options().SetWith(ctx, "@test_format", tmux.SetOptionOptions{
		Value:        tmux.PresentValue("#{session_windows}"),
		ExpandFormat: true,
	}); err != nil {
		t.Fatalf("failed to set option with format expansion: %v", err)
	}
	gotFmt, err := session.Options().User(ctx, "@test_format")
	if v, ok := gotFmt.Local.Get(); err != nil || !ok || v != "1" {
		t.Fatalf("expected expanded format user option '1', got %+v, %v", gotFmt, err)
	}

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
	gotFirst, err := session.Options().User(ctx, "@test_nounset")
	if v, ok := gotFirst.Local.Get(); err != nil || !ok || v != "first" {
		t.Fatalf("expected 'first', got %+v, %v", gotFirst, err)
	}

	// Attempting to set an already set option with OnlyIfUnset fails in tmux and prevents overwrite
	if err := session.Options().SetWith(ctx, "@test_nounset", tmux.SetOptionOptions{
		Value:       tmux.PresentValue("second"),
		OnlyIfUnset: true,
	}); err == nil {
		t.Fatal("expected error setting already set option with OnlyIfUnset, got nil")
	}
	gotNoOver, err := session.Options().User(ctx, "@test_nounset")
	if v, ok := gotNoOver.Local.Get(); err != nil || !ok || v != "first" {
		t.Fatalf("value was overwritten despite OnlyIfUnset: %+v, %v", gotNoOver, err)
	}

	// 5. Cascade unset (-U)
	if err := session.Options().UnsetWith(ctx, "monitor-activity", tmux.UnsetOptionOptions{Cascade: true}); err != nil {
		t.Fatalf("UnsetWith cascade failed: %v", err)
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

		found := false

		for _, entry := range entries {
			switch entry.Name {
			case listing.entry:
				found = true

				if v, ok := entry.Value.Value.Get(); !ok || v != value || entry.Value.Unset || entry.Value.Hidden != listing.hidden {
					t.Fatalf("%s multiline entry = %+v, want value %q", listing.name, entry, value)
				}
			case "GOTMUX_TEST_FORGED", "GOTMUX_TEST_FORGED_REMOVAL", "", "    --color":
				t.Fatalf("%s listing parsed value continuation as entry %q", listing.name, entry.Name)
			}
		}

		if !found {
			t.Fatalf("%s listing is missing %s", listing.name, listing.entry)
		}
	}
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

	// List standard environment
	entries, err := session.Environment().List(ctx)
	if err != nil {
		t.Fatalf("Environment.List failed: %v", err)
	}

	var foundReg, foundEmpty, foundRemoved bool
	for _, e := range entries {
		switch e.Name {
		case "GOTMUX_TEST_REGULAR":
			foundReg = true
			if v, ok := e.Value.Value.Get(); e.Value.Unset || !ok || v != "val123" {
				t.Fatalf("unexpected regular env: %+v", e)
			}
		case "GOTMUX_TEST_EMPTY":
			foundEmpty = true
			if v, ok := e.Value.Value.Get(); e.Value.Unset || !ok || v != "" {
				t.Fatalf("unexpected empty env: %+v", e)
			}
		case "GOTMUX_TEST_REMOVED":
			foundRemoved = true
			if !e.Value.Unset || e.Value.Value.State() != tmux.Unavailable {
				t.Fatalf("unexpected removed env: %+v", e)
			}
		}
	}

	if !foundReg || !foundEmpty || !foundRemoved {
		t.Fatalf("missing expected entries in session environment: reg=%v, empty=%v, removed=%v",
			foundReg, foundEmpty, foundRemoved)
	}

	// List hidden environment
	hiddenEntries, err := server.Environment().ListWith(ctx, tmux.ListEnvironmentOptions{Hidden: true})
	if err != nil {
		t.Fatalf("Environment.ListWith(Hidden) failed: %v", err)
	}

	var foundHidden bool
	for _, e := range hiddenEntries {
		if e.Name == "GOTMUX_TEST_HIDDEN" {
			foundHidden = true
			if v, ok := e.Value.Value.Get(); !e.Value.Hidden || !ok || v != "secret" {
				t.Fatalf("unexpected hidden env entry: %+v", e)
			}
		}
	}
	if !foundHidden {
		t.Fatal("expected GOTMUX_TEST_HIDDEN in hidden environment list")
	}
}
