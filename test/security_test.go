//go:build integration

package test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zigai/gotmux/tmux"
)

func TestSecurityFormatInjection(t *testing.T) {
	server, baseSession, ctx := apiFixture(t)

	// 1. A session name holding a format stays literal, not the expanded pane ID.
	sessName := "test-#{pane_id}-name"

	session, err := server.NewSession(ctx, tmux.NewSessionOptions{
		Name: sessName,
	})
	if err != nil {
		t.Fatalf("failed to create session with format string in name: %v", err)
	}

	t.Cleanup(func() {
		_ = session.Kill(ctx)
	})

	if got := sessionName(t, ctx, session); got != sessName {
		t.Fatalf("session name expanded format string: got %q, want literal %q", got, sessName)
	}

	// 2. A window name holding a run-shell format stays literal and runs nothing.
	vulnMarker := filepath.Join(t.TempDir(), "vuln")
	winName := "win-#{run-shell:touch " + vulnMarker + "}"

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{
		Name: winName,
	})
	if err != nil {
		t.Fatalf("failed to create window with command execution format string: %v", err)
	}

	if got := windowName(t, ctx, link.Window()); got != winName {
		t.Fatalf("window name expanded format string: got %q, want literal %q", got, winName)
	}

	assertNotCreated(t, vulnMarker, "window name")

	// 3. User option values holding a conditional and a run-shell format read back literally.
	vulnOptMarker := filepath.Join(t.TempDir(), "vuln-opt")
	for _, value := range []string{"#{?#{==:1,1},yes,no}", "#{run-shell:touch " + vulnOptMarker + "}"} {
		if err := session.Options().Set(ctx, "@my_opt", value); err != nil {
			t.Fatalf("failed to set user option @my_opt to %q: %v", value, err)
		}

		got, err := session.Options().Get(ctx, "@my_opt")
		assertLocal(t, "@my_opt", got, err, value)
	}

	assertNotCreated(t, vulnOptMarker, "option value")

	// 4. A buffer with formats in its name and content keeps both literal.
	assertLiteralBuffer(t, ctx, server, "buf-#{pane_id}", []byte("#{pane_id}:literal-content-#{run-shell:echo evil}"))

	// 5. Guarded mutations and queries keep working on objects with '#' in their names.
	assertHashNamedWindowUsable(t, ctx, session, link)

	if _, err := baseSession.Info(ctx); err != nil {
		t.Fatalf("base fixture session should remain valid: %v", err)
	}
}

func assertLiteralBuffer(t *testing.T, ctx context.Context, server *tmux.Server, name string, content []byte) {
	t.Helper()

	bufRef, err := tmux.NamedBuffer(name)
	if err != nil {
		t.Fatalf("NamedBuffer failed for %q: %v", name, err)
	}

	if err := server.WriteBuffer(ctx, bufRef, content); err != nil {
		t.Fatalf("server.WriteBuffer failed: %v", err)
	}

	t.Cleanup(func() {
		_ = server.DeleteBuffer(ctx, bufRef)
	})

	if readContent := bufferContent(t, ctx, server, name); !bytes.Equal(readContent, content) {
		t.Fatalf("server.ReadBuffer content mismatch: got %q, want %q", readContent, content)
	}

	buffers, err := server.Buffers(ctx)
	if err != nil || !slices.ContainsFunc(buffers, func(b tmux.BufferInfo) bool { return b.Name == name }) {
		t.Fatalf("buffer %q was not found by its literal name in server.Buffers (got %+v, %v)", name, buffers, err)
	}
}

func assertHashNamedWindowUsable(t *testing.T, ctx context.Context, session tmux.Session, link tmux.WindowLink) {
	t.Helper()

	renamedName := "win-#{pane_id}-renamed"
	if err := link.Window().Rename(ctx, renamedName); err != nil {
		t.Fatalf("Window.Rename failed with '#' in target name: %v", err)
	}

	if got := windowName(t, ctx, link.Window()); got != renamedName {
		t.Fatalf("Window.Info after rename: got %q, want literal %q", got, renamedName)
	}

	if err := link.Select(ctx); err != nil {
		t.Fatalf("link.Select failed on window with '#' in name: %v", err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatalf("ActivePane failed: %v", err)
	}

	setAndReadTitle(t, ctx, pane, "title-#{pane_id}-safe")

	link2, err := session.NewWindow(ctx, tmux.NewWindowOptions{
		Name: "win2-#{session_name}-test",
	})
	if err != nil {
		t.Fatalf("session.NewWindow for second window failed: %v", err)
	}

	runSteps(t, []namedStep{
		{"link2.Select", func() error { return link2.Select(ctx) }},
		{"link.Select back to first window", func() error { return link.Select(ctx) }},
	})
}

func sessionName(t *testing.T, ctx context.Context, session tmux.Session) string {
	t.Helper()

	info, err := session.Info(ctx)
	if err != nil {
		t.Fatalf("session.Info failed: %v", err)
	}

	return info.Name
}

func windowName(t *testing.T, ctx context.Context, window tmux.Window) string {
	t.Helper()

	info, err := window.Info(ctx)
	if err != nil {
		t.Fatalf("window.Info failed: %v", err)
	}

	return info.Name
}

func assertNotCreated(t *testing.T, marker, where string) {
	t.Helper()

	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tmux ran a command injected through a %s: %s exists", where, marker)
	}
}

// hostileValue combines a separator, formats, #(), both quotes, a backslash, and non-ASCII.
func hostileValue(marker string) string {
	return `a;b #{pane_id} #(touch ` + marker + `) "q' \ é`
}

func TestSecurityRenamesAndCreationNamesStayLiteral(t *testing.T) {
	server, session, ctx := apiFixture(t)
	marker := filepath.Join(t.TempDir(), "expanded")
	// Names cannot hold a backslash, which tmux escapes (Literal C6), and session
	// names cannot hold ":" or ".", which tmux replaces; window names keep both.
	literalSession := `a;b #{pane_id} #(touch ` + marker + `) "q' é`
	window := literalSession + ` :.`

	created, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "created", Window: window})
	if err != nil {
		t.Fatal(err)
	}

	links, err := created.Windows(ctx)
	if err != nil || len(links) != 1 {
		t.Fatalf("NewSession windows: %d, %v", len(links), err)
	}

	if err := session.Rename(ctx, literalSession); err != nil {
		t.Fatal(err)
	}

	added, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: window})
	if err != nil {
		t.Fatal(err)
	}

	renamed, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "plain"})
	if err != nil {
		t.Fatal(err)
	}

	if err := renamed.Window().Rename(ctx, window); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		want string
		got  string
	}{
		{"NewSession window name", window, windowName(t, ctx, links[0].Window())},
		{"Session.Rename", literalSession, sessionName(t, ctx, session)},
		{"NewWindow name", window, windowName(t, ctx, added.Window())},
		{"Window.Rename", window, windowName(t, ctx, renamed.Window())},
	} {
		if tc.got != tc.want {
			t.Errorf("%s stored %q, want %q byte for byte", tc.name, tc.got, tc.want)
		}
	}

	assertNotCreated(t, marker, "name")
}

func TestSecurityShellProgramReachesTheShellUnchanged(t *testing.T) {
	_, session, ctx := apiFixture(t)
	out := filepath.Join(t.TempDir(), "out")
	script := `V='a "b" $c'; printf '%s' "$V;#{pane_id};$((1+1))" > '` + out + `'`

	if _, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "shell", Program: tmux.Shell(script)}); err != nil {
		t.Fatal(err)
	}

	var got []byte

	awaitObservation(t, ctx, "the shell script's output file", func() bool {
		data, err := os.ReadFile(out)
		got = data

		return err == nil && len(data) > 0
	})

	if want := `a "b" $c;#{pane_id};2`; string(got) != want {
		t.Fatalf("script wrote %q, want %q", got, want)
	}
}
