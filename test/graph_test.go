//go:build integration

package tmux_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"

	tmux "github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

func graphState(t *testing.T, ctx context.Context, server *tmux.Server) map[string]string {
	t.Helper()

	snapshot, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if snapshot.Consistency != tmux.Consistent {
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
	if !shared.Identity().Equal(other.Identity()) || !shared.Window().Equal(original.Window()) {
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

	if err := first.Swap(ctx, second, false); err != nil {
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

	options.Name = "broken"

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

			if err := first.Swap(ctx, second, false); err != nil {
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
			target := firstPane(t, other, ctx)
			before := graphState(t, ctx, server)
			otherBefore := graphState(t, ctx, other)
			// The foreign ID must name a pane in a different source window,
			// so an incorrectly routed mutation changes the graph.
			if collision, ok := before[string(target.ID())]; !ok || collision == before[string(source.ID())] {
				t.Fatal("fixture lacks a cross-window pane ID collision")
			}

			var err error
			if tc.unprobedSource {
				source, err = server.PaneHandle(source.ID())
				if err != nil {
					t.Fatal(err)
				}
			}
			if tc.unprobedTarget {
				target, err = other.PaneHandle(target.ID())
				if err != nil {
					t.Fatal(err)
				}
			}

			var options tmux.JoinOptions
			for _, action := range []struct {
				name string
				run  func() error
			}{
				{name: "swap", run: func() error { return source.Swap(ctx, target, false) }},
				{name: "join", run: func() error { return source.Join(ctx, target, options) }},
			} {
				t.Run(action.name, func(t *testing.T) {
					err := action.run()
					if !errors.Is(err, tmux.ErrInvalidHandle) {
						t.Errorf("cross-server mutation: %v", err)
					}
					if op, ok := errors.AsType[*tmux.OperationError](err); !ok || op.Outcome.Effect != tmux.NotSent {
						t.Errorf("cross-server mutation was not rejected before send: %v", err)
					}

					assertGraph(t, ctx, server, before)
					assertGraph(t, ctx, other, otherBefore)
				})
			}
		})
	}
}

func TestIntegrationGraphHandleLifetimes(t *testing.T) {
	for _, test := range []struct {
		name string
		want error
	}{
		{name: "control-and-auxiliary", want: nil},
		{name: "different-connections", want: tmux.ErrInvalidHandle},
		{name: "unbound-target", want: tmux.ErrInvalidHandle},
		{name: "closed-connection", want: tmux.ErrClosed},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, session, ctx := apiFixture(t)
			_, first := graphWindow(t, ctx, session)
			_, second := graphWindow(t, ctx, session)
			connection := apiControl(t, server, session, ctx)
			targetServer := connection.AuxiliaryServer()
			switch test.name {
			case "different-connections":
				targetServer = apiControl(t, server, session, ctx).Server()
			case "unbound-target":
				targetServer = server
			case "closed-connection":
				if err := connection.Close(); err != nil {
					t.Fatal(err)
				}
			}

			source, err := connection.Server().PaneHandle(first.ID())
			if err != nil {
				t.Fatal(err)
			}
			target, err := targetServer.PaneHandle(second.ID())
			if err != nil {
				t.Fatal(err)
			}
			want := graphState(t, ctx, server)
			if test.want == nil {
				a, b := string(first.ID()), string(second.ID())
				want[a], want[b] = want[b], want[a]
			}

			err = source.Swap(ctx, target, false)
			if !errors.Is(err, test.want) {
				t.Errorf("swap error: %v, want %v", err, test.want)
			}
			if test.want != nil {
				if op, ok := errors.AsType[*tmux.OperationError](err); !ok || op.Outcome.Effect != tmux.NotSent {
					t.Errorf("incompatible lifetime was not rejected before send: %v", err)
				}
			}
			assertGraph(t, ctx, server, want)
		})
	}
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

	var winCreate tmux.NewWindowOptions
	winCreate.Name = "win-initial"

	winLink, err := session.NewWindow(ctx, winCreate)
	if err != nil {
		t.Fatal(err)
	}

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

	snap, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Consistency != tmux.Consistent {
		t.Fatalf("inconsistent snapshot: %+v", snap.MissingReferences())
	}

	var controlClientInfo tmux.ClientInfo
	var found bool

	for _, c := range snap.Clients() {
		if c.Control {
			controlClientInfo = c
			found = true

			break
		}
	}

	if !found {
		t.Fatal("control client not found in snapshot")
	}

	resSession, ok := snap.ResolveSession(session.ID())
	if !ok || !resSession.Valid() || !resSession.Equal(session) || !resSession.Identity().Equal(snap.Identity) {
		t.Fatalf("session resolution failed: ok=%v, resSession=%v", ok, resSession)
	}

	resWindow, ok := snap.ResolveWindow(targetWin.ID())
	if !ok || !resWindow.Valid() || !resWindow.Equal(targetWin) || !resWindow.Identity().Equal(snap.Identity) {
		t.Fatalf("window resolution failed: ok=%v, resWindow=%v", ok, resWindow)
	}

	resClient, ok := snap.ResolveClient(controlClientInfo.Name)
	if !ok || !resClient.Valid() || !resClient.Equal(controlClientInfo.Handle()) || !resClient.Identity().Equal(snap.Identity) {
		t.Fatalf("client resolution failed: ok=%v, resClient=%v", ok, resClient)
	}

	if _, ok := snap.ResolveSession(tmux.SessionID("$9999")); ok {
		t.Fatal("expected decoy session resolution to fail")
	}
	if _, ok := snap.ResolveWindow(tmux.WindowID("@9999")); ok {
		t.Fatal("expected decoy window resolution to fail")
	}
	if _, ok := snap.ResolveClient(tmux.ClientName("/dev/pts/nonexistent")); ok {
		t.Fatal("expected decoy client resolution to fail")
	}

	foreignServer := tmuxtest.NewServer(t)
	foreignSession, err := foreignServer.FindSession(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}

	var swOpts tmux.SwitchOptions
	if err := resClient.Switch(ctx, foreignSession, swOpts); !errors.Is(err, tmux.ErrInvalidHandle) {
		t.Fatalf("cross-server switch expected ErrInvalidHandle, got: %v", err)
	}

	var linkOpts tmux.LinkOptions
	if _, err := resWindow.Link(ctx, foreignSession, linkOpts); !errors.Is(err, tmux.ErrInvalidHandle) {
		t.Fatalf("cross-server link expected ErrInvalidHandle, got: %v", err)
	}

	if err := resSession.Rename(ctx, "session-mutated"); err != nil {
		t.Fatal(err)
	}
	sinfo, err := resSession.Info(ctx)
	if err != nil || sinfo.Name != "session-mutated" {
		t.Fatalf("expected renamed session name 'session-mutated', got: %v, err=%v", sinfo.Name, err)
	}

	if err := resWindow.Rename(ctx, "win-renamed"); err != nil {
		t.Fatal(err)
	}
	winfo, err := resWindow.Info(ctx)
	if err != nil || winfo.Name != "win-renamed" {
		t.Fatalf("expected renamed window name 'win-renamed', got: %v, err=%v", winfo.Name, err)
	}

	if err := resClient.Switch(ctx, otherSession, swOpts); err != nil {
		t.Fatal(err)
	}
	cinfo, err := resClient.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sessID, ok := cinfo.SessionID.Get(); !ok || sessID != otherSession.ID() {
		t.Fatalf("expected switched client session %v, got ok=%v id=%v", otherSession.ID(), ok, sessID)
	}

	if err := resClient.Detach(ctx); err != nil {
		t.Fatal(err)
	}

	if err := conn.Wait(ctx); !errors.Is(err, tmux.ErrClosed) {
		t.Fatalf("expected ErrClosed on connection wait after detach, got: %v", err)
	}

	if _, err := resClient.Info(ctx); !errors.Is(err, tmux.ErrClientChanged) {
		t.Fatalf("expected ErrClientChanged on detached client handle, got: %v", err)
	}

	snapPost, err := server.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapPost.ResolveClient(resClient.Name()); ok {
		t.Fatalf("detached client %q still found in snapshot", resClient.Name())
	}
}
