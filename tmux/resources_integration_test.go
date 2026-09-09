//go:build integration

package tmux_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	tmux "github.com/zigai/gotmux/tmux"
)

func assertEnvironment(t *testing.T, ctx context.Context, scope tmux.EnvironmentScope, name string, hidden bool, value string, present, removed bool) {
	t.Helper()

	got, err := scope.Get(ctx, name, hidden)
	if err != nil {
		t.Fatal(err)
	}

	actual, ok := got.Value.Get()
	if actual != value || ok != present || got.Unset != removed || got.Hidden != hidden {
		t.Fatalf("%s: got %+v (%q,%v), want value %q present=%v removed=%v hidden=%v", name, got, actual, ok, value, present, removed, hidden)
	}

	if !present && got.Value.State() != tmux.Unavailable {
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
