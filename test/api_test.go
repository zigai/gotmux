//go:build integration

package test

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/zigai/gotmux/tmux"
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
	if err != nil || value.Local.State() != tmux.ValueStateUnavailable {
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

	got, err := session.Format(ctx, "#{session_id}")
	assertFormatValues(t, "session format", [][]byte{got}, err, string(session.ID()))

	window := windows[0].Handle()
	got, err = window.Format(ctx, "#{window_id}")
	assertFormatValues(t, "window format", [][]byte{got}, err, string(window.ID()))

	sMulti, err := session.FormatMulti(ctx, "#{session_id}", "#{session_name}")
	assertFormatValues(t, "session format multi", sMulti, err, string(session.ID()), "fixture")

	wMulti, err := window.FormatMulti(ctx, "#{window_id}", "#{window_name}")
	assertFormatValues(t, "window format multi", wMulti, err, string(window.ID()), windows[0].Name)

	panes, err := window.PanesWith(ctx, tmux.QueryOptions{Filter: "0", ExtraFields: nil})
	if err != nil || len(panes) != 0 {
		t.Fatalf("pane filter: %+v, %v", panes, err)
	}
}

func stackedPaneFixture(t *testing.T) (*tmux.Server, tmux.Session, tmux.Pane, context.Context) {
	t.Helper()
	server, session, ctx := apiFixture(t)

	pane := firstPane(t, ctx, server)
	if err := pane.CopyMode(ctx, tmux.CopyModeOptions{}); err != nil {
		t.Fatal(err)
	}

	if err := pane.ChooseTree(ctx); err != nil {
		t.Fatal(err)
	}

	command, err := tmux.NewCommand("display-message", "-p", "-t", string(pane.ID()), "#{pane_in_mode}")
	if err != nil {
		t.Fatal(err)
	}

	result, err := server.Run(ctx, command)
	if err != nil || string(result.Stdout) != "2\n" {
		t.Fatalf("native mode count = %q, %v", result.Stdout, err)
	}

	return server, session, pane, ctx
}

func TestIntegrationQueriesWithStackedPaneModes(t *testing.T) {
	server, _, pane, ctx := stackedPaneFixture(t)

	info, err := pane.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if mode, ok := info.Mode.Get(); !ok || mode != "tree-mode" {
		t.Fatalf("pane mode = %q, present = %v", mode, ok)
	}

	panes, err := server.Panes(ctx)
	if err != nil || len(panes) != 1 || panes[0].ID != pane.ID() {
		t.Fatalf("panes = %+v, %v", panes, err)
	}

	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, found := snapshot.Pane(pane.ID()); !found {
		t.Fatal("snapshot lost pane in stacked modes")
	}
}

func TestIntegrationReadWithStackedPaneModes(t *testing.T) {
	server, session, pane, ctx := stackedPaneFixture(t)

	read, err := server.Read(ctx, tmux.ReadRequest{Panes: tmux.PresentValue(tmux.QueryOptions{})})
	if err != nil || len(read.Panes) != 1 || read.Panes[0].ID != pane.ID() {
		t.Fatalf("read panes = %+v, %v", read.Panes, err)
	}

	current, err := server.CurrentFrom(ctx, currentVars(session.ServerIdentity(), session.ID(), pane.ID()))
	if err != nil || current.Pane.ID != pane.ID() {
		t.Fatalf("current pane = %s, %v", current.Pane.ID, err)
	}
}

func TestIntegrationFormatLiteralBraces(t *testing.T) {
	server, session, ctx := apiFixture(t)
	pane := firstPane(t, ctx, server)

	cases := []struct {
		expression tmux.Format
		want       string
	}{
		{"a}b", "a}b"},
		{"a}}b", "a}}b"},
		{"#{pane_id}}", string(pane.ID()) + "}"},
		{"#{?#{==:a,a},yes,no}}tail", "yes}tail"},
		{"trailing#", "trailing"},
		{"##", "#"},
		{"##}#{pane_id}", "#}" + string(pane.ID())},
		{"##{text}", "#{text}"},
		{"a#}b", "a}b"},
		{"{text},é}", "{text},é}"},
	}
	for _, test := range cases {
		t.Run(string(test.expression), func(t *testing.T) {
			got, err := pane.Format(ctx, test.expression)
			assertFormatValues(t, "format", [][]byte{got}, err, test.want)
		})
	}

	got, err := session.FormatMulti(ctx, "a}b", "#{session_name}}", "tail")
	assertFormatValues(t, "format multi", got, err, "a}b", "fixture}", "tail")

	connection := apiControl(t, ctx, server, session)

	boundPane, err := connection.Server().PaneHandle(pane.ID())
	if err != nil {
		t.Fatal(err)
	}

	formatted, err := boundPane.Format(ctx, "a}b")
	assertFormatValues(t, "control format", [][]byte{formatted}, err, "a}b")
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
	pane := firstPane(t, ctx, server)

	var options tmux.SplitOptions

	options.Direction = tmux.Horizontal

	right, err := pane.Split(ctx, options)
	if err != nil {
		t.Fatal(err)
	}

	if err := pane.SelectAdjacent(ctx, tmux.PaneDirectionRight); err != nil {
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
	bound := apiControl(t, ctx, server, session).Server()

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
	got, err := client.Format(ctx, "#{client_name}")
	assertFormatValues(t, "client format", [][]byte{got}, err, string(client.Name()))

	cMulti, err := client.FormatMulti(ctx, "#{client_name}", "#{client_control_mode}")
	assertFormatValues(t, "client format multi", cMulti, err, string(client.Name()), "1")
}

func assertFormatValues(t *testing.T, call string, got [][]byte, err error, want ...string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", call, err)
	}

	values := make([]string, len(got))
	for i, value := range got {
		values[i] = string(value)
	}

	if !slices.Equal(values, want) {
		t.Fatalf("%s = %q, want %q", call, values, want)
	}
}

func TestIntegrationAPISubprocessLifetime(t *testing.T) {
	server, session, ctx := apiFixture(t)
	connection := apiControl(t, ctx, server, session)
	bound := connection.Server()

	controlSession, err := bound.Session(ctx, session.ID())
	if err != nil {
		t.Fatal(err)
	}

	subprocessSession, err := controlSession.ViaSubprocess()
	if err != nil || !subprocessSession.ServerIdentity().Equal(controlSession.ServerIdentity()) {
		t.Fatalf("identity: %v", err)
	}

	if _, err := subprocessSession.Options().Get(ctx, "status-left"); err != nil {
		t.Fatal(err)
	}

	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := subprocessSession.Info(ctx); err == nil {
		t.Fatal("closed generation accepted")
	}

	if _, err := subprocessSession.ViaSubprocess(); err == nil {
		t.Fatal("closed generation converted")
	}
}

func TestIntegrationAPIRawSubprocess(t *testing.T) {
	server, session, ctx := apiFixture(t)
	bound := apiControl(t, ctx, server, session).Server()

	subprocess, err := bound.ViaSubprocess()
	if err != nil || !subprocess.Support().RawCommands {
		t.Fatalf("subprocess: %v", err)
	}

	command, err := tmux.NewCommand("display-message", "-p", "literal")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := bound.Run(ctx, command); !errors.Is(err, tmux.ErrTransportUnsupported) {
		t.Fatalf("raw control: %v", err)
	}

	result, err := subprocess.Run(ctx, command)
	if err != nil || string(result.Stdout) != "literal\n" {
		t.Fatalf("raw subprocess: %q, %v", result.Stdout, err)
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

	installSlotConflict(t, ctx, destination)

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

func installSlotConflict(t *testing.T, ctx context.Context, destination tmux.Session) {
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

func TestIntegrationQueryRecords(t *testing.T) {
	server, session, ctx := apiFixture(t)
	pane := firstPane(t, ctx, server)
	connection := apiControl(t, ctx, server, session)

	const text = "line\nbreak\t;#{literal}\xff"
	if err := pane.Options().SetUser(ctx, "@record_text", text); err != nil {
		t.Fatal(err)
	}

	for _, transport := range []struct {
		name   string
		server *tmux.Server
	}{
		{name: "subprocess", server: server},
		{name: "control", server: connection.Server()},
		{name: "bound subprocess", server: connection.SubprocessServer()},
	} {
		t.Run(transport.name, func(t *testing.T) {
			query := tmux.RecordQuery{
				Kind:   tmux.ObjectKindPane,
				Fields: []string{"@record_text", "pane_id", "@unset", "@record_text"},
				Filter: tmux.Format("#{==:#{pane_id}," + string(pane.ID()) + "}"),
			}

			records, err := transport.server.QueryRecords(ctx, query)
			if err != nil {
				t.Fatal(err)
			}

			if len(records.Rows) != 1 {
				t.Fatalf("rows = %+v", records.Rows)
			}

			want := map[string]string{
				"@record_text": text,
				"pane_id":      string(pane.ID()),
				"@unset":       "",
			}
			if !maps.Equal(records.Rows[0], want) {
				t.Fatalf("selected values = %+v, want %+v", records.Rows[0], want)
			}

			if records.Identity.ReportedSocket != server.Endpoint().SocketPath || !records.Version.AtLeast(3, 6) {
				t.Fatalf("identity/version = %+v, %+v", records.Identity, records.Version)
			}

			if transport.name != "subprocess" && records.Identity.Generation == 0 {
				t.Fatal("missing control generation")
			}
		})
	}
}

func TestIntegrationQueryRecordsEmptyFilter(t *testing.T) {
	server, session, ctx := apiFixture(t)
	connection := apiControl(t, ctx, server, session)

	query := tmux.RecordQuery{Kind: tmux.ObjectKindPane, Fields: []string{"pane_id"}, Filter: "0"}

	for _, reader := range []*tmux.Server{server, connection.Server(), connection.SubprocessServer()} {
		records, err := reader.QueryRecords(ctx, query)
		if err != nil || len(records.Rows) != 0 || records.Identity.PID <= 0 {
			t.Fatalf("empty filtered query = %+v, %v", records, err)
		}
	}
}

func TestIntegrationQueryRecordsPreservesWindowLinks(t *testing.T) {
	server, session, ctx := apiFixture(t)
	pane := firstPane(t, ctx, server)

	info, err := pane.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	window, err := server.Window(ctx, info.WindowID)
	if err != nil {
		t.Fatal(err)
	}

	destination, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "destination"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := window.Link(ctx, destination, tmux.LinkOptions{}); err != nil {
		t.Fatal(err)
	}

	for _, kind := range []tmux.ObjectKind{tmux.ObjectKindWindow, tmux.ObjectKindPane} {
		records, queryErr := server.QueryRecords(ctx, tmux.RecordQuery{
			Kind:   kind,
			Fields: []string{"session_id", "window_id"},
			Filter: tmux.Format("#{==:#{window_id}," + string(window.ID()) + "}"),
		})
		if queryErr != nil {
			t.Fatal(queryErr)
		}

		links := make(map[string]string, len(records.Rows))
		for _, row := range records.Rows {
			links[row["session_id"]] = row["window_id"]
		}

		want := map[string]string{
			string(session.ID()):     string(window.ID()),
			string(destination.ID()): string(window.ID()),
		}
		if len(records.Rows) != 2 || !maps.Equal(links, want) {
			t.Fatalf("%s links = %+v", kind, records.Rows)
		}
	}
}

func TestIntegrationQueryRecordsNoServerAndClosedConnection(t *testing.T) {
	server, session, ctx := apiFixture(t)
	connection := apiControl(t, ctx, server, session)

	bound := connection.SubprocessServer()
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}

	query := tmux.RecordQuery{Kind: tmux.ObjectKindPane, Fields: []string{"pane_id"}, Filter: ""}
	if _, err := bound.QueryRecords(ctx, query); !errors.Is(err, tmux.ErrClosed) {
		t.Fatalf("closed connection query = %v", err)
	}

	if err := server.KillMatching(ctx, session.ServerIdentity()); err != nil {
		t.Fatal(err)
	}

	if _, err := server.QueryRecords(ctx, query); !errors.Is(err, tmux.ErrNoServer) {
		t.Fatalf("missing server query = %v", err)
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
		if opErr.Operation != "EnvironmentScope.Unset" {
			t.Errorf("expected opErr.Operation to be 'EnvironmentScope.Unset', got %q", opErr.Operation)
		}
	} else {
		t.Fatalf("expected *tmux.OperationError, got %T: %v", err, err)
	}

	err = session.Environment().Remove(ctx, "invalid=name")
	if err == nil {
		t.Fatal("expected error on invalid env name")
	}

	if errors.As(err, &opErr) {
		if opErr.Operation != "EnvironmentScope.Remove" {
			t.Errorf("expected opErr.Operation to be 'EnvironmentScope.Remove', got %q", opErr.Operation)
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

	if opErr, ok := errors.AsType[*tmux.OperationError](err); ok {
		if opErr.Outcome.Effect == tmux.EffectConfirmed {
			t.Errorf("read-only missing session lookup reported EffectConfirmed; it must not be EffectConfirmed.")
		}
	}
}

func TestDuplicateSessionCreationReportsRejection(t *testing.T) {
	server, session, ctx := apiFixture(t)

	var opts tmux.NewSessionOptions

	opts.Name = "fixture"

	_, err := server.NewSession(ctx, opts)

	opErr, ok := errors.AsType[*tmux.OperationError](err)
	if !ok || !errors.Is(err, tmux.ErrAlreadyExists) || opErr.Outcome.Effect != tmux.EffectRejected || len(opErr.Outcome.Created) != 0 {
		t.Fatalf("duplicate session = %#v, %v", opErr, err)
	}

	if _, err := session.Info(ctx); err != nil {
		t.Fatalf("original session was lost: %v", err)
	}
}

func TestStaleSessionEnvironmentReportsNotFound(t *testing.T) {
	server, _, ctx := apiFixture(t)

	var opts tmux.NewSessionOptions

	opts.Name = "stale"

	stale, err := server.NewSession(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}

	if err := stale.Kill(ctx); err != nil {
		t.Fatal(err)
	}

	_, err = stale.Environment().Get(ctx, "HOME")
	if !errors.Is(err, tmux.ErrNotFound) || errors.Is(err, tmux.ErrServerChanged) {
		t.Fatalf("stale session environment = %v", err)
	}
}

func TestCaptureIncludeEscapesRetainsHyperlinks(t *testing.T) {
	server, _, ctx := apiFixture(t)

	pane := firstPane(t, ctx, server)
	if err := pane.Submit(ctx, `printf '\033]8;;https://example.invalid\033\\LINK\033]8;;\033\\\n'`); err != nil {
		t.Fatal(err)
	}

	var opts tmux.CaptureOptions

	opts.IncludeEscapes = true

	var captured []byte

	awaitObservation(t, ctx, "OSC 8 capture", func() bool {
		var err error

		captured, err = pane.Capture(ctx, opts)

		return err == nil && bytes.Contains(captured, []byte("\x1b]8;;https://example.invalid"))
	})

	if !bytes.Contains(captured, []byte("LINK")) {
		t.Fatalf("linked text missing from capture: %q", captured)
	}

	plain, err := pane.Capture(ctx, tmux.CaptureOptions{})
	if err != nil || bytes.Contains(plain, []byte("\x1b]8;;https://example.invalid")) {
		t.Fatalf("plain capture = %q, %v", plain, err)
	}
}

func TestCaptureRangeEntireHistoryWithEnd(t *testing.T) {
	server, _, ctx := apiFixture(t)
	pane := firstPane(t, ctx, server)

	end := 10

	_, err := pane.Capture(ctx, tmux.CaptureOptions{
		EntireHistory: true,
		End:           &end,
	})
	if err != nil {
		t.Fatalf("pane.Capture with EntireHistory and End failed: %v", err)
	}
}
