//go:build integration

package tmux_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tmux "github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

func TestIntegrationInspectCreateAndCapture(t *testing.T) {
	s := tmuxtest.NewServer(t)
	ctx := integrationContext(t)
	info, e := s.Probe(ctx)
	if e != nil || !info.Version.AtLeast(3, 6) {
		t.Fatal(info, e)
	}
	session, e := s.FindSession(ctx, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.FindSession(ctx, "fixt"); !errors.Is(e, tmux.ErrNotFound) {
		t.Fatalf("prefix unexpectedly matched: %v", e)
	}
	link, e := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "logs;#{literal}", Program: tmux.Exec("/bin/sh")})
	if e != nil {
		t.Fatal(e)
	}
	pane, e := link.Window().ActivePane(ctx)
	if e != nil {
		t.Fatal(e)
	}
	split, e := pane.Split(ctx, tmux.SplitOptions{Direction: tmux.Horizontal, Size: tmux.SplitSize{Percent: 40}, Program: tmux.Exec("/bin/sh")})
	if e != nil {
		t.Fatal(e)
	}
	if e = split.SetTitle(ctx, "quotes '$; #{pane_id} #[style]"); e != nil {
		t.Fatal(e)
	}
	record, e := split.Info(ctx)
	if e != nil || record.Title != "quotes '$; #{pane_id} #[style]" {
		t.Fatal(record, e)
	}
	if e = link.Window().SelectLayout(ctx, tmux.Tiled); e != nil {
		t.Fatal(e)
	}
	data, e := pane.Capture(ctx, tmux.CaptureOptions{JoinWrapped: true})
	if e != nil || data == nil {
		t.Fatalf("capture %q: %v", data, e)
	}
	snap, e := s.Snapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if snap.Consistency != tmux.Consistent || len(snap.Panes()) != 3 {
		t.Fatalf("snapshot: %+v", snap.MissingReferences())
	}
	original := record.Handle()
	record.ID = "%999"
	if !record.Handle().Equal(original) {
		t.Fatal("record mutation retargeted handle")
	}
}

func TestIntegrationBinaryBufferAndLiteralInput(t *testing.T) {
	s := tmuxtest.NewServer(t)
	ctx := integrationContext(t)
	pane := firstPane(t, s, ctx)
	b, e := tmux.NamedBuffer("name;#{literal}")
	if e != nil {
		t.Fatal(e)
	}
	want := []byte("\x00\xff\r\nquotes'\"\\;$()#{pane_id}")
	if e = s.WriteBuffer(ctx, b, want); e != nil {
		t.Fatal(e)
	}
	got, e := s.ReadBuffer(ctx, b)
	if e != nil || !bytes.Equal(got, want) {
		t.Fatalf("buffer %q != %q (%v)", got, want, e)
	}
	// Application completion policy belongs here, not in Submit.
	if e = pane.SendText(ctx, "printf '%s' 'literal;#{pane_id}'"); e != nil {
		t.Fatal(e)
	}
	if e = pane.SendKeys(ctx, tmux.KeyCtrlC); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteBuffer(ctx, b); e != nil {
		t.Fatal(e)
	}
}

func TestIntegrationOptionsInheritanceAndEmpty(t *testing.T) {
	s := tmuxtest.NewServer(t)
	ctx := integrationContext(t)
	pane := firstPane(t, s, ctx)
	if e := s.GlobalWindowOptions().SetUser(ctx, "@probe", "global"); e != nil {
		t.Fatal(e)
	}
	inherited, e := pane.Options().User(ctx, "@probe")
	if e != nil {
		t.Fatal(e)
	}
	if v, ok := inherited.Effective.Get(); !ok || v != "global" || inherited.Local.State() != tmux.Unavailable {
		t.Fatal(inherited)
	}
	if e = pane.Options().SetUser(ctx, "@probe", ""); e != nil {
		t.Fatal(e)
	}
	local, e := pane.Options().User(ctx, "@probe")
	if e != nil {
		t.Fatal(e)
	}
	if v, ok := local.Local.Get(); !ok || v != "" {
		t.Fatal(local)
	}
	if e = pane.Options().UnsetUser(ctx, "@probe"); e != nil {
		t.Fatal(e)
	}
	inherited, e = pane.Options().User(ctx, "@probe")
	if e != nil {
		t.Fatal(e)
	}
	if v, ok := inherited.Effective.Get(); !ok || v != "global" {
		t.Fatal(inherited)
	}
	session, e := s.FindSession(ctx, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	if e = session.Options().SetHistoryLimit(ctx, 5000); e != nil {
		t.Fatal(e)
	}
	value, e := session.Options().HistoryLimit(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if v, ok := value.Effective.Get(); !ok || v != 5000 {
		t.Fatal(value)
	}
	if e = session.Options().UnsetHistoryLimit(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestIntegrationLinkedGraphAndStaleIndex(t *testing.T) {
	s := tmuxtest.NewServer(t)
	ctx := integrationContext(t)
	session, e := s.FindSession(ctx, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	original, e := session.Windows(ctx)
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.NewSession(ctx, tmux.NewSessionOptions{Name: "other", Program: tmux.Exec("/bin/sh")})
	if e != nil {
		t.Fatal(e)
	}
	zero := 0
	linked, e := original[0].Window().Link(ctx, other, tmux.LinkOptions{Index: &zero, Replace: true})
	if e != nil {
		t.Fatal(e)
	}
	links, e := original[0].Window().Links(ctx)
	if e != nil || len(links) != 2 {
		t.Fatalf("links=%d %v", len(links), e)
	}
	snap, e := s.Snapshot(ctx)
	if e != nil || len(snap.Windows()) != 1 || len(snap.Links()) != 2 {
		t.Fatalf("unique=%d links=%d %v", len(snap.Windows()), len(snap.Links()), e)
	}
	newIndex := 9
	if _, e = linked.Move(ctx, other, tmux.LinkOptions{Index: &newIndex}); e != nil {
		t.Fatal(e)
	}
	if e = linked.Select(ctx); !errors.Is(e, tmux.ErrLinkChanged) {
		t.Fatalf("stale index selected: %v", e)
	}
}

func TestIntegrationTwoServersAndReplacement(t *testing.T) {
	a := tmuxtest.NewServer(t)
	b := tmuxtest.NewServer(t)
	ctx := integrationContext(t)
	pa := firstPane(t, a, ctx)
	pb := firstPane(t, b, ctx)
	if pa.ID() != pb.ID() {
		t.Fatal("fixtures should initially reuse the same pane id")
	}
	if e := pa.SetTitle(ctx, "server-a"); e != nil {
		t.Fatal(e)
	}
	bi, e := pb.Info(ctx)
	if e != nil || bi.Title == "server-a" {
		t.Fatal("cross-server targeting", e)
	}
	old := pa.Identity()
	if e = a.KillIfIdentity(ctx, old); e != nil {
		t.Fatal(e)
	}
	// Same private endpoint; new lookup may discover a replacement, old handle may not.
	replacement, e := a.NewSession(ctx, tmux.NewSessionOptions{Name: "replacement", Program: tmux.Exec("/bin/sh")})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.KillIfIdentity(c, replacement.Identity())
	})
	if e = pa.Kill(ctx); !errors.Is(e, tmux.ErrServerChanged) {
		t.Fatalf("replacement not rejected: %v", e)
	}
	if _, e = replacement.Info(ctx); e != nil {
		t.Fatal("replacement was touched", e)
	}
}

func TestIntegrationControlParityAndExplicitAuxiliary(t *testing.T) {
	s := tmuxtest.NewServer(t)
	ctx := integrationContext(t)
	session, e := s.FindSession(ctx, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	conn, e := s.OpenControl(ctx, session, tmux.ControlOptions{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := conn.Close(); e != nil {
			t.Error(e)
		}
	})
	stream, e := conn.Events(ctx, tmux.EventOptions{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = stream.Close() })
	plain, e := s.Panes(ctx)
	if e != nil {
		t.Fatal(e)
	}
	controlled, e := conn.Server().Panes(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if len(plain) != len(controlled) || plain[0].ID != controlled[0].ID || plain[0].Title != controlled[0].Title {
		t.Fatal("transport record mismatch")
	}
	pane := controlled[0].Handle()
	if _, e = pane.Capture(ctx, tmux.CaptureOptions{}); !errors.Is(e, tmux.ErrTransportUnsupported) {
		t.Fatalf("unsafe control capture accepted: %v", e)
	}
	aux, e := pane.UsingSubprocess()
	if e != nil {
		t.Fatal(e)
	}
	a, e := aux.Capture(ctx, tmux.CaptureOptions{})
	if e != nil {
		t.Fatal(e)
	}
	b, e := plain[0].Handle().Capture(ctx, tmux.CaptureOptions{})
	if e != nil || !bytes.Equal(a, b) {
		t.Fatal("auxiliary capture mismatch", e)
	}
	if e = pane.SetTitle(ctx, "control-literal-#{id}"); e != nil {
		t.Fatal(e)
	}
	data, e := pane.Format(ctx, tmux.Format("%%end 1 1 1\n#{pane_title}"))
	if e != nil || string(data) != "%end 1 1 1\ncontrol-literal-#{id}" {
		t.Fatalf("delimiter framing: %q %v", data, e)
	}
	if e = conn.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = aux.Info(ctx); !errors.Is(e, tmux.ErrClosed) {
		t.Fatalf("auxiliary handle outlived connection: %v", e)
	}
}

func TestIntegrationNoServerAndExistingOnly(t *testing.T) {
	fixture := tmuxtest.NewServer(t)
	_ = fixture
	binary := os.Getenv("TMUX_TEST_BINARY")
	if binary == "" {
		binary = "tmux"
	}
	dir := shortTempDir(t)
	path := filepath.Join(dir, "absent")
	s, e := tmux.New(tmux.Config{Binary: binary, SocketPath: path, ConfigFile: "/dev/null"})
	if e != nil {
		t.Fatal(e)
	}
	ctx := integrationContext(t)
	if _, e = s.Panes(ctx); !errors.Is(e, tmux.ErrNoServer) {
		t.Fatalf("missing server: %v", e)
	}
	if _, e = s.NewSession(ctx, tmux.NewSessionOptions{Start: tmux.ExistingOnly}); !errors.Is(e, tmux.ErrNoServer) {
		t.Fatalf("existing-only: %v", e)
	}
	if _, e = os.Stat(path); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("query created server")
	}
}

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

func assertPaneFocus(t *testing.T, ctx context.Context, active, inactive tmux.Pane) {
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

func assertWindowLinkFocus(t *testing.T, ctx context.Context, active, inactive tmux.WindowLink) {
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

	if err := window.SelectLayout(ctx, tmux.EvenHorizontal); err != nil {
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
		t.Fatalf("expected layout restored on PreviousLayout, got %q, want %q", prevLayout, initialLayout)
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

func testOperationsWindowSelectAndLastWindow(t *testing.T, ctx context.Context, session tmux.Session) {
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

// TestIntegrationProcessEnvironmentNotSession verifies that NewSessionOptions.Env
// sets the process environment via an execution wrapper, NOT the tmux session environment.
func TestIntegrationProcessEnvironmentNotSession(t *testing.T) {
	server, _, ctx := apiFixture(t)
	dir := t.TempDir()
	resultFile := filepath.Join(dir, "proc_env.txt")

	envKey := "TGO_PROC_ENV_TEST"
	envVal := "process_isolated_value"

	// Create a session with Env overrides and an explicit Exec program
	session, err := server.NewSession(ctx, tmux.NewSessionOptions{
		Window: "env-test-win",
		Dir:    dir,
		Env: map[string]string{
			envKey: envVal,
		},
		Program: tmux.Exec("/bin/sh", "-c", "printf '%s' \"$"+envKey+"\" > "+resultFile+" && sleep 60"),
	})
	if err != nil {
		t.Fatalf("NewSession with Env failed: %v", err)
	}

	// Verify the process received the environment variable
	awaitFile(t, ctx, resultFile)
	assertFileBytes(t, resultFile, []byte(envVal))

	// Verify that the tmux session environment does NOT contain the variable
	envScope := session.Environment()
	envValResult, err := envScope.Get(ctx, envKey, false)
	if err != nil {
		t.Fatalf("session.Environment().Get failed: %v", err)
	}
	if val, ok := envValResult.Value.Get(); ok {
		t.Fatalf("expected %s NOT to be set in tmux session environment, got %q", envKey, val)
	}
}

// TestIntegrationRunWithStartPolicy verifies that RunWith explicitly controls server startup policy
// via StartPolicy (AllowStart vs ExistingOnly) independently of command names or aliases.
func TestIntegrationRunWithStartPolicy(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "start.sock")
	ctx := integrationContext(t)
	binary := os.Getenv("TMUX_TEST_BINARY")
	if binary == "" {
		binary = "tmux"
	}

	server, err := tmux.New(tmux.Config{
		Binary:           binary,
		SocketPath:       socketPath,
		SocketName:       "",
		ConfigFile:       "/dev/null",
		Env:              testEnvironment(dir),
		Dir:              dir,
		Limits:           tmux.DefaultLimits(),
		UTF8:             tmux.UTF8Default,
		Colors256:        false,
		TerminalFeatures: nil,
		LogLevel:         tmux.LogNone,
		LoginShell:       false,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. ExistingOnly with new-session should fail with ErrNoServer (because -N is passed)
	cmd, err := tmux.NewCommand("new-session", "-d", "-s", "s1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = server.RunWith(ctx, cmd, tmux.RunOptions{Start: tmux.ExistingOnly})
	if !errors.Is(err, tmux.ErrNoServer) {
		t.Fatalf("expected ErrNoServer for ExistingOnly on unstarted server, got %v", err)
	}

	// 2. AllowStart with alias "new" should succeed and spawn daemon
	aliasCmd, err := tmux.NewCommand("new", "-d", "-s", "s-alias")
	if err != nil {
		t.Fatal(err)
	}

	_, err = server.RunWith(ctx, aliasCmd, tmux.RunOptions{Start: tmux.AllowStart})
	if err != nil {
		t.Fatalf("RunWith with AllowStart failed: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Kill(cleanupCtx)
	})

	// Verify session exists
	sess, err := server.FindSession(ctx, "s-alias")
	if err != nil {
		t.Fatalf("FindSession failed after started: %v", err)
	}
	_ = sess

	// 3. ExistingOnly with RunSequenceWith while running should succeed
	seqCmd, err := tmux.NewCommand("display-message", "-p", "alive")
	if err != nil {
		t.Fatal(err)
	}

	seq, err := tmux.Sequence(seqCmd)
	if err != nil {
		t.Fatal(err)
	}

	seqRes, err := server.RunSequenceWith(ctx, seq, tmux.RunOptions{Start: tmux.ExistingOnly})
	if err != nil {
		t.Fatalf("RunSequenceWith failed: %v", err)
	}
	if !bytes.Contains(seqRes.Stdout, []byte("alive")) {
		t.Fatalf("expected 'alive' in output, got %q", string(seqRes.Stdout))
	}
}

// TestIntegrationRootFlagsExecution verifies execution of commands with explicit root flags
// (Colors256, TerminalFeatures, UTF8Omit, LogLevel).
func TestIntegrationRootFlagsExecution(t *testing.T) {
	dir := shortTempDir(t)
	socketPath := filepath.Join(dir, "root.sock")
	ctx := integrationContext(t)
	binary := os.Getenv("TMUX_TEST_BINARY")
	if binary == "" {
		binary = "tmux"
	}

	server, err := tmux.New(tmux.Config{
		Binary:           binary,
		SocketPath:       socketPath,
		SocketName:       "",
		ConfigFile:       "/dev/null",
		Env:              testEnvironment(dir),
		Dir:              dir,
		Limits:           tmux.DefaultLimits(),
		UTF8:             tmux.UTF8Omit,
		Colors256:        true,
		TerminalFeatures: []string{"256", "RGB"},
		LogLevel:         tmux.LogVerbose,
		LoginShell:       false,
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd, err := tmux.NewCommand("new-session", "-d", "-s", "root-flags-sess")
	if err != nil {
		t.Fatal(err)
	}

	_, err = server.RunWith(ctx, cmd, tmux.RunOptions{Start: tmux.AllowStart})
	if err != nil {
		t.Fatalf("RunWith failed: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Kill(cleanupCtx)
	})

	// Verify server log file was generated (from -v flag)
	awaitObservation(t, ctx, "server log file created", func() bool {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}

		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "tmux-") && strings.HasSuffix(e.Name(), ".log") {
				return true
			}
		}

		return false
	})
}

// TestIntegrationAuxiliaryServerNeverAutoSpawns verifies that bound auxiliary servers
// strictly forbid auto-spawning replacement daemons across all execution paths.
func TestIntegrationAuxiliaryServerNeverAutoSpawns(t *testing.T) {
	server, session, ctx := apiFixture(t)
	connection, err := server.OpenControl(ctx, session, tmux.ControlOptions{PaneOutput: false, QueuedBytes: 0})
	if err != nil {
		t.Fatal(err)
	}
	auxiliary := connection.AuxiliaryServer()

	// 1. Calling RunWith with AllowStart on auxiliary server must NOT spawn daemon if dead.
	// First, test when daemon is alive: commands execute through guard.
	pingCmd, err := tmux.NewCommand("display-message", "-p", "alive")
	if err != nil {
		t.Fatal(err)
	}

	res, err := auxiliary.RunWith(ctx, pingCmd, tmux.RunOptions{Start: tmux.AllowStart})
	if err != nil {
		t.Fatalf("auxiliary RunWith while alive failed: %v", err)
	}
	if !bytes.Contains(res.Stdout, []byte("alive")) {
		t.Fatalf("expected 'alive', got %q", string(res.Stdout))
	}

	// 2. Kill the daemon
	_ = server.Kill(ctx)
	_ = connection.Close()

	// 3. Both Run and RunWith with AllowStart must fail with ErrNoServer or ErrServerChanged,
	// and must NEVER leak an auto-spawned replacement daemon on the socket!
	leakCmd, err := tmux.NewCommand("new-session", "-d", "-s", "leak-attempt")
	if err != nil {
		t.Fatal(err)
	}

	_, err = auxiliary.Run(ctx, leakCmd)
	if err == nil {
		t.Fatal("expected auxiliary.Run to fail after daemon killed")
	}

	_, err = auxiliary.RunWith(ctx, leakCmd, tmux.RunOptions{Start: tmux.AllowStart})
	if err == nil {
		t.Fatal("expected auxiliary.RunWith to fail after daemon killed")
	}

	// Verify no daemon exists on the socket path
	_, err = server.Panes(ctx)
	if !errors.Is(err, tmux.ErrNoServer) {
		t.Fatalf("expected ErrNoServer, indicating no daemon was spawned, got %v", err)
	}
}

// TestIntegrationSplitWindowStdin verifies that raw RunWith executing "split-window -I"
// passes stdin bytes into an empty pane, creating a dead pane upon EOF with the exact content.
func TestIntegrationSplitWindowStdin(t *testing.T) {
	server, session, ctx := apiFixture(t)

	links, err := session.Windows(ctx)
	if err != nil || len(links) == 0 {
		t.Fatalf("session.Windows failed: %v", err)
	}
	window := links[0].Window()

	pane, err := window.ActivePane(ctx)
	if err != nil {
		t.Fatalf("ActivePane failed: %v", err)
	}

	wantLines := "first line from split-window stdin\nsecond line with #{special} syntax\n"
	cmd, err := tmux.NewCommand("split-window", "-d", "-I", "-t", string(pane.ID()))
	if err != nil {
		t.Fatalf("NewCommand failed: %v", err)
	}

	_, err = server.RunWith(ctx, cmd, tmux.RunOptions{Input: []byte(wantLines)})
	if err != nil {
		t.Fatalf("RunWith split-window -I failed: %v", err)
	}

	panes, err := window.Panes(ctx)
	if err != nil {
		t.Fatalf("window.Panes failed: %v", err)
	}
	if len(panes) < 2 {
		t.Fatalf("expected at least 2 panes after split, got %d", len(panes))
	}

	var targetPane tmux.Pane
	for _, p := range panes {
		if p.ID != pane.ID() {
			targetPane, err = server.Pane(ctx, p.ID)
			if err != nil {
				t.Fatalf("server.Pane failed: %v", err)
			}
			break
		}
	}

	var captured []byte
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		captured, err = targetPane.Capture(ctx, tmux.CaptureOptions{})
		if err == nil && strings.Contains(string(captured), "first line from split-window stdin") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !strings.Contains(string(captured), "first line from split-window stdin") ||
		!strings.Contains(string(captured), "second line with #{special} syntax") {
		t.Fatalf("captured pane output missing expected lines, got: %q", string(captured))
	}

	var isDead bool
	for time.Now().Before(deadline) {
		info, err := targetPane.Info(ctx)
		if err == nil && info.Dead {
			isDead = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !isDead {
		t.Errorf("expected pane to be dead after stdin EOF, but Dead is false")
	}
}

// TestIntegrationDisplayMessageStdin verifies that raw RunWith executing "display-message -I"
// delivers standard input bytes into an existing empty pane and observes them.
func TestIntegrationDisplayMessageStdin(t *testing.T) {
	server, session, ctx := apiFixture(t)

	links, err := session.Windows(ctx)
	if err != nil || len(links) == 0 {
		t.Fatalf("session.Windows failed: %v", err)
	}
	window := links[0].Window()

	pane, err := window.ActivePane(ctx)
	if err != nil {
		t.Fatalf("ActivePane failed: %v", err)
	}

	// 1. Create an empty pane in the window using split-window with empty command
	splitCmd, err := tmux.NewCommand("split-window", "-d", "-t", string(pane.ID()), "")
	if err != nil {
		t.Fatalf("NewCommand split-window failed: %v", err)
	}
	if _, err := server.Run(ctx, splitCmd); err != nil {
		t.Fatalf("Run split-window failed: %v", err)
	}

	panes, err := window.Panes(ctx)
	if err != nil {
		t.Fatalf("window.Panes failed: %v", err)
	}
	var emptyPane tmux.Pane
	for _, p := range panes {
		if p.ID != pane.ID() {
			emptyPane, err = server.Pane(ctx, p.ID)
			if err != nil {
				t.Fatalf("server.Pane failed: %v", err)
			}
			break
		}
	}
	if emptyPane.ID() == "" {
		t.Fatalf("could not find empty pane")
	}

	// 2. Forward stdin into the empty pane using display-message -I
	wantText := "hello to empty pane from display-message -I\nsecond line"
	displayCmd, err := tmux.NewCommand("display-message", "-I", "-t", string(emptyPane.ID()))
	if err != nil {
		t.Fatalf("NewCommand display-message failed: %v", err)
	}

	_, err = server.RunWith(ctx, displayCmd, tmux.RunOptions{Input: []byte(wantText)})
	if err != nil {
		t.Fatalf("RunWith display-message -I failed: %v", err)
	}

	// 3. Capture pane content and assert delivery
	var captured []byte
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		captured, err = emptyPane.Capture(ctx, tmux.CaptureOptions{})
		if err == nil && strings.Contains(string(captured), "hello to empty pane from display-message -I") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !strings.Contains(string(captured), "hello to empty pane from display-message -I") ||
		!strings.Contains(string(captured), "second line") {
		t.Fatalf("captured empty pane output missing delivered text, got: %q", string(captured))
	}
}

// TestIntegrationRunSequenceWithStdinSingleStream verifies that in a CommandSequence,
// standard input is a single shared stream delivered to the tmux process, not duplicated
// per command.
func TestIntegrationRunSequenceWithStdinSingleStream(t *testing.T) {
	server, _, ctx := apiFixture(t)

	b1, err := tmux.NamedBuffer("seq-buf-1")
	if err != nil {
		t.Fatalf("NamedBuffer failed: %v", err)
	}
	b2, err := tmux.NamedBuffer("seq-buf-2")
	if err != nil {
		t.Fatalf("NamedBuffer 2 failed: %v", err)
	}

	cmd1, err := tmux.NewCommand("load-buffer", "-b", "seq-buf-1", "-")
	if err != nil {
		t.Fatalf("NewCommand 1 failed: %v", err)
	}
	cmd2, err := tmux.NewCommand("load-buffer", "-b", "seq-buf-2", "-")
	if err != nil {
		t.Fatalf("NewCommand 2 failed: %v", err)
	}

	seq, err := tmux.Sequence(cmd1, cmd2)
	if err != nil {
		t.Fatalf("Sequence failed: %v", err)
	}

	inputData := []byte("payload for sequence single stream\n")
	// The first command consumes stdin to EOF. The second command finds stdin exhausted,
	// demonstrating that stdin is a single shared stream delivered to the tmux process, not duplicated.
	_, _ = server.RunSequenceWith(ctx, seq, tmux.RunOptions{Start: tmux.AllowStart, Input: inputData})

	got, err := server.ReadBuffer(ctx, b1)
	if err != nil {
		t.Fatalf("ReadBuffer b1 failed: %v", err)
	}
	if !bytes.Equal(got, inputData) {
		t.Fatalf("buffer data mismatch: got %q, want %q", got, inputData)
	}

	// Buffer 2 was not created because stdin was consumed to EOF by command 1
	if _, err := server.ReadBuffer(ctx, b2); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("expected buffer 2 not to exist because stdin was consumed by command 1, got %v", err)
	}
}

// TestIntegrationPrepareCommandFileStreaming verifies that PrepareCommand executes a command
// with caller-owned os.File streams (pipes), observing unbuffered streaming and EOF.
func TestIntegrationPrepareCommandFileStreaming(t *testing.T) {
	server, _, ctx := apiFixture(t)

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinR.Close()
	defer stdinW.Close()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdoutR.Close()
	defer stdoutW.Close()

	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stderrR.Close()
	defer stderrW.Close()

	b, err := tmux.NamedBuffer("stream-buf")
	if err != nil {
		t.Fatal(err)
	}

	cmd, err := tmux.NewCommand("load-buffer", "-b", "stream-buf", "-")
	if err != nil {
		t.Fatal(err)
	}

	streams := tmux.Streams{In: stdinR, Out: stdoutW, Err: stderrW}
	execCmd, err := server.PrepareCommand(ctx, cmd, streams, tmux.CommandOptions{})
	if err != nil {
		t.Fatalf("PrepareCommand failed: %v", err)
	}

	if err := execCmd.Start(); err != nil {
		t.Fatalf("execCmd.Start failed: %v", err)
	}

	payload := []byte("streamed data via os.Pipe\n")
	if _, err := stdinW.Write(payload); err != nil {
		t.Fatalf("stdinW.Write failed: %v", err)
	}
	_ = stdinW.Close()

	if err := execCmd.Wait(); err != nil {
		t.Fatalf("execCmd.Wait failed: %v", err)
	}

	got, err := server.ReadBuffer(ctx, b)
	if err != nil {
		t.Fatalf("ReadBuffer failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("streamed data mismatch: got %q, want %q", got, payload)
	}
}

// TestIntegrationPrepareCommandIndefiniteWait verifies that PrepareCommand allows indefinite waits
// without being bounded by Limits.CommandTimeout, and completes when signaled by another client.
func TestIntegrationPrepareCommandIndefiniteWait(t *testing.T) {
	server, _, ctx := apiFixture(t)

	channel := "wait_chan_r4"
	waitCmd, err := tmux.NewCommand("wait-for", channel)
	if err != nil {
		t.Fatal(err)
	}

	execCmd, err := server.PrepareCommand(ctx, waitCmd, tmux.Streams{}, tmux.CommandOptions{})
	if err != nil {
		t.Fatalf("PrepareCommand failed: %v", err)
	}

	if err := execCmd.Start(); err != nil {
		t.Fatalf("execCmd.Start failed: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- execCmd.Wait()
	}()

	t.Cleanup(func() {
		if execCmd.Process != nil {
			_ = execCmd.Process.Kill()
		}
	})
	time.Sleep(100 * time.Millisecond)

	signalCmd, err := tmux.NewCommand("wait-for", "-S", channel)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Run(ctx, signalCmd); err != nil {
		t.Fatalf("server.Run wait-for -S failed: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait-for command exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait-for command did not wake after signal")
	}
}

// TestIntegrationPrepareForegroundServer verifies that PrepareForegroundServer runs a tmux server
// in the foreground (-D), allowing connections and sessions, and exits cleanly when killed.
func TestIntegrationPrepareForegroundServer(t *testing.T) {
	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "fg.sock")
	s, err := tmux.New(tmux.Config{
		Binary:     "",
		SocketPath: sockPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	fgCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cmd, err := s.PrepareForegroundServer(fgCtx)
	if err != nil {
		t.Fatalf("PrepareForegroundServer failed: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start failed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	t.Cleanup(func() {
		cancel()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})

	readyCtx, readyCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer readyCancel()

	for {
		if _, err := s.Probe(readyCtx); err == nil {
			break
		}
		if readyCtx.Err() != nil {
			t.Fatalf("foreground server failed to become ready: %v", readyCtx.Err())
		}
		time.Sleep(20 * time.Millisecond)
	}

	sess, err := s.NewSession(t.Context(), tmux.NewSessionOptions{Name: "fg_session"})
	if err != nil {
		t.Fatalf("NewSession on foreground server failed: %v", err)
	}
	if !sess.Valid() {
		t.Fatal("expected valid session handle")
	}

	if err := s.Kill(t.Context()); err != nil {
		t.Fatalf("s.Kill failed: %v", err)
	}

	select {
	case err := <-done:
		done <- err
		if err != nil {
			t.Fatalf("foreground server process exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("foreground server process did not exit after Kill")
	}
}

// TestIntegrationRootShell verifies that RunRootShell and PrepareRootShell execute shell commands
// using tmux -c, respecting default-shell semantics and StartPolicy.
func TestIntegrationRootShell(t *testing.T) {
	server, _, ctx := apiFixture(t)

	// 1. RunRootShell bounded execution
	res, err := server.RunRootShell(ctx, "echo rootshell_output", tmux.RootShellOptions{})
	if err != nil {
		t.Fatalf("RunRootShell failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", res.ExitCode)
	}
	if !strings.Contains(string(res.Stdout), "rootshell_output") {
		t.Errorf("expected 'rootshell_output', got %q", string(res.Stdout))
	}

	// 2. PrepareRootShell with pipe streaming
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	shCmd, err := server.PrepareRootShell(ctx, "echo streaming_rootshell", tmux.Streams{Out: w}, tmux.RootShellOptions{})
	if err != nil {
		t.Fatalf("PrepareRootShell failed: %v", err)
	}

	if err := shCmd.Start(); err != nil {
		t.Fatalf("shCmd.Start failed: %v", err)
	}
	_ = w.Close()

	outBuf := new(bytes.Buffer)
	_, _ = io.Copy(outBuf, r)

	if err := shCmd.Wait(); err != nil {
		t.Fatalf("shCmd.Wait failed: %v", err)
	}
	if !strings.Contains(outBuf.String(), "streaming_rootshell") {
		t.Errorf("expected 'streaming_rootshell', got %q", outBuf.String())
	}
}

// TestIntegrationPrepareSequence verifies that PrepareSequence executes a sequence of commands
// with caller-owned streams.
func TestIntegrationPrepareSequence(t *testing.T) {
	server, session, ctx := apiFixture(t)

	b, err := tmux.NamedBuffer("seq-prep-buf")
	if err != nil {
		t.Fatal(err)
	}

	cmd1, err := tmux.NewCommand("set-buffer", "-b", "seq-prep-buf", "initial_val")
	if err != nil {
		t.Fatal(err)
	}

	cmd2, err := tmux.NewCommand("set-option", "-t", string(session.ID()), "@seq_prep_done", "yes")
	if err != nil {
		t.Fatal(err)
	}

	seq, err := tmux.Sequence(cmd1, cmd2)
	if err != nil {
		t.Fatal(err)
	}

	execCmd, err := server.PrepareSequence(ctx, seq, tmux.Streams{}, tmux.CommandOptions{})
	if err != nil {
		t.Fatalf("PrepareSequence failed: %v", err)
	}

	if err := execCmd.Run(); err != nil {
		t.Fatalf("execCmd.Run failed: %v", err)
	}

	got, err := server.ReadBuffer(ctx, b)
	if err != nil {
		t.Fatalf("ReadBuffer failed: %v", err)
	}
	if string(got) != "initial_val" {
		t.Fatalf("expected 'initial_val', got %q", string(got))
	}

	assertUserOption(t, ctx, session, "@seq_prep_done", "yes")
}

// TestIntegrationRunWithEmptyInputSlice verifies that RunWith with Input: []byte{}
// (empty non-nil slice) connects standard input and immediately sends EOF.
func TestIntegrationRunWithEmptyInputSlice(t *testing.T) {
	server, session, ctx := apiFixture(t)

	// split-window -I creates an empty pane and forwards stdin until EOF.
	// Passing Input: []byte{} should immediately deliver EOF, leaving the pane dead.
	links, err := session.Windows(ctx)
	if err != nil || len(links) == 0 {
		t.Fatalf("session.Windows failed: %v", err)
	}
	window := links[0].Window()

	pane, err := window.ActivePane(ctx)
	if err != nil {
		t.Fatalf("ActivePane failed: %v", err)
	}

	cmd, err := tmux.NewCommand("split-window", "-d", "-I", "-t", string(pane.ID()))
	if err != nil {
		t.Fatalf("NewCommand failed: %v", err)
	}

	_, err = server.RunWith(ctx, cmd, tmux.RunOptions{Start: tmux.AllowStart, Input: []byte{}})
	if err != nil {
		t.Fatalf("RunWith with empty input slice failed: %v", err)
	}

	panes, err := window.Panes(ctx)
	if err != nil {
		t.Fatalf("window.Panes failed: %v", err)
	}

	var newPane tmux.Pane
	for _, p := range panes {
		if p.ID != pane.ID() {
			newPane, err = server.Pane(ctx, p.ID)
			if err != nil {
				t.Fatalf("server.Pane failed: %v", err)
			}
			break
		}
	}

	var isDead bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		info, err := newPane.Info(ctx)
		if err == nil && info.Dead {
			isDead = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !isDead {
		t.Errorf("expected pane to be dead immediately after empty input EOF")
	}
}

// TestIntegrationOpenControlNewSession verifies OpenControlNewSession creates a new session,
// attaches directly in control mode, and discovers the exact owned Client via Connection.Client().
func TestIntegrationOpenControlNewSession(t *testing.T) {
	server, _, ctx := apiFixture(t)

	sessName := "new_ctl_sess_r5"
	conn, session, err := server.OpenControlNewSession(ctx, tmux.ControlNewSessionOptions{
		Control: tmux.ControlOptions{PaneOutput: false},
		Name:    sessName,
		Window:  "win_r5",
		Dir:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("OpenControlNewSession failed: %v", err)
	}
	defer conn.Close()

	info, err := session.Info(ctx)
	if err != nil {
		t.Fatalf("session.Info failed: %v", err)
	}
	if info.Name != sessName {
		t.Errorf("expected session name %q, got %q", sessName, info.Name)
	}

	// Test Connection.Client() discovers the exact owned client
	client, err := conn.Client(ctx)
	if err != nil {
		t.Fatalf("conn.Client failed: %v", err)
	}
	if !client.Valid() {
		t.Fatalf("expected valid client handle, got %v", client)
	}

	clientInfo, err := client.Info(ctx)
	if err != nil {
		t.Fatalf("client.Info failed: %v", err)
	}
	if !clientInfo.Control {
		t.Errorf("expected client to be in control mode")
	}

	// Execute command via connection's server wire
	windows, err := conn.Server().Windows(ctx)
	if err != nil || len(windows) == 0 {
		t.Fatalf("conn.Server().Windows failed: %v", err)
	}
}

// TestIntegrationControlNoEchoPTY verifies that a control connection started with NoEcho: true
// operates natively over a PTY, negotiating the -CC DSC preamble, executing commands, and exiting cleanly.
func TestIntegrationControlNoEchoPTY(t *testing.T) {
	server, session, ctx := apiFixture(t)

	conn, err := server.OpenControl(ctx, session, tmux.ControlOptions{
		NoEcho:     true,
		PaneOutput: true,
	})
	if err != nil {
		t.Fatalf("OpenControl with NoEcho failed: %v", err)
	}
	defer conn.Close()

	// Verify commands execute over the -CC connection
	windows, err := conn.Server().Windows(ctx)
	if err != nil || len(windows) == 0 {
		t.Fatalf("Windows query over -CC connection failed: %v", err)
	}

	client, err := conn.Client(ctx)
	if err != nil {
		t.Fatalf("conn.Client over -CC failed: %v", err)
	}
	if !client.Valid() {
		t.Fatalf("expected valid client handle over -CC")
	}
}

// TestIntegrationSetPaneOutputAction verifies that SetPaneOutputAction applies pause/continue
// actions on control clients and receives %pause and %continue notifications.
func TestIntegrationSetPaneOutputAction(t *testing.T) {
	server, session, ctx := apiFixture(t)

	conn, err := server.OpenControl(ctx, session, tmux.ControlOptions{
		PaneOutput: true,
	})
	if err != nil {
		t.Fatalf("OpenControl failed: %v", err)
	}
	defer conn.Close()

	stream, err := conn.Events(ctx, tmux.EventOptions{})
	if err != nil {
		t.Fatalf("conn.Events failed: %v", err)
	}
	defer stream.Close()

	links, err := session.Windows(ctx)
	if err != nil || len(links) == 0 {
		t.Fatal(err)
	}
	pane, err := links[0].Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}
	boundPane, err := conn.Server().Pane(ctx, pane.ID())
	if err != nil {
		t.Fatal(err)
	}

	// 1. Pause pane output
	if err := conn.SetPaneOutputAction(ctx, boundPane, tmux.PaneOutputPause); err != nil {
		t.Fatalf("SetPaneOutputAction pause failed: %v", err)
	}

	// Wait for %pause event
	pauseFound := false
	pauseCtx, pauseCancel := context.WithTimeout(ctx, 2*time.Second)
	defer pauseCancel()
	for {
		ev, err := stream.Next(pauseCtx)
		if err != nil {
			break
		}
		if pe, ok := ev.(tmux.PanePauseEvent); ok && pe.PaneID == pane.ID() {
			pauseFound = true
			break
		}
	}
	if !pauseFound {
		t.Errorf("expected PanePauseEvent for pane %s", pane.ID())
	}

	// 2. Continue pane output
	if err := conn.SetPaneOutputAction(ctx, boundPane, tmux.PaneOutputContinue); err != nil {
		t.Fatalf("SetPaneOutputAction continue failed: %v", err)
	}

	// Wait for %continue event
	contFound := false
	contCtx, contCancel := context.WithTimeout(ctx, 2*time.Second)
	defer contCancel()
	for {
		ev, err := stream.Next(contCtx)
		if err != nil {
			break
		}
		if ce, ok := ev.(tmux.PaneContinueEvent); ok && ce.PaneID == pane.ID() {
			contFound = true
			break
		}
	}
	if !contFound {
		t.Errorf("expected PaneContinueEvent for pane %s", pane.ID())
	}

	// 3. Batch action
	if err := conn.SetPaneOutputActions(ctx, tmux.PaneOutputTarget{Pane: boundPane, Action: tmux.PaneOutputOn}); err != nil {
		t.Fatalf("SetPaneOutputActions failed: %v", err)
	}
}

// TestIntegrationClientRefreshExtended exercises per-window sizing, cursor reset,
// scroll adjustments, and client flag toggles on an attached control client.
func TestIntegrationClientRefreshExtended(t *testing.T) {
	server, session, ctx := apiFixture(t)

	conn, err := server.OpenControl(ctx, session, tmux.ControlOptions{
		PaneOutput: false,
	})
	if err != nil {
		t.Fatalf("OpenControl failed: %v", err)
	}
	defer conn.Close()

	client, err := conn.Client(ctx)
	if err != nil {
		t.Fatalf("conn.Client failed: %v", err)
	}

	links, err := session.Windows(ctx)
	if err != nil || len(links) == 0 {
		t.Fatal(err)
	}
	window := links[0].Window()

	// 1. Set per-window size on control client (-C @win:w,h)
	if err := client.SetWindowSize(ctx, window, tmux.Size{Width: 90, Height: 30}); err != nil {
		t.Fatalf("client.SetWindowSize failed: %v", err)
	}

	// 2. Clear per-window size (-C @win:)
	if err := client.ClearWindowSize(ctx, window); err != nil {
		t.Fatalf("client.ClearWindowSize failed: %v", err)
	}

	// 3. Reset cursor tracking (-c)
	if err := client.ResetCursorTracking(ctx); err != nil {
		t.Fatalf("client.ResetCursorTracking failed: %v", err)
	}

	// 4. Scroll viewport (-D 2)
	if err := client.Scroll(ctx, tmux.ScrollDown, 2); err != nil {
		t.Fatalf("client.Scroll failed: %v", err)
	}

	// 5. Refresh with negated flag (!read-only)
	if err := client.Refresh(ctx, tmux.RefreshOptions{
		Flags: []tmux.ClientFlag{tmux.ClientFlagReadOnly.Negate()},
	}); err != nil {
		t.Fatalf("client.Refresh with negated flag failed: %v", err)
	}

	// 6. Direct Connection convenience methods
	if err := conn.SetWindowSize(ctx, window, tmux.Size{Width: 95, Height: 35}); err != nil {
		t.Fatalf("conn.SetWindowSize failed: %v", err)
	}
	if err := conn.ClearWindowSize(ctx, window); err != nil {
		t.Fatalf("conn.ClearWindowSize failed: %v", err)
	}
	if err := conn.ResetCursorTracking(ctx); err != nil {
		t.Fatalf("conn.ResetCursorTracking failed: %v", err)
	}
	if err := conn.Scroll(ctx, tmux.ScrollUp, 1); err != nil {
		t.Fatalf("conn.Scroll failed: %v", err)
	}
	if err := conn.Refresh(ctx, tmux.RefreshOptions{StatusOnly: true}); err != nil {
		t.Fatalf("conn.Refresh failed: %v", err)
	}
}

// TestIntegrationControlExitLifecycle verifies that when the target session is terminated,
// the control connection receives %exit, transitions to closed, and conn.Wait unblocks cleanly.
func TestIntegrationControlExitLifecycle(t *testing.T) {
	server, session, ctx := apiFixture(t)

	conn, err := server.OpenControl(ctx, session, tmux.ControlOptions{
		PaneOutput: false,
	})
	if err != nil {
		t.Fatalf("OpenControl failed: %v", err)
	}
	defer conn.Close()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- conn.Wait(ctx)
	}()

	// Kill session to trigger native %exit
	if err := session.Kill(ctx); err != nil {
		t.Fatalf("session.Kill failed: %v", err)
	}

	select {
	case err := <-waitDone:
		if err != nil && !errors.Is(err, tmux.ErrClosed) {
			t.Fatalf("conn.Wait exited with unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("conn.Wait did not unblock within 5 seconds of session termination")
	}
}

func TestIntegrationDedicatedCommands(t *testing.T) {
	server, session, ctx := apiFixture(t)

	// 1. NewSession with SessionEnv and zero Program{} (default shell with native session environment)
	sess2, err := server.NewSession(ctx, tmux.NewSessionOptions{
		Name:       "custom-sess-env",
		SessionEnv: map[string]string{"CUSTOM_SESSION_VAR": "session_native_val"},
	})
	if err != nil {
		t.Fatalf("NewSession with SessionEnv failed: %v", err)
	}
	t.Cleanup(func() { _ = sess2.Kill(ctx) })

	envVal, err := sess2.Environment().Get(ctx, "CUSTOM_SESSION_VAR", false)
	if v, ok := envVal.Value.Get(); err != nil || !ok || v != "session_native_val" {
		t.Fatalf("expected session native env var, got %+v, err: %v", envVal, err)
	}

	// 2. NewWindowOptions: mutual exclusivity validation for Before, After, Index
	idx := 5
	_, err = session.NewWindow(ctx, tmux.NewWindowOptions{
		Name:   "invalid-placement",
		Index:  &idx,
		Before: true,
	})
	if !errors.Is(err, tmux.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for both Index and Before, got %v", err)
	}

	_, err = session.NewWindow(ctx, tmux.NewWindowOptions{
		Name:   "invalid-placement-2",
		Before: true,
		After:  true,
	})
	if !errors.Is(err, tmux.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for both Before and After, got %v", err)
	}

	// Valid NewWindow with Before: true and TmuxEnv
	win, err := session.NewWindow(ctx, tmux.NewWindowOptions{
		Name:    "custom-win-before",
		Before:  true,
		TmuxEnv: map[string]string{"CUSTOM_WIN_VAR": "win_val"},
	})
	if err != nil {
		t.Fatalf("NewWindow with Before and TmuxEnv failed: %v", err)
	}

	// 3. SplitOptions with TmuxEnv
	winPanes, err := win.Window().Panes(ctx)
	if err != nil || len(winPanes) == 0 {
		t.Fatalf("failed to query window panes: %v", err)
	}
	initialPane := winPanes[0].Handle()
	splitPane, err := initialPane.Split(ctx, tmux.SplitOptions{
		Direction: tmux.Horizontal,
		TmuxEnv:   map[string]string{"CUSTOM_SPLIT_VAR": "split_val"},
	})
	if err != nil {
		t.Fatalf("Split with TmuxEnv failed: %v", err)
	}

	// 4. Pane marks and unmarks
	if err := splitPane.Mark(ctx); err != nil {
		t.Fatalf("Pane.Mark failed: %v", err)
	}
	if err := splitPane.Unmark(ctx); err != nil {
		t.Fatalf("Pane.Unmark failed: %v", err)
	}

	// 5. Pane Swaps: SwapUp, SwapDown, SwapWith
	if err := splitPane.SwapUp(ctx); err != nil {
		t.Fatalf("Pane.SwapUp failed: %v", err)
	}
	if err := splitPane.SwapDown(ctx); err != nil {
		t.Fatalf("Pane.SwapDown failed: %v", err)
	}
	if err := splitPane.SwapWith(ctx, initialPane, tmux.SwapPaneOptions{Up: true}); err != nil {
		t.Fatalf("Pane.SwapWith failed: %v", err)
	}

	if err := splitPane.ResizeRelative(ctx, 2, tmux.ResizeDown); err != nil {
		t.Fatalf("Pane.ResizeRelative failed: %v", err)
	}
	if err := splitPane.ToggleZoom(ctx); err != nil {
		t.Fatalf("Pane.ToggleZoom failed: %v", err)
	}
	if err := splitPane.ToggleZoom(ctx); err != nil {
		t.Fatalf("Pane.ToggleZoom back failed: %v", err)
	}

	// 7. JoinOptions with FullSize
	if err := splitPane.Join(ctx, initialPane, tmux.JoinOptions{
		Direction: tmux.Vertical,
		FullSize:  true,
	}); err != nil {
		t.Fatalf("Pane.Join with FullSize failed: %v", err)
	}

	// 8. Window.Rotate
	if err := win.Window().Rotate(ctx, false); err != nil {
		t.Fatalf("Window.Rotate failed: %v", err)
	}
	if err := win.Window().Rotate(ctx, true); err != nil {
		t.Fatalf("Window.Rotate reverse failed: %v", err)
	}

	// 9. Pane.ClearHistory
	if err := initialPane.ClearHistory(ctx); err != nil {
		t.Fatalf("Pane.ClearHistory failed: %v", err)
	}

	// 10. Session.RenumberWindows and ClearAlerts
	if err := session.RenumberWindows(ctx); err != nil {
		t.Fatalf("Session.RenumberWindows failed: %v", err)
	}
	if err := session.ClearAlerts(ctx); err != nil {
		t.Fatalf("Session.ClearAlerts failed: %v", err)
	}
	if err := session.LockScreen(ctx); err != nil {
		t.Fatalf("Session.LockScreen failed: %v", err)
	}

	// 11. Navigation options: Window.SelectWith and Pane.SelectWith
	if err := win.Window().SelectWith(ctx, tmux.SelectWindowOptions{}); err != nil {
		t.Fatalf("Window.SelectWith failed: %v", err)
	}
	if err := initialPane.SelectWith(ctx, tmux.SelectPaneOptions{PreserveZoom: true}); err != nil {
		t.Fatalf("Pane.SelectWith failed: %v", err)
	}

	// 12. Session collections: Windows, Panes, Clients
	wins, err := session.Windows(ctx)
	if err != nil || len(wins) == 0 {
		t.Fatalf("Session.Windows failed: %v, count: %d", err, len(wins))
	}
	panes, err := session.Panes(ctx)
	if err != nil || len(panes) == 0 {
		t.Fatalf("Session.Panes failed: %v, count: %d", err, len(panes))
	}
	clients, err := session.Clients(ctx)
	if err != nil {
		t.Fatalf("Session.Clients failed: %v", err)
	}
	_ = clients

	// 13. CaptureToBuffer and CaptureOptions
	bufName := "capture-buf-test"
	if err := initialPane.CaptureToBuffer(ctx, tmux.CaptureOptions{
		Buffer:         bufName,
		IncludeEscapes: true,
		PaneState:      false,
		Quiet:          true,
	}); err != nil {
		t.Fatalf("CaptureToBuffer failed: %v", err)
	}
	bCapt, err := tmux.NamedBuffer(bufName)
	if err != nil {
		t.Fatalf("NamedBuffer failed: %v", err)
	}
	if _, err := server.ReadBuffer(ctx, bCapt); err != nil {
		t.Fatalf("ReadBuffer after CaptureToBuffer failed: %v", err)
	}

	// 14. SendKeysWith and SendTextWith
	if err := initialPane.SendKeysWith(ctx, tmux.SendKeysOptions{
		Reset:       true,
		RepeatCount: 1,
	}, tmux.Key("Enter")); err != nil {
		t.Fatalf("SendKeysWith failed: %v", err)
	}
	if err := initialPane.SendTextWith(ctx, tmux.SendKeysOptions{
		ExpandFormat: true,
	}, "#{session_name}"); err != nil {
		t.Fatalf("SendTextWith failed: %v", err)
	}

	// 15. Session.KillOtherWindows and Server.KillOtherSessions
	if err := session.KillOtherWindows(ctx); err != nil {
		t.Fatalf("Session.KillOtherWindows failed: %v", err)
	}
	if err := server.KillOtherSessions(ctx, session.ID()); err != nil {
		t.Fatalf("Server.KillOtherSessions failed: %v", err)
	}
}

func TestIntegrationDaemonSignals(t *testing.T) {
	server, _, ctx := apiFixture(t)

	// 1. Guarded RecreateSocket (SIGUSR1) on running daemon
	if err := server.RecreateSocket(ctx); err != nil {
		t.Fatalf("RecreateSocket on running daemon failed: %v", err)
	}

	// 2. Guarded ToggleLogging (SIGUSR2) on running daemon
	if err := server.ToggleLogging(ctx); err != nil {
		t.Fatalf("ToggleLogging on running daemon failed: %v", err)
	}

	sEmpty, err := tmux.New(tmux.Config{SocketName: "nonexistent-signal-test"})
	if err != nil {
		t.Fatalf("tmux.New failed: %v", err)
	}

	if err := sEmpty.RecreateSocket(ctx); !errors.Is(err, tmux.ErrNoServer) {
		t.Fatalf("expected ErrNoServer from RecreateSocket on non-running server, got: %v", err)
	}
	if err := sEmpty.ToggleLogging(ctx); !errors.Is(err, tmux.ErrNoServer) {
		t.Fatalf("expected ErrNoServer from ToggleLogging on non-running server, got: %v", err)
	}
}

func TestIntegrationRecreateSocket_ConcurrentLoad(t *testing.T) {
	server, session, ctx := apiFixture(t)

	var wg sync.WaitGroup
	errCh := make(chan error, 16)
	stop := make(chan struct{})

	// Launch 6 workers executing continuous commands
	for i := range 6 {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			cmd, err := tmux.NewCommand("display-message", "-t", string(session.ID()), fmt.Sprintf("load_worker_%d", workerID))
			if err != nil {
				errCh <- err
				return
			}

			for {
				select {
				case <-stop:
					return
				default:
					if _, err := server.RunWith(ctx, cmd, tmux.RunOptions{Start: tmux.ExistingOnly}); err != nil {
						errCh <- err
						return
					}
					time.Sleep(2 * time.Millisecond)
				}
			}
		}(i)
	}

	// Trigger RecreateSocket twice during active load
	time.Sleep(20 * time.Millisecond)
	if err := server.RecreateSocket(ctx); err != nil {
		t.Fatalf("first RecreateSocket failed: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	if err := server.RecreateSocket(ctx); err != nil {
		t.Fatalf("second RecreateSocket failed: %v", err)
	}

	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("worker encountered error during socket recreation: %v", err)
		}
	}

	// Verify subsequent operations succeed on recreated socket
	if _, err := server.Panes(ctx); err != nil {
		t.Fatalf("server.Panes failed after socket recreation: %v", err)
	}
}
