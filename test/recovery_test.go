//go:build integration

package test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

func outcomeOf(err error) tmux.Outcome {
	if op, ok := errors.AsType[*tmux.OperationError](err); ok {
		return op.Outcome
	}

	if ce, ok := errors.AsType[*tmux.CommandError](err); ok {
		return ce.Outcome
	}

	return tmux.Outcome{Effect: tmux.EffectNotSent}
}

func TestDaemonReplacement(t *testing.T) {
	ctx := integrationContext(t)

	serverA := tmuxtest.NewServer(t)

	infoA, err := serverA.Probe(ctx)
	if err != nil {
		t.Fatalf("serverA.Probe failed: %v", err)
	}

	sessionA, err := serverA.FindSession(ctx, "fixture")
	if err != nil {
		t.Fatalf("serverA.FindSession failed: %v", err)
	}

	paneA := firstPane(t, ctx, serverA)

	parentWindow := unprobedParentWindow(t, ctx, serverA, paneA.ID())

	staleEnv := currentVars(infoA.Identity, sessionA.ID(), paneA.ID())

	killDaemon(t, infoA.Identity.PID)

	serverB, sessionB := startReplacementDaemon(t, ctx, infoA.Identity)

	paneB := firstPane(t, ctx, serverB)
	if sessionB.ID() != sessionA.ID() || paneB.ID() != paneA.ID() {
		t.Fatalf("replacement did not reuse context IDs: session %s/%s, pane %s/%s", sessionA.ID(), sessionB.ID(), paneA.ID(), paneB.ID())
	}

	for _, name := range []string{"stale-environment-with-pane", "stale-environment-without-pane"} {
		t.Run(name, func(t *testing.T) {
			env := staleEnv
			if name == "stale-environment-without-pane" {
				env.TMUXPane = ""
			}

			current, err := serverB.CurrentFrom(ctx, env)
			if !errors.Is(err, tmux.ErrServerChanged) {
				t.Fatalf("stale environment resolved replacement daemon: %+v, %v", current, err)
			}

			assertNoCurrentContext(t, current)
		})
	}

	assertStaleMutationRefused(t, ctx, sessionA, serverB)

	assertStaleParentWindowRefused(t, ctx, parentWindow, paneB)
	_, freshPane := graphWindow(t, ctx, sessionB)

	unprobed, err := serverB.PaneHandle(freshPane.ID())
	if err != nil {
		t.Fatal(err)
	}

	before := graphState(t, ctx, serverB)
	for _, test := range []struct {
		name   string
		source tmux.Pane
		target tmux.Pane
	}{
		{name: "stale-verified-source", source: paneA, target: unprobed},
		{name: "stale-verified-target", source: unprobed, target: paneA},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.source.Swap(ctx, test.target, tmux.SwapPaneOptions{Select: false})
			if !errors.Is(err, tmux.ErrServerChanged) || outcomeOf(err).Effect != tmux.EffectNotSent {
				t.Errorf("mixed handles lost stale daemon guard: %v", err)
			}

			assertGraph(t, ctx, serverB, before)
		})
	}
}

func unprobedParentWindow(t *testing.T, ctx context.Context, server *tmux.Server, paneID tmux.PaneID) tmux.Window {
	t.Helper()

	pane, err := server.PaneHandle(paneID)
	if err != nil {
		t.Fatal(err)
	}

	window, err := pane.Window(ctx)
	if err != nil {
		t.Fatal(err)
	}

	return window
}

func assertStaleParentWindowRefused(t *testing.T, ctx context.Context, parentWindow tmux.Window, paneB tmux.Pane) {
	t.Helper()

	replacementWindow, err := paneB.Window(ctx)
	if err != nil {
		t.Fatal(err)
	}

	beforeWindowName := windowName(t, ctx, replacementWindow)
	if err := parentWindow.Rename(ctx, "stale-parent"); !errors.Is(err, tmux.ErrServerChanged) || outcomeOf(err).Effect != tmux.EffectNotSent {
		t.Errorf("stale parent window mutation = %v", err)
	}

	if got := windowName(t, ctx, replacementWindow); got != beforeWindowName {
		t.Fatalf("replacement window renamed: got %q, want %q", got, beforeWindowName)
	}
}

// killDaemon leaves the socket file behind, as a crash would.
func killDaemon(t *testing.T, pid int) {
	t.Helper()

	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("os.FindProcess failed: %v", err)
	}

	_ = p.Kill()
	_, _ = p.Wait()

	time.Sleep(50 * time.Millisecond)
}

func startReplacementDaemon(t *testing.T, ctx context.Context, old tmux.ServerIdentity) (*tmux.Server, tmux.Session) {
	t.Helper()

	server, err := tmux.New(tmux.Config{
		Binary:     os.Getenv("TMUX_TEST_BINARY"),
		SocketPath: old.ReportedSocket,
		ConfigFile: "/dev/null",
	})
	if err != nil {
		t.Fatalf("tmux.New for Server B failed: %v", err)
	}

	session, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "replacement", Start: tmux.StartPolicyAllowStart})
	if session.Valid() {
		identity := session.ServerIdentity()

		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()

			if err := server.KillMatching(cleanupCtx, identity); err != nil && !errors.Is(err, tmux.ErrNoServer) {
				t.Errorf("kill Server B: %v", err)
			}
		})
	}

	if err != nil {
		t.Fatalf("failed to start Server B: %v", err)
	}

	if session.ServerIdentity().PID == old.PID {
		t.Fatalf("Server B reports Server A's PID %d", old.PID)
	}

	return server, session
}

func assertStaleMutationRefused(t *testing.T, ctx context.Context, stale tmux.Session, replacement *tmux.Server) {
	t.Helper()

	_, err := stale.NewWindow(ctx, tmux.NewWindowOptions{Name: "should-fail"})
	if !errors.Is(err, tmux.ErrServerChanged) {
		t.Fatalf("expected ErrServerChanged from a stale handle, got: %v", err)
	}

	if outcome := outcomeOf(err); outcome.Effect != tmux.EffectNotSent {
		t.Fatalf("expected outcome effect EffectNotSent, got: %v", outcome.Effect)
	}

	windows, err := replacement.Windows(ctx)
	if err != nil {
		t.Fatalf("serverB.Windows failed: %v", err)
	}

	for _, w := range windows {
		if w.Name == "should-fail" {
			t.Fatalf("window 'should-fail' was created on Server B despite guard rejection")
		}
	}
}
