package tmux

import (
	"errors"
	"slices"
	"testing"
)

func TestParseAccessLine(t *testing.T) {
	tests := []struct {
		input    string
		expected AccessEntry
		ok       bool
	}{
		{
			input: "zigai (U,W)",
			expected: AccessEntry{
				Name:     "zigai",
				IsGroup:  false,
				ReadOnly: false,
			},
			ok: true,
		},
		{
			input: "users (G,R)",
			expected: AccessEntry{
				Name:     "users",
				IsGroup:  true,
				ReadOnly: true,
			},
			ok: true,
		},
		{
			input: "admin (U,R)",
			expected: AccessEntry{
				Name:     "admin",
				IsGroup:  false,
				ReadOnly: true,
			},
			ok: true,
		},
		{
			input: "wheel (G,W)",
			expected: AccessEntry{
				Name:     "wheel",
				IsGroup:  true,
				ReadOnly: false,
			},
			ok: true,
		},
		{
			input: "user with spaces (U,W)",
			expected: AccessEntry{
				Name:     "user with spaces",
				IsGroup:  false,
				ReadOnly: false,
			},
			ok: true,
		},
		{input: "", expected: AccessEntry{Name: "", IsGroup: false, ReadOnly: false}, ok: false},
		{input: "invalid line", expected: AccessEntry{Name: "", IsGroup: false, ReadOnly: false}, ok: false},
		{input: "nobody (X)", expected: AccessEntry{Name: "", IsGroup: false, ReadOnly: false}, ok: false},
		{input: "(U,W)", expected: AccessEntry{Name: "", IsGroup: false, ReadOnly: false}, ok: true},
	}

	for _, tc := range tests {
		got, ok := parseAccessLine(tc.input)
		if ok != tc.ok {
			t.Errorf("parseAccessLine(%q) ok = %v, want %v", tc.input, ok, tc.ok)
			continue
		}

		if ok && got != tc.expected {
			t.Errorf("parseAccessLine(%q) = %+v, want %+v", tc.input, got, tc.expected)
		}
	}
}

func TestPopupBorderValid(t *testing.T) {
	validBorders := []PopupBorder{
		PopupBorderSingle,
		PopupBorderDouble,
		PopupBorderHeavy,
		PopupBorderRounded,
		PopupBorderSimple,
		PopupBorderPadded,
		PopupBorderNone,
	}

	for _, b := range validBorders {
		if !b.Valid() {
			t.Errorf("expected %q to be valid", b)
		}
	}

	invalidBorders := []PopupBorder{"", "invalid", "triple", "custom"}
	for _, b := range invalidBorders {
		if b.Valid() {
			t.Errorf("expected %q to be invalid", b)
		}
	}
}

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

func TestServerAccessValidation(t *testing.T) {
	ctx := t.Context()

	var nilServer *Server

	if _, err := nilServer.AccessList(ctx); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on nilServer.AccessList, got: %v", err)
	}

	if err := nilServer.GrantAccess(ctx, "user", AccessOptions{Group: false, ReadOnly: false}); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on nilServer.GrantAccess, got: %v", err)
	}

	if err := nilServer.RevokeAccess(ctx, "user", false); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on nilServer.RevokeAccess, got: %v", err)
	}

	s, err := New(Config{
		Binary:           "",
		SocketPath:       "/tmp/test.sock",
		SocketName:       "",
		ConfigFile:       "",
		Env:              nil,
		Dir:              "",
		Limits:           Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0},
		UTF8:             0,
		Colors256:        false,
		TerminalFeatures: nil,
		LogLevel:         0,
		LoginShell:       false,
	})
	if err != nil {
		t.Fatal(err)
	}

	invalidNames := []string{"", "\x00bad", "bad\x00user"}
	for _, name := range invalidNames {
		if err := s.GrantAccess(ctx, name, AccessOptions{Group: false, ReadOnly: false}); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("expected ErrInvalidArgument on GrantAccess(%q), got: %v", name, err)
		}

		if err := s.RevokeAccess(ctx, name, false); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("expected ErrInvalidArgument on RevokeAccess(%q), got: %v", name, err)
		}
	}
}

func TestMessagesValidation(t *testing.T) {
	ctx := t.Context()

	var nilServer *Server

	if _, err := nilServer.Messages(ctx, MessagesOptions{Jobs: false, Terminal: false}); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on nilServer.Messages, got: %v", err)
	}

	var invalidClient Client

	if _, err := invalidClient.Messages(ctx, MessagesOptions{Jobs: false, Terminal: false}); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on invalidClient.Messages, got: %v", err)
	}
}

func TestPromptHistoryValidation(t *testing.T) {
	ctx := t.Context()

	var nilServer *Server

	if _, err := nilServer.PromptHistory(ctx, "command"); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on nilServer.PromptHistory, got: %v", err)
	}

	if err := nilServer.ClearPromptHistory(ctx, "command"); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on nilServer.ClearPromptHistory, got: %v", err)
	}

	s, err := New(Config{
		Binary:           "",
		SocketPath:       "/tmp/test.sock",
		SocketName:       "",
		ConfigFile:       "",
		Env:              nil,
		Dir:              "",
		Limits:           Limits{CommandTimeout: 0, OutputBytes: 0, InputBytes: 0, Concurrent: 0},
		UTF8:             0,
		Colors256:        false,
		TerminalFeatures: nil,
		LogLevel:         0,
		LoginShell:       false,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.PromptHistory(ctx, "\x00invalid"); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument on PromptHistory with NUL byte, got: %v", err)
	}

	if err := s.ClearPromptHistory(ctx, "\x00invalid"); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument on ClearPromptHistory with NUL byte, got: %v", err)
	}
}

func TestPaneMethodsValidation(t *testing.T) {
	ctx := t.Context()

	var (
		invalidPane Pane
		invalidWin  Window
	)

	if err := invalidPane.ClockMode(ctx); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on invalidPane.ClockMode, got: %v", err)
	}

	if err := invalidPane.SendPrefix(ctx, false); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on invalidPane.SendPrefix, got: %v", err)
	}

	defaultNewPaneOpts := NewPaneOptions{
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

	if _, err := invalidPane.NewPane(ctx, defaultNewPaneOpts); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on invalidPane.NewPane, got: %v", err)
	}

	if _, err := invalidWin.NewPane(ctx, defaultNewPaneOpts); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on invalidWin.NewPane, got: %v", err)
	}
}

func TestSplitArgsEnhanced(t *testing.T) {
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

func TestCaptureFlagArgsEnhanced(t *testing.T) {
	opts := CaptureOptions{
		Start:               nil,
		End:                 nil,
		EntireHistory:       false,
		ScrollbackEnd:       false,
		JoinWrapped:         false,
		IncludeEscapes:      false,
		PreserveSpaces:      false,
		PaneState:           false,
		Quiet:               false,
		Hyperlinks:          false,
		Screen:              0,
		Buffer:              "",
		MaxBytes:            0,
		EscapeNonPrintable:  true,
		AlternateScreenOnly: true,
	}

	flags := captureFlagArgs(opts)
	if !slices.Contains(flags, "-C") {
		t.Errorf("expected captureFlagArgs to contain -C, got: %v", flags)
	}

	if !slices.Contains(flags, "-F") {
		t.Errorf("expected captureFlagArgs to contain -F, got: %v", flags)
	}
}

func TestRespawnPreserveEnvironment(t *testing.T) {
	opts := RespawnOptions{
		Program:             Program{kind: 0, name: "", args: nil},
		Dir:                 "\x00invalid",
		Env:                 nil,
		TmuxEnv:             nil,
		KillRunning:         true,
		PreserveEnvironment: true,
	}

	err := respawn(zeroHandle, t.Context(), "respawn-pane", opts)
	if err == nil {
		t.Fatal("expected error on invalid dir")
	}
}

func TestOptionalNonnegativeDecoder(t *testing.T) {
	d := &recordDecoder{
		kind: "pane",
		raw: map[string]string{
			"empty":    "",
			"valid":    "42",
			"zero":     "0",
			"negative": "-5",
			"bad":      "notanumber",
		},
		err: nil,
	}

	if val := d.optionalNonnegative("empty"); val != 0 || d.err != nil {
		t.Errorf("expected 0 with no error on empty string, got %d, err=%v", val, d.err)
	}

	if val := d.optionalNonnegative("valid"); val != 42 || d.err != nil {
		t.Errorf("expected 42 with no error, got %d, err=%v", val, d.err)
	}

	if val := d.optionalNonnegative("zero"); val != 0 || d.err != nil {
		t.Errorf("expected 0 with no error, got %d, err=%v", val, d.err)
	}

	_ = d.optionalNonnegative("negative")

	if d.err == nil {
		t.Error("expected error on negative number")
	}

	d.err = nil

	_ = d.optionalNonnegative("bad")

	if d.err == nil {
		t.Error("expected error on non-numeric string")
	}
}
