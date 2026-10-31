//go:build integration

package test

import (
	"bytes"
	"context"
	"testing"

	"github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

func assertWindowZoomed(t *testing.T, ctx context.Context, w tmux.Window, expected bool) {
	t.Helper()

	info, err := w.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if info.Zoomed != expected {
		t.Fatalf("expected window zoomed=%v, got %v", expected, info.Zoomed)
	}
}

func assertPaneFocus(t *testing.T, ctx context.Context, active tmux.Pane, inactive tmux.Pane) {
	t.Helper()

	activeInfo, err := active.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	inactiveInfo, err := inactive.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !activeInfo.Active {
		t.Fatal("expected pane to be active")
	}

	if inactiveInfo.Active {
		t.Fatal("expected pane to be inactive")
	}
}

func assertWindowLinkFocus(
	t *testing.T,
	ctx context.Context,
	active tmux.WindowLink,
	inactive tmux.WindowLink,
) {
	t.Helper()

	activeInfo, err := active.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	inactiveInfo, err := inactive.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !activeInfo.Active {
		t.Fatal("expected window link to be active")
	}

	if inactiveInfo.Active {
		t.Fatal("expected window link to be inactive")
	}
}

func windowLayout(t *testing.T, ctx context.Context, w tmux.Window) tmux.Layout {
	t.Helper()

	info, err := w.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	return info.Layout
}

func assertPaneInputOff(t *testing.T, ctx context.Context, p tmux.Pane, expected string) {
	t.Helper()

	out, err := p.Format(ctx, tmux.Format("#{pane_input_off}"))
	if err != nil {
		t.Fatal(err)
	}

	if got := string(bytes.TrimSpace(out)); got != expected {
		t.Fatalf("expected #{pane_input_off} to be %q, got %q", expected, got)
	}
}

func panePID(t *testing.T, ctx context.Context, p tmux.Pane) int {
	t.Helper()

	info, err := p.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	return info.PID
}

func windowActivePanePID(t *testing.T, ctx context.Context, w tmux.Window) int {
	t.Helper()

	pane, err := w.ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	return panePID(t, ctx, pane)
}

func assertWindowPaneCount(t *testing.T, ctx context.Context, w tmux.Window, expected int) {
	t.Helper()

	info, err := w.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if info.PaneCount != expected {
		t.Fatalf("expected window pane count %d, got %d", expected, info.PaneCount)
	}
}

func testOperationsToggleZoom(t *testing.T, ctx context.Context, session tmux.Session) {
	t.Helper()

	var winOpts tmux.NewWindowOptions

	winOpts.Name = "op-zoom"
	winOpts.Program = tmux.Exec("/bin/sh")

	link, err := session.NewWindow(ctx, winOpts)
	if err != nil {
		t.Fatal(err)
	}

	window := link.Window()

	p1, err := window.ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var splitOpts tmux.SplitOptions

	splitOpts.Direction = tmux.Horizontal
	splitOpts.Program = tmux.Exec("/bin/sh")

	if _, err = p1.Split(ctx, splitOpts); err != nil {
		t.Fatal(err)
	}

	assertWindowZoomed(t, ctx, window, false)

	if err := p1.ToggleZoom(ctx); err != nil {
		t.Fatal(err)
	}

	assertWindowZoomed(t, ctx, window, true)

	if err := p1.ToggleZoom(ctx); err != nil {
		t.Fatal(err)
	}

	assertWindowZoomed(t, ctx, window, false)
}

func testOperationsPaneSelectAndLastPane(t *testing.T, ctx context.Context, session tmux.Session) {
	t.Helper()

	var winOpts tmux.NewWindowOptions

	winOpts.Name = "op-panes"
	winOpts.Program = tmux.Exec("/bin/sh")

	link, err := session.NewWindow(ctx, winOpts)
	if err != nil {
		t.Fatal(err)
	}

	window := link.Window()

	p1, err := window.ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var splitOpts tmux.SplitOptions

	splitOpts.Direction = tmux.Horizontal
	splitOpts.Program = tmux.Exec("/bin/sh")

	p2, err := p1.Split(ctx, splitOpts)
	if err != nil {
		t.Fatal(err)
	}

	if err := p1.Select(ctx); err != nil {
		t.Fatal(err)
	}

	assertPaneFocus(t, ctx, p1, p2)

	if err := p2.Select(ctx); err != nil {
		t.Fatal(err)
	}

	assertPaneFocus(t, ctx, p2, p1)

	if err := window.LastPane(ctx); err != nil {
		t.Fatal(err)
	}

	assertPaneFocus(t, ctx, p1, p2)

	if err := window.LastPane(ctx); err != nil {
		t.Fatal(err)
	}

	assertPaneFocus(t, ctx, p2, p1)
}

func testOperationsLayoutCycling(t *testing.T, ctx context.Context, session tmux.Session) {
	t.Helper()

	var winOpts tmux.NewWindowOptions

	winOpts.Name = "op-layout"
	winOpts.Program = tmux.Exec("/bin/sh")

	link, err := session.NewWindow(ctx, winOpts)
	if err != nil {
		t.Fatal(err)
	}

	window := link.Window()

	p1, err := window.ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var splitOpts tmux.SplitOptions

	splitOpts.Direction = tmux.Horizontal
	splitOpts.Program = tmux.Exec("/bin/sh")

	if _, err = p1.Split(ctx, splitOpts); err != nil {
		t.Fatal(err)
	}

	if err := window.SelectLayout(ctx, tmux.LayoutEvenHorizontal); err != nil {
		t.Fatal(err)
	}

	initialLayout := windowLayout(t, ctx, window)

	if err := window.NextLayout(ctx); err != nil {
		t.Fatal(err)
	}

	nextLayout := windowLayout(t, ctx, window)
	if nextLayout == initialLayout {
		t.Fatalf("expected layout change on NextLayout, got %q", nextLayout)
	}

	if err := window.PreviousLayout(ctx); err != nil {
		t.Fatal(err)
	}

	prevLayout := windowLayout(t, ctx, window)
	if prevLayout != initialLayout {
		t.Fatalf(
			"expected layout restored on PreviousLayout, got %q, want %q",
			prevLayout,
			initialLayout,
		)
	}
}

func testOperationsInputGating(t *testing.T, ctx context.Context, session tmux.Session) {
	t.Helper()

	var winOpts tmux.NewWindowOptions

	winOpts.Name = "op-input"
	winOpts.Program = tmux.Exec("/bin/sh")

	link, err := session.NewWindow(ctx, winOpts)
	if err != nil {
		t.Fatal(err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	assertPaneInputOff(t, ctx, pane, "0")

	if err := pane.SetInputEnabled(ctx, false); err != nil {
		t.Fatal(err)
	}

	assertPaneInputOff(t, ctx, pane, "1")

	if err := pane.SetInputEnabled(ctx, true); err != nil {
		t.Fatal(err)
	}

	assertPaneInputOff(t, ctx, pane, "0")
}

func testOperationsWindowSelectAndLastWindow(
	t *testing.T,
	ctx context.Context,
	session tmux.Session,
) {
	t.Helper()

	var winOpts1 tmux.NewWindowOptions

	winOpts1.Name = "op-win1"
	winOpts1.Program = tmux.Exec("/bin/sh")
	winOpts1.Select = true

	w1Link, err := session.NewWindow(ctx, winOpts1)
	if err != nil {
		t.Fatal(err)
	}

	var winOpts2 tmux.NewWindowOptions

	winOpts2.Name = "op-win2"
	winOpts2.Program = tmux.Exec("/bin/sh")
	winOpts2.Select = true

	w2Link, err := session.NewWindow(ctx, winOpts2)
	if err != nil {
		t.Fatal(err)
	}

	if err := w1Link.Select(ctx); err != nil {
		t.Fatal(err)
	}

	assertWindowLinkFocus(t, ctx, w1Link, w2Link)

	if err := session.LastWindow(ctx); err != nil {
		t.Fatal(err)
	}

	assertWindowLinkFocus(t, ctx, w2Link, w1Link)

	if err := session.LastWindow(ctx); err != nil {
		t.Fatal(err)
	}

	assertWindowLinkFocus(t, ctx, w1Link, w2Link)
}

func testOperationsRespawn(t *testing.T, ctx context.Context, session tmux.Session) {
	t.Helper()

	var winOpts tmux.NewWindowOptions

	winOpts.Name = "op-respawn"
	winOpts.Program = tmux.Exec("/bin/sh")

	link, err := session.NewWindow(ctx, winOpts)
	if err != nil {
		t.Fatal(err)
	}

	window := link.Window()

	pane, err := window.ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var noKillRespawn tmux.RespawnOptions

	noKillRespawn.Program = tmux.Exec("/bin/sh")

	if err := pane.Respawn(ctx, noKillRespawn); err == nil {
		t.Fatal("expected error respawning running pane without KillRunning")
	}

	beforePanePID := panePID(t, ctx, pane)

	var killRespawn tmux.RespawnOptions

	killRespawn.KillRunning = true
	killRespawn.Program = tmux.Exec("/bin/sh")

	if err := pane.Respawn(ctx, killRespawn); err != nil {
		t.Fatal(err)
	}

	afterPanePID := panePID(t, ctx, pane)
	if afterPanePID == beforePanePID {
		t.Fatalf("expected PID change after pane respawn, got %d", afterPanePID)
	}

	beforeWinPID := windowActivePanePID(t, ctx, window)

	if err := window.Respawn(ctx, killRespawn); err != nil {
		t.Fatal(err)
	}

	afterWinPID := windowActivePanePID(t, ctx, window)
	if afterWinPID == beforeWinPID {
		t.Fatalf("expected PID change after window respawn, got %d", afterWinPID)
	}
}

func testOperationsPaneMove(t *testing.T, ctx context.Context, session tmux.Session) {
	t.Helper()

	var winOpts1 tmux.NewWindowOptions

	winOpts1.Name = "op-move1"
	winOpts1.Program = tmux.Exec("/bin/sh")

	w1Link, err := session.NewWindow(ctx, winOpts1)
	if err != nil {
		t.Fatal(err)
	}

	w1 := w1Link.Window()

	var winOpts2 tmux.NewWindowOptions

	winOpts2.Name = "op-move2"
	winOpts2.Program = tmux.Exec("/bin/sh")

	w2Link, err := session.NewWindow(ctx, winOpts2)
	if err != nil {
		t.Fatal(err)
	}

	w2 := w2Link.Window()

	p1, err := w1.ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var splitOpts tmux.SplitOptions

	splitOpts.Direction = tmux.Horizontal
	splitOpts.Program = tmux.Exec("/bin/sh")

	p2, err := p1.Split(ctx, splitOpts)
	if err != nil {
		t.Fatal(err)
	}

	assertWindowPaneCount(t, ctx, w1, 2)
	assertWindowPaneCount(t, ctx, w2, 1)

	targetPane, err := w2.ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var moveOpts tmux.JoinOptions

	moveOpts.Direction = tmux.Horizontal

	if err := p2.Move(ctx, targetPane, moveOpts); err != nil {
		t.Fatal(err)
	}

	assertWindowPaneCount(t, ctx, w1, 1)
	assertWindowPaneCount(t, ctx, w2, 2)
}

func TestIntegrationOperations(t *testing.T) {
	s := tmuxtest.NewServer(t)
	ctx := integrationContext(t)

	session, err := s.FindSession(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("ToggleZoom", func(t *testing.T) {
		testOperationsToggleZoom(t, ctx, session)
	})

	t.Run("PaneSelectAndLastPane", func(t *testing.T) {
		testOperationsPaneSelectAndLastPane(t, ctx, session)
	})

	t.Run("LayoutCycling", func(t *testing.T) {
		testOperationsLayoutCycling(t, ctx, session)
	})

	t.Run("InputGating", func(t *testing.T) {
		testOperationsInputGating(t, ctx, session)
	})

	t.Run("WindowSelectAndLastWindow", func(t *testing.T) {
		testOperationsWindowSelectAndLastWindow(t, ctx, session)
	})

	t.Run("Respawn", func(t *testing.T) {
		testOperationsRespawn(t, ctx, session)
	})

	t.Run("PaneMove", func(t *testing.T) {
		testOperationsPaneMove(t, ctx, session)
	})
}
