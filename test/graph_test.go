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

	conn, err := server.OpenControl(ctx, session, ctrlOpts)
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
