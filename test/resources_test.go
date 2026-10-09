//go:build integration

package test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/gotmux/tmux"
)

func assertEnvironment(t *testing.T, ctx context.Context, scope tmux.EnvironmentScope, name string, hidden bool, value string, present, removed bool) {
	t.Helper()

	got, err := scope.GetWith(ctx, name, tmux.GetEnvironmentOptions{Hidden: hidden})
	if err != nil {
		t.Fatal(err)
	}

	actual, ok := got.Value.Get()
	if actual != value || ok != present || got.Unset != removed || got.Hidden != hidden {
		t.Fatalf("%s: got %+v (%q,%v), want value %q present=%v removed=%v hidden=%v", name, got, actual, ok, value, present, removed, hidden)
	}

	if !present && got.Value.State() != tmux.ValueStateUnavailable {
		t.Fatalf("%s: missing state %v", name, got.Value.State())
	}
}

func TestIntegrationResourcesEnvironment(t *testing.T) {
	server, session, ctx := apiFixture(t)

	var create tmux.NewSessionOptions

	create.Name = "other"

	other, err := server.NewSession(ctx, create)
	if err != nil {
		t.Fatal(err)
	}

	const name = "TGO_RESOURCE_VALUE"

	global := server.Environment()
	local := session.Environment()

	if err := global.Set(ctx, name, "global"); err != nil {
		t.Fatal(err)
	}

	if err := other.Environment().Set(ctx, name, "sentinel"); err != nil {
		t.Fatal(err)
	}

	assertEnvironment(t, ctx, local, name, false, "", false, false)

	for _, value := range []string{"local\n'\";$()#{pane_id}", ""} {
		if err := local.Set(ctx, name, value); err != nil {
			t.Fatal(err)
		}

		assertEnvironment(t, ctx, local, name, false, value, true, false)
		assertEnvironment(t, ctx, global, name, false, "global", true, false)
		assertEnvironment(t, ctx, other.Environment(), name, false, "sentinel", true, false)
	}

	if err := local.Remove(ctx, name); err != nil {
		t.Fatal(err)
	}

	assertEnvironment(t, ctx, local, name, false, "", false, true)

	const hidden = "TGO_RESOURCE_HIDDEN"
	if err := local.SetHidden(ctx, hidden, "private"); err != nil {
		t.Fatal(err)
	}

	assertEnvironment(t, ctx, local, hidden, true, "private", true, false)
	assertEnvironment(t, ctx, local, hidden, false, "", false, false)
	assertChildEnvironment(t, ctx, session, "absent|absent")

	if err := local.Unset(ctx, name); err != nil {
		t.Fatal(err)
	}

	assertEnvironment(t, ctx, local, name, false, "", false, false)
	assertChildEnvironment(t, ctx, session, "global|absent")
	assertEnvironment(t, ctx, other.Environment(), name, false, "sentinel", true, false)
	assertEnvironment(t, ctx, global, name, false, "global", true, false)
}

func assertChildEnvironment(t *testing.T, ctx context.Context, session tmux.Session, want string) {
	t.Helper()
	dir := t.TempDir()
	script := `printf '%s|%s' "${TGO_RESOURCE_VALUE-absent}" "${TGO_RESOURCE_HIDDEN-absent}" > "$1/pending"
mv "$1/pending" "$1/result"
IFS= read -r hold`

	var options tmux.NewWindowOptions

	options.Program = tmux.Exec("/bin/sh", "-c", script, "environment", dir)
	if _, err := session.NewWindow(ctx, options); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "result")

	awaitObservation(t, ctx, "child environment", func() bool { _, err := os.Stat(path); return err == nil })

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, []byte(want)) {
		t.Fatalf("child environment = %q, want %q", got, want)
	}
}

func TestIntegrationResourcesArray(t *testing.T) {
	_, session, ctx := apiFixture(t)
	options := session.Options()

	const name = "update-environment"

	before, err := options.Array(ctx, name)
	if err != nil {
		t.Fatal(err)
	}

	updates := make([]tmux.ArrayUpdate, 0, len(before))
	for _, entry := range before {
		updates = append(updates, tmux.ArrayUpdate{Index: entry.Index, Value: "", Unset: true})
	}

	if _, err := options.UpdateArray(ctx, name, updates); err != nil {
		t.Fatal(err)
	}

	const literal = "a b;'\"\\#{pane_id}"

	updates = []tmux.ArrayUpdate{{Index: 2, Value: literal, Unset: false}, {Index: 7, Value: "second", Unset: false}}

	result, err := options.UpdateArray(ctx, name, updates)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]int{0, 1}, result.Applied); diff != "" {
		t.Fatal(diff)
	}

	assertArray(t, ctx, options, name, []tmux.ArrayEntry{{Index: 2, Value: literal}, {Index: 7, Value: "second"}})

	updates = []tmux.ArrayUpdate{{Index: 2, Value: "replacement", Unset: false}, {Index: 7, Value: "", Unset: true}}
	if _, err := options.UpdateArray(ctx, name, updates); err != nil {
		t.Fatal(err)
	}

	want := []tmux.ArrayEntry{{Index: 2, Value: "replacement"}}
	assertArray(t, ctx, options, name, want)

	updates = []tmux.ArrayUpdate{{Index: 2, Value: "must not apply", Unset: false}, {Index: -1, Value: "invalid", Unset: false}}

	result, err = options.UpdateArray(ctx, name, updates)
	if !errors.Is(err, tmux.ErrInvalidArgument) || len(result.Applied) != 0 {
		t.Fatalf("invalid batch: %+v, %v", result, err)
	}

	assertArray(t, ctx, options, name, want)
}

func TestIntegrationOptionReadsIgnoreUnrelatedValues(t *testing.T) {
	server, session, ctx := apiFixture(t)
	link, pane := graphWindow(t, ctx, session)

	largeValue := strings.Repeat("x", 8192)

	settings := []struct {
		set   func(context.Context, string, string) error
		name  string
		value string
	}{
		{set: server.Options().Set, name: "escape-time", value: "25"},
		{set: server.Options().SetUser, name: "@unrelated", value: largeValue},
		{set: pane.Options().SetUser, name: "@unrelated", value: largeValue},
		{set: session.Options().Set, name: "status-left", value: "local"},
		{set: link.Window().Options().Set, name: "monitor-activity", value: "on"},
		{set: pane.Options().Set, name: "remain-on-exit", value: "on"},
	}
	for _, setting := range settings {
		if err := setting.set(ctx, setting.name, setting.value); err != nil {
			t.Fatal(err)
		}
	}

	limited, err := tmux.New(tmux.Config{
		Binary: os.Getenv("TMUX_TEST_BINARY"), SocketPath: server.Endpoint().SocketPath,
		ConfigFile: "/dev/null", Limits: tmux.Limits{OutputBytes: 1024},
	})
	if err != nil {
		t.Fatal(err)
	}

	limitedPane, err := limited.PaneHandle(pane.ID())
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		value string
		want  tmux.Scope
	}{
		{name: "escape-time", value: "25", want: tmux.ScopeServer},
		{name: "status-left", value: "local", want: tmux.ScopeSession},
		{name: "monitor-activity", value: "on", want: tmux.ScopeWindow},
		{name: "remain-on-exit", value: "on", want: tmux.ScopePane},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := limitedPane.Options().Get(ctx, test.name)
			if err != nil {
				t.Fatal(err)
			}

			if origin, ok := got.Origin.Get(); !ok || origin != test.want {
				t.Fatalf("origin = %v, present=%v, want %v", origin, ok, test.want)
			}

			if value, ok := got.Effective.Get(); !ok || value != test.value {
				t.Fatalf("effective value = %q, present=%v, want %q", value, ok, test.value)
			}
		})
	}
}

func TestIntegrationOptionOriginUsesNativeScope(t *testing.T) {
	server, session, ctx := apiFixture(t)

	cases := []struct {
		name    string
		options tmux.SessionOptions
		option  string
		value   string
		want    tmux.Scope
	}{
		{name: "window through session", options: session.Options(), option: "monitor-activity", value: "on", want: tmux.ScopeWindow},
		{name: "global window through global session", options: server.GlobalSessionOptions(), option: "monitor-activity", value: "on", want: tmux.ScopeGlobalWindow},
		{name: "server through session", options: session.Options(), option: "escape-time", value: "25", want: tmux.ScopeServer},
		{name: "session", options: session.Options(), option: "status-left", value: "local", want: tmux.ScopeSession},
		{name: "user", options: session.Options(), option: "@origin", value: "local", want: tmux.ScopeSession},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := test.options.Set(ctx, test.option, test.value); err != nil {
				t.Fatal(err)
			}

			got, err := test.options.Get(ctx, test.option)
			if err != nil {
				t.Fatal(err)
			}

			if origin, ok := got.Origin.Get(); !ok || origin != test.want {
				t.Fatalf("%s origin = %v, present=%v, want %v", test.option, origin, ok, test.want)
			}
		})
	}
}

func TestIntegrationOptionOriginRequiresLocalValue(t *testing.T) {
	_, session, ctx := apiFixture(t)
	_, pane := graphWindow(t, ctx, session)

	if err := pane.Options().Set(ctx, "status-left", "from pane"); err != nil {
		t.Fatal(err)
	}

	got, err := pane.Options().Get(ctx, "status-left")
	if origin, ok := got.Origin.Get(); err != nil || !ok || origin != tmux.ScopeSession {
		t.Fatalf("session option through pane: %+v, %v", got, err)
	}

	if err := session.Options().Unset(ctx, "status-left"); err != nil {
		t.Fatal(err)
	}

	got, err = session.Options().Get(ctx, "status-left")
	if err != nil || got.Local.State() != tmux.ValueStateUnavailable || got.Origin.State() != tmux.ValueStateUnavailable {
		t.Fatalf("inherited option origin must stay unproven: %+v, %v", got, err)
	}
}

func TestIntegrationPaneOptionOriginOverridesWindow(t *testing.T) {
	_, session, ctx := apiFixture(t)

	link, pane := graphWindow(t, ctx, session)
	if err := link.Window().Options().Set(ctx, "remain-on-exit", "off"); err != nil {
		t.Fatal(err)
	}

	if err := pane.Options().Set(ctx, "remain-on-exit", "on"); err != nil {
		t.Fatal(err)
	}

	got, err := pane.Options().Get(ctx, "remain-on-exit")
	origin, present := got.Origin.Get()

	value, effective := got.Effective.Get()
	if err != nil || !present || origin != tmux.ScopePane || !effective || value != "on" {
		t.Fatalf("pane override origin: %+v, %v", got, err)
	}
}

func TestIntegrationArrayIncludesInheritedDefaults(t *testing.T) {
	server, session, ctx := apiFixture(t)
	for _, name := range []string{"status-format", "update-environment"} {
		t.Run(name, func(t *testing.T) {
			defaults, err := server.GlobalSessionOptions().Array(ctx, name)
			if err != nil || len(defaults) == 0 {
				t.Fatalf("global defaults: %+v, %v", defaults, err)
			}

			assertArray(t, ctx, session.Options(), name, defaults)

			updates := []tmux.ArrayUpdate{{Index: defaults[0].Index, Value: "custom", Unset: false}}
			if _, err := server.GlobalSessionOptions().UpdateArray(ctx, name, updates); err != nil {
				t.Fatal(err)
			}

			defaults[0].Value = "custom"
			assertArray(t, ctx, session.Options(), name, defaults)
		})
	}
}

func assertArray(t *testing.T, ctx context.Context, options tmux.SessionOptions, name string, want []tmux.ArrayEntry) {
	t.Helper()

	got, err := options.Array(ctx, name)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("%s (-want +got):\n%s", name, diff)
	}
}
