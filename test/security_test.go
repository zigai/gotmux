//go:build integration

package tmux_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	tmux "github.com/zigai/gotmux/tmux"
)

func TestSecurityFormatInjection(t *testing.T) {
	server, baseSession, ctx := apiFixture(t)

	// 1. Creating session with format string in name: name := "test-#{pane_id}-name".
	// Verify session.Info(ctx).Name returns literal "test-#{pane_id}-name", NOT expanded pane ID.
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

	sessInfo, err := session.Info(ctx)
	if err != nil {
		t.Fatalf("session.Info failed: %v", err)
	}
	if sessInfo.Name != sessName {
		t.Fatalf("session name expanded format string: got %q, want literal %q", sessInfo.Name, sessName)
	}

	// 2. Creating window with command execution in name:
	// winName := "win-#{run-shell:touch " + filepath.Join(t.TempDir(), "vuln") + "}"
	// Verify window name is literal and marker file is NOT created.
	vulnMarker := filepath.Join(t.TempDir(), "vuln")
	winName := "win-#{run-shell:touch " + vulnMarker + "}"
	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{
		Name: winName,
	})
	if err != nil {
		t.Fatalf("failed to create window with command execution format string: %v", err)
	}

	winInfo, err := link.Window().Info(ctx)
	if err != nil {
		t.Fatalf("window.Info failed: %v", err)
	}
	if winInfo.Name != winName {
		t.Fatalf("window name expanded format string: got %q, want literal %q", winInfo.Name, winName)
	}

	if _, err := os.Stat(vulnMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command injection in window name executed! marker file exists at %s", vulnMarker)
	}

	// 3. Setting user option @my_opt to #{?#{==:1,1},yes,no} and #{run-shell:touch ...}.
	// Verify reading it back via options.Get(ctx, "@my_opt") yields the exact literal expression.
	options := session.Options()
	optCond := "#{?#{==:1,1},yes,no}"
	if err := options.Set(ctx, "@my_opt", optCond); err != nil {
		t.Fatalf("failed to set user option @my_opt to conditional format: %v", err)
	}

	val, err := options.Get(ctx, "@my_opt")
	if err != nil {
		t.Fatalf("failed to get user option @my_opt: %v", err)
	}
	gotOpt, ok := val.Local.Get()
	if !ok || gotOpt != optCond {
		t.Fatalf("user option @my_opt format expanded: got %q (ok=%v), want literal %q", gotOpt, ok, optCond)
	}

	vulnOptMarker := filepath.Join(t.TempDir(), "vuln-opt")
	optRun := "#{run-shell:touch " + vulnOptMarker + "}"
	if err := options.Set(ctx, "@my_opt", optRun); err != nil {
		t.Fatalf("failed to set user option @my_opt to run-shell format: %v", err)
	}

	val, err = options.Get(ctx, "@my_opt")
	if err != nil {
		t.Fatalf("failed to get user option @my_opt after run-shell set: %v", err)
	}
	gotOpt, ok = val.Local.Get()
	if !ok || gotOpt != optRun {
		t.Fatalf("user option @my_opt format expanded: got %q (ok=%v), want literal %q", gotOpt, ok, optRun)
	}

	if _, err := os.Stat(vulnOptMarker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command injection in option value executed! marker file exists at %s", vulnOptMarker)
	}

	// 4. Creating buffer with format strings in name and content. Verify buffer name is literal.
	bufName := "buf-#{pane_id}"
	bufRef, err := tmux.NamedBuffer(bufName)
	if err != nil {
		t.Fatalf("NamedBuffer failed for %q: %v", bufName, err)
	}
	bufContent := []byte("#{pane_id}:literal-content-#{run-shell:echo evil}")
	if err := server.WriteBuffer(ctx, bufRef, bufContent); err != nil {
		t.Fatalf("server.WriteBuffer failed: %v", err)
	}
	t.Cleanup(func() {
		_ = server.DeleteBuffer(ctx, bufRef)
	})

	readContent, err := server.ReadBuffer(ctx, bufRef)
	if err != nil {
		t.Fatalf("server.ReadBuffer failed: %v", err)
	}
	if !bytes.Equal(readContent, bufContent) {
		t.Fatalf("server.ReadBuffer content mismatch: got %q, want %q", readContent, bufContent)
	}

	buffers, err := server.Buffers(ctx)
	if err != nil {
		t.Fatalf("server.Buffers failed: %v", err)
	}
	foundBuf := false
	for _, b := range buffers {
		if b.Name == bufName {
			foundBuf = true
			break
		}
	}
	if !foundBuf {
		t.Fatalf("buffer %q was not found by its literal name in server.Buffers (got %+v)", bufName, buffers)
	}

	// 5. Verify guard execution remains valid and safe even when objects have # in their names.
	// We perform mutations and queries using handles associated with objects containing # in their names.
	// Window Rename using format string in the new name:
	renamedName := "win-#{pane_id}-renamed"
	if err := link.Window().Rename(ctx, renamedName); err != nil {
		t.Fatalf("Window.Rename failed with '#' in target name: %v", err)
	}

	renamedInfo, err := link.Window().Info(ctx)
	if err != nil {
		t.Fatalf("Window.Info after rename failed: %v", err)
	}
	if renamedInfo.Name != renamedName {
		t.Fatalf("Window.Info after rename: got %q, want literal %q", renamedInfo.Name, renamedName)
	}

	// Link operations assert link guards:
	if err := link.Select(ctx); err != nil {
		t.Fatalf("link.Select failed on window with '#' in name: %v", err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatalf("ActivePane failed: %v", err)
	}

	paneTitle := "title-#{pane_id}-safe"
	if err := pane.SetTitle(ctx, paneTitle); err != nil {
		t.Fatalf("pane.SetTitle failed: %v", err)
	}

	paneInfo, err := pane.Info(ctx)
	if err != nil {
		t.Fatalf("pane.Info failed: %v", err)
	}
	if paneInfo.Title != paneTitle {
		t.Fatalf("pane title expanded format: got %q, want %q", paneInfo.Title, paneTitle)
	}

	// Create a second window in this session to verify multiple links in session with '#' in name.
	win2Name := "win2-#{session_name}-test"
	link2, err := session.NewWindow(ctx, tmux.NewWindowOptions{
		Name: win2Name,
	})
	if err != nil {
		t.Fatalf("session.NewWindow for second window failed: %v", err)
	}

	if err := link2.Select(ctx); err != nil {
		t.Fatalf("link2.Select failed: %v", err)
	}

	if err := link.Select(ctx); err != nil {
		t.Fatalf("link.Select failed back to first window: %v", err)
	}

	// Ensure base session is unharmed
	if _, err := baseSession.Info(ctx); err != nil {
		t.Fatalf("base fixture session should remain valid: %v", err)
	}
}
