package tmux

import (
	"errors"
	"slices"
	"testing"
)

func TestRunShellOptions(t *testing.T) {
	// Cancel is rejected
	_, err := runShellArgs("echo 1", RunShellOptions{Cancel: true})
	if err == nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported for Cancel=true, got %v", err)
	}

	// ClearEnvironment is rejected
	_, err = runShellArgs("echo 1", RunShellOptions{ClearEnvironment: true})
	if err == nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported for ClearEnvironment=true, got %v", err)
	}

	// TmuxCommands emits -C
	args, err := runShellArgs("display-message hi", RunShellOptions{TmuxCommands: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Contains(args, "-C") {
		t.Fatalf("expected -C in args, got %v", args)
	}

	// IncludeStderr emits -E
	args, err = runShellArgs("echo err >&2", RunShellOptions{IncludeStderr: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Contains(args, "-E") {
		t.Fatalf("expected -E in args, got %v", args)
	}
}
