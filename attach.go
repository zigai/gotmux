package tmux

import (
	"context"
	"os"
	"os/exec"

	"golang.org/x/term"
)

type TerminalStreams struct {
	In  *os.File
	Out *os.File
	Err *os.File
}
type AttachOptions struct {
	ReadOnly            bool
	PreserveEnvironment bool
}

func validateTerminals(t TerminalStreams) error {
	if t.In == nil || t.Out == nil || t.Err == nil {
		return invalid("terminal streams")
	}
	if !term.IsTerminal(int(t.In.Fd())) || !term.IsTerminal(int(t.Out.Fd())) {
		return invalid("stdin/stdout must be terminal files")
	}
	_, _, e := term.GetSize(int(t.Out.Fd()))
	return e
}

// PrepareAttach is an ADVANCED, endpoint-relative escape hatch. It returns an
// unstarted command and performs no external work. The caller owns Start, Wait,
// cancellation, and outcome interpretation. Unlike materialized-handle methods,
// it does not assert object-generation protection: session is an endpoint-relative
// ID, exactly like a raw Command target. It does not detach another client.
//
// tmux owns terminal setup/restoration. This library changes no terminal state.
func (s *Server) PrepareAttach(ctx context.Context, session SessionID, streams TerminalStreams, o AttachOptions) (*exec.Cmd, error) {
	if s == nil || s.runner == nil {
		return nil, opError("PrepareAttach", ErrInvalidHandle)
	}
	if ctx == nil || !session.Valid() {
		return nil, opError("PrepareAttach", invalid("context/session"))
	}
	if s.conn != nil || s.bound != nil {
		return nil, opError("PrepareAttach", ErrTransportUnsupported)
	}
	if e := validateTerminals(streams); e != nil {
		return nil, opError("PrepareAttach", e)
	}
	args := append(s.baseArgs(false), "attach-session", "-t", string(session))
	if o.ReadOnly {
		args = append(args, "-r")
	}
	if o.PreserveEnvironment {
		args = append(args, "-E")
	}
	cmd := s.runner.Command(ctx, args)
	cmd.Stdin = streams.In
	cmd.Stdout = streams.Out
	cmd.Stderr = streams.Err
	return cmd, nil
}
