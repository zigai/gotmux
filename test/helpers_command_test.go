//go:build integration

package test

import (
	"testing"

	"github.com/zigai/gotmux/tmux"
)

func testCommand(t *testing.T, name string, args ...string) tmux.Command {
	t.Helper()

	command, err := tmux.NewCommand(name, args...)
	if err != nil {
		t.Fatal(err)
	}

	return command
}

func testSequence(t *testing.T, commands ...tmux.Command) tmux.CommandSequence {
	t.Helper()

	sequence, err := tmux.Sequence(commands...)
	if err != nil {
		t.Fatal(err)
	}

	return sequence
}
