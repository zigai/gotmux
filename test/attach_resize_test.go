//go:build integration && (linux || darwin)

package tmux_test

import (
	"context"
	"errors"
	"io"
	"os"
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
	drainPTY(t, master)

	initialState, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}

	var options tmux.AttachOptions

	_, cancel, done := attachCancelable(t, ctx, server, session, slave, options)

	client := terminalClientInfo(t, ctx, server, slave)
	if client.Width != 80 || client.Height != 24 {
		t.Logf("initial client size: %dx%d (expected 80x24)", client.Width, client.Height)
	}

	resizePTY(t, master, slave, 50, 120)
	signalResize(client.PID)

	awaitObservation(t, ctx, "client geometry 120x50", func() bool {
		info := terminalClientInfo(t, ctx, server, slave)

		return info.Width == 120 && info.Height == 50
	})

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

func TestIntegrationAttachResize_HighBandwidthStorm(t *testing.T) {
	server, session, ctx := apiFixture(t)

	master, slave := openPTY(t)
	setTerminalSize(t, slave)
	drainPTY(t, master)

	var options tmux.AttachOptions

	client, cancel, done := attachCancelable(t, ctx, server, session, slave, options)
	if !client.Valid() {
		t.Fatal("expected valid client")
	}

	clientPID := terminalClientInfo(t, ctx, server, slave).PID

	panes, err := session.Panes(ctx)
	if err != nil || len(panes) == 0 {
		t.Fatalf("failed to query panes: %v", err)
	}

	_ = panes[0].Handle().Submit(ctx, "for i in $(seq 1 200); do echo \"DATA_STORM_LINE_$i\"; done\n")

	for i := range 30 {
		resizePTY(t, master, slave, uint16(24+(i%10)), uint16(80+(i%20)))
		signalResize(clientPID)

		time.Sleep(5 * time.Millisecond)
	}

	cancel()

	select {
	case err := <-done:
		done <- err

		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("unexpected error from Attach after resize storm: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Attach deadlocked or timed out after resize storm")
	}
}

func drainPTY(t *testing.T, master *os.File) {
	t.Helper()

	doneReading := make(chan struct{})
	go func() {
		defer close(doneReading)

		_, _ = io.Copy(io.Discard, master)
	}()

	t.Cleanup(func() {
		_ = master.Close()

		<-doneReading
	})
}

func resizePTY(t *testing.T, master, slave *os.File, rows, cols uint16) {
	t.Helper()

	size := struct{ Rows, Columns, Xpixel, Ypixel uint16 }{Rows: rows, Columns: cols, Xpixel: 0, Ypixel: 0}
	for _, end := range []*os.File{master, slave} {
		ptyIoctl(t, int(end.Fd()), syscall.TIOCSWINSZ, unsafe.Pointer(&size)) //nolint:gosec // G103: TIOCSWINSZ reads this owned struct winsize (four uint16s, the Linux and Darwin layout) synchronously; ptyIoctl retains its lifetime; covered by the PTY resize tests.
	}
}

// signalResize skips PID 0, which would signal the test's own process group.
func signalResize(pid int) {
	if pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGWINCH)
	}
}

func terminalClientInfo(t *testing.T, ctx context.Context, server *tmux.Server, terminal *os.File) tmux.ClientInfo {
	t.Helper()

	clients, err := server.Clients(ctx)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range clients {
		if string(c.Name) == terminal.Name() {
			return c
		}
	}

	t.Fatalf("client for terminal %s not found in server.Clients", terminal.Name())

	return tmux.ClientInfo{}
}
