//go:build integration

package test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

func TestIntegrationSocketDiscoveryVerifiesDaemons(t *testing.T) {
	ctx := integrationContext(t)
	directory := shortTempDir(t)
	t.Chdir(directory)
	t.Setenv("TMUX_TMPDIR", "relative")

	socketDirectory := filepath.Join(directory, "relative", "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(socketDirectory, 0o700); err != nil {
		t.Fatal(err)
	}

	server := discoveryFixture(t, ctx, directory, filepath.Join(socketDirectory, "live"))
	stalePath := filepath.Join(socketDirectory, "stale")
	staleDiscoverySocket(t, stalePath)

	otherPath := filepath.Join(socketDirectory, "other")
	nonDaemonDiscoverySocket(t, otherPath)

	sockets, err := tmux.DiscoverSockets()
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(sockets, server.Endpoint().SocketPath) {
		t.Errorf("live socket not discovered: %v", sockets)
	}

	for _, socket := range sockets {
		if !filepath.IsAbs(socket) || slices.Contains([]string{stalePath, otherPath}, socket) {
			t.Errorf("unverified or relative socket discovered: %q", socket)
		}
	}

	servers, err := tmux.DiscoverServers(tmux.Config{Binary: os.Getenv("TMUX_TEST_BINARY")})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.ContainsFunc(servers, func(found *tmux.Server) bool { return found.Endpoint() == server.Endpoint() }) {
		t.Error("live daemon not returned by DiscoverServers")
	}

	for _, found := range servers {
		if socket := found.Endpoint().SocketPath; slices.Contains([]string{stalePath, otherPath}, socket) {
			t.Errorf("non-daemon server returned: %q", socket)
		}
	}
}

func discoveryFixture(t *testing.T, ctx context.Context, directory, socket string) *tmux.Server {
	t.Helper()

	server, err := tmux.New(tmux.Config{
		Binary: os.Getenv("TMUX_TEST_BINARY"), SocketPath: socket,
		ConfigFile: "/dev/null", Env: testEnvironment(directory),
	})
	if err != nil {
		t.Fatal(err)
	}

	session, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "discovered", Start: tmux.StartPolicyAllowStart})
	if session.Valid() {
		identity := session.ServerIdentity()

		t.Cleanup(func() {
			if err := server.KillMatching(ctx, identity); err != nil {
				t.Error(err)
			}
		})
	}

	if err != nil {
		t.Fatal(err)
	}

	return server
}

func staleDiscoverySocket(t *testing.T, path string) {
	t.Helper()

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}

	listener.SetUnlinkOnClose(false)

	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
}

func nonDaemonDiscoverySocket(t *testing.T, path string) {
	t.Helper()

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)

		for {
			connection, err := listener.AcceptUnix()
			if err != nil {
				return
			}

			if err := connection.Close(); err != nil {
				t.Error(err)
			}
		}
	}()

	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}

		<-done
	})
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
