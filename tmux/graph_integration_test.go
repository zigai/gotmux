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

	var options tmux.LinkOptions

	shared, err := original.Window().Link(ctx, other, options)
	if err != nil {
		t.Fatal(err)
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
	server, session, ctx := apiFixture(t)
	_, first := graphWindow(t, ctx, session)
	_, second := graphWindow(t, ctx, session)
	want := graphState(t, ctx, server)
	a, b := string(first.ID()), string(second.ID())
	want[a], want[b] = want[b], want[a]

	if err := first.Swap(ctx, second, false); err != nil {
		t.Fatal(err)
	}

	assertGraph(t, ctx, server, want)
}

func TestIntegrationGraphCrossServer(t *testing.T) {
	server, session, ctx := apiFixture(t)
	_, source := graphWindow(t, ctx, session)
	other := tmuxtest.NewServer(t)
	target := firstPane(t, other, ctx)
	before := graphState(t, ctx, server)
	otherBefore := graphState(t, ctx, other)

	var options tmux.JoinOptions

	for _, action := range []func() error{
		func() error { return source.Swap(ctx, target, false) },
		func() error { return source.Join(ctx, target, options) },
	} {
		if err := action(); !errors.Is(err, tmux.ErrInvalidHandle) {
			t.Fatalf("cross-server mutation: %v", err)
		}

		assertGraph(t, ctx, server, before)
		assertGraph(t, ctx, other, otherBefore)
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
