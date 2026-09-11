//go:build integration && (linux || darwin)

package tmux_test

import (
	"context"
	"errors"
	"io"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/term"

	tmux "github.com/zigai/gotmux/tmux"
)

func TestAttachResizeAndSignal(t *testing.T) {
	server, session, ctx := apiFixture(t)

	master, slave := openPTY(t)
	setTerminalSize(t, slave)

	doneReading := make(chan struct{})
	go func() {
		defer close(doneReading)
		_, _ = io.Copy(io.Discard, master)
	}()
	t.Cleanup(func() {
		_ = master.Close()
		<-doneReading
	})

	initialState, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}

	attachCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() {
		var options tmux.AttachOptions
		done <- session.Attach(attachCtx, tmux.TerminalStreams{In: slave, Out: slave, Err: slave}, options)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("attachment failed to stop during cleanup")
		}
	})

	_ = waitTerminalClient(t, ctx, server, slave, done)

	// Verify initial client geometry was received (24 rows x 80 cols)
	clients, err := server.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	var clientPID int
	for _, c := range clients {
		if string(c.Name) == slave.Name() {
			found = true
			clientPID = c.PID
			if c.Width != 80 || c.Height != 24 {
				t.Logf("initial client size: %dx%d (expected 80x24)", c.Width, c.Height)
			}
			break
		}
	}
	if !found {
		t.Fatalf("client for terminal %s not found in server.Clients", slave.Name())
	}

	// Dynamically change PTY size to 50 rows, 120 cols via TIOCSWINSZ
	newSize := struct{ Rows, Columns, Xpixel, Ypixel uint16 }{Rows: 50, Columns: 120, Xpixel: 0, Ypixel: 0}
	ptyIoctl(t, int(master.Fd()), syscall.TIOCSWINSZ, unsafe.Pointer(&newSize))
	ptyIoctl(t, int(slave.Fd()), syscall.TIOCSWINSZ, unsafe.Pointer(&newSize))

	// Signal client with SIGWINCH to notify it of the terminal window resize
	if clientPID > 0 {
		_ = syscall.Kill(clientPID, syscall.SIGWINCH)
	}

	var resized bool
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		clients, err := server.Clients(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range clients {
			if string(c.Name) == slave.Name() && c.Width == 120 && c.Height == 50 {
				resized = true
				break
			}
		}
		if resized {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !resized {
		t.Fatalf("timed out waiting for client geometry to update to 120x50")
	}

	// Cancel attachment context, verify Attach returns cleanly and restores terminal state
	cancel()

	select {
	case err := <-done:
		done <- err
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled from Attach, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Attach failed to exit within timeout after cancellation")
	}

	restoredState, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}

	if *initialState != *restoredState {
		t.Fatal("terminal state was not restored to initial state")
	}
}
