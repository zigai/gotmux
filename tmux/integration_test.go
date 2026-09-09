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

func integrationContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func firstPane(t *testing.T, s *tmux.Server, ctx context.Context) tmux.Pane {
	t.Helper()
	panes, e := s.Panes(ctx)
	if e != nil || len(panes) == 0 {
		t.Fatalf("panes: %v (%d)", e, len(panes))
	}
	return panes[0].Handle()
}

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
