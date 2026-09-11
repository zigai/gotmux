package tmux

import (
	"errors"
	"testing"
)

func TestMenuItemEmptyLabel(t *testing.T) {
	cmd, err := NewCommand("display-message", "hello")
	if err != nil {
		t.Fatal(err)
	}

	seq, err := Sequence(cmd)
	if err != nil {
		t.Fatal(err)
	}

	_, err = menuItemArg(MenuItem{Label: "", Key: "a", Commands: seq, Command: "", Separator: false, Disabled: false})
	if err == nil {
		t.Fatal("expected error on MenuItem with empty Label and Separator=false")
	}

	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}

	// Empty label WITH Separator=true should succeed
	args, err := menuItemArg(MenuItem{Label: "", Separator: true, Key: Key(""), Commands: CommandSequence{commands: nil}, Command: "", Disabled: false})
	if err != nil {
		t.Fatalf("expected Separator=true to succeed, got %v", err)
	}

	if len(args) != 1 || args[0].text != "" {
		t.Fatalf("expected 1 empty wireArg for separator, got %+v", args)
	}
}

func TestMenuItemWithCommandString(t *testing.T) {
	args, err := menuItemArg(MenuItem{
		Label:     "Run Shell",
		Key:       "r",
		Commands:  CommandSequence{commands: nil},
		Command:   "run-shell -b 'echo hi'",
		Separator: false,
		Disabled:  false,
	})
	if err != nil {
		t.Fatalf("expected MenuItem with Command to succeed, got %v", err)
	}

	if len(args) != 3 {
		t.Fatalf("expected 3 wireArgs, got %d", len(args))
	}

	if args[2].text != "run-shell -b 'echo hi'" {
		t.Fatalf("expected command text, got %q", args[2].text)
	}
}

func TestPopupArgsOptions(t *testing.T) {
	args, err := popupArgsWithTarget("-t", "%1", PopupOptions{
		Program:        Program{kind: 0, name: "", args: nil},
		Dir:            "",
		Env:            nil,
		Size:           Size{Width: 0, Height: 0},
		Width:          "80%",
		Height:         "75%",
		X:              "C",
		Y:              "M",
		Border:         PopupBorderRounded,
		Style:          "fg=white,bg=black",
		BorderStyle:    "fg=green",
		Title:          "Test Popup",
		CloseOnExit:    false,
		CloseOnSuccess: false,
		Borderless:     false,
	})
	if err != nil {
		t.Fatalf("popupArgsWithTarget failed: %v", err)
	}

	expected := map[string]string{
		"-w": "80%",
		"-h": "75%",
		"-x": "C",
		"-y": "M",
		"-b": "rounded",
		"-s": "fg=white,bg=black",
		"-S": "fg=green",
	}

	for flag, val := range expected {
		found := false

		for i := range len(args) - 1 {
			if args[i] == flag && args[i+1] == val {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("flag %s %s not found in args: %v", flag, val, args)
		}
	}
}
