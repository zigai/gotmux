//go:build integration && darwin

package tmux_test

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

const ptyNameBytes = 128

func openPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	fd := int(master.Fd())
	ptyIoctl(t, fd, syscall.TIOCPTYGRANT, nil)
	ptyIoctl(t, fd, syscall.TIOCPTYUNLK, nil)
	var name [ptyNameBytes]byte
	ptyIoctl(t, fd, syscall.TIOCPTYGNAME, unsafe.Pointer(&name[0])) //nolint:gosec // G103: Darwin ioctl writes its fixed 128-byte name ABI into this owned array on amd64/arm64; KeepAlive retains it; cross-build coverage, native execution still required.
	end := bytes.IndexByte(name[:], 0)
	if end <= 0 {
		t.Fatal("PTY slave name is empty or unterminated")
	}
	slave, err := os.OpenFile(string(name[:end]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	setTerminalSize(t, slave)
	return master, slave
}
