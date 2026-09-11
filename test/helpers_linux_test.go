//go:build integration && linux

package tmux_test

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"unsafe"
)

func openPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()

	masterFD, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}

	master := os.NewFile(uintptr(masterFD), "pty-master")

	t.Cleanup(func() { _ = master.Close() })

	var unlock int32
	ptyIoctl(t, masterFD, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)) //nolint:gosec // G103: Linux reads this owned, aligned int32 synchronously; ptyIoctl retains its lifetime; covered by PTY race tests.

	var number uint32
	ptyIoctl(t, masterFD, syscall.TIOCGPTN, unsafe.Pointer(&number)) //nolint:gosec // G103: Linux writes four bytes to this owned, aligned uint32; ptyIoctl retains its lifetime; covered by PTY race tests.

	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = slave.Close() })

	setTerminalSize(t, slave)

	return master, slave
}
