//go:build integration

package test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zigai/gotmux/tmux"
)

func currentVars(identity tmux.ServerIdentity, session tmux.SessionID, pane tmux.PaneID) tmux.TmuxVars {
	return tmux.TmuxVars{
		TMUX:     fmt.Sprintf("%s,%d,%s", identity.ReportedSocket, identity.PID, strings.TrimPrefix(string(session), "$")),
		TMUXPane: string(pane),
	}
}

func assertNoCurrentContext(t *testing.T, info tmux.CurrentInfo) {
	t.Helper()

	_, hasSession := info.Session.Get()
	_, hasLink := info.Link.Get()

	_, hasClient := info.Client.Get()
	if info.Identity.PID != 0 || info.Pane.ID.Valid() || info.Window.ID.Valid() || hasSession || hasLink || hasClient {
		t.Fatalf("failed discovery returned a current context: %+v", info)
	}
}

func TestIntegrationCurrentFromDiscovery(t *testing.T) {
	server, session, ctx := apiFixture(t)
	explicitLink, explicitPane := graphWindow(t, ctx, session)

	activeLink, activePane := graphWindow(t, ctx, session)
	if err := activeLink.Select(ctx); err != nil {
		t.Fatal(err)
	}

	identity, err := server.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		hint tmux.PaneID
		pane tmux.PaneID
		link tmux.WindowLink
	}{
		{name: "explicit-pane", hint: explicitPane.ID(), pane: explicitPane.ID(), link: explicitLink},
		{name: "active-pane", pane: activePane.ID(), link: activeLink},
	} {
		t.Run(test.name, func(t *testing.T) {
			info, err := server.CurrentFrom(ctx, currentVars(identity.Identity, session.ID(), test.hint))
			if err != nil {
				t.Fatal(err)
			}

			assertCurrentContext(t, info, identity.Identity, session.ID(), test.pane, test.link)
		})
	}
}

func assertCurrentContext(t *testing.T, info tmux.CurrentInfo, identity tmux.ServerIdentity, session tmux.SessionID, pane tmux.PaneID, link tmux.WindowLink) {
	t.Helper()

	window := link.Window().ID()
	if !info.Identity.Equal(identity) || info.Pane.ID != pane || info.Window.ID != window || info.Pane.WindowID != window {
		t.Fatalf("wrong daemon/pane/window context: %+v", info)
	}

	if got, ok := info.Session.Get(); !ok || got.ID != session {
		t.Fatalf("session: %+v, available=%v", got, ok)
	}

	if got, ok := info.Link.Get(); !ok || got.SessionID != session || got.WindowID != window || got.Index != link.Index() {
		t.Fatalf("link: %+v, available=%v", got, ok)
	}
}

func TestIntegrationCurrentFromRejectsInvalidContext(t *testing.T) {
	server, session, ctx := apiFixture(t)
	pane := firstPane(t, ctx, server)

	identity, err := server.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	wrongPID := identity.Identity
	wrongPID.PID++
	wrongSocket := identity.Identity
	wrongSocket.ReportedSocket = filepath.Join(t.TempDir(), "other.sock")

	for _, test := range []struct {
		name string
		env  tmux.TmuxVars
		want error
	}{
		{name: "pid-with-pane", env: currentVars(wrongPID, session.ID(), pane.ID()), want: tmux.ErrServerChanged},
		{name: "pid-without-pane", env: currentVars(wrongPID, session.ID(), ""), want: tmux.ErrServerChanged},
		{name: "socket", env: currentVars(wrongSocket, session.ID(), pane.ID()), want: tmux.ErrInvalidHandle},
		{name: "missing-pane", env: currentVars(identity.Identity, session.ID(), "%999999"), want: tmux.ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			info, err := server.CurrentFrom(ctx, test.env)
			if !errors.Is(err, test.want) {
				t.Fatalf("CurrentFrom error: %v, want %v", err, test.want)
			}

			assertNoCurrentContext(t, info)
		})
	}
}
