//go:build integration

package tmux_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
	path := filepath.Join(t.TempDir(), "absent")
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
