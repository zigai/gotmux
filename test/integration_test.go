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

	session := exactFixtureSession(t, ctx, s)

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

	record := setAndReadTitle(t, ctx, split, "quotes '$; #{pane_id} #[style]")

	if e = link.Window().SelectLayout(ctx, tmux.LayoutTiled); e != nil {
		t.Fatal(e)
	}

	data, e := pane.Capture(ctx, tmux.CaptureOptions{JoinWrapped: true})
	if e != nil || data == nil {
		t.Fatalf("capture %q: %v", data, e)
	}

	assertConsistentSnapshot(t, ctx, s, 3)

	original := record.Handle()

	record.ID = "%999"
	if !record.Handle().Equal(original) {
		t.Fatal("record mutation retargeted handle")
	}
}

func exactFixtureSession(t *testing.T, ctx context.Context, s *tmux.Server) tmux.Session {
	t.Helper()

	session, e := s.FindSession(ctx, "fixture")
	if e != nil {
		t.Fatal(e)
	}

	if _, e = s.FindSession(ctx, "fixt"); !errors.Is(e, tmux.ErrNotFound) {
		t.Fatalf("prefix unexpectedly matched: %v", e)
	}

	return session
}

func setAndReadTitle(t *testing.T, ctx context.Context, pane tmux.Pane, title string) tmux.PaneInfo {
	t.Helper()

	if e := pane.SetTitle(ctx, title); e != nil {
		t.Fatal(e)
	}

	record, e := pane.Info(ctx)
	if e != nil || record.Title != title {
		t.Fatal(record, e)
	}

	return record
}

func assertConsistentSnapshot(t *testing.T, ctx context.Context, s *tmux.Server, panes int) {
	t.Helper()

	snap, e := s.Snapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}

	if snap.Consistency != tmux.ConsistencyComplete || len(snap.Panes()) != panes {
		t.Fatalf("snapshot: %d panes, missing %+v", len(snap.Panes()), snap.MissingReferences())
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

	inherited := paneUserOption(t, ctx, pane, "@probe")
	if v, ok := inherited.Effective.Get(); !ok || v != "global" || inherited.Local.State() != tmux.ValueStateUnavailable {
		t.Fatal(inherited)
	}

	if e := pane.Options().SetUser(ctx, "@probe", ""); e != nil {
		t.Fatal(e)
	}

	if local := paneUserOption(t, ctx, pane, "@probe"); local.Local != tmux.PresentValue("") {
		t.Fatal(local)
	}

	if e := pane.Options().UnsetUser(ctx, "@probe"); e != nil {
		t.Fatal(e)
	}

	inherited = paneUserOption(t, ctx, pane, "@probe")
	if v, ok := inherited.Effective.Get(); !ok || v != "global" {
		t.Fatal(inherited)
	}

	session, e := s.FindSession(ctx, "fixture")
	if e != nil {
		t.Fatal(e)
	}

	assertHistoryLimitRoundTrip(t, ctx, session, 5000)
}

func paneUserOption(t *testing.T, ctx context.Context, pane tmux.Pane, name string) tmux.OptionValue[string] {
	t.Helper()

	value, e := pane.Options().User(ctx, name)
	if e != nil {
		t.Fatal(e)
	}

	return value
}

func assertHistoryLimitRoundTrip(t *testing.T, ctx context.Context, session tmux.Session, limit int) {
	t.Helper()

	if e := session.Options().SetHistoryLimit(ctx, limit); e != nil {
		t.Fatal(e)
	}

	value, e := session.Options().HistoryLimit(ctx)
	if e != nil {
		t.Fatal(e)
	}

	if v, ok := value.Effective.Get(); !ok || v != limit {
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

	old := pa.ServerIdentity()
	if e = a.KillMatching(ctx, old); e != nil {
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

		_ = a.KillMatching(c, replacement.ServerIdentity())
	})

	if e = a.KillMatching(ctx, old); !errors.Is(e, tmux.ErrServerChanged) {
		t.Fatalf("stale identity was not rejected: %v", e)
	}

	if e = pa.Kill(ctx); !errors.Is(e, tmux.ErrServerChanged) {
		t.Fatalf("replacement not rejected: %v", e)
	}

	if _, e = replacement.Info(ctx); e != nil {
		t.Fatal("replacement was touched", e)
	}
}

func TestIntegrationControlParityAndExplicitSubprocess(t *testing.T) {
	s := tmuxtest.NewServer(t)
	ctx := integrationContext(t)

	session, e := s.FindSession(ctx, "fixture")
	if e != nil {
		t.Fatal(e)
	}

	conn := apiControl(t, s, session, ctx)

	stream, e := conn.Events(ctx, tmux.EventOptions{})
	if e != nil {
		t.Fatal(e)
	}

	t.Cleanup(func() { _ = stream.Close() })

	plain, controlled := transportPanes(t, ctx, s, conn)

	pane := controlled.Handle()
	if _, e = pane.Capture(ctx, tmux.CaptureOptions{}); !errors.Is(e, tmux.ErrTransportUnsupported) {
		t.Fatalf("unsafe control capture accepted: %v", e)
	}

	aux, e := pane.ViaSubprocess()
	if e != nil {
		t.Fatal(e)
	}

	assertSameCapture(t, ctx, aux, plain.Handle())

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
		t.Fatalf("subprocess handle outlived connection: %v", e)
	}
}

func transportPanes(t *testing.T, ctx context.Context, s *tmux.Server, conn *tmux.Connection) (tmux.PaneInfo, tmux.PaneInfo) {
	t.Helper()

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

	return plain[0], controlled[0]
}

func assertSameCapture(t *testing.T, ctx context.Context, a, b tmux.Pane) {
	t.Helper()

	first, e := a.Capture(ctx, tmux.CaptureOptions{})
	if e != nil {
		t.Fatal(e)
	}

	second, e := b.Capture(ctx, tmux.CaptureOptions{})
	if e != nil || !bytes.Equal(first, second) {
		t.Fatal("subprocess capture mismatch", e)
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

	if _, e = s.NewSession(ctx, tmux.NewSessionOptions{Start: tmux.StartPolicyExistingOnly}); !errors.Is(e, tmux.ErrNoServer) {
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

	awaitFile(t, ctx, resultFile)
	assertFileBytes(t, resultFile, []byte(envVal))

	envScope := session.Environment()

	envValResult, err := envScope.Get(ctx, envKey)
	if err != nil {
		t.Fatalf("session.Environment().Get failed: %v", err)
	}

	if val, ok := envValResult.Value.Get(); ok {
		t.Fatalf("expected %s NOT to be set in tmux session environment, got %q", envKey, val)
	}
}

// TestIntegrationRunWithStartPolicy verifies that RunWith explicitly controls server startup policy
// via StartPolicy (StartPolicyAllowStart vs StartPolicyExistingOnly) independently of command names or aliases.
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
		LogLevel:         tmux.LogLevelNone,
		LoginShell:       false,
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd, err := tmux.NewCommand("new-session", "-d", "-s", "s1")
	if err != nil {
		t.Fatal(err)
	}

	_, err = server.RunWith(ctx, cmd, tmux.RunOptions{Start: tmux.StartPolicyExistingOnly})
	if !errors.Is(err, tmux.ErrNoServer) {
		t.Fatalf("expected ErrNoServer for StartPolicyExistingOnly on unstarted server, got %v", err)
	}

	aliasCmd, err := tmux.NewCommand("new", "-d", "-s", "s-alias")
	if err != nil {
		t.Fatal(err)
	}

	_, err = server.RunWith(ctx, aliasCmd, tmux.RunOptions{Start: tmux.StartPolicyAllowStart})
	if err != nil {
		t.Fatalf("RunWith with StartPolicyAllowStart failed: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = server.Kill(cleanupCtx)
	})

	sess, err := server.FindSession(ctx, "s-alias")
	if err != nil {
		t.Fatalf("FindSession failed after started: %v", err)
	}

	_ = sess

	seqCmd, err := tmux.NewCommand("display-message", "-p", "alive")
	if err != nil {
		t.Fatal(err)
	}

	seq, err := tmux.Sequence(seqCmd)
	if err != nil {
		t.Fatal(err)
	}

	seqRes, err := server.RunSequenceWith(ctx, seq, tmux.RunOptions{Start: tmux.StartPolicyExistingOnly})
	if err != nil {
		t.Fatalf("RunSequenceWith failed: %v", err)
	}

	if !bytes.Contains(seqRes.Stdout, []byte("alive")) {
		t.Fatalf("expected 'alive' in output, got %q", string(seqRes.Stdout))
	}
}

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
		LogLevel:         tmux.LogLevelVerbose,
		LoginShell:       false,
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd, err := tmux.NewCommand("new-session", "-d", "-s", "root-flags-sess")
	if err != nil {
		t.Fatal(err)
	}

	_, err = server.RunWith(ctx, cmd, tmux.RunOptions{Start: tmux.StartPolicyAllowStart})
	if err != nil {
		t.Fatalf("RunWith failed: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = server.Kill(cleanupCtx)
	})

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

// TestIntegrationSubprocessServerNeverAutoSpawns verifies that a connection's subprocess servers
// strictly forbid auto-spawning replacement daemons across all execution paths.
func TestIntegrationSubprocessServerNeverAutoSpawns(t *testing.T) {
	server, session, ctx := apiFixture(t)

	connection, err := server.OpenControl(ctx, session, tmux.ControlOptions{PaneOutput: false, QueuedBytes: 0})
	if err != nil {
		t.Fatal(err)
	}

	subprocess := connection.SubprocessServer()

	pingCmd, err := tmux.NewCommand("display-message", "-p", "alive")
	if err != nil {
		t.Fatal(err)
	}

	res, err := subprocess.RunWith(ctx, pingCmd, tmux.RunOptions{Start: tmux.StartPolicyAllowStart})
	if err != nil {
		t.Fatalf("subprocess RunWith while alive failed: %v", err)
	}

	if !bytes.Contains(res.Stdout, []byte("alive")) {
		t.Fatalf("expected 'alive', got %q", string(res.Stdout))
	}

	_ = server.Kill(ctx)
	_ = connection.Close()

	leakCmd, err := tmux.NewCommand("new-session", "-d", "-s", "leak-attempt")
	if err != nil {
		t.Fatal(err)
	}

	_, err = subprocess.Run(ctx, leakCmd)
	if err == nil {
		t.Fatal("expected subprocess.Run to fail after daemon killed")
	}

	_, err = subprocess.RunWith(ctx, leakCmd, tmux.RunOptions{Start: tmux.StartPolicyAllowStart})
	if err == nil {
		t.Fatal("expected subprocess.RunWith to fail after daemon killed")
	}

	_, err = server.Panes(ctx)
	if !errors.Is(err, tmux.ErrNoServer) {
		t.Fatalf("expected ErrNoServer, indicating no daemon was spawned, got %v", err)
	}
}

// TestIntegrationSplitWindowStdin verifies that raw RunWith executing "split-window -I"
// passes stdin bytes into an empty pane, creating a dead pane upon EOF with the exact content.
func TestIntegrationSplitWindowStdin(t *testing.T) {
	server, session, ctx := apiFixture(t)
	window, pane := firstWindowPane(t, ctx, session)

	wantLines := "first line from split-window stdin\nsecond line with #{special} syntax\n"

	cmd, err := tmux.NewCommand("split-window", "-d", "-I", "-t", string(pane.ID()))
	if err != nil {
		t.Fatalf("NewCommand failed: %v", err)
	}

	_, err = server.RunWith(ctx, cmd, tmux.RunOptions{Input: []byte(wantLines)})
	if err != nil {
		t.Fatalf("RunWith split-window -I failed: %v", err)
	}

	targetPane := otherPane(t, ctx, server, window, pane)
	awaitCaptureContains(t, ctx, targetPane, "first line from split-window stdin", "second line with #{special} syntax")
}

// TestIntegrationDisplayMessageStdin verifies that raw RunWith executing "display-message -I"
// delivers standard input bytes into an existing empty pane and observes them.
func TestIntegrationDisplayMessageStdin(t *testing.T) {
	server, session, ctx := apiFixture(t)
	window, pane := firstWindowPane(t, ctx, session)

	splitCmd, err := tmux.NewCommand("split-window", "-d", "-t", string(pane.ID()), "")
	if err != nil {
		t.Fatalf("NewCommand split-window failed: %v", err)
	}

	if _, err := server.Run(ctx, splitCmd); err != nil {
		t.Fatalf("Run split-window failed: %v", err)
	}

	emptyPane := otherPane(t, ctx, server, window, pane)

	wantText := "hello to empty pane from display-message -I\nsecond line"

	displayCmd, err := tmux.NewCommand("display-message", "-I", "-t", string(emptyPane.ID()))
	if err != nil {
		t.Fatalf("NewCommand display-message failed: %v", err)
	}

	_, err = server.RunWith(ctx, displayCmd, tmux.RunOptions{Input: []byte(wantText)})
	if err != nil {
		t.Fatalf("RunWith display-message -I failed: %v", err)
	}

	awaitCaptureContains(t, ctx, emptyPane, "hello to empty pane from display-message -I", "second line")
}

func firstWindowPane(t *testing.T, ctx context.Context, session tmux.Session) (tmux.Window, tmux.Pane) {
	t.Helper()

	links, err := session.Windows(ctx)
	if err != nil || len(links) == 0 {
		t.Fatalf("session.Windows = %d links, %v", len(links), err)
	}

	window := links[0].Window()

	pane, err := window.ActivePane(ctx)
	if err != nil {
		t.Fatalf("ActivePane failed: %v", err)
	}

	return window, pane
}

func otherPane(t *testing.T, ctx context.Context, server *tmux.Server, window tmux.Window, known tmux.Pane) tmux.Pane {
	t.Helper()

	panes, err := window.Panes(ctx)
	if err != nil {
		t.Fatalf("window.Panes failed: %v", err)
	}

	for _, p := range panes {
		if p.ID == known.ID() {
			continue
		}

		pane, err := server.Pane(ctx, p.ID)
		if err != nil {
			t.Fatalf("server.Pane failed: %v", err)
		}

		return pane
	}

	t.Fatalf("window has no pane besides %s", known.ID())

	return tmux.Pane{}
}

func awaitCaptureContains(t *testing.T, ctx context.Context, pane tmux.Pane, want ...string) {
	t.Helper()

	ticker := time.NewTicker(observationInterval)
	defer ticker.Stop()

	for {
		captured, err := pane.Capture(ctx, tmux.CaptureOptions{})
		if err == nil && containsAll(string(captured), want) {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("pane %s never showed %q; last capture %q (%v)", pane.ID(), want, captured, err)
		case <-ticker.C:
		}
	}
}

func containsAll(text string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}

	return true
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
	_, _ = server.RunSequenceWith(ctx, seq, tmux.RunOptions{Start: tmux.StartPolicyAllowStart, Input: inputData})

	got, err := server.ReadBuffer(ctx, b1)
	if err != nil {
		t.Fatalf("ReadBuffer b1 failed: %v", err)
	}

	if !bytes.Equal(got, inputData) {
		t.Fatalf("buffer data mismatch: got %q, want %q", got, inputData)
	}

	if _, err := server.ReadBuffer(ctx, b2); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("expected buffer 2 not to exist because stdin was consumed by command 1, got %v", err)
	}
}

func TestIntegrationPrepareCommandFileStreaming(t *testing.T) {
	server, _, ctx := apiFixture(t)

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	closeOnCleanup(t, stdinR)
	closeOnCleanup(t, stdinW)

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	closeOnCleanup(t, stdoutR)
	closeOnCleanup(t, stdoutW)

	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	closeOnCleanup(t, stderrR)
	closeOnCleanup(t, stderrW)

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

	done := startForegroundServer(t, s)
	awaitServerReady(t, s)

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

func startForegroundServer(t *testing.T, s *tmux.Server) chan error {
	t.Helper()

	fgCtx, cancel := context.WithCancel(t.Context())

	cmd, err := s.PrepareForegroundServer(fgCtx)
	if err != nil {
		cancel()
		t.Fatalf("PrepareForegroundServer failed: %v", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
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

	return done
}

func awaitServerReady(t *testing.T, s *tmux.Server) {
	t.Helper()

	readyCtx, readyCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer readyCancel()

	awaitObservation(t, readyCtx, "foreground server ready", func() bool {
		_, err := s.Probe(readyCtx)

		return err == nil
	})
}

func TestIntegrationRootShell(t *testing.T) {
	server, _, ctx := apiFixture(t)

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

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	closeOnCleanup(t, r)
	closeOnCleanup(t, w)

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

	_, err = server.RunWith(ctx, cmd, tmux.RunOptions{Start: tmux.StartPolicyAllowStart, Input: []byte{}})
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

	if !newPane.Valid() {
		t.Fatal("expected valid new pane")
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

	closeOnCleanup(t, conn)

	info, err := session.Info(ctx)
	if err != nil {
		t.Fatalf("session.Info failed: %v", err)
	}

	if info.Name != sessName {
		t.Errorf("expected session name %q, got %q", sessName, info.Name)
	}

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

	closeOnCleanup(t, conn)

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

func TestIntegrationSetPaneOutputAction(t *testing.T) {
	server, session, ctx := apiFixture(t)

	conn, err := server.OpenControl(ctx, session, tmux.ControlOptions{
		PaneOutput: true,
	})
	if err != nil {
		t.Fatalf("OpenControl failed: %v", err)
	}

	closeOnCleanup(t, conn)

	stream, err := conn.Events(ctx, tmux.EventOptions{})
	if err != nil {
		t.Fatalf("conn.Events failed: %v", err)
	}

	closeOnCleanup(t, stream)

	_, pane := firstWindowPane(t, ctx, session)

	boundPane, err := conn.Server().Pane(ctx, pane.ID())
	if err != nil {
		t.Fatal(err)
	}

	if err := conn.SetPaneOutputAction(ctx, boundPane, tmux.PaneOutputPause); err != nil {
		t.Fatalf("SetPaneOutputAction pause failed: %v", err)
	}

	paused := awaitEvent(ctx, stream, func(ev tmux.Event) bool {
		pe, ok := ev.(tmux.PanePauseEvent)

		return ok && pe.PaneID == pane.ID()
	})
	if !paused {
		t.Errorf("expected PanePauseEvent for pane %s", pane.ID())
	}

	if err := conn.SetPaneOutputAction(ctx, boundPane, tmux.PaneOutputContinue); err != nil {
		t.Fatalf("SetPaneOutputAction continue failed: %v", err)
	}

	continued := awaitEvent(ctx, stream, func(ev tmux.Event) bool {
		ce, ok := ev.(tmux.PaneContinueEvent)

		return ok && ce.PaneID == pane.ID()
	})
	if !continued {
		t.Errorf("expected PaneContinueEvent for pane %s", pane.ID())
	}

	if err := conn.SetPaneOutputActions(ctx, tmux.PaneOutputSetting{Pane: boundPane, Action: tmux.PaneOutputOn}); err != nil {
		t.Fatalf("SetPaneOutputActions failed: %v", err)
	}
}

func awaitEvent(ctx context.Context, stream *tmux.EventStream, match func(tmux.Event) bool) bool {
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	for {
		ev, err := stream.Next(waitCtx)
		if err != nil {
			return false
		}

		if match(ev) {
			return true
		}
	}
}

func TestIntegrationClientRefreshExtended(t *testing.T) {
	server, session, ctx := apiFixture(t)

	conn, err := server.OpenControl(ctx, session, tmux.ControlOptions{
		PaneOutput: false,
	})
	if err != nil {
		t.Fatalf("OpenControl failed: %v", err)
	}

	closeOnCleanup(t, conn)

	client, err := conn.Client(ctx)
	if err != nil {
		t.Fatalf("conn.Client failed: %v", err)
	}

	window, _ := firstWindowPane(t, ctx, session)

	runSteps(t, []namedStep{
		{"client.SetWindowSize", func() error { return client.SetWindowSize(ctx, window, tmux.Size{Width: 90, Height: 30}) }},
		{"client.ClearWindowSize", func() error { return client.ClearWindowSize(ctx, window) }},
		{"client.ResetCursorTracking", func() error { return client.ResetCursorTracking(ctx) }},
		{"client.Scroll", func() error { return client.Scroll(ctx, tmux.ScrollDirectionDown, 2) }},
		{"client.Refresh with negated flag", func() error {
			return client.Refresh(ctx, tmux.RefreshOptions{Flags: []tmux.ClientFlag{tmux.ClientFlagReadOnly.Negate()}})
		}},
		{"conn.SetWindowSize", func() error { return conn.SetWindowSize(ctx, window, tmux.Size{Width: 95, Height: 35}) }},
		{"conn.ClearWindowSize", func() error { return conn.ClearWindowSize(ctx, window) }},
		{"conn.ResetCursorTracking", func() error { return conn.ResetCursorTracking(ctx) }},
		{"conn.Scroll", func() error { return conn.Scroll(ctx, tmux.ScrollDirectionUp, 1) }},
		{"conn.Refresh", func() error { return conn.Refresh(ctx, tmux.RefreshOptions{StatusOnly: true}) }},
	})
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

	closeOnCleanup(t, conn)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- conn.Wait(ctx)
	}()

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

	assertSessionTmuxEnv(t, ctx, server)
	assertPlacementConflictsRejected(t, ctx, session)

	win, err := session.NewWindow(ctx, tmux.NewWindowOptions{
		Name:    "custom-win-before",
		Before:  true,
		TmuxEnv: map[string]string{"CUSTOM_WIN_VAR": "win_val"},
	})
	if err != nil {
		t.Fatalf("NewWindow with Before and TmuxEnv failed: %v", err)
	}

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

	runSteps(t, []namedStep{
		{"Pane.Mark", func() error { return splitPane.Mark(ctx) }},
		{"Pane.Unmark", func() error { return splitPane.Unmark(ctx) }},
		{"Pane.SwapUp", func() error { return splitPane.SwapUp(ctx) }},
		{"Pane.SwapDown", func() error { return splitPane.SwapDown(ctx) }},
		{"Pane.Swap", func() error { return splitPane.Swap(ctx, initialPane, tmux.SwapPaneOptions{Select: true}) }},
		{"Pane.ResizeRelative", func() error { return splitPane.ResizeRelative(ctx, 2, tmux.ResizeDirectionDown) }},
		{"Pane.ToggleZoom", func() error { return splitPane.ToggleZoom(ctx) }},
		{"Pane.ToggleZoom back", func() error { return splitPane.ToggleZoom(ctx) }},
		{"Pane.Join with FullSize", func() error {
			return splitPane.Join(ctx, initialPane, tmux.JoinOptions{Direction: tmux.Vertical, FullSize: true})
		}},
		{"Window.Rotate", func() error { return win.Window().Rotate(ctx, false) }},
		{"Window.Rotate reverse", func() error { return win.Window().Rotate(ctx, true) }},
		{"Pane.ClearHistory", func() error { return initialPane.ClearHistory(ctx) }},
		{"Session.RenumberWindows", func() error { return session.RenumberWindows(ctx) }},
		{"Session.ClearAlerts", func() error { return session.ClearAlerts(ctx) }},
		{"Session.LockScreen", func() error { return session.LockScreen(ctx) }},
		{"Window.SelectWith", func() error { return win.Window().SelectWith(ctx, tmux.SelectWindowOptions{}) }},
		{"Pane.SelectWith", func() error { return initialPane.SelectWith(ctx, tmux.SelectPaneOptions{PreserveZoom: true}) }},
		{"Session collections", func() error { return sessionCollectionsListed(ctx, session) }},
		{"CaptureToBuffer and ReadBuffer", func() error { return captureThroughBuffer(ctx, server, initialPane, "capture-buf-test") }},
		{"SendKeysWith", func() error {
			return initialPane.SendKeysWith(ctx, tmux.SendKeysOptions{Reset: true, RepeatCount: 1}, tmux.Key("Enter"))
		}},
		{"SendTextWith", func() error {
			return initialPane.SendTextWith(ctx, tmux.SendKeysOptions{ExpandFormat: true}, "#{session_name}")
		}},
		{"Session.KillOtherWindows", func() error { return session.KillOtherWindows(ctx) }},
		{"Server.KillOtherSessions", func() error { return server.KillOtherSessions(ctx, session.ID()) }},
	})
}

func assertSessionTmuxEnv(t *testing.T, ctx context.Context, server *tmux.Server) {
	t.Helper()

	sess, err := server.NewSession(ctx, tmux.NewSessionOptions{
		Name:    "custom-sess-env",
		TmuxEnv: map[string]string{"CUSTOM_SESSION_VAR": "session_native_val"},
	})
	if err != nil {
		t.Fatalf("NewSession with TmuxEnv failed: %v", err)
	}

	t.Cleanup(func() { _ = sess.Kill(ctx) })

	envVal, err := sess.Environment().Get(ctx, "CUSTOM_SESSION_VAR")
	if v, ok := envVal.Value.Get(); err != nil || !ok || v != "session_native_val" {
		t.Fatalf("expected session native env var, got %+v, err: %v", envVal, err)
	}
}

func assertPlacementConflictsRejected(t *testing.T, ctx context.Context, session tmux.Session) {
	t.Helper()

	idx := 5

	_, err := session.NewWindow(ctx, tmux.NewWindowOptions{
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
}

func sessionCollectionsListed(ctx context.Context, session tmux.Session) error {
	wins, err := session.Windows(ctx)
	if err != nil {
		return fmt.Errorf("Session.Windows: %w", err)
	}

	panes, err := session.Panes(ctx)
	if err != nil {
		return fmt.Errorf("Session.Panes: %w", err)
	}

	if len(wins) == 0 || len(panes) == 0 {
		return fmt.Errorf("%d windows and %d panes: %w", len(wins), len(panes), errEmptyResult)
	}

	if _, err := session.Clients(ctx); err != nil {
		return fmt.Errorf("Session.Clients: %w", err)
	}

	return nil
}

func captureThroughBuffer(ctx context.Context, server *tmux.Server, pane tmux.Pane, name string) error {
	err := pane.CaptureToBuffer(ctx, tmux.CaptureOptions{
		Buffer:         name,
		IncludeEscapes: true,
		PaneState:      false,
		Quiet:          true,
	})
	if err != nil {
		return fmt.Errorf("CaptureToBuffer: %w", err)
	}

	buffer, err := tmux.NamedBuffer(name)
	if err != nil {
		return fmt.Errorf("NamedBuffer: %w", err)
	}

	if _, err := server.ReadBuffer(ctx, buffer); err != nil {
		return fmt.Errorf("ReadBuffer: %w", err)
	}

	return nil
}

func TestIntegrationDaemonSignals(t *testing.T) {
	server, _, ctx := apiFixture(t)

	if err := server.RecreateSocket(ctx); err != nil {
		t.Fatalf("RecreateSocket on running daemon failed: %v", err)
	}

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

	for i := range 6 {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			if err := displayUntilStopped(ctx, server, session, workerID, stop); err != nil {
				errCh <- err
			}
		}(i)
	}

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
		t.Fatalf("worker encountered error during socket recreation: %v", err)
	}

	if _, err := server.Panes(ctx); err != nil {
		t.Fatalf("server.Panes failed after socket recreation: %v", err)
	}
}

func displayUntilStopped(ctx context.Context, server *tmux.Server, session tmux.Session, workerID int, stop <-chan struct{}) error {
	cmd, err := tmux.NewCommand("display-message", "-t", string(session.ID()), fmt.Sprintf("load_worker_%d", workerID))
	if err != nil {
		return fmt.Errorf("NewCommand: %w", err)
	}

	for {
		select {
		case <-stop:
			return nil
		default:
		}

		if err := runWithRetries(ctx, server, cmd, 5); err != nil {
			return err
		}

		time.Sleep(2 * time.Millisecond)
	}
}

func runWithRetries(ctx context.Context, server *tmux.Server, cmd tmux.Command, attempts int) error {
	var err error

	for range attempts {
		if _, err = server.RunWith(ctx, cmd, tmux.RunOptions{Start: tmux.StartPolicyExistingOnly}); err == nil {
			return nil
		}

		time.Sleep(5 * time.Millisecond)
	}

	return fmt.Errorf("display-message after %d attempts: %w", attempts, err)
}
