//go:build integration

package tmux_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tmux "github.com/zigai/gotmux/tmux"
)

func currentEnvironment(identity tmux.ServerIdentity, session tmux.SessionID, pane tmux.PaneID) tmux.Environment {
	return tmux.Environment{
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

func TestIntegrationCurrentWithEnvDiscovery(t *testing.T) {
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
			info, err := server.CurrentWithEnv(ctx, currentEnvironment(identity.Identity, session.ID(), test.hint))
			if err != nil {
				t.Fatal(err)
			}
			if !info.Identity.Equal(identity.Identity) || info.Pane.ID != test.pane || info.Window.ID != test.link.Window().ID() || info.Pane.WindowID != info.Window.ID {
				t.Fatalf("wrong daemon/pane/window context: %+v", info)
			}
			if got, ok := info.Session.Get(); !ok || got.ID != session.ID() {
				t.Fatalf("session: %+v, available=%v", got, ok)
			}
			if got, ok := info.Link.Get(); !ok || got.SessionID != session.ID() || got.WindowID != test.link.Window().ID() || got.Index != test.link.Index() {
				t.Fatalf("link: %+v, available=%v", got, ok)
			}
		})
	}
}

func TestIntegrationCurrentWithEnvRejectsInvalidContext(t *testing.T) {
	server, session, ctx := apiFixture(t)
	pane := firstPane(t, server, ctx)
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
		env  tmux.Environment
		want error
	}{
		{name: "pid-with-pane", env: currentEnvironment(wrongPID, session.ID(), pane.ID()), want: tmux.ErrServerChanged},
		{name: "pid-without-pane", env: currentEnvironment(wrongPID, session.ID(), ""), want: tmux.ErrServerChanged},
		{name: "socket", env: currentEnvironment(wrongSocket, session.ID(), pane.ID()), want: tmux.ErrInvalidHandle},
		{name: "missing-pane", env: currentEnvironment(identity.Identity, session.ID(), "%999999"), want: tmux.ErrNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			info, err := server.CurrentWithEnv(ctx, test.env)
			if !errors.Is(err, test.want) {
				t.Fatalf("CurrentWithEnv error: %v, want %v", err, test.want)
			}
			assertNoCurrentContext(t, info)
		})
	}
}
