//go:build darwin

package tmux

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"
)

const (
	ptyNameBytes   = 128
	defaultPTYCols = 80
	defaultPTYRows = 24
)

func openPTY() (*os.File, *os.File, error) {
	masterFD, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}

	//nolint:gosec // ioctl TIOCPTYGRANT on Darwin pty master
	if err := ptyIoctl(masterFD, syscall.TIOCPTYGRANT, nil); err != nil {
		_ = syscall.Close(masterFD)
		return nil, nil, err
	}

	//nolint:gosec // ioctl TIOCPTYUNLK on Darwin pty master
	if err := ptyIoctl(masterFD, syscall.TIOCPTYUNLK, nil); err != nil {
		_ = syscall.Close(masterFD)
		return nil, nil, err
	}

	var name [ptyNameBytes]byte
	//nolint:gosec // ioctl writes Darwin 128-byte pty name ABI into this array
	if err := ptyIoctl(masterFD, syscall.TIOCPTYGNAME, unsafe.Pointer(&name[0])); err != nil {
		_ = syscall.Close(masterFD)
		return nil, nil, err
	}

	end := bytes.IndexByte(name[:], 0)
	if end <= 0 {
		_ = syscall.Close(masterFD)
		return nil, nil, invalid("pty slave name")
	}

	slave, err := os.OpenFile(string(name[:end]), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = syscall.Close(masterFD)
		return nil, nil, err
	}

	setTerminalDimensions(slave, defaultPTYCols, defaultPTYRows)

	master := os.NewFile(uintptr(masterFD), "pty-master")

	return master, slave, nil
}
