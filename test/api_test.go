//go:build integration

package tmux_test

import (
	"context"
	"errors"
	"testing"

	tmux "github.com/zigai/gotmux/tmux"
)

func TestIntegrationAPIScalarOptions(t *testing.T) {
	_, session, ctx := apiFixture(t)
	options := session.Options()

	const text = "a\nb;#{literal}\x01\xff"
	if err := options.Set(ctx, "status-left", text); err != nil {
		t.Fatal(err)
	}

	value, err := options.Get(ctx, "status-left")
	if err != nil {
		t.Fatal(err)
	}

	if got, ok := value.Local.Get(); !ok || got != text {
		t.Fatalf("local value: %q, %v", got, ok)
	}

	if err := options.Unset(ctx, "status-left"); err != nil {
		t.Fatal(err)
	}

	value, err = options.Get(ctx, "status-left")
	if err != nil || value.Local.State() != tmux.Unavailable {
		t.Fatalf("unset: %+v, %v", value, err)
	}

	for _, name := range []string{"update-environment", "status-left-l"} {
		if _, err := options.Get(ctx, name); !errors.Is(err, tmux.ErrInvalidArgument) {
			t.Fatalf("non-scalar or abbreviated option %q accepted: %v", name, err)
		}
	}
}

func TestIntegrationAPITypedOptionNames(t *testing.T) {
	server, session, ctx := apiFixture(t)
	if err := server.Options().SetClipboard(ctx, tmux.ClipboardOff); err != nil {
		t.Fatal(err)
	}

	clipboard, err := server.Options().Clipboard(ctx)
	if got, _ := clipboard.Effective.Get(); err != nil || got != tmux.ClipboardOff {
		t.Fatalf("clipboard: %q, %v", got, err)
	}

	if err := session.Options().SetTitles(ctx, true); err != nil {
		t.Fatal(err)
	}

	titles, err := session.Options().Titles(ctx)
	if got, _ := titles.Effective.Get(); err != nil || !got {
		t.Fatalf("titles: %v, %v", got, err)
	}
}

func TestIntegrationAPISequences(t *testing.T) {
	server, session, ctx := apiFixture(t)

	first, err := tmux.NewCommand("set-option", "-t", string(session.ID()), "@sequence", "one")
	if err != nil {
		t.Fatal(err)
	}

	second, err := tmux.NewCommand("show-options", "-v", "-t", string(session.ID()), "@sequence")
	if err != nil {
		t.Fatal(err)
	}

	sequence, err := tmux.Sequence(first, second)
	if err != nil {
		t.Fatal(err)
	}

	result, err := server.RunSequence(ctx, sequence)
	if err != nil || string(result.Stdout) != "one\n" {
		t.Fatalf("sequence: %q, %v", result.Stdout, err)
	}
}

func TestIntegrationAPIQueries(t *testing.T) {
	server, session, ctx := apiFixture(t)

	windows, err := server.WindowsWith(ctx, tmux.QueryOptions{Filter: tmux.Format("#{==:#{session_id}," + string(session.ID()) + "}"), ExtraFields: []string{"window_activity"}})
	if err != nil || len(windows) != 1 {
		t.Fatalf("windows: %+v, %v", windows, err)
	}

	if raw, ok := windows[0].Raw("window_activity"); !ok || len(raw) == 0 {
		t.Fatalf("extra field: %q, %v", raw, ok)
	}

	if got, err := session.Format(ctx, "#{session_id}"); err != nil || string(got) != string(session.ID()) {
		t.Fatalf("session format: %q, %v", got, err)
	}

	window := windows[0].Handle()
	if got, err := window.Format(ctx, "#{window_id}"); err != nil || string(got) != string(window.ID()) {
		t.Fatalf("window format: %q, %v", got, err)
	}

	sMulti, err := session.FormatMulti(ctx, "#{session_id}", "#{session_name}")
	if err != nil || len(sMulti) != 2 || string(sMulti[0]) != string(session.ID()) || string(sMulti[1]) != "fixture" {
		t.Fatalf("session format multi: %q, %v", sMulti, err)
	}

	wMulti, err := window.FormatMulti(ctx, "#{window_id}", "#{window_name}")
	if err != nil || len(wMulti) != 2 || string(wMulti[0]) != string(window.ID()) {
		t.Fatalf("window format multi: %q, %v", wMulti, err)
	}

	panes, err := window.PanesWith(ctx, tmux.QueryOptions{Filter: "0", ExtraFields: nil})
	if err != nil || len(panes) != 0 {
		t.Fatalf("pane filter: %+v, %v", panes, err)
	}
}

func TestIntegrationAPIWindowNavigation(t *testing.T) {
	_, session, ctx := apiFixture(t)

	links, err := session.Windows(ctx)
	if err != nil || len(links) != 1 {
		t.Fatalf("links: %v, %v", links, err)
	}

	first := links[0]

	var options tmux.NewWindowOptions

	second, err := session.NewWindow(ctx, options)
	if err != nil {
		t.Fatal(err)
	}

	if err := first.Select(ctx); err != nil {
		t.Fatal(err)
	}

	if err := session.NextWindow(ctx); err != nil {
		t.Fatal(err)
	}

	info, err := second.Info(ctx)
	if err != nil || !info.Active {
		t.Fatalf("next: %+v, %v", info, err)
	}

	if err := session.PreviousWindow(ctx); err != nil {
		t.Fatal(err)
	}

	info, err = first.Info(ctx)
	if err != nil || !info.Active {
		t.Fatalf("previous: %+v, %v", info, err)
	}
}

func TestIntegrationAPIPaneNavigationAndCopy(t *testing.T) {
	server, _, ctx := apiFixture(t)
	pane := firstPane(t, server, ctx)

	var options tmux.SplitOptions

	options.Direction = tmux.Horizontal

	right, err := pane.Split(ctx, options)
	if err != nil {
		t.Fatal(err)
	}

	if err := pane.SelectAdjacent(ctx, tmux.PaneRight); err != nil {
		t.Fatal(err)
	}

	info, err := right.Info(ctx)
	if err != nil || !info.Active {
		t.Fatalf("adjacent: %+v, %v", info, err)
	}

	var copyOptions tmux.CopyModeOptions
	if err := pane.CopyMode(ctx, copyOptions); err != nil {
		t.Fatal(err)
	}

	if err := pane.CopyAction(ctx, tmux.CopyAction("search-forward"), "literal ; #{pane_id}"); err != nil {
		t.Fatal(err)
	}

	if err := pane.CopyAction(ctx, tmux.CopyCancel); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationAPIAutomaticSlots(t *testing.T) {
	server, session, ctx := apiFixture(t)

	links, err := session.Windows(ctx)
	if err != nil || len(links) != 1 {
		t.Fatalf("links: %v, %v", links, err)
	}

	var create tmux.NewSessionOptions

	create.Name = "destination"

	destination, err := server.NewSession(ctx, create)
	if err != nil {
		t.Fatal(err)
	}

	if err := destination.Options().SetBaseIndex(ctx, 5); err != nil {
		t.Fatal(err)
	}

	var options tmux.LinkOptions

	linked, err := links[0].Window().Link(ctx, destination, options)
	if err != nil || linked.Index() != 5 {
		t.Fatalf("automatic link: %+v, %v", linked, err)
	}

	moved, err := linked.Move(ctx, destination, options)
	if err != nil || moved.Index() != 6 || moved.Window().ID() != linked.Window().ID() {
		t.Fatalf("automatic move: %+v, %v", moved, err)
	}

	options.Replace = true
	if _, err := links[0].Window().Link(ctx, destination, options); !errors.Is(err, tmux.ErrInvalidArgument) {
		t.Fatalf("implicit replacement: %v", err)
	}
}

func TestIntegrationAPIControlSupport(t *testing.T) {
	server, session, ctx := apiFixture(t)
	bound := apiControl(t, server, session, ctx).Server()

	support := bound.Support()
	if support.RawCommands || support.ScalarReads || support.TerminalAttach {
		t.Fatalf("control support: %+v", support)
	}

	capabilities, err := bound.Capabilities(ctx)
	if err != nil || !capabilities.HasCommand("list-windows") || capabilities.HasCommand("nonexistent-command") {
		t.Fatalf("capabilities: %+v, %v", capabilities, err)
	}

	clients, err := bound.ClientsWith(ctx, tmux.QueryOptions{Filter: "#{client_control_mode}", ExtraFields: []string{"client_termname"}})
	if err != nil || len(clients) != 1 {
		t.Fatalf("clients: %+v, %v", clients, err)
	}

	client := clients[0].Handle()
	if got, err := client.Format(ctx, "#{client_name}"); err != nil || string(got) != string(client.Name()) {
		t.Fatalf("client format: %q, %v", got, err)
	}

	cMulti, err := client.FormatMulti(ctx, "#{client_name}", "#{client_control_mode}")
	if err != nil || len(cMulti) != 2 || string(cMulti[0]) != string(client.Name()) || string(cMulti[1]) != "1" {
		t.Fatalf("client format multi: %q, %v", cMulti, err)
	}
}

func TestIntegrationAPISubprocessLifetime(t *testing.T) {
	server, session, ctx := apiFixture(t)
	connection := apiControl(t, server, session, ctx)
	bound := connection.Server()

	controlSession, err := bound.Session(ctx, session.ID())
	if err != nil {
		t.Fatal(err)
	}

	auxiliarySession, err := controlSession.UsingSubprocess()
	if err != nil || !auxiliarySession.Identity().Equal(controlSession.Identity()) {
		t.Fatalf("identity: %v", err)
	}

	if _, err := auxiliarySession.Options().Get(ctx, "status-left"); err != nil {
		t.Fatal(err)
	}

	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := auxiliarySession.Info(ctx); err == nil {
		t.Fatal("closed generation accepted")
	}

	if _, err := auxiliarySession.UsingSubprocess(); err == nil {
		t.Fatal("closed generation converted")
	}
}

func TestIntegrationAPIRawSubprocess(t *testing.T) {
	server, session, ctx := apiFixture(t)
	bound := apiControl(t, server, session, ctx).Server()

	auxiliary, err := bound.UsingSubprocess()
	if err != nil || !auxiliary.Support().RawCommands {
		t.Fatalf("auxiliary: %v", err)
	}

	command, err := tmux.NewCommand("display-message", "-p", "literal")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := bound.Run(ctx, command); !errors.Is(err, tmux.ErrTransportUnsupported) {
		t.Fatalf("raw control: %v", err)
	}

	result, err := auxiliary.Run(ctx, command)
	if err != nil || string(result.Stdout) != "literal\n" {
		t.Fatalf("raw auxiliary: %q, %v", result.Stdout, err)
	}
}

func TestIntegrationAPIAutomaticSlotConflict(t *testing.T) {
	server, session, ctx := apiFixture(t)

	source, err := session.Windows(ctx)
	if err != nil || len(source) != 1 {
		t.Fatalf("source: %v, %v", source, err)
	}

	var create tmux.NewSessionOptions

	create.Name = "destination"

	destination, err := server.NewSession(ctx, create)
	if err != nil {
		t.Fatal(err)
	}

	if err := destination.Options().SetBaseIndex(ctx, 5); err != nil {
		t.Fatal(err)
	}

	installSlotConflict(t, destination, ctx)

	var options tmux.LinkOptions
	if _, err := source[0].Window().Link(ctx, destination, options); err == nil {
		t.Fatal("automatically chosen occupied slot was accepted")
	}

	links, err := destination.Windows(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, link := range links {
		if link.Index() == 5 {
			if link.Window().ID() == source[0].Window().ID() {
				t.Fatal("conflicting window was replaced")
			}

			return
		}
	}

	t.Fatal("conflict fixture did not create its window")
}

func installSlotConflict(t *testing.T, destination tmux.Session, ctx context.Context) {
	t.Helper()
	// Claim the chosen slot after its discovery snapshot but before the mutation.
	remove, err := tmux.NewCommand("set-hook", "-u", "-t", string(destination.ID()), "after-list-windows[0]")
	if err != nil {
		t.Fatal(err)
	}

	claim, err := tmux.NewCommand("new-window", "-d", "-t", string(destination.ID())+":5", "-n", "conflict")
	if err != nil {
		t.Fatal(err)
	}

	sequence, err := tmux.Sequence(remove, claim)
	if err != nil {
		t.Fatal(err)
	}

	if err := destination.Hooks().Set(ctx, "after-list-windows", 0, sequence); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationWindowInfosAndLinkNames(t *testing.T) {
	_, session, ctx := apiFixture(t)

	windows, links, err := session.WindowInfos(ctx)
	if err != nil {
		t.Fatalf("WindowInfos failed: %v", err)
	}

	if len(windows) == 0 || len(links) == 0 {
		t.Fatalf("expected windows and links, got %d windows, %d links", len(windows), len(links))
	}

	if links[0].WindowName == "" {
		t.Fatal("expected non-empty WindowName on WindowLinkInfo")
	}

	if links[0].WindowName != windows[0].Name {
		t.Fatalf("expected WindowName %q to match window.Name %q", links[0].WindowName, windows[0].Name)
	}
}

func TestIntegrationPaneInfoParentMetadata(t *testing.T) {
	server, session, ctx := apiFixture(t)

	panes, err := server.Panes(ctx)
	if err != nil {
		t.Fatalf("Panes failed: %v", err)
	}

	if len(panes) == 0 {
		t.Fatal("expected at least one pane")
	}

	p := panes[0]
	sid, ok := p.SessionID.Get()
	if !ok || sid != session.ID() {
		t.Fatalf("expected pane session ID %v, got %v (ok=%v)", session.ID(), sid, ok)
	}

	sname, ok := p.SessionName.Get()
	if !ok || sname == "" {
		t.Fatalf("expected non-empty session name on pane, got %q (ok=%v)", sname, ok)
	}

	wname, ok := p.WindowName.Get()
	if !ok || wname == "" {
		t.Fatalf("expected non-empty window name on pane, got %q (ok=%v)", wname, ok)
	}

	widx, ok := p.WindowIndex.Get()
	if !ok || widx < 0 {
		t.Fatalf("expected non-negative window index on pane, got %d (ok=%v)", widx, ok)
	}
}

func TestIntegrationInfoWithExtraFields(t *testing.T) {
	server, _, ctx := apiFixture(t)

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) == 0 {
		t.Fatal(err)
	}

	info, err := panes[0].Handle().InfoWith(ctx, tmux.QueryOptions{
		ExtraFields: []string{"session_name", "window_name"},
	})
	if err != nil {
		t.Fatalf("InfoWith failed: %v", err)
	}

	raw, ok := info.Raw("session_name")
	if !ok || len(raw) == 0 {
		t.Fatalf("expected Raw('session_name') to be present, got %q (ok=%v)", string(raw), ok)
	}
}

func TestIntegrationCaptureWithTitle(t *testing.T) {
	server, _, ctx := apiFixture(t)

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) == 0 {
		t.Fatal(err)
	}

	pane := panes[0].Handle()
	res, err := pane.CaptureWithTitle(ctx, tmux.CaptureOptions{})
	if err != nil {
		t.Fatalf("CaptureWithTitle failed: %v", err)
	}

	plain, err := pane.Capture(ctx, tmux.CaptureOptions{})
	if err != nil {
		t.Fatalf("Capture failed: %v", err)
	}

	if string(res.Output) != string(plain) {
		t.Fatalf("expected capture output %q to match plain capture %q", string(res.Output), string(plain))
	}
}

func TestIntegrationUnprobedPaneHandle(t *testing.T) {
	server, _, ctx := apiFixture(t)

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) == 0 {
		t.Fatal(err)
	}

	unprobed, err := server.PaneHandle(panes[0].ID)
	if err != nil {
		t.Fatalf("PaneHandle failed: %v", err)
	}

	data, err := unprobed.Capture(ctx, tmux.CaptureOptions{})
	if err != nil {
		t.Fatalf("unprobed Capture failed: %v", err)
	}

	expected, err := panes[0].Handle().Capture(ctx, tmux.CaptureOptions{})
	if err != nil {
		t.Fatalf("verified Capture failed: %v", err)
	}

	if string(data) != string(expected) {
		t.Fatalf("expected %q, got %q", string(expected), string(data))
	}
}

func TestEnvironmentOperationNames(t *testing.T) {
	_, session, ctx := apiFixture(t)

	err := session.Environment().Unset(ctx, "invalid=name")
	if err == nil {
		t.Fatal("expected error on invalid env name")
	}
	var opErr *tmux.OperationError
	if errors.As(err, &opErr) {
		if opErr.Operation != "Environment.Unset" {
			t.Errorf("expected opErr.Operation to be 'Environment.Unset', got %q", opErr.Operation)
		}
	} else {
		t.Fatalf("expected *tmux.OperationError, got %T: %v", err, err)
	}

	err = session.Environment().Remove(ctx, "invalid=name")
	if err == nil {
		t.Fatal("expected error on invalid env name")
	}
	if errors.As(err, &opErr) {
		if opErr.Operation != "Environment.Remove" {
			t.Errorf("expected opErr.Operation to be 'Environment.Remove', got %q", opErr.Operation)
		}
	} else {
		t.Fatalf("expected *tmux.OperationError, got %T: %v", err, err)
	}
}

func TestMissingObjectEffect(t *testing.T) {
	server, _, ctx := apiFixture(t)

	_, err := server.Session(ctx, "$99999")
	if err == nil {
		t.Fatal("expected error looking up nonexistent session")
	}
	if !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	var opErr *tmux.OperationError
	if errors.As(err, &opErr) {
		if opErr.Outcome.Effect == tmux.Confirmed {
			t.Errorf("read-only missing session lookup reported Effect: Confirmed! Must not be Confirmed.")
		}
	}
}

func TestCaptureRangeEntireHistoryWithEnd(t *testing.T) {
	server, _, ctx := apiFixture(t)
	pane := firstPane(t, server, ctx)

	end := 10
	_, err := pane.Capture(ctx, tmux.CaptureOptions{
		EntireHistory: true,
		End:           &end,
	})
	if err != nil {
		t.Fatalf("pane.Capture with EntireHistory and End failed: %v", err)
	}
}
