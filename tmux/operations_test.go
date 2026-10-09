package tmux

import (
	"errors"
	"os"
	"testing"
)

func TestRespawnPreserveEnvironmentRejectsBeforeCallingTmux(t *testing.T) {
	server, _, argv := mockScriptServer(t)

	pane, err := server.PaneHandle("%1")
	if err != nil {
		t.Fatal(err)
	}

	err = pane.Respawn(t.Context(), RespawnOptions{KillRunning: true, PreserveEnvironment: true, Program: Exec("/bin/sleep", "60"), Dir: "", Env: nil, TmuxEnv: nil})

	var operation *OperationError
	if !errors.Is(err, ErrUnsupported) || !errors.As(err, &operation) || operation.Outcome.Effect != EffectNotSent {
		t.Fatalf("Respawn error = %v, want ErrUnsupported with EffectNotSent", err)
	}

	if _, err := os.Stat(argv); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported respawn invoked tmux: argv log stat = %v", err)
	}
}

func TestRespawnRejectsNULInDir(t *testing.T) {
	s, _, argv := mockScriptServer(t)

	p, err := s.PaneHandle("%1")
	if err != nil {
		t.Fatal(err)
	}

	opts := RespawnOptions{
		Program:             Program{kind: 0, name: "", args: nil},
		Dir:                 "\x00invalid",
		Env:                 nil,
		TmuxEnv:             nil,
		KillRunning:         true,
		PreserveEnvironment: false,
	}

	err = p.Respawn(t.Context(), opts)

	var op *OperationError
	if !errors.Is(err, ErrInvalidArgument) || !errors.As(err, &op) || op.Outcome.Effect != EffectNotSent {
		t.Fatalf("got %v, want ErrInvalidArgument with effect EffectNotSent", err)
	}

	if _, err := os.Stat(argv); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tmux ran for a Dir containing NUL (argv log stat: %v)", err)
	}
}
