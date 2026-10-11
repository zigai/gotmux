//go:build integration

package test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

func TestIntegrationControlParityAndExplicitSubprocess(t *testing.T) {
	s := tmuxtest.NewServer(t)
	ctx := integrationContext(t)

	session, e := s.FindSession(ctx, "fixture")
	if e != nil {
		t.Fatal(e)
	}

	conn := apiControl(t, ctx, s, session)

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

func TestIntegrationOpenControlMissingSessionIsNotFound(t *testing.T) {
	server, _, ctx := apiFixture(t)

	if _, err := server.OpenControl(ctx, "$999", tmux.ControlOptions{}); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("missing session: %v, want ErrNotFound", err)
	}

	if clients, err := server.Clients(ctx); err != nil || len(clients) != 0 {
		t.Fatalf("clients after failed open = %d %v, want none", len(clients), err)
	}
}

func TestIntegrationOpenControlBindsAnsweringDaemon(t *testing.T) {
	server, session, ctx := apiFixture(t)
	conn := apiControl(t, ctx, server, session)

	probe, err := server.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	sessions, err := conn.Server().Sessions(ctx)
	if err != nil || len(sessions) == 0 {
		t.Fatal("control sessions", err)
	}

	bound := sessions[0].Handle().ServerIdentity()
	if bound.PID != probe.Identity.PID || !bound.Started.Equal(probe.Identity.Started) || bound.ReportedSocket != probe.Identity.ReportedSocket || bound.Generation == 0 {
		t.Fatalf("control-bound identity = %+v, daemon = %+v", bound, probe.Identity)
	}

	version, err := conn.Server().Version(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if version.Major != probe.Version.Major || version.Minor != probe.Version.Minor {
		t.Fatalf("control version = %+v, daemon reports %+v", version, probe.Version)
	}

	if _, err := conn.SubprocessServer().OpenControl(ctx, session.ID(), tmux.ControlOptions{}); !errors.Is(err, tmux.ErrTransportUnsupported) {
		t.Fatalf("open from bound server: %v, want ErrTransportUnsupported", err)
	}
}

// TestIntegrationSubprocessServerNeverAutoSpawns verifies that a connection's subprocess servers
// strictly forbid auto-spawning replacement daemons across all execution paths.
func TestIntegrationSubprocessServerNeverAutoSpawns(t *testing.T) {
	server, session, ctx := apiFixture(t)

	connection, err := server.OpenControl(ctx, session.ID(), tmux.ControlOptions{PaneOutput: false, QueuedBytes: 0})
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

// TestIntegrationControlExitLifecycle verifies that when the target session is terminated,
// the control connection receives %exit, transitions to closed, and conn.Wait unblocks cleanly.
func TestIntegrationControlExitLifecycle(t *testing.T) {
	server, session, ctx := apiFixture(t)

	conn, err := server.OpenControl(ctx, session.ID(), tmux.ControlOptions{
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
