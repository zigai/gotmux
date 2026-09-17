//go:build linux || darwin

package tmux

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func ptyIoctl(fd int, request uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), request, uintptr(arg))
	runtime.KeepAlive(arg)

	if errno != 0 {
		return errno
	}

	return nil
}

func setTerminalDimensions(slave *os.File, cols, rows uint16) {
	size := struct{ Rows, Columns, Xpixel, Ypixel uint16 }{Rows: rows, Columns: cols, Xpixel: 0, Ypixel: 0}
	//nolint:gosec // ioctl reads eight-byte winsize synchronously on Linux and Darwin
	_ = ptyIoctl(int(slave.Fd()), syscall.TIOCSWINSZ, unsafe.Pointer(&size))
}
