//go:build integration

package tmux_test

import (
	"context"
	"errors"
	"testing"

	tmux "github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

func apiFixture(t *testing.T) (*tmux.Server, tmux.Session, context.Context) {
	t.Helper()
	server := tmuxtest.NewServer(t)
	ctx := integrationContext(t)

	session, err := server.FindSession(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}

	return server, session, ctx
}

func apiControl(t *testing.T, server *tmux.Server, session tmux.Session, ctx context.Context) *tmux.Connection {
	t.Helper()

	var options tmux.ControlOptions

	connection, err := server.OpenControl(ctx, session, options)
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
