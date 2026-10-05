//go:build integration

package test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

func graphState(t *testing.T, ctx context.Context, server *tmux.Server) map[string]string {
	t.Helper()

	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if snapshot.Consistency != tmux.ConsistencyComplete {
		t.Fatalf("inconsistent fixture: %+v", snapshot.MissingReferences())
	}

	state := map[string]string{}
	for _, session := range snapshot.Sessions() {
		state["session:"+string(session.ID)] = session.Name
	}

	for _, link := range snapshot.Links() {
		state[string(link.SessionID)+":"+strconv.Itoa(link.Index)] = string(link.WindowID)
	}

	for _, pane := range snapshot.Panes() {
		state[string(pane.ID)] = string(pane.WindowID)
	}

	return state
}

func assertGraph(t *testing.T, ctx context.Context, server *tmux.Server, want map[string]string) {
	t.Helper()

	if diff := cmp.Diff(want, graphState(t, ctx, server)); diff != "" {
		t.Fatalf("graph (-want +got):\n%s", diff)
	}
}

func graphWindow(t *testing.T, ctx context.Context, session tmux.Session) (tmux.WindowLink, tmux.Pane) {
	t.Helper()

	var options tmux.NewWindowOptions

	link, err := session.NewWindow(ctx, options)
	if err != nil {
		t.Fatal(err)
	}

	pane, err := link.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	return link, pane
}

func TestIntegrationUnprobedHandleLists(t *testing.T) {
	server, session, ctx := apiFixture(t)

	second, err := session.NewWindow(ctx, tmux.NewWindowOptions{Name: "second"})
	if err != nil {
		t.Fatal(err)
	}

	secondPane, err := second.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := secondPane.Split(ctx, tmux.SplitOptions{}); err != nil {
		t.Fatal(err)
	}

	other, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}

	zero := 0
	if _, err := second.Window().Link(ctx, other, tmux.LinkOptions{Index: &zero, Replace: true}); err != nil {
		t.Fatal(err)
	}

	unprobedSession, err := server.SessionHandle(session.ID())
	if err != nil {
		t.Fatal(err)
	}

	unprobedWindow, err := server.WindowHandle(second.Window().ID())
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name          string
		probed, found func() ([]string, error)
	}{
		{"Session.Windows", func() ([]string, error) { return linkKeys(session.Windows(ctx)) }, func() ([]string, error) { return linkKeys(unprobedSession.Windows(ctx)) }},
		{"Session.WindowInfos", func() ([]string, error) { return windowInfoKeys(session.WindowInfos(ctx)) }, func() ([]string, error) { return windowInfoKeys(unprobedSession.WindowInfos(ctx)) }},
		{"Session.Panes", func() ([]string, error) { return paneKeys(session.Panes(ctx)) }, func() ([]string, error) { return paneKeys(unprobedSession.Panes(ctx)) }},
		{"Window.Links", func() ([]string, error) { return linkKeys(second.Window().Links(ctx)) }, func() ([]string, error) { return linkKeys(unprobedWindow.Links(ctx)) }},
		{"Window.Panes", func() ([]string, error) { return paneKeys(second.Window().Panes(ctx)) }, func() ([]string, error) { return paneKeys(unprobedWindow.Panes(ctx)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertSameListing(t, tc.probed, tc.found)
		})
	}

	if _, err := unprobedSession.Clients(ctx); err != nil {
		t.Fatalf("Session.Clients: %v", err)
	}
}

func assertSameListing(t *testing.T, probed, unprobed func() ([]string, error)) {
	t.Helper()

	want, err := probed()
	if err != nil {
		t.Fatal(err)
	}

	got, err := unprobed()
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("unprobed handle listed different objects (-probed +unprobed):\n%s", diff)
	}
}

func linkKeys(links []tmux.WindowLink, err error) ([]string, error) {
	keys := make([]string, 0, len(links))
	for _, l := range links {
		keys = append(keys, string(l.Session().ID())+":"+strconv.Itoa(l.Index())+"="+string(l.Window().ID()))
	}

	return keys, err
}

func windowInfoKeys(windows []tmux.WindowInfo, links []tmux.WindowLinkInfo, err error) ([]string, error) {
	keys := make([]string, 0, len(windows)+len(links))
	for _, w := range windows {
		keys = append(keys, string(w.ID))
	}

	for _, l := range links {
		keys = append(keys, string(l.SessionID)+":"+strconv.Itoa(l.Index)+"="+string(l.WindowID))
	}

	return keys, err
}

func paneKeys(panes []tmux.PaneInfo, err error) ([]string, error) {
	keys := make([]string, 0, len(panes))
	for _, p := range panes {
		keys = append(keys, string(p.ID))
	}

	return keys, err
}

func TestIntegrationRenumberWindowsTargetsSession(t *testing.T) {
	server, session, ctx := apiFixture(t)

	other, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}

	index := 5
	for _, target := range []tmux.Session{session, other} {
		if _, err := target.NewWindow(ctx, tmux.NewWindowOptions{Index: &index}); err != nil {
			t.Fatal(err)
		}
	}

	if err := session.RenumberWindows(ctx); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		session tmux.Session
		want    []int
	}{
		{session, []int{0, 1}},
		{other, []int{0, 5}},
	} {
		links, err := tc.session.Windows(ctx)
		if err != nil {
			t.Fatal(err)
		}

		got := make([]int, len(links))
		for i, link := range links {
			got[i] = link.Index()
		}

		if diff := cmp.Diff(tc.want, got); diff != "" {
			t.Errorf("session %s indices (-want +got):\n%s", tc.session.ID(), diff)
		}
	}
}

func TestIntegrationNewSessionGroupUsesExactName(t *testing.T) {
	server, _, ctx := apiFixture(t)

	development, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "development"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "joined", Group: "dev"}); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("Group dev matched development: %v", err)
	}

	dev, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}

	joined, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "joined", Group: "dev"})
	if err != nil {
		t.Fatal(err)
	}

	assertInGroup(t, ctx, dev, "dev")
	assertInGroup(t, ctx, joined, "dev")

	if err := dev.Rename(ctx, "renamed"); err != nil {
		t.Fatal(err)
	}

	third, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "third", Group: "dev"})
	if err != nil {
		t.Fatal(err)
	}

	assertInGroup(t, ctx, third, "dev")

	if group, ok := sessionGroup(t, ctx, development); ok {
		t.Errorf("development joined group %q", group)
	}
}

func sessionGroup(t *testing.T, ctx context.Context, session tmux.Session) (string, bool) {
	t.Helper()

	info, err := session.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	return info.Group.Get()
}

func assertInGroup(t *testing.T, ctx context.Context, session tmux.Session, want string) {
	t.Helper()

	if group, ok := sessionGroup(t, ctx, session); !ok || group != want {
		t.Errorf("session %s group = %q, %t; want %s", session.ID(), group, ok, want)
	}
}

func TestIntegrationGraphUnlink(t *testing.T) {
	server, session, ctx := apiFixture(t)
	original, pane := graphWindow(t, ctx, session)

	var create tmux.NewSessionOptions

	create.Name = "other"

	other, err := server.NewSession(ctx, create)
	if err != nil {
		t.Fatal(err)
	}

	window, err := server.WindowHandle(original.Window().ID())
	if err != nil {
		t.Fatal(err)
	}

	var options tmux.LinkOptions

	shared, err := window.Link(ctx, other, options)
	if err != nil {
		t.Fatal(err)
	}

	if !shared.ServerIdentity().Equal(other.ServerIdentity()) || !shared.Window().Equal(original.Window()) {
		t.Fatalf("returned link lost window provenance: %+v", shared)
	}

	want := graphState(t, ctx, server)
	delete(want, string(other.ID())+":"+strconv.Itoa(shared.Index()))

	if err := shared.Unlink(ctx); err != nil {
		t.Fatal(err)
	}

	assertGraph(t, ctx, server, want)

	if _, err := pane.Info(ctx); err != nil {
		t.Fatalf("shared pane destroyed: %v", err)
	}

	if err := shared.Select(ctx); !errors.Is(err, tmux.ErrLinkChanged) {
		t.Fatalf("stale link: %v", err)
	}

	assertGraph(t, ctx, server, want)
}

func TestIntegrationGraphWindowSwap(t *testing.T) {
	server, session, ctx := apiFixture(t)
	first, _ := graphWindow(t, ctx, session)
	second, _ := graphWindow(t, ctx, session)
	want := graphState(t, ctx, server)
	a := string(session.ID()) + ":" + strconv.Itoa(first.Index())
	b := string(session.ID()) + ":" + strconv.Itoa(second.Index())
	want[a], want[b] = want[b], want[a]

	if err := first.Swap(ctx, second, tmux.SwapWindowOptions{Select: false}); err != nil {
		t.Fatal(err)
	}

	assertGraph(t, ctx, server, want)

	for _, old := range []tmux.WindowLink{first, second} {
		if err := old.Select(ctx); !errors.Is(err, tmux.ErrLinkChanged) {
			t.Fatalf("stale link accepted: %v", err)
		}
	}

	assertGraph(t, ctx, server, want)
}

func TestIntegrationGraphJoinBreak(t *testing.T) {
	server, session, ctx := apiFixture(t)
	source, moving := graphWindow(t, ctx, session)
	target, stationary := graphWindow(t, ctx, session)

	var split tmux.SplitOptions

	sentinel, err := moving.Split(ctx, split)
	if err != nil {
		t.Fatal(err)
	}

	want := graphState(t, ctx, server)
	want[string(moving.ID())] = string(target.Window().ID())

	var join tmux.JoinOptions
	if err := moving.Join(ctx, stationary, join); err != nil {
		t.Fatal(err)
	}

	assertGraph(t, ctx, server, want)

	if want[string(sentinel.ID())] != string(source.Window().ID()) {
		t.Fatal("bad source sentinel fixture")
	}

	var options tmux.BreakOptions

	// break-pane stores -n verbatim; format syntax must survive unchanged.
	options.Name = "broken #{session_name}"

	broken, err := moving.Break(ctx, session, options)
	if err != nil {
		t.Fatal(err)
	}

	want[string(moving.ID())] = string(broken.Window().ID())
	want[string(session.ID())+":"+strconv.Itoa(broken.Index())] = string(broken.Window().ID())
	assertGraph(t, ctx, server, want)

	panes, err := broken.Window().Panes(ctx)
	if err != nil || len(panes) != 1 || panes[0].ID != moving.ID() {
		t.Fatalf("returned window panes: %+v, %v", panes, err)
	}

	info, err := broken.Window().Info(ctx)
	if err != nil || info.Name != options.Name {
		t.Fatalf("broken window name = %q, %v; want %q", info.Name, err, options.Name)
	}
}

func TestIntegrationGraphPaneSwap(t *testing.T) {
	for _, tc := range []struct {
		name           string
		unprobedSource bool
		unprobedTarget bool
	}{
		{name: "verified"},
		{name: "unprobed-source", unprobedSource: true},
		{name: "unprobed-target", unprobedTarget: true},
		{name: "unprobed-both", unprobedSource: true, unprobedTarget: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, session, ctx := apiFixture(t)
			_, first := graphWindow(t, ctx, session)
			_, second := graphWindow(t, ctx, session)
			want := graphState(t, ctx, server)
			a, b := string(first.ID()), string(second.ID())
			want[a], want[b] = want[b], want[a]

			var err error
			if tc.unprobedSource {
				first, err = server.PaneHandle(first.ID())
				if err != nil {
					t.Fatal(err)
				}
			}

			if tc.unprobedTarget {
				second, err = server.PaneHandle(second.ID())
				if err != nil {
					t.Fatal(err)
				}
			}

			if err := first.Swap(ctx, second, tmux.SwapPaneOptions{Select: false}); err != nil {
				t.Fatal(err)
			}

			assertGraph(t, ctx, server, want)
		})
	}
}

func TestIntegrationGraphCrossServer(t *testing.T) {
	for _, tc := range []struct {
		name           string
		unprobedSource bool
		unprobedTarget bool
	}{
		{name: "verified"},
		{name: "unprobed-source", unprobedSource: true},
		{name: "unprobed-target", unprobedTarget: true},
		{name: "unprobed-both", unprobedSource: true, unprobedTarget: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, session, ctx := apiFixture(t)
			_, source := graphWindow(t, ctx, session)
			other := tmuxtest.NewServer(t)
			target := firstPane(t, ctx, other)
			before := graphState(t, ctx, server)
			otherBefore := graphState(t, ctx, other)
			// The foreign ID must name a pane in a different source window,
			// so an incorrectly routed mutation changes the graph.
			if collision, ok := before[string(target.ID())]; !ok || collision == before[string(source.ID())] {
				t.Fatal("fixture lacks a cross-window pane ID collision")
			}

			source = paneHandleFor(t, server, source, tc.unprobedSource)
			target = paneHandleFor(t, other, target, tc.unprobedTarget)

			var options tmux.JoinOptions

			for _, action := range []struct {
				name string
				run  func() error
			}{
				{name: "swap", run: func() error { return source.Swap(ctx, target, tmux.SwapPaneOptions{Select: false}) }},
				{name: "join", run: func() error { return source.Join(ctx, target, options) }},
			} {
				t.Run(action.name, func(t *testing.T) {
					assertRejectedBeforeSend(t, action.run(), tmux.ErrInvalidHandle)
					assertGraph(t, ctx, server, before)
					assertGraph(t, ctx, other, otherBefore)
				})
			}
		})
	}
}

func paneHandleFor(t *testing.T, server *tmux.Server, pane tmux.Pane, unprobed bool) tmux.Pane {
	t.Helper()

	if !unprobed {
		return pane
	}

	handle, err := server.PaneHandle(pane.ID())
	if err != nil {
		t.Fatal(err)
	}

	return handle
}

func assertRejectedBeforeSend(t *testing.T, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Errorf("mutation error: %v, want %v", err, want)
	}

	if op, ok := errors.AsType[*tmux.OperationError](err); !ok || op.Outcome.Effect != tmux.EffectNotSent {
		t.Errorf("mutation was not rejected before send: %v", err)
	}
}

func TestIntegrationGraphHandleLifetimes(t *testing.T) {
	for _, test := range []struct {
		name string
		want error
	}{
		{name: "control-and-subprocess", want: nil},
		{name: "different-connections", want: tmux.ErrInvalidHandle},
		{name: "unbound-target", want: tmux.ErrInvalidHandle},
		{name: "closed-connection", want: tmux.ErrClosed},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, session, ctx := apiFixture(t)
			_, first := graphWindow(t, ctx, session)
			_, second := graphWindow(t, ctx, session)
			connection := apiControl(t, ctx, server, session)
			targetServer := lifetimeTarget(t, ctx, server, session, connection, test.name)

			source, err := connection.Server().PaneHandle(first.ID())
			if err != nil {
				t.Fatal(err)
			}

			target, err := targetServer.PaneHandle(second.ID())
			if err != nil {
				t.Fatal(err)
			}

			want := graphState(t, ctx, server)

			err = source.Swap(ctx, target, tmux.SwapPaneOptions{Select: false})
			if test.want == nil {
				if err != nil {
					t.Errorf("swap error: %v, want nil", err)
				}

				a, b := string(first.ID()), string(second.ID())
				want[a], want[b] = want[b], want[a]
			} else {
				assertRejectedBeforeSend(t, err, test.want)
			}

			assertGraph(t, ctx, server, want)
		})
	}
}

func lifetimeTarget(t *testing.T, ctx context.Context, server *tmux.Server, session tmux.Session, connection *tmux.Connection, name string) *tmux.Server {
	t.Helper()

	switch name {
	case "different-connections":
		return apiControl(t, ctx, server, session).Server()
	case "unbound-target":
		return server
	case "closed-connection":
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	}

	return connection.SubprocessServer()
}

func TestIntegrationGraphMissingLookups(t *testing.T) {
	server, session, ctx := apiFixture(t)

	window, pane := graphWindow(t, ctx, session)
	if err := window.Window().Kill(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := server.Window(ctx, window.Window().ID()); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("missing window: %v", err)
	}

	if _, err := server.Pane(ctx, pane.ID()); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("missing pane: %v", err)
	}

	if _, err := pane.Info(ctx); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("stale pane: %v", err)
	}
}

func TestIntegrationResolveProvenanceAndMutations(t *testing.T) {
	server, session, ctx := apiFixture(t)

	winLink, _ := graphWindow(t, ctx, session)
	targetWin := winLink.Window()

	var sessCreate tmux.NewSessionOptions

	sessCreate.Name = "other-session"

	otherSession, err := server.NewSession(ctx, sessCreate)
	if err != nil {
		t.Fatal(err)
	}

	var ctrlOpts tmux.ControlOptions

	conn, err := server.OpenControl(ctx, session.ID(), ctrlOpts)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
	})

	snap := consistentSnapshot(t, ctx, server)
	controlClientInfo := snapshotControlClient(t, snap)

	resSession, ok := snap.ResolveSession(session.ID())
	assertResolved(t, "session", resSession, ok, session, snap.Identity)

	resWindow, ok := snap.ResolveWindow(targetWin.ID())
	assertResolved(t, "window", resWindow, ok, targetWin, snap.Identity)

	resClient, ok := snap.ResolveClient(controlClientInfo.Name)
	assertResolved(t, "client", resClient, ok, controlClientInfo.Handle(), snap.Identity)

	assertDecoysUnresolved(t, snap)

	foreignSession, err := tmuxtest.NewServer(t).FindSession(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}

	assertForeignTargetsRefused(t, ctx, resClient, resWindow, foreignSession)

	if err := resSession.Rename(ctx, "session-mutated"); err != nil {
		t.Fatal(err)
	}

	name, err := resSession.Format(ctx, "#{session_name}")
	assertFormatValues(t, "renamed session name", [][]byte{name}, err, "session-mutated")

	if err := resWindow.Rename(ctx, "win-renamed"); err != nil {
		t.Fatal(err)
	}

	name, err = resWindow.Format(ctx, "#{window_name}")
	assertFormatValues(t, "renamed window name", [][]byte{name}, err, "win-renamed")

	var swOpts tmux.SwitchOptions
	if err := resClient.Switch(ctx, otherSession, swOpts); err != nil {
		t.Fatal(err)
	}

	if sessID := clientSession(t, ctx, resClient); sessID != otherSession.ID() {
		t.Fatalf("expected switched client session %v, got %v", otherSession.ID(), sessID)
	}

	assertDetachedClientGone(t, ctx, server, conn, resClient)
}

func assertForeignTargetsRefused(t *testing.T, ctx context.Context, client tmux.Client, window tmux.Window, foreignSession tmux.Session) {
	t.Helper()

	var swOpts tmux.SwitchOptions
	if err := client.Switch(ctx, foreignSession, swOpts); !errors.Is(err, tmux.ErrInvalidHandle) {
		t.Fatalf("cross-server switch expected ErrInvalidHandle, got: %v", err)
	}

	var linkOpts tmux.LinkOptions
	if _, err := window.Link(ctx, foreignSession, linkOpts); !errors.Is(err, tmux.ErrInvalidHandle) {
		t.Fatalf("cross-server link expected ErrInvalidHandle, got: %v", err)
	}
}

func assertDetachedClientGone(t *testing.T, ctx context.Context, server *tmux.Server, conn *tmux.Connection, client tmux.Client) {
	t.Helper()

	if err := client.Detach(ctx); err != nil {
		t.Fatal(err)
	}

	if err := conn.Wait(ctx); !errors.Is(err, tmux.ErrClosed) {
		t.Fatalf("expected ErrClosed on connection wait after detach, got: %v", err)
	}

	if _, err := client.Info(ctx); !errors.Is(err, tmux.ErrClientChanged) {
		t.Fatalf("expected ErrClientChanged on detached client handle, got: %v", err)
	}

	if _, ok := consistentSnapshot(t, ctx, server).ResolveClient(client.Name()); ok {
		t.Fatalf("detached client %q still found in snapshot", client.Name())
	}
}

func consistentSnapshot(t *testing.T, ctx context.Context, server *tmux.Server) tmux.Snapshot {
	t.Helper()

	snap, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if snap.Consistency != tmux.ConsistencyComplete {
		t.Fatalf("inconsistent snapshot: %+v", snap.MissingReferences())
	}

	return snap
}

func snapshotControlClient(t *testing.T, snap tmux.Snapshot) tmux.ClientInfo {
	t.Helper()

	for _, c := range snap.Clients() {
		if c.Control {
			return c
		}
	}

	t.Fatal("control client not found in snapshot")

	return tmux.ClientInfo{}
}

type resolvedHandle[H any] interface {
	Valid() bool
	Equal(other H) bool
	ServerIdentity() tmux.ServerIdentity
}

func assertResolved[H resolvedHandle[H]](t *testing.T, kind string, got H, ok bool, want H, identity tmux.ServerIdentity) {
	t.Helper()

	if !ok || !got.Valid() || !got.Equal(want) || !got.ServerIdentity().Equal(identity) {
		t.Fatalf("%s resolution failed: ok=%v, resolved=%v", kind, ok, got)
	}
}

func assertDecoysUnresolved(t *testing.T, snap tmux.Snapshot) {
	t.Helper()

	_, session := snap.ResolveSession(tmux.SessionID("$9999"))
	_, window := snap.ResolveWindow(tmux.WindowID("@9999"))
	_, client := snap.ResolveClient(tmux.ClientName("/dev/pts/nonexistent"))

	if session || window || client {
		t.Fatalf("decoy IDs resolved: session=%v window=%v client=%v", session, window, client)
	}
}

// readFixture adds a two-pane "other" session and returns a request whose middle list is empty.
func readFixture(t *testing.T) (*tmux.Server, tmux.Session, context.Context, tmux.ReadRequest) {
	t.Helper()

	server, session, ctx := apiFixture(t)

	other, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}

	otherPanes, err := other.Panes(ctx)
	if err != nil || len(otherPanes) == 0 {
		t.Fatal("other session has no pane", err)
	}

	if _, err := otherPanes[0].Handle().Split(ctx, tmux.SplitOptions{}); err != nil {
		t.Fatal(err)
	}

	return server, session, ctx, tmux.ReadRequest{
		Sessions: tmux.PresentValue(tmux.QueryOptions{ExtraFields: []string{"session_last_attached"}}),
		Windows:  tmux.PresentValue(tmux.QueryOptions{Filter: "#{==:#{window_name},absent-window}"}),
		Panes:    tmux.PresentValue(tmux.QueryOptions{Filter: "#{==:#{session_name},other}"}),
		Clients:  tmux.PresentValue(tmux.QueryOptions{}),
	}
}

func TestIntegrationReadMatchesSeparateQueries(t *testing.T) {
	server, _, ctx, request := readFixture(t)
	sessionsOpts, _ := request.Sessions.Get()
	panesOpts, _ := request.Panes.Get()

	got, err := server.Read(ctx, request)
	if err != nil {
		t.Fatal(err)
	}

	assertSameListing(t, func() ([]string, error) { return sessionKeys(server.SessionsWith(ctx, sessionsOpts)) }, func() ([]string, error) { return sessionKeys(got.Sessions, nil) })
	assertSameListing(t, func() ([]string, error) { return paneKeys(server.PanesWith(ctx, panesOpts)) }, func() ([]string, error) { return paneKeys(got.Panes, nil) })
	assertRawField(t, got.Sessions, "session_last_attached")

	if len(got.Panes) != 2 || len(got.Windows) != 0 || len(got.Links) != 0 || len(got.Clients) != 0 {
		t.Fatalf("panes=%d windows=%d links=%d clients=%d, want the other session's 2 panes and empty lists", len(got.Panes), len(got.Windows), len(got.Links), len(got.Clients))
	}

	probe, err := server.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if !got.Server.Identity.Equal(probe.Identity) {
		t.Fatalf("read identity = %+v, probe identity = %+v", got.Server.Identity, probe.Identity)
	}
}

func TestIntegrationReadOverControlMatchesSubprocess(t *testing.T) {
	server, session, ctx, request := readFixture(t)

	viaSubprocess, err := server.Read(ctx, request)
	if err != nil {
		t.Fatal(err)
	}

	conn := apiControl(t, ctx, server, session)

	viaControl, err := conn.Server().Read(ctx, request)
	if err != nil {
		t.Fatal(err)
	}

	assertSameListing(t, func() ([]string, error) { return sessionKeys(viaSubprocess.Sessions, nil) }, func() ([]string, error) { return sessionKeys(viaControl.Sessions, nil) })
	assertSameListing(t, func() ([]string, error) { return paneKeys(viaSubprocess.Panes, nil) }, func() ([]string, error) { return paneKeys(viaControl.Panes, nil) })

	if len(viaControl.Clients) != 1 {
		t.Fatalf("control read clients = %d, want the control client", len(viaControl.Clients))
	}
}

func TestIntegrationReadReportsMissingServer(t *testing.T) {
	server, _, ctx := apiFixture(t)

	if err := server.Kill(ctx); err != nil {
		t.Fatal(err)
	}

	_, err := server.Read(ctx, tmux.ReadRequest{Panes: tmux.PresentValue(tmux.QueryOptions{})})
	if !errors.Is(err, tmux.ErrNoServer) {
		t.Fatalf("err = %v, want ErrNoServer", err)
	}
}

// linkedPaneFixture links a window from an "other" session into the fixture session and
// returns the linked window's pane and a pane that only the other session contains.
func linkedPaneFixture(t *testing.T) (tmux.Session, context.Context, tmux.PaneID, tmux.PaneID) {
	t.Helper()

	server, session, ctx := apiFixture(t)

	other, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "other"})
	if err != nil {
		t.Fatal(err)
	}

	otherPanes, err := other.Panes(ctx)
	if err != nil || len(otherPanes) == 0 {
		t.Fatal("other session has no pane", err)
	}

	shared, err := other.NewWindow(ctx, tmux.NewWindowOptions{Name: "shared"})
	if err != nil {
		t.Fatal(err)
	}

	sharedPane, err := shared.Window().ActivePane(ctx)
	if err != nil {
		t.Fatal(err)
	}

	slot := 5
	if _, err := shared.Window().Link(ctx, session, tmux.LinkOptions{Index: &slot}); err != nil {
		t.Fatal(err)
	}

	return session, ctx, sharedPane.ID(), otherPanes[0].ID
}

func TestIntegrationSessionPanesWithStaysInSession(t *testing.T) {
	session, ctx, linkedPane, outsidePane := linkedPaneFixture(t)

	byID := func(id tmux.PaneID) tmux.QueryOptions {
		return tmux.QueryOptions{Filter: tmux.Format("#{==:#{pane_id}," + string(id) + "}")}
	}

	linked, err := paneKeys(session.PanesWith(ctx, byID(linkedPane)))
	if err != nil || len(linked) != 1 || linked[0] != string(linkedPane) {
		t.Fatalf("linked pane lookup = %v %v, want %s", linked, err, linkedPane)
	}

	outside, err := paneKeys(session.PanesWith(ctx, byID(outsidePane)))
	if err != nil || len(outside) != 0 {
		t.Fatalf("pane outside session = %v %v, want none", outside, err)
	}

	withExtra, err := session.PanesWith(ctx, tmux.QueryOptions{ExtraFields: []string{"window_active"}})
	if err != nil || len(withExtra) == 0 {
		t.Fatal("session panes with extra field", err)
	}

	assertRawField(t, withExtra, "window_active")
}

type rawFields interface {
	Raw(name string) ([]byte, bool)
}

func assertRawField[R rawFields](t *testing.T, records []R, field string) {
	t.Helper()

	for i, record := range records {
		if _, ok := record.Raw(field); !ok {
			t.Fatalf("record %d missing requested field %s", i, field)
		}
	}
}

func sessionKeys(sessions []tmux.SessionInfo, err error) ([]string, error) {
	keys := make([]string, 0, len(sessions))
	for _, s := range sessions {
		keys = append(keys, string(s.ID))
	}

	return keys, err
}
