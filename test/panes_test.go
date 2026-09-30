//go:build integration

package tmux_test

import (
	"errors"
	"testing"
	"time"

	tmux "github.com/zigai/gotmux/tmux"
)

func TestIntegrationClockModeAndSendPrefix(t *testing.T) {
	server, session, ctx := apiFixture(t)

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "clock-test"})
	if err != nil {
		t.Fatal(err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := pane.SendPrefix(ctx); err != nil {
		t.Fatalf("SendPrefix failed: %v", err)
	}

	if err := pane.SendSecondaryPrefix(ctx); err != nil {
		t.Fatalf("SendSecondaryPrefix failed: %v", err)
	}

	if err := pane.ClockMode(ctx); err != nil {
		t.Fatalf("ClockMode failed: %v", err)
	}

	_ = server
	_ = pane.SendKeys(ctx, "q")
}

func TestIntegrationSplitAppearanceAndKillTargetOptions(t *testing.T) {
	server, session, ctx := apiFixture(t)
	requireTmux38(t, ctx, server)

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "split-appearance"})
	if err != nil {
		t.Fatal(err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	const testTitle = "CustomPaneTitle"

	newPane, err := pane.Split(ctx, tmux.SplitOptions{
		Direction:   tmux.Vertical,
		Title:       testTitle,
		BorderLines: tmux.PaneBorderSingle,
		Zoom:        false,
	})
	if err != nil {
		fatalCommand(t, "Split with enhanced options", err)
	}

	if !newPane.Valid() {
		t.Fatal("expected valid pane handle")
	}

	assertPaneTitle(t, ctx, newPane, testTitle)

	paneWithK, err := newPane.Split(ctx, tmux.SplitOptions{
		Direction:  tmux.Horizontal,
		KillTarget: true,
	})
	if err != nil {
		t.Fatalf("Split with KillTarget failed: %v", err)
	}

	if !paneWithK.Valid() {
		t.Fatal("expected valid pane from Split with KillTarget")
	}

	optVal, err := paneWithK.Options().Get(ctx, "remain-on-exit")
	if err != nil {
		t.Fatalf("failed to query remain-on-exit: %v", err)
	}

	if val, ok := optVal.Local.Get(); !ok || val != "key" {
		t.Errorf("expected remain-on-exit to be 'key', got: %q (ok=%v)", val, ok)
	}
}

func TestIntegrationNewPaneFloating(t *testing.T) {
	server, session, ctx := apiFixture(t)
	requireTmux38(t, ctx, server)

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "float-win"})
	if err != nil {
		t.Fatal(err)
	}

	window := link.Window()

	floatPane, err := window.NewPane(ctx, tmux.NewPaneOptions{
		Width:       "50",
		Height:      "15",
		X:           "10",
		Y:           "5",
		Title:       "FloatingPane",
		BorderLines: tmux.PaneBorderDouble,
	})
	if err != nil {
		fatalCommand(t, "Window.NewPane", err)
	}

	if !floatPane.Valid() {
		t.Fatal("expected valid floating pane handle")
	}

	assertPaneTitle(t, ctx, floatPane, "FloatingPane")

	if err := floatPane.Submit(ctx, "echo hello-floating"); err != nil {
		t.Fatalf("floatPane.Submit failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	captured, err := floatPane.Capture(ctx, tmux.CaptureOptions{
		JoinWrapped:        true,
		EscapeNonPrintable: true,
	})
	if err != nil {
		t.Fatalf("floatPane.Capture failed: %v", err)
	}

	if len(captured) == 0 {
		t.Fatal("expected non-empty captured output")
	}

	childPane, err := floatPane.NewPane(ctx, tmux.NewPaneOptions{
		Width:       "30",
		Height:      "10",
		X:           "20",
		Y:           "8",
		Title:       "ChildFloatPane",
		BorderLines: tmux.PaneBorderHeavy,
	})
	if err != nil {
		t.Fatalf("Pane.NewPane failed: %v", err)
	}

	if !childPane.Valid() {
		t.Fatal("expected valid child pane handle")
	}

	assertPaneTitle(t, ctx, childPane, "ChildFloatPane")

	modalPane, err := window.NewPane(ctx, tmux.NewPaneOptions{
		Width:          "40",
		Height:         "12",
		Modal:          true,
		CloseOnClick:   true,
		CaptureAllKeys: true,
		Title:          "ModalPane",
		BorderLines:    tmux.PaneBorderSingle,
	})
	if err != nil {
		t.Fatalf("Window.NewPane(Modal) failed: %v", err)
	}

	if !modalPane.Valid() {
		t.Fatal("expected valid modal pane handle")
	}
}

func TestIntegrationRespawnPreserveEnvironment(t *testing.T) {
	server, session, ctx := apiFixture(t)
	requireTmux38(t, ctx, server)

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "respawn-win"})
	if err != nil {
		t.Fatal(err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := pane.Respawn(ctx, tmux.RespawnOptions{
		KillRunning:         true,
		PreserveEnvironment: true,
	}); err != nil {
		if cmdErr, ok := errors.AsType[*tmux.CommandError](err); ok {
			t.Fatalf("Respawn stderr: %q", string(cmdErr.Result.Stderr))
		}

		t.Fatalf("pane.Respawn with PreserveEnvironment failed: %v", err)
	}

	link2, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "respawn-win2"})
	if err != nil {
		t.Fatal(err)
	}

	if err := link2.Window().Respawn(ctx, tmux.RespawnOptions{
		KillRunning:         true,
		PreserveEnvironment: true,
	}); err != nil {
		if cmdErr, ok := errors.AsType[*tmux.CommandError](err); ok {
			t.Fatalf("Respawn window stderr: %q", string(cmdErr.Result.Stderr))
		}

		t.Fatalf("window.Respawn with PreserveEnvironment failed: %v", err)
	}
}

func TestIntegrationCaptureEscapeOptions(t *testing.T) {
	_, session, ctx := apiFixture(t)

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "capture-opt-test"})
	if err != nil {
		t.Fatal(err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := pane.Submit(ctx, "printf 'Testing escape sequences: \\033[31mred\\033[0m \\001\\002\\n'"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	captured, err := pane.Capture(ctx, tmux.CaptureOptions{
		EscapeNonPrintable:  true,
		AlternateScreenOnly: false,
	})
	if err != nil {
		t.Fatalf("pane.Capture with EscapeNonPrintable failed: %v", err)
	}

	if len(captured) == 0 {
		t.Fatal("expected non-empty capture")
	}
}
