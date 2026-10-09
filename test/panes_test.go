//go:build integration

package test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zigai/gotmux/tmux"
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

func TestIntegrationSplitAppearanceOptions(t *testing.T) {
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
}

func TestIntegrationSplitKillTargetReplacesPane(t *testing.T) {
	for _, control := range []bool{false, true} {
		t.Run(map[bool]string{false: "subprocess", true: "control"}[control], func(t *testing.T) {
			assertSplitReplacesPane(t, control)
		})
	}
}

func assertSplitReplacesPane(t *testing.T, control bool) {
	t.Helper()

	server, session, ctx := apiFixture(t)
	if control {
		server = apiControl(t, ctx, server, session).Server()
	}

	pane := firstPane(t, ctx, server)

	before, err := server.Panes(ctx)
	if err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(t.TempDir(), "replacement")

	replacement, err := pane.Split(ctx, tmux.SplitOptions{
		KillTarget: true,
		Program:    tmux.Exec("/bin/sh", "-c", `printf ready > "$1"; exec sleep 60`, "replacement", marker),
	})
	if err != nil {
		fatalCommand(t, "Split(KillTarget)", err)
	}

	if replacement.ID() == pane.ID() {
		t.Fatal("replacement reused the target pane")
	}

	if _, err := pane.Info(ctx); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("target Info error = %v, want ErrNotFound", err)
	}

	after, err := server.Panes(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(after) != len(before) || after[0].ID != replacement.ID() {
		t.Fatalf("panes after replacement = %v, want only %s", after, replacement.ID())
	}

	awaitObservation(t, ctx, "replacement program", func() bool {
		output, err := os.ReadFile(marker)
		return err == nil && string(output) == "ready"
	})
}

func TestIntegrationSplitKillTargetFailureLeavesTarget(t *testing.T) {
	server, _, ctx := apiFixture(t)
	pane := firstPane(t, ctx, server)

	window, err := pane.Window(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := window.Resize(ctx, tmux.Size{Width: 10, Height: 1}); err != nil {
		t.Fatal(err)
	}

	_, err = pane.Split(ctx, tmux.SplitOptions{KillTarget: true})
	if err == nil {
		t.Fatal("replacement in an undersized window unexpectedly succeeded")
	}

	if _, err := pane.Info(ctx); err != nil {
		t.Fatalf("failed replacement removed target: %v", err)
	}

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 || panes[0].ID != pane.ID() {
		t.Fatalf("panes after failed replacement = %v, error %v", panes, err)
	}
}

func TestIntegrationCreationTitlesAreLiteral(t *testing.T) {
	server, _, ctx := apiFixture(t)
	requireTmux38(t, ctx, server)
	pane := firstPane(t, ctx, server)

	const title = "literal-#{pane_id}-##-é"

	split, err := pane.Split(ctx, tmux.SplitOptions{Title: title, Program: tmux.Exec("/bin/sh")})
	if err != nil {
		fatalCommand(t, "Split", err)
	}

	assertPaneTitle(t, ctx, split, title)

	floating, err := split.NewPane(ctx, tmux.NewPaneOptions{Title: title, Program: tmux.Exec("/bin/sh")})
	if err != nil {
		fatalCommand(t, "NewPane", err)
	}

	assertPaneTitle(t, ctx, floating, title)
}

func TestIntegrationSplitMessageIsLiteral(t *testing.T) {
	server, _, ctx := apiFixture(t)
	requireTmux38(t, ctx, server)
	pane := firstPane(t, ctx, server)
	marker := filepath.Join(shortTempDir(t), "executed")
	message := "literal-#{pane_id}-#(touch " + marker + ")"

	dead, err := pane.Split(ctx, tmux.SplitOptions{
		Message: message,
		Program: tmux.Exec("/bin/sh", "-c", "exit 0"),
	})
	if err != nil {
		fatalCommand(t, "Split(Message)", err)
	}

	awaitObservation(t, ctx, "pane exit", func() bool {
		info, err := dead.Info(ctx)
		return err == nil && info.Dead
	})

	output, err := dead.Capture(ctx, tmux.CaptureOptions{JoinWrapped: true})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(output), message) {
		t.Fatalf("exit output = %q, want literal message %q", output, message)
	}

	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("message executed a shell job: marker stat = %v", err)
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

func TestIntegrationRespawnPreserveEnvironmentRejectsWithoutMutation(t *testing.T) {
	for _, target := range []string{"pane", "window"} {
		t.Run(target, func(t *testing.T) {
			assertPreservedRespawnRejected(t, target)
		})
	}
}

func assertPreservedRespawnRejected(t *testing.T, target string) {
	t.Helper()
	_, session, ctx := apiFixture(t)

	link, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "respawn-win", Program: tmux.Exec("/bin/sleep", "60")})
	if err != nil {
		t.Fatal(err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	before, err := pane.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	options := tmux.RespawnOptions{KillRunning: true, PreserveEnvironment: true, Program: tmux.Exec("/bin/sleep", "120")}
	if target == "pane" {
		err = pane.Respawn(ctx, options)
	} else {
		err = link.Window().Respawn(ctx, options)
	}

	var operation *tmux.OperationError
	if !errors.Is(err, tmux.ErrUnsupported) || !errors.As(err, &operation) || operation.Outcome.Effect != tmux.EffectNotSent {
		t.Fatalf("Respawn error = %v, want ErrUnsupported with EffectNotSent", err)
	}

	after, err := pane.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if after.PID != before.PID || after.Dead {
		t.Fatalf("rejected respawn changed the running process: before PID %d, after PID %d, dead %v", before.PID, after.PID, after.Dead)
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
