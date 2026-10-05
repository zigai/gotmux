//go:build integration

package test

import (
	"bytes"
	"context"
	"testing"

	"github.com/zigai/gotmux/tmux"
)

func nextSubscription(t *testing.T, ctx context.Context, stream *tmux.EventStream, name, value string) tmux.SubscriptionEvent {
	t.Helper()

	for {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatalf("waiting for %s=%q: %v", name, value, err)
		}

		if got, ok := event.(tmux.SubscriptionEvent); ok && got.Name == name && bytes.Equal(got.Data(), []byte(value)) {
			return got
		}
	}
}

func TestIntegrationSubscriptions(t *testing.T) {
	server, session, ctx := apiFixture(t)
	pane := firstPane(t, ctx, server)
	connection := apiControl(t, ctx, server, session)

	bound, err := connection.Server().Pane(ctx, pane.ID())
	if err != nil {
		t.Fatal(err)
	}

	stream := controlEvents(t, ctx, connection)

	if err := connection.WatchFormat(ctx, "watched", bound, "#{pane_title}"); err != nil {
		t.Fatal(err)
	}

	if err := pane.SetTitle(ctx, "subscription-first"); err != nil {
		t.Fatal(err)
	}

	event := nextSubscription(t, ctx, stream, "watched", "subscription-first")
	if id, ok := event.PaneID.Get(); !ok || id != pane.ID() {
		t.Fatalf("subscription pane: %+v", event)
	}

	if err := connection.UnwatchFormat(ctx, "watched"); err != nil {
		t.Fatal(err)
	}
	// Re-registering the same name proves replacement after the acknowledged removal.
	if err := connection.WatchFormat(ctx, "watched", bound, "#{pane_id}:#{pane_title}"); err != nil {
		t.Fatal(err)
	}

	if err := pane.SetTitle(ctx, "subscription-second"); err != nil {
		t.Fatal(err)
	}

	nextSubscription(t, ctx, stream, "watched", string(pane.ID())+":subscription-second")

	if err := connection.UnwatchFormat(ctx, "watched"); err != nil {
		t.Fatal(err)
	}

	assertUnwatched(t, ctx, connection, stream, bound)

	if _, err := connection.Server().Panes(ctx); err != nil {
		t.Fatalf("event traffic broke requests: %v", err)
	}
}

func TestIntegrationPaneOutputControl(t *testing.T) {
	server, session, ctx := apiFixture(t)
	_, pane := outputProducer(t, ctx, session)
	// With every client off, tmux stops reading the pane itself. Keep an
	// independent observer on so the disabled client's filtering is observable.
	outputControl(t, ctx, server, session)
	connection := outputControl(t, ctx, server, session)

	bound, err := connection.Server().Pane(ctx, pane.ID())
	if err != nil {
		t.Fatal(err)
	}

	stream := controlEvents(t, ctx, connection)

	if err := connection.SetPaneOutput(ctx, bound, false); err != nil {
		t.Fatal(err)
	}

	if err := pane.Submit(ctx, "suppressed-marker"); err != nil {
		t.Fatal(err)
	}

	awaitPaneTitle(t, ctx, pane, "suppressed-marker")

	if err := connection.SetPaneOutput(ctx, bound, true); err != nil {
		t.Fatal(err)
	}

	if err := pane.Submit(ctx, "visible-marker"); err != nil {
		t.Fatal(err)
	}

	assertPaneOutput(t, ctx, stream, pane.ID())
}

func assertPaneOutput(t *testing.T, ctx context.Context, stream *tmux.EventStream, pane tmux.PaneID) {
	t.Helper()

	var output []byte

	for {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatalf("pane output: %q, %v", output, err)
		}

		got, ok := event.(tmux.PaneOutputEvent)
		if !ok || got.PaneID != pane {
			continue
		}

		output = append(output, got.Data()...)
		if bytes.Contains(output, []byte("suppressed-marker")) {
			t.Fatalf("disabled output delivered: %q", output)
		}

		if bytes.Contains(output, []byte("OUTPUT:visible-marker")) {
			break
		}
	}
}

func outputControl(t *testing.T, ctx context.Context, server *tmux.Server, session tmux.Session) *tmux.Connection {
	t.Helper()

	var options tmux.ControlOptions

	options.PaneOutput = true

	connection, err := server.OpenControl(ctx, session.ID(), options)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})

	return connection
}

func assertUnwatched(t *testing.T, ctx context.Context, connection *tmux.Connection, stream *tmux.EventStream, pane tmux.Pane) {
	t.Helper()

	if err := connection.WatchFormat(ctx, "barrier", pane, "#{pane_title}"); err != nil {
		t.Fatal(err)
	}
	// Two sampling barriers catch removed-watch events even if tmux emits them
	// after the surviving watch during the first timer callback.
	for _, value := range []string{"subscription-final-1", "subscription-final-2"} {
		if err := pane.SetTitle(ctx, value); err != nil {
			t.Fatal(err)
		}

		waitWatchBarrier(t, ctx, stream, value)
	}
}

func waitWatchBarrier(t *testing.T, ctx context.Context, stream *tmux.EventStream, value string) {
	t.Helper()

	for {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}

		got, ok := event.(tmux.SubscriptionEvent)
		if !ok {
			continue
		}

		if got.Name == "watched" && bytes.Contains(got.Data(), []byte("subscription-final")) {
			t.Fatalf("removed subscription delivered: %+v", got)
		}

		if got.Name == "barrier" && string(got.Data()) == value {
			return
		}
	}
}

func controlEvents(t *testing.T, ctx context.Context, connection *tmux.Connection) *tmux.EventStream {
	t.Helper()

	var options tmux.EventOptions

	stream, err := connection.Events(ctx, options)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := stream.Close(); err != nil {
			t.Error(err)
		}
	})

	return stream
}

func TestIntegrationSubscriptionsWithTargets(t *testing.T) {
	server, session, ctx := apiFixture(t)
	pane := firstPane(t, ctx, server)
	connection := apiControl(t, ctx, server, session)

	stream := controlEvents(t, ctx, connection)

	if err := connection.WatchFormat(ctx, "all_panes", tmux.TargetAllPanes(), "#{pane_title}"); err != nil {
		t.Fatalf("WatchFormat TargetAllPanes failed: %v", err)
	}

	if err := pane.SetTitle(ctx, "sub-all-panes-val"); err != nil {
		t.Fatal(err)
	}

	event := nextSubscription(t, ctx, stream, "all_panes", "sub-all-panes-val")
	assertPresent(t, "all_panes subscription pane", event.PaneID, pane.ID())

	if err := connection.WatchFormat(ctx, "session_sub", tmux.TargetSession(), "#{session_name}"); err != nil {
		t.Fatalf("WatchFormat TargetSession failed: %v", err)
	}

	sessionInfo, err := session.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	sessEvent := nextSubscription(t, ctx, stream, "session_sub", sessionInfo.Name)
	assertPresent(t, "session subscription session", sessEvent.SessionID, session.ID())

	window, _ := firstWindowPane(t, ctx, session)

	boundWin, err := connection.Server().Window(ctx, window.ID())
	if err != nil {
		t.Fatal(err)
	}

	if err := connection.WatchFormat(ctx, "win_sub", boundWin, "#{window_name}"); err != nil {
		t.Fatalf("WatchFormat Window failed: %v", err)
	}

	if err := window.Rename(ctx, "new-win-sub-name"); err != nil {
		t.Fatal(err)
	}

	winEvent := nextSubscription(t, ctx, stream, "win_sub", "new-win-sub-name")
	assertPresent(t, "window subscription window", winEvent.WindowID, window.ID())
	assertPresent(t, "window subscription index", winEvent.WindowIndex, 0)

	if err := connection.WatchFormat(ctx, "all_windows", tmux.TargetAllWindows(), "#{window_name}"); err != nil {
		t.Fatalf("WatchFormat TargetAllWindows failed: %v", err)
	}

	if err := window.Rename(ctx, "all-windows-name"); err != nil {
		t.Fatal(err)
	}

	allWinEvent := nextSubscription(t, ctx, stream, "all_windows", "all-windows-name")
	assertPresent(t, "all_windows subscription window", allWinEvent.WindowID, window.ID())
}

func assertPresent[T comparable](t *testing.T, field string, value tmux.Value[T], want T) {
	t.Helper()

	if got, ok := value.Get(); !ok || got != want {
		t.Fatalf("%s = %v (present %v), want %v", field, got, ok, want)
	}
}
