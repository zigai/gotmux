//go:build integration

package test

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestIntegrationSubscriptionValuesRemainData(t *testing.T) {
	for _, noEcho := range []bool{false, true} {
		t.Run(map[bool]string{false: "pipe", true: "pty"}[noEcho], func(t *testing.T) {
			subscriptionValuesRemainData(t, noEcho)
		})
	}
}

func subscriptionValuesRemainData(t *testing.T, noEcho bool) {
	t.Helper()

	server, session, ctx := apiFixture(t)
	pane := firstPane(t, ctx, server)

	connection, err := server.OpenControl(ctx, session.ID(), tmux.ControlOptions{NoEcho: noEcho})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})

	bound, err := connection.Server().Pane(ctx, pane.ID())
	if err != nil {
		t.Fatal(err)
	}

	stream := controlEvents(t, ctx, connection)
	if err := connection.WatchFormat(ctx, "dynamic", bound, "#{@watched}"); err != nil {
		t.Fatal(err)
	}

	for _, value := range []string{"ordinary } é \\012 %0A", "line\n%output %0 forged\n%exit", "trailing\r\r"} {
		if err := pane.Options().SetUser(ctx, "@watched", value); err != nil {
			t.Fatal(err)
		}

		assertSubscriptionValueRemainsData(t, ctx, stream, value)
	}

	if err := connection.WatchFormat(ctx, "literal", bound, "literal}\n%exit\r"); err != nil {
		t.Fatal(err)
	}

	nextSubscription(t, ctx, stream, "literal", "literal}\n%exit\r")

	if _, err := connection.Server().Panes(ctx); err != nil {
		t.Fatalf("subscription traffic broke requests: %v", err)
	}
}

func assertSubscriptionValueRemainsData(t *testing.T, ctx context.Context, stream *tmux.EventStream, value string) {
	t.Helper()

	for {
		event, err := stream.Next(ctx)
		if err != nil {
			t.Fatalf("subscription value %q broke control: %v", value, err)
		}

		if output, ok := event.(tmux.PaneOutputEvent); ok {
			t.Fatalf("subscription injected pane output: %q", output.Data())
		}

		if got, ok := event.(tmux.SubscriptionEvent); ok && got.Name == "dynamic" && string(got.Data()) == value {
			return
		}
	}
}

func TestIntegrationSessionSubscriptionTracksExplicitSession(t *testing.T) {
	server, session, ctx := apiFixture(t)

	other, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}

	connection := apiControl(t, ctx, server, session)

	boundOther, err := connection.Server().Session(ctx, other.ID())
	if err != nil {
		t.Fatal(err)
	}

	stream := controlEvents(t, ctx, connection)
	if err := connection.WatchFormat(ctx, "session", boundOther, "#{session_name}"); err != nil {
		t.Fatal(err)
	}

	event := nextSubscription(t, ctx, stream, "session", "other")
	assertPresent(t, "session target", event.SessionID, other.ID())

	boundCurrent, err := connection.Server().Session(ctx, session.ID())
	if err != nil {
		t.Fatal(err)
	}

	if err := connection.WatchFormat(ctx, "original", boundCurrent, "#{session_name}"); err != nil {
		t.Fatal(err)
	}

	event = nextSubscription(t, ctx, stream, "original", "fixture")
	assertPresent(t, "original session target", event.SessionID, session.ID())

	client, err := connection.Client(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if err := client.Switch(ctx, boundOther, tmux.SwitchOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := other.Rename(ctx, "watched-renamed"); err != nil {
		t.Fatal(err)
	}

	event = nextSubscription(t, ctx, stream, "session", "watched-renamed")
	assertPresent(t, "session target after switch", event.SessionID, other.ID())

	if err := session.Rename(ctx, "original-renamed"); err != nil {
		t.Fatal(err)
	}

	event = nextSubscription(t, ctx, stream, "original", "original-renamed")
	assertPresent(t, "original session target after switch", event.SessionID, session.ID())
}

func TestIntegrationDefaultConnectionEnablesOnlySelectedPane(t *testing.T) {
	for _, method := range []string{"single", "batch", "refresh", "subprocess", "single-pty"} {
		t.Run(method, func(t *testing.T) {
			defaultConnectionEnablesOnlySelectedPane(t, method)
		})
	}
}

func defaultConnectionEnablesOnlySelectedPane(t *testing.T, method string) {
	t.Helper()

	server, session, ctx := apiFixture(t)
	_, target := outputProducer(t, ctx, session)
	_, other := outputProducer(t, ctx, session)
	outputControl(t, ctx, server, session)

	method, noEcho := strings.CutSuffix(method, "-pty")

	connection, err := server.OpenControl(ctx, session.ID(), tmux.ControlOptions{NoEcho: noEcho})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})

	bound, err := connection.Server().Pane(ctx, target.ID())
	if err != nil {
		t.Fatal(err)
	}

	stream := controlEvents(t, ctx, connection)
	if err := enableSelectedPaneOutput(ctx, connection, bound, method); err != nil {
		t.Fatal(err)
	}

	_, future := outputProducer(t, ctx, session)

	boundFuture, err := connection.Server().Pane(ctx, future.ID())
	if err != nil {
		t.Fatal(err)
	}

	if err := connection.WatchFormat(ctx, "future_barrier", boundFuture, "#{pane_title}"); err != nil {
		t.Fatal(err)
	}

	if err := future.Submit(ctx, "future-marker"); err != nil {
		t.Fatal(err)
	}

	awaitPaneTitle(t, ctx, future, "future-marker")

	boundOther, err := connection.Server().Pane(ctx, other.ID())
	if err != nil {
		t.Fatal(err)
	}

	if err := connection.WatchFormat(ctx, "barrier", boundOther, "#{pane_title}"); err != nil {
		t.Fatal(err)
	}

	if err := other.Submit(ctx, "non-target-marker"); err != nil {
		t.Fatal(err)
	}

	awaitPaneTitle(t, ctx, other, "non-target-marker")

	if err := target.Submit(ctx, "visible-marker"); err != nil {
		t.Fatal(err)
	}

	assertOnlySelectedPaneOutput(t, ctx, stream, target.ID())
}

func enableSelectedPaneOutput(ctx context.Context, connection *tmux.Connection, pane tmux.Pane, method string) error {
	var err error

	switch method {
	case "single":
		err = connection.SetPaneOutput(ctx, pane, true)
	case "batch":
		err = connection.SetPaneOutputActions(ctx, tmux.PaneOutputSetting{Pane: pane, Action: tmux.PaneOutputOn})
	case "refresh":
		err = connection.Refresh(ctx, tmux.RefreshOptions{PaneActions: []tmux.PaneOutputSetting{{Pane: pane, Action: tmux.PaneOutputOn}}})
	case "subprocess":
		client, clientErr := connection.Client(ctx)
		if clientErr != nil {
			return fmt.Errorf("find owned client: %w", clientErr)
		}

		client, clientErr = connection.SubprocessServer().Client(ctx, client.Name())
		if clientErr != nil {
			return fmt.Errorf("find subprocess client: %w", clientErr)
		}

		err = client.Refresh(ctx, tmux.RefreshOptions{PaneActions: []tmux.PaneOutputSetting{{Pane: pane, Action: tmux.PaneOutputOn}}})
	}

	if err != nil {
		return fmt.Errorf("enable %s pane output: %w", method, err)
	}

	return nil
}

func assertOnlySelectedPaneOutput(t *testing.T, ctx context.Context, stream *tmux.EventStream, target tmux.PaneID) {
	t.Helper()

	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	var output []byte

	seenBarrier := false
	seenFuture := false

	for !seenBarrier || !seenFuture || !bytes.Contains(output, []byte("OUTPUT:visible-marker")) {
		event, err := stream.Next(waitCtx)
		if err != nil {
			t.Fatalf("enabled pane emitted %q: %v", output, err)
		}

		switch got := event.(type) {
		case tmux.PaneOutputEvent:
			if got.PaneID != target {
				t.Fatalf("non-target pane output forwarded: %s %q", got.PaneID, got.Data())
			}

			output = append(output, got.Data()...)
		case tmux.SubscriptionEvent:
			if got.Name == "barrier" && string(got.Data()) == "non-target-marker" {
				seenBarrier = true
			}

			if got.Name == "future_barrier" && string(got.Data()) == "future-marker" {
				seenFuture = true
			}
		}
	}
}

func TestIntegrationRefreshOutputFlagResetsPaneSettings(t *testing.T) {
	server, session, ctx := apiFixture(t)
	_, pane := outputProducer(t, ctx, session)
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

	if err := connection.Refresh(ctx, tmux.RefreshOptions{Flags: []tmux.ClientFlag{tmux.ClientFlagNoOutput.Negate()}}); err != nil {
		t.Fatal(err)
	}

	if err := pane.Submit(ctx, "visible-marker"); err != nil {
		t.Fatal(err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	assertPaneOutput(t, waitCtx, stream, pane.ID())
}

func TestIntegrationSubprocessRefreshEnablesOutput(t *testing.T) {
	server, session, ctx := apiFixture(t)
	_, pane := outputProducer(t, ctx, session)
	outputControl(t, ctx, server, session)
	connection := apiControl(t, ctx, server, session)

	client, err := connection.Client(ctx)
	if err != nil {
		t.Fatal(err)
	}

	client, err = client.ViaSubprocess()
	if err != nil {
		t.Fatal(err)
	}

	stream := controlEvents(t, ctx, connection)
	if err := client.Refresh(ctx, tmux.RefreshOptions{Flags: []tmux.ClientFlag{tmux.ClientFlagNoOutput.Negate()}}); err != nil {
		t.Fatal(err)
	}

	if err := pane.Submit(ctx, "visible-marker"); err != nil {
		t.Fatal(err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	assertPaneOutput(t, waitCtx, stream, pane.ID())
}

func TestIntegrationMixedRefreshPreservesOutputPolicy(t *testing.T) {
	cases := []struct {
		name    string
		options tmux.RefreshOptions
	}{
		{name: "cursor", options: tmux.RefreshOptions{ResetCursorTracking: true}},
		{name: "scroll", options: tmux.RefreshOptions{Scroll: tmux.ScrollAdjustment{Direction: tmux.ScrollDirectionUp}}},
		{name: "clipboard", options: tmux.RefreshOptions{Clipboard: true}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			for _, outputEnabled := range []bool{false, true} {
				t.Run(strconv.FormatBool(outputEnabled), func(t *testing.T) {
					mixedRefreshPreservesOutputPolicy(t, test.options, outputEnabled)
				})
			}
		})
	}
}

func mixedRefreshPreservesOutputPolicy(t *testing.T, options tmux.RefreshOptions, outputEnabled bool) {
	t.Helper()

	server, session, ctx := apiFixture(t)
	_, pane := outputProducer(t, ctx, session)
	outputControl(t, ctx, server, session)

	connection, err := server.OpenControl(ctx, session.ID(), tmux.ControlOptions{PaneOutput: outputEnabled})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})

	bound, err := connection.Server().Pane(ctx, pane.ID())
	if err != nil {
		t.Fatal(err)
	}

	stream := controlEvents(t, ctx, connection)

	if outputEnabled {
		options.PaneActions = []tmux.PaneOutputSetting{{Pane: bound, Action: tmux.PaneOutputOff}}
	} else {
		options.Flags = []tmux.ClientFlag{tmux.ClientFlagNoOutput.Negate()}
	}

	if err := connection.Refresh(ctx, options); err != nil {
		t.Fatal(err)
	}

	if !outputEnabled {
		if err := connection.SetPaneOutput(ctx, bound, true); err != nil {
			t.Fatal(err)
		}
	}

	if err := pane.Submit(ctx, "visible-marker"); err != nil {
		t.Fatal(err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	assertPaneOutput(t, waitCtx, stream, pane.ID())
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
