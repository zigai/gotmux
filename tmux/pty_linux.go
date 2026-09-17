//go:build linux

package tmux

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	defaultPTYCols = 80
	defaultPTYRows = 24
)

func openPTY() (*os.File, *os.File, error) {
	masterFD, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // OS error is propagated directly
	}

	master := os.NewFile(uintptr(masterFD), "pty-master")

	var unlock int32
	//nolint:gosec // ioctl reads/writes Linux TIOCSPTLCK ABI int32 synchronously
	if err := ptyIoctl(masterFD, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		_ = master.Close()
		return nil, nil, err
	}

	var number uint32
	//nolint:gosec // ioctl writes Linux TIOCGPTN ABI uint32 synchronously
	if err := ptyIoctl(masterFD, syscall.TIOCGPTN, unsafe.Pointer(&number)); err != nil {
		_ = master.Close()
		return nil, nil, err
	}

	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, err //nolint:wrapcheck // OS error is propagated directly
	}

	setTerminalDimensions(slave, defaultPTYCols, defaultPTYRows)

	return master, slave, nil
}
