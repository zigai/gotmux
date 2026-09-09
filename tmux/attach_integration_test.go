//go:build integration && (linux || darwin)

package tmux_test

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/term"

	tmux "github.com/zigai/gotmux/tmux"
	"github.com/zigai/gotmux/tmuxtest"
)

func interactiveTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, slave := openPTY(t)

	done := make(chan struct{})
	go func() { defer close(done); _, _ = io.Copy(io.Discard, master) }()

	t.Cleanup(func() { _ = master.Close(); <-done })

	return master, slave
}

func attachmentTerminal(t *testing.T) *os.File {
	t.Helper()
	_, slave := interactiveTerminal(t)

	return slave
}

func setTerminalSize(t *testing.T, slave *os.File) {
	t.Helper()

	size := struct{ Rows, Columns, Xpixel, Ypixel uint16 }{Rows: 24, Columns: 80, Xpixel: 0, Ypixel: 0}
	ptyIoctl(t, int(slave.Fd()), syscall.TIOCSWINSZ, unsafe.Pointer(&size)) //nolint:gosec // G103: ioctl reads this owned aligned eight-byte winsize synchronously on Linux/Darwin amd64/arm64; KeepAlive retains it; Linux PTY tests and Darwin cross-builds cover the ABI use.
}

func TestIntegrationAPIAttach(t *testing.T) {
	for _, finish := range []string{"detach", "cancel", "connection-close"} {
		t.Run(finish, func(t *testing.T) { testAttachmentLifecycle(t, finish) })
	}
}

func attachmentSession(t *testing.T, server *tmux.Server, session tmux.Session, ctx context.Context, finish string) (tmux.Session, *tmux.Connection) {
	t.Helper()

	if finish != "connection-close" {
		return session, nil
	}

	connection := apiControl(t, server, session, ctx)

	bound, err := connection.Server().Session(ctx, session.ID())
	if err != nil {
		t.Fatal(err)
	}

	auxiliary, err := bound.UsingSubprocess()
	if err != nil {
		t.Fatal(err)
	}

	return auxiliary, connection
}

func waitTerminalClient(t *testing.T, ctx context.Context, server *tmux.Server, terminal *os.File, done chan error) tmux.Client {
	t.Helper()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-done:
			done <- err

			t.Fatalf("attachment exited before connecting: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
			clients, err := server.Clients(ctx)
			if err != nil {
				t.Fatal(err)
			}

			for _, client := range clients {
				if string(client.Name) == terminal.Name() {
					return client.Handle()
				}
			}
		}
	}
}

func finishAttachment(t *testing.T, ctx context.Context, finish string, client tmux.Client, connection *tmux.Connection, cancel context.CancelFunc) {
	t.Helper()

	switch finish {
	case "cancel":
		cancel()
	case "connection-close":
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
	default:
		if err := client.Detach(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func testAttachmentLifecycle(t *testing.T, finish string) {
	t.Helper()
	server, session, ctx := apiFixture(t)
	session, connection := attachmentSession(t, server, session, ctx, finish)
	terminal := attachmentTerminal(t)

	initial, err := term.GetState(int(terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}

	attachCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() {
		var options tmux.AttachOptions
		done <- session.Attach(attachCtx, tmux.TerminalStreams{In: terminal, Out: terminal, Err: terminal}, options)
	}()

	t.Cleanup(func() {
		cancel()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("attachment failed to stop")
		}
	})
	attached := waitTerminalClient(t, ctx, server, terminal, done)
	finishAttachment(t, ctx, finish, attached, connection, cancel)

	select {
	case err := <-done:
		done <- err

		if finish == "detach" && err != nil {
			t.Fatal(err)
		}

		if finish != "detach" && !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	restored, err := term.GetState(int(terminal.Fd()))
	if err != nil {
		t.Fatal(err)
	}

	if *initial != *restored {
		t.Fatal("terminal state not restored")
	}
}

func TestIntegrationAPIAttachRejectsReplacementDaemon(t *testing.T) {
	server := tmuxtest.NewServer(t)
	ctx := integrationContext(t)

	stale, err := server.FindSession(ctx, "fixture")
	if err != nil {
		t.Fatal(err)
	}

	socket := server.Endpoint().SocketPath
	// Keep the original daemon reachable for fixture cleanup while starting a
	// replacement at the same endpoint with the same numeric session ID.
	saved := socket + ".original"
	if err := os.Rename(socket, saved); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := os.Rename(saved, socket); err != nil {
			t.Error(err)
		}
	})

	var create tmux.NewSessionOptions

	create.Name = "replacement"

	replacement, err := server.NewSession(ctx, create)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := server.KillIfIdentity(cleanup, replacement.Identity()); err != nil {
			t.Error(err)
		}
	})

	if replacement.ID() != stale.ID() {
		t.Fatalf("expected ID reuse: %v, %v", replacement.ID(), stale.ID())
	}

	terminal := attachmentTerminal(t)

	var options tmux.AttachOptions

	err = stale.Attach(ctx, tmux.TerminalStreams{In: terminal, Out: terminal, Err: terminal}, options)
	if !errors.Is(err, tmux.ErrServerChanged) {
		t.Fatalf("stale attach: %v", err)
	}

	clients, err := server.Clients(ctx)
	if err != nil || len(clients) != 0 {
		t.Fatalf("stale attach reached replacement: %+v, %v", clients, err)
	}
}

func ptyIoctl(t *testing.T, fd int, request uintptr, value unsafe.Pointer) {
	t.Helper()

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), request, uintptr(value))
	runtime.KeepAlive(value)

	if errno != 0 {
		t.Fatal(errno)
	}
}
