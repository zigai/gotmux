package tmux

import (
	"errors"
	"slices"
	"testing"
)

func TestPaneBorderLinesValid(t *testing.T) {
	validBorders := []PaneBorderLines{
		PaneBorderSingle,
		PaneBorderDouble,
		PaneBorderHeavy,
		PaneBorderSimple,
		PaneBorderNumber,
	}

	for _, b := range validBorders {
		if !b.Valid() {
			t.Errorf("expected %q to be valid", b)
		}
	}

	invalidBorders := []PaneBorderLines{"", "invalid", "rounded", "padded"}
	for _, b := range invalidBorders {
		if b.Valid() {
			t.Errorf("expected %q to be invalid", b)
		}
	}
}

func TestSplitArgsAppearanceAndBehaviorFlags(t *testing.T) {
	opts := SplitOptions{
		Direction:           Vertical,
		Size:                SplitSize{Cells: 0, Percent: 0},
		Dir:                 "",
		Program:             Program{kind: 0, name: "", args: nil},
		Env:                 nil,
		TmuxEnv:             nil,
		Select:              false,
		Before:              false,
		FullSize:            false,
		KillTarget:          true,
		Zoom:                true,
		Title:               "MyPane",
		BorderLines:         PaneBorderDouble,
		Style:               "bg=black",
		ActiveBorderStyle:   "fg=green",
		InactiveBorderStyle: "fg=red",
		Message:             "hello",
	}

	args, err := splitArgs("%1", opts)
	if err != nil {
		t.Fatalf("splitArgs failed: %v", err)
	}

	expectedFlags := []string{"-k", "-Z", "-T", "MyPane", "-B", "double", "-s", "bg=black", "-S", "fg=green", "-R", "fg=red", "-m", "hello"}
	for _, ef := range expectedFlags {
		if !slices.Contains(args, ef) {
			t.Errorf("expected splitArgs to contain %q, got: %v", ef, args)
		}
	}
}

func TestSplitArgsValidation(t *testing.T) {
	baseOpts := SplitOptions{
		Direction:           Vertical,
		Size:                SplitSize{Cells: 0, Percent: 0},
		Dir:                 "",
		Program:             Program{kind: 0, name: "", args: nil},
		Env:                 nil,
		TmuxEnv:             nil,
		Select:              false,
		Before:              false,
		FullSize:            false,
		KillTarget:          false,
		Zoom:                false,
		Title:               "",
		BorderLines:         "",
		Style:               "",
		ActiveBorderStyle:   "",
		InactiveBorderStyle: "",
		Message:             "",
	}

	opts := baseOpts
	opts.Title = "\x00bad"

	if _, err := splitArgs("%1", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for Title with NUL, got: %v", err)
	}

	opts = baseOpts
	opts.BorderLines = "invalid"

	if _, err := splitArgs("%1", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for invalid BorderLines, got: %v", err)
	}

	opts = baseOpts
	opts.Style = "\x00bad"

	if _, err := splitArgs("%1", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for Style with NUL, got: %v", err)
	}

	opts = baseOpts
	opts.ActiveBorderStyle = "\x00bad"

	if _, err := splitArgs("%1", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for ActiveBorderStyle with NUL, got: %v", err)
	}

	opts = baseOpts
	opts.InactiveBorderStyle = "\x00bad"

	if _, err := splitArgs("%1", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for InactiveBorderStyle with NUL, got: %v", err)
	}

	opts = baseOpts
	opts.Message = "\x00bad"

	if _, err := splitArgs("%1", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for Message with NUL, got: %v", err)
	}
}

func TestNewPaneArgs(t *testing.T) {
	opts := NewPaneOptions{
		Width:               "50%",
		Height:              "20",
		X:                   "10",
		Y:                   "5",
		Modal:               true,
		Dir:                 "",
		Program:             Program{kind: 0, name: "", args: nil},
		Env:                 nil,
		TmuxEnv:             nil,
		Select:              false,
		CloseOnClick:        true,
		CaptureAllKeys:      true,
		FloatOverZoom:       true,
		Zoom:                true,
		Title:               "FloatModal",
		Style:               "bg=blue",
		ActiveBorderStyle:   "fg=yellow",
		InactiveBorderStyle: "fg=white",
		BorderLines:         PaneBorderDouble,
	}

	args, err := newPaneArgs("@0", opts)
	if err != nil {
		t.Fatalf("newPaneArgs failed: %v", err)
	}

	expectedFlags := []string{
		"-x", "50%", "-y", "20",
		"-X", "10", "-Y", "5",
		"-O", "-C", "-K",
		"-A", "-Z",
		"-T", "FloatModal",
		"-B", "double",
		"-s", "bg=blue",
		"-S", "fg=yellow",
		"-R", "fg=white",
	}

	for _, ef := range expectedFlags {
		if !slices.Contains(args, ef) {
			t.Errorf("expected newPaneArgs to contain %q, got: %v", ef, args)
		}
	}
}

func TestNewPaneArgsValidation(t *testing.T) {
	baseOpts := NewPaneOptions{
		Width:               "",
		Height:              "",
		X:                   "",
		Y:                   "",
		Modal:               false,
		Dir:                 "",
		Program:             Program{kind: 0, name: "", args: nil},
		Env:                 nil,
		TmuxEnv:             nil,
		Select:              false,
		Zoom:                false,
		Title:               "",
		BorderLines:         "",
		Style:               "",
		ActiveBorderStyle:   "",
		InactiveBorderStyle: "",
		FloatOverZoom:       false,
		CloseOnClick:        false,
		CaptureAllKeys:      false,
	}

	opts := baseOpts
	opts.Width = "\x00bad"

	if _, err := newPaneArgs("@0", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for Width with NUL, got: %v", err)
	}

	opts = baseOpts
	opts.Height = "\x00bad"

	if _, err := newPaneArgs("@0", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for Height with NUL, got: %v", err)
	}

	opts = baseOpts
	opts.X = "\x00bad"

	if _, err := newPaneArgs("@0", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for X with NUL, got: %v", err)
	}

	opts = baseOpts
	opts.Y = "\x00bad"

	if _, err := newPaneArgs("@0", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for Y with NUL, got: %v", err)
	}

	opts = baseOpts
	opts.Title = "\x00bad"

	if _, err := newPaneArgs("@0", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for Title with NUL, got: %v", err)
	}

	opts = baseOpts
	opts.BorderLines = "invalid"

	if _, err := newPaneArgs("@0", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for invalid BorderLines, got: %v", err)
	}

	opts = baseOpts
	opts.Style = "\x00bad"

	if _, err := newPaneArgs("@0", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for Style with NUL, got: %v", err)
	}

	opts = baseOpts
	opts.ActiveBorderStyle = "\x00bad"

	if _, err := newPaneArgs("@0", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for ActiveBorderStyle with NUL, got: %v", err)
	}

	opts = baseOpts
	opts.InactiveBorderStyle = "\x00bad"

	if _, err := newPaneArgs("@0", opts); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for InactiveBorderStyle with NUL, got: %v", err)
	}
}
