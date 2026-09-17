//go:build !linux && !darwin

package tmux

import "os"

func openPTY() (*os.File, *os.File, error) {
	return nil, nil, unsupportedControl("PTY allocation on this platform", ErrTransportUnsupported)
}
