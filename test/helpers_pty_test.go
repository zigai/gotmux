//go:build integration && (linux || darwin)

package tmux_test

import (
	"io"
	"os"
	"syscall"
	"testing"
	"unsafe"
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

func ptyIoctl(t *testing.T, fd int, request uintptr, value unsafe.Pointer) {
	t.Helper()

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), request, uintptr(value))
	if errno != 0 {
		t.Fatalf("ioctl %#x failed: %v", request, errno)
	}
}
