package tmux

import (
	"errors"
	"os"
	"testing"
)

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
		PreserveEnvironment: true,
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
