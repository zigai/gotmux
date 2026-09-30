//go:build integration && (linux || darwin)

package tmux_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tmux "github.com/zigai/gotmux/tmux"
)

func attachOn(t *testing.T, ctx context.Context, server *tmux.Server, session tmux.Session, slave *os.File) (tmux.Client, chan error) {
	t.Helper()

	var options tmux.AttachOptions

	return attachWith(t, ctx, server, session, slave, options)
}

func attachWith(t *testing.T, ctx context.Context, server *tmux.Server, session tmux.Session, slave *os.File, options tmux.AttachOptions) (tmux.Client, chan error) {
	t.Helper()

	client, _, done := attachCancelable(t, ctx, server, session, slave, options)

	return client, done
}

func attachCancelable(t *testing.T, ctx context.Context, server *tmux.Server, session tmux.Session, slave *os.File, options tmux.AttachOptions) (tmux.Client, context.CancelFunc, chan error) {
	t.Helper()

	attachCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	finished := make(chan struct{})

	go func() {
		defer close(finished)

		done <- session.Attach(attachCtx, tmux.Streams{In: slave, Out: slave, Err: slave}, options)
	}()

	// Registered after openPTY's cleanup, so it runs first: Attach must stop
	// using the terminal before the terminal is closed.
	t.Cleanup(func() {
		cancel()

		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("attachment failed to stop")
		}
	})

	return waitTerminalClient(t, ctx, server, slave, done), cancel, done
}

func clientSession(t *testing.T, ctx context.Context, client tmux.Client) tmux.SessionID {
	t.Helper()

	info, err := client.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}

	id, ok := info.SessionID.Get()
	if !ok {
		t.Fatalf("client %s reports no session", info.Name)
	}

	return id
}

func clientNames(t *testing.T, ctx context.Context, server *tmux.Server) []tmux.ClientName {
	t.Helper()

	clients, err := server.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}

	names := make([]tmux.ClientName, 0, len(clients))
	for _, c := range clients {
		names = append(names, c.Name)
	}

	return names
}

func TestIntegrationClientHandleRefusesReattachedTerminal(t *testing.T) {
	server, session, ctx := apiFixture(t)
	master, slave := openPTY(t)

	readDone := make(chan struct{})
	go func() { defer close(readDone); _, _ = io.Copy(io.Discard, master) }()

	t.Cleanup(func() { _ = master.Close(); <-readDone })

	old, firstDone := attachOn(t, ctx, server, session, slave)
	if err := old.Detach(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case <-firstDone:
	case <-ctx.Done():
		t.Fatal("first attachment did not end after Detach")
	}

	current, _ := attachOn(t, ctx, server, session, slave)
	if current.Name() != old.Name() {
		t.Fatalf("re-attached as %q, want the same terminal name %q", current.Name(), old.Name())
	}

	if err := old.Message(ctx, "stale"); !errors.Is(err, tmux.ErrClientChanged) {
		t.Fatalf("old handle on a re-attached terminal: got %v, want ErrClientChanged", err)
	}

	if err := current.Message(ctx, "current"); err != nil {
		t.Fatalf("current handle: %v", err)
	}
}

func TestIntegrationSwitchAndDetachActOnOneClient(t *testing.T) {
	server, first, ctx := apiFixture(t)

	second, err := server.NewSession(ctx, tmux.NewSessionOptions{Name: "second", Program: tmux.Shell("sleep 60")})
	if err != nil {
		t.Fatal(err)
	}

	moved, _, _ := uiClient(t, ctx, server, first)
	stays, _, _ := uiClient(t, ctx, server, first)

	var options tmux.SwitchOptions
	if err := moved.Switch(ctx, second, options); err != nil {
		t.Fatal(err)
	}

	if got := clientSession(t, ctx, moved); got != second.ID() {
		t.Fatalf("switched client shows %s, want %s", got, second.ID())
	}

	if got := clientSession(t, ctx, stays); got != first.ID() {
		t.Fatalf("other client moved to %s, want it to stay on %s", got, first.ID())
	}

	if err := moved.Detach(ctx); err != nil {
		t.Fatal(err)
	}

	awaitObservation(t, ctx, "the detached client to disappear", func() bool {
		return !slices.Contains(clientNames(t, ctx, server), moved.Name())
	})

	if !slices.Contains(clientNames(t, ctx, server), stays.Name()) {
		t.Fatalf("Detach removed the other client %s too", stays.Name())
	}
}

func TestIntegrationServerClientLookup(t *testing.T) {
	server, session, ctx := apiFixture(t)
	attached, _, _ := uiClient(t, ctx, server, session)
	uiClient(t, ctx, server, session)

	found, err := server.Client(ctx, attached.Name())
	if err != nil {
		t.Fatal(err)
	}

	info, err := found.Info(ctx)
	if err != nil || info.Name != attached.Name() {
		t.Fatalf("Server.Client(%q) returned %q, %v", attached.Name(), info.Name, err)
	}

	if _, err := server.Client(ctx, "/dev/no-such-terminal"); !errors.Is(err, tmux.ErrNotFound) {
		t.Fatalf("unknown client: got %v, want ErrNotFound", err)
	}
}

func TestIntegrationClosePopupClosesOneClientsPopup(t *testing.T) {
	server, session, ctx := apiFixture(t)
	dir := t.TempDir()

	type popup struct {
		client tmux.Client
		ready  string
		closed chan error
	}

	// Attach both clients before opening either popup.
	clients := make([]tmux.Client, 0, 2)

	for range 2 {
		client, _, _ := uiClient(t, ctx, server, session)
		clients = append(clients, client)
	}

	// Open the popups one at a time: tmux 3.6 can crash when two clients open
	// popups at the same moment, which this claim is not about.
	popups := make([]popup, len(clients))
	for i, client := range clients {
		ready := filepath.Join(dir, "ready"+string(rune('0'+i)))

		var options tmux.PopupOptions

		options.Program = tmux.Exec("/bin/sh", "-c", `touch "$0"; exec sleep 60`, ready)
		options.CloseOnExit = true

		closed := make(chan error, 1)
		go func() { closed <- client.Popup(ctx, options) }()

		popups[i] = popup{client: client, ready: ready, closed: closed}

		awaitObservation(t, ctx, "popup program to start", func() bool {
			select {
			case err := <-closed:
				t.Fatalf("popup %d returned before its program started: %v", i, err)
			default:
			}

			_, err := os.Stat(ready)

			return err == nil
		})
	}

	if err := popups[0].client.ClosePopup(ctx); err != nil {
		t.Fatal(err)
	}

	// A closed popup returns nil, not tmux's exit status 129.
	waitUIResult(t, ctx, popups[0].closed)

	select {
	case err := <-popups[1].closed:
		t.Fatalf("ClosePopup on one client also closed the other client's popup (%v)", err)
	case <-time.After(200 * time.Millisecond):
	}

	if err := popups[1].client.ClosePopup(ctx); err != nil {
		t.Fatal(err)
	}

	waitUIResult(t, ctx, popups[1].closed)
}

func TestIntegrationMenuAndPopupReturnOnCancellation(t *testing.T) {
	server, session, ctx := apiFixture(t)

	info, err := server.Probe(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Menu", func(t *testing.T) {
		client, _, output := uiClient(t, ctx, server, session)
		command := testCommand(t, "set-option", "-t", string(session.ID()), "@cancel-effect", "ran")
		items := []tmux.MenuItem{{Label: "TGO_CANCEL_MENU", Key: "x", Commands: testSequence(t, command), IsSeparator: false, Disabled: false}}

		menuCtx, cancel := context.WithCancel(ctx)
		result := make(chan error, 1)

		go func() {
			var options tmux.MenuOptions
			result <- client.Menu(menuCtx, items, options)
		}()

		awaitObservation(t, ctx, "menu rendered", func() bool { return output.contains("TGO_CANCEL_MENU") })

		if info.Version.AtLeast(3, 8) {
			// From tmux 3.8, display-menu returns once the menu is shown.
			if err := receiveResult(t, ctx, result, "Menu"); err != nil {
				t.Fatalf("Menu on tmux %s returned %v, want nil once shown", info.Version.Raw, err)
			}

			cancel()
		} else {
			select {
			case err := <-result:
				t.Fatalf("Menu on tmux %s returned before dismissal: %v", info.Version.Raw, err)
			default:
			}

			cancel()

			if err := receiveResult(t, ctx, result, "canceled Menu"); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled Menu returned %v, want context.Canceled", err)
			}
		}

		value, err := session.Options().User(ctx, "@cancel-effect")
		if err != nil {
			t.Fatal(err)
		}

		if got, ok := value.Local.Get(); ok {
			t.Fatalf("canceling the menu ran its item (@cancel-effect=%q)", got)
		}
	})

	t.Run("Popup", func(t *testing.T) {
		client, _, _ := uiClient(t, ctx, server, session)
		ready := filepath.Join(t.TempDir(), "ready")

		var options tmux.PopupOptions

		options.Program = tmux.Exec("/bin/sh", "-c", `touch "$0"; exec sleep 60`, ready)

		popupCtx, cancel := context.WithCancel(ctx)
		result := make(chan error, 1)

		go func() { result <- client.Popup(popupCtx, options) }()

		awaitFile(t, ctx, ready)
		cancel()

		if err := receiveResult(t, ctx, result, "canceled Popup"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Popup returned %v, want context.Canceled", err)
		}

		if err := client.ClosePopup(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

func receiveResult(t *testing.T, ctx context.Context, result <-chan error, call string) error {
	t.Helper()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatalf("%s did not return: %v", call, ctx.Err())

		return nil
	}
}

func TestIntegrationClientMessageStaysLiteral(t *testing.T) {
	server, session, ctx := apiFixture(t)
	client, _, _ := uiClient(t, ctx, server, session)
	marker := filepath.Join(t.TempDir(), "expanded")
	text := hostileValue(marker)

	if err := client.Message(ctx, text); err != nil {
		t.Fatal(err)
	}

	var options tmux.MessagesOptions

	var logged []string

	awaitObservation(t, ctx, "the message in the client's log", func() bool {
		lines, err := client.Messages(ctx, options)
		if err != nil {
			t.Fatal(err)
		}

		logged = lines

		return slices.ContainsFunc(lines, func(line string) bool { return strings.HasSuffix(line, text) })
	})

	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tmux ran a #() substitution from a message: %s exists (log %q)", marker, logged)
	}
}

func TestIntegrationConnectionClientIsItsOwn(t *testing.T) {
	server, session, ctx := apiFixture(t)
	first := apiControl(t, server, session, ctx)
	second := apiControl(t, server, session, ctx)

	firstClient, err := first.Client(ctx)
	if err != nil {
		t.Fatal(err)
	}

	secondClient, err := second.Client(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if firstClient.Name() == secondClient.Name() {
		t.Fatalf("both connections report client %q", firstClient.Name())
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	awaitObservation(t, ctx, "the closed connection's client to disappear", func() bool {
		return !slices.Contains(clientNames(t, ctx, server), firstClient.Name())
	})

	if !slices.Contains(clientNames(t, ctx, server), secondClient.Name()) {
		t.Fatalf("closing the first connection removed the second connection's client %q", secondClient.Name())
	}
}

func TestIntegrationConcurrentCallersGetTheirOwnReplies(t *testing.T) {
	server, session, ctx := apiFixture(t)
	connection := apiControl(t, server, session, ctx)

	panes, err := connection.Server().Panes(ctx)
	if err != nil || len(panes) == 0 {
		t.Fatalf("panes: %d, %v", len(panes), err)
	}

	pane := panes[0].Handle()
	paneID := string(pane.ID())

	const callers = 40

	var wg sync.WaitGroup

	for i := range callers {
		wg.Go(func() {
			tag := "caller-" + strings.Repeat("x", i)

			got, err := pane.Format(ctx, tmux.Format(tag+"-#{pane_id}"))
			if err != nil {
				t.Error(err)
				return
			}

			if want := tag + "-" + paneID; string(got) != want {
				t.Errorf("%s received %q, want %q", tag, got, want)
			}
		})
	}

	wg.Wait()
}

func TestIntegrationMenuAndPopupOutliveCommandTimeout(t *testing.T) {
	server, session, ctx := apiFixture(t)

	const commandTimeout = 200 * time.Millisecond

	short, err := tmux.New(tmux.Config{
		Binary:           os.Getenv("TMUX_TEST_BINARY"),
		SocketPath:       server.Endpoint().SocketPath,
		SocketName:       "",
		ConfigFile:       "",
		Env:              nil,
		Dir:              "",
		Limits:           tmux.Limits{CommandTimeout: commandTimeout, OutputBytes: 0, InputBytes: 0, Concurrent: 0},
		UTF8:             tmux.UTF8Default,
		Colors256:        false,
		TerminalFeatures: nil,
		LogLevel:         tmux.LogLevelNone,
		LoginShell:       false,
	})
	if err != nil {
		t.Fatal(err)
	}

	attached, terminal, output := uiClient(t, ctx, server, session)

	client, err := short.Client(ctx, attached.Name())
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Popup", func(t *testing.T) {
		ready := filepath.Join(t.TempDir(), "ready")

		var options tmux.PopupOptions

		options.Program = tmux.Exec("/bin/sh", "-c", `touch "$0"; exec sleep 60`, ready)

		result := make(chan error, 1)
		go func() { result <- client.Popup(ctx, options) }()

		awaitFile(t, ctx, ready)
		// Outlive CommandTimeout; if it applied, Popup would already have failed.
		time.Sleep(3 * commandTimeout)

		select {
		case err := <-result:
			t.Fatalf("Popup ended before dismissal: %v", err)
		default:
		}

		if err := client.ClosePopup(ctx); err != nil {
			t.Fatal(err)
		}

		waitUIResult(t, ctx, result)
	})

	t.Run("Menu", func(t *testing.T) {
		command := testCommand(t, "set-option", "-t", string(session.ID()), "@late-choice", "chosen")
		items := []tmux.MenuItem{{Label: "TGO_LATE_MENU", Key: "x", Commands: testSequence(t, command), IsSeparator: false, Disabled: false}}
		result := make(chan error, 1)

		go func() {
			var options tmux.MenuOptions
			result <- client.Menu(ctx, items, options)
		}()

		awaitObservation(t, ctx, "menu rendered", func() bool { return output.contains("TGO_LATE_MENU") })
		time.Sleep(3 * commandTimeout)

		if _, err := terminal.WriteString("x"); err != nil {
			t.Fatal(err)
		}

		waitUIResult(t, ctx, result)
		awaitUserOption(t, ctx, session, "@late-choice", "chosen")
	})
}
