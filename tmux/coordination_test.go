package tmux

import (
	"errors"
	"slices"
	"testing"
	"time"
)

func TestRunShellOptions(t *testing.T) {
	_, err := runShellArgs("echo 1", RunShellOptions{
		Background:       false,
		Delay:            0,
		Cancel:           true,
		ClearEnvironment: false,
		TmuxCommands:     false,
		IncludeStderr:    false,
		Dir:              "",
		Target:           "",
	})
	if err == nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported for Cancel=true, got %v", err)
	}

	_, err = runShellArgs("echo 1", RunShellOptions{
		Background:       false,
		Delay:            0,
		Cancel:           false,
		ClearEnvironment: true,
		TmuxCommands:     false,
		IncludeStderr:    false,
		Dir:              "",
		Target:           "",
	})
	if err == nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported for ClearEnvironment=true, got %v", err)
	}

	args, err := runShellArgs("display-message hi", RunShellOptions{
		Background:       false,
		Delay:            0,
		Cancel:           false,
		ClearEnvironment: false,
		TmuxCommands:     true,
		IncludeStderr:    false,
		Dir:              "",
		Target:           "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !slices.Contains(args, "-C") {
		t.Fatalf("expected -C in args, got %v", args)
	}

	args, err = runShellArgs("echo err >&2", RunShellOptions{
		Background:       false,
		Delay:            0,
		Cancel:           false,
		ClearEnvironment: false,
		TmuxCommands:     false,
		IncludeStderr:    true,
		Dir:              "",
		Target:           "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !slices.Contains(args, "-E") {
		t.Fatalf("expected -E in args, got %v", args)
	}
}

func TestRunShellDelayInSeconds(t *testing.T) {
	tests := []struct {
		delay time.Duration
		want  string
	}{
		{50 * time.Millisecond, "0.05"},
		{1500 * time.Millisecond, "1.5"},
		{2 * time.Second, "2"},
	}

	for _, tt := range tests {
		args, err := runShellArgs("true", RunShellOptions{
			Background:       false,
			Delay:            tt.delay,
			Cancel:           false,
			ClearEnvironment: false,
			TmuxCommands:     false,
			IncludeStderr:    false,
			Dir:              "",
			Target:           "",
		})
		if err != nil {
			t.Fatalf("Delay %v: %v", tt.delay, err)
		}

		i := slices.Index(args, "-d")
		if i < 0 || i+1 >= len(args) || args[i+1] != tt.want {
			t.Errorf("Delay %v: args = %q, want -d %s", tt.delay, args, tt.want)
		}
	}
}
