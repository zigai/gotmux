package tmux

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
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

func TestMenuItemKeylessAndSectionHeader(t *testing.T) {
	// Keyless menu item (mouse/arrow navigation only)
	keylessArgs, err := menuItemArg(MenuItem{
		Label:     "Mouse Click Only",
		Key:       "",
		Commands:  CommandSequence{commands: nil},
		Command:   "display-message clicked",
		Separator: false,
		Disabled:  false,
	})
	if err != nil {
		t.Fatalf("expected keyless MenuItem to succeed, got %v", err)
	}

	if len(keylessArgs) != 3 || keylessArgs[1].text != "" {
		t.Fatalf("expected empty key in wireArgs, got %+v", keylessArgs)
	}

	// Section header: disabled item with empty key and empty command
	headerArgs, err := menuItemArg(MenuItem{
		Label:     "Section Header",
		Key:       "",
		Commands:  CommandSequence{commands: nil},
		Command:   "",
		Separator: false,
		Disabled:  true,
	})
	if err != nil {
		t.Fatalf("expected section header MenuItem to succeed, got %v", err)
	}

	if len(headerArgs) != 3 || headerArgs[0].text != "-Section Header" || headerArgs[1].text != "" || headerArgs[2].text != "" {
		t.Fatalf("unexpected section header wireArgs: %+v", headerArgs)
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
		CloseOnKey:     false,
		DisableDismiss: false,
		TmuxEnv:        nil,
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

func TestClosePopupTargetsAndGuardsClient(t *testing.T) {
	s, response, log := mockScriptServer(t)
	c := Client{h: s.newHandle("/dev/pts/7", ClientKind, mockServerIdentity(s))}
	c.h.client = clientCheck{name: "/dev/pts/7", pid: 77, created: 100}

	writeMockResponse(t, response, []byte(guardClientChanged))

	if err := c.ClosePopup(context.Background()); !errors.Is(err, ErrClientChanged) {
		t.Fatalf("ClosePopup stale client = %v, want ErrClientChanged", err)
	}

	writeMockResponse(t, response, []byte(guardOK))

	if err := c.ClosePopup(context.Background()); err != nil {
		t.Fatal(err)
	}

	args, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(args, []byte(`display-popup \042-C\042 \042-c\042 \042/dev/pts/7\042`)) {
		t.Fatalf("ClosePopup target flags: %q", args)
	}
}

func TestFormatMultiEmpty(t *testing.T) {
	var (
		p Pane
		w Window
		s Session
		c Client
	)

	for _, name := range []string{"pane", "window", "session", "client"} {
		var (
			out [][]byte
			err error
		)

		switch name {
		case "pane":
			out, err = p.FormatMulti(t.Context())
		case "window":
			out, err = w.FormatMulti(t.Context())
		case "session":
			out, err = s.FormatMulti(t.Context())
		case "client":
			out, err = c.FormatMulti(t.Context())
		}

		if err != nil || len(out) != 0 {
			t.Fatalf("%s: expected empty result and nil error, got %v, %v", name, out, err)
		}
	}
}

func TestMessageValidation(t *testing.T) {
	var (
		c Client
		p Pane
		s *Server
	)

	if err := c.Message(t.Context(), "\x00invalid"); err == nil {
		t.Fatal("expected error on NUL byte in message")
	}

	if err := p.Message(t.Context(), "\x00invalid"); err == nil {
		t.Fatal("expected error on NUL byte in message")
	}

	if err := s.Message(t.Context(), "text"); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("expected ErrInvalidHandle on nil server, got %v", err)
	}
}

func TestMenuArgsRequireClick(t *testing.T) {
	args, err := menuArgsWithTarget("-t", "%0", MenuOptions{
		Title:         "My Menu",
		Mouse:         true,
		RequireClick:  true,
		X:             "M",
		Y:             "M",
		Style:         "",
		SelectedStyle: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(args, "-O") {
		t.Fatalf("expected -O flag in menuArgsWithTarget, got %v", args)
	}

	if !slices.Contains(args, "-M") {
		t.Fatalf("expected -M flag in menuArgsWithTarget when Mouse=true, got %v", args)
	}
}

func TestMenuArgsMouseAndRequireClickFlags(t *testing.T) {
	// Default MenuOptions (Mouse: false, RequireClick: false) must emit neither -M nor -O
	argsDefault, err := menuArgsWithTarget("-t", "%0", MenuOptions{
		Title:         "",
		Mouse:         false,
		RequireClick:  false,
		X:             "",
		Y:             "",
		Style:         "",
		SelectedStyle: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if slices.Contains(argsDefault, "-M") {
		t.Fatalf("expected no -M flag when Mouse=false, got %v", argsDefault)
	}

	if slices.Contains(argsDefault, "-O") {
		t.Fatalf("expected no -O flag when RequireClick=false, got %v", argsDefault)
	}

	// Explicit Mouse: true must emit -M
	argsMouse, err := menuArgsWithTarget("-c", "c0", MenuOptions{
		Title:         "",
		Mouse:         true,
		RequireClick:  false,
		X:             "",
		Y:             "",
		Style:         "",
		SelectedStyle: "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(argsMouse, "-M") {
		t.Fatalf("expected -M flag when Mouse=true, got %v", argsMouse)
	}

	if slices.Contains(argsMouse, "-O") {
		t.Fatalf("expected no -O flag when RequireClick=false, got %v", argsMouse)
	}
}

func TestMenuSeparator(t *testing.T) {
	sep := MenuSeparator()
	if !sep.Separator {
		t.Fatal("expected MenuSeparator() to have Separator=true")
	}

	args, err := menuItemArg(sep)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(args) != 1 || args[0].text != "" {
		t.Fatalf("expected 1 empty wireArg for separator, got %+v", args)
	}
}

func TestUIOperationsRejectNilContext(t *testing.T) {
	var (
		c          Client
		p          Pane
		popupOpts  PopupOptions
		menuOpts   MenuOptions
		promptOpts PromptOptions
		nilCtx     context.Context
	)

	if err := c.Popup(nilCtx, popupOpts); err == nil {
		t.Fatal("expected error on nil context")
	}

	if err := p.Popup(nilCtx, popupOpts); err == nil {
		t.Fatal("expected error on nil context")
	}

	if err := c.Menu(nilCtx, nil, menuOpts); err == nil {
		t.Fatal("expected error on nil context")
	}

	if err := p.Menu(nilCtx, nil, menuOpts); err == nil {
		t.Fatal("expected error on nil context")
	}

	if err := c.Prompt(nilCtx, "", promptOpts); err == nil {
		t.Fatal("expected error on nil context")
	}
}

func TestClientFlagExtendedValidation(t *testing.T) {
	// Base and new flags
	for _, f := range []ClientFlag{
		ClientFlagIgnoreSize, ClientFlagNoOutput, ClientFlagReadOnly,
		ClientFlagActivePane, ClientFlagNoDetachOnDestroy, ClientFlagWaitExit,
		ClientFlagNewLayouts,
	} {
		if !f.Valid() {
			t.Errorf("expected flag %q to be valid", f)
		}
		// Negation should also be valid
		neg := f.Negate()
		if !neg.Valid() {
			t.Errorf("expected negated flag %q to be valid", neg)
		}
		// Double negation returns original
		if neg.Negate() != f {
			t.Errorf("double negate mismatch: got %q, want %q", neg.Negate(), f)
		}
	}

	// Invalid flags
	for _, f := range []ClientFlag{
		"invalid", "!invalid",
	} {
		if f.Valid() {
			t.Errorf("expected flag %q to be invalid", f)
		}
	}
}

func TestClientFlagPauseAfterValidation(t *testing.T) {
	p1 := ClientFlagPauseAfter(2)
	if p1 != "pause-after=2" || !p1.Valid() {
		t.Errorf("expected pause-after=2 valid, got %q", p1)
	}

	p0 := ClientFlagPauseAfter(0)
	if p0 != "pause-after" || !p0.Valid() {
		t.Errorf("expected pause-after valid, got %q", p0)
	}

	pNeg := ClientFlagPauseAfter(-1)
	if pNeg != "pause-after" || !pNeg.Valid() {
		t.Errorf("expected pause-after for <= 0, got %q", pNeg)
	}

	for _, f := range []ClientFlag{
		"pause-after=-1", "pause-after=abc", "pause-after=1.5",
	} {
		if f.Valid() {
			t.Errorf("expected flag %q to be invalid", f)
		}
	}
}

func TestPaneOutputActionValidation(t *testing.T) {
	for _, a := range []PaneOutputAction{
		PaneOutputOn, PaneOutputOff, PaneOutputPause, PaneOutputContinue,
	} {
		if !a.Valid() {
			t.Errorf("expected action %q to be valid", a)
		}
	}

	for _, a := range []PaneOutputAction{
		"", "restart", "stop", "ON",
	} {
		if a.Valid() {
			t.Errorf("expected action %q to be invalid", a)
		}
	}
}

func TestSubscriptionTargetWildcards(t *testing.T) {
	//nolint:exhaustruct_v5 // mock Connection for wildcard target validation
	c := &Connection{}

	pSpec, g, err := TargetAllPanes().subscriptionTarget(c)
	if err != nil || pSpec != "%*" || g != nil {
		t.Fatalf("TargetAllPanes failed: spec=%q, err=%v", pSpec, err)
	}

	wSpec, g, err := TargetAllWindows().subscriptionTarget(c)
	if err != nil || wSpec != "@*" || g != nil {
		t.Fatalf("TargetAllWindows failed: spec=%q, err=%v", wSpec, err)
	}

	sSpec, g, err := TargetSession().subscriptionTarget(c)
	if err != nil || sSpec != "" || g != nil {
		t.Fatalf("TargetSession failed: spec=%q, err=%v", sSpec, err)
	}
}

func TestClientRefreshValidation(t *testing.T) {
	s := localServer(t)
	//nolint:exhaustruct_v5 // test client handle
	c := Client{h: handle{id: "/dev/pts/1", server: s}}
	ctx := t.Context()

	// Incomplete Size
	//nolint:exhaustruct_v5 // testing partial Size
	err := c.Refresh(ctx, RefreshOptions{Size: Size{Width: 10, Height: 0}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for incomplete Size, got %v", err)
	}

	// Incomplete WindowSize
	//nolint:exhaustruct_v5 // testing partial WindowSizes
	err = c.Refresh(ctx, RefreshOptions{WindowSizes: []WindowSizeOverride{{Window: "@1", Size: Size{Width: 10, Height: 0}}}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for incomplete WindowSize, got %v", err)
	}

	// Invalid WindowID
	//nolint:exhaustruct_v5 // testing invalid WindowID
	err = c.Refresh(ctx, RefreshOptions{WindowSizes: []WindowSizeOverride{{Window: "invalid", Size: Size{Width: 80, Height: 24}}}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for invalid WindowID, got %v", err)
	}

	// Invalid ScrollDirection
	//nolint:exhaustruct_v5 // testing invalid ScrollDirection
	err = c.Refresh(ctx, RefreshOptions{Scroll: ScrollAdjustment{Direction: ScrollDirection(99), Amount: 0}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for invalid ScrollDirection, got %v", err)
	}

	// Invalid ClipboardPane
	//nolint:exhaustruct_v5 // testing invalid ClipboardPane
	err = c.Refresh(ctx, RefreshOptions{Clipboard: true, ClipboardPane: "invalid"})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for invalid ClipboardPane, got %v", err)
	}

	// Invalid ClientFlag
	//nolint:exhaustruct_v5 // testing invalid ClientFlag
	err = c.Refresh(ctx, RefreshOptions{Flags: []ClientFlag{"invalid"}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for invalid ClientFlag, got %v", err)
	}

	// Invalid PaneAction
	pane, _ := s.PaneHandle("%1")
	//nolint:exhaustruct_v5 // testing invalid PaneAction
	err = c.Refresh(ctx, RefreshOptions{PaneActions: []PaneOutputTarget{{Pane: pane, Action: "invalid"}}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for invalid PaneAction, got %v", err)
	}
	// Invalid PaneReport
	//nolint:exhaustruct_v5 // testing invalid PaneReport
	err = c.Refresh(ctx, RefreshOptions{PaneReports: []PaneReport{{PaneID: "invalid", Report: "ok"}}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for invalid PaneReport, got %v", err)
	}
}

func TestControlNewSessionValidation(t *testing.T) {
	var nilServer *Server

	ctx := t.Context()

	// Nil server
	//nolint:exhaustruct_v5 // testing nil server
	_, _, err := nilServer.OpenControlNewSession(ctx, ControlNewSessionOptions{})
	if !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle for nil server, got %v", err)
	}

	s := localServer(t)
	// Nil context
	//nolint:exhaustruct_v5,staticcheck // testing nil context validation
	_, _, err = s.OpenControlNewSession(nil, ControlNewSessionOptions{})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for nil context, got %v", err)
	}

	// Invalid session name with NUL
	//nolint:exhaustruct_v5 // testing NUL name
	_, _, err = s.OpenControlNewSession(ctx, ControlNewSessionOptions{Name: "bad\x00name"})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for NUL session name, got %v", err)
	}

	// Invalid size
	//nolint:exhaustruct_v5 // testing negative size
	_, _, err = s.OpenControlNewSession(ctx, ControlNewSessionOptions{Size: Size{Width: -5, Height: 10}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument for negative size, got %v", err)
	}

	// Control-bound server
	boundServer := localServer(t)
	//nolint:exhaustruct_v5 // mock control connection
	boundServer.conn = &Connection{}
	//nolint:exhaustruct_v5 // testing control-bound server
	_, _, err = boundServer.OpenControlNewSession(ctx, ControlNewSessionOptions{})
	if !errors.Is(err, ErrTransportUnsupported) {
		t.Errorf("expected ErrTransportUnsupported for control-bound server, got %v", err)
	}
}

func TestConnectionClientValidation(t *testing.T) {
	var nilConn *Connection

	ctx := t.Context()

	_, err := nilConn.Client(ctx)
	if !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle for nil connection, got %v", err)
	}
	//nolint:exhaustruct_v5 // testing closed connection
	closedConn := &Connection{closed: true, done: make(chan struct{})}

	_, err = closedConn.Client(ctx)
	if !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed for closed connection, got %v", err)
	}
}

func TestConnectionSetPaneOutputValidation(t *testing.T) {
	var nilConn *Connection

	ctx := t.Context()
	//nolint:exhaustruct_v5 // test handle
	pane := Pane{h: handle{id: "%1"}}

	if err := nilConn.SetPaneOutputAction(ctx, pane, PaneOutputPause); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on nil connection, got %v", err)
	}

	if err := nilConn.SetPaneOutputActions(ctx, PaneOutputTarget{Pane: pane, Action: PaneOutputPause}); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on nil connection, got %v", err)
	}

	//nolint:exhaustruct_v5 // test connection
	conn := &Connection{identity: ServerIdentity{Generation: 1}}
	//nolint:exhaustruct_v5 // mismatched origin
	paneOther := Pane{h: handle{id: "%1", origin: ServerIdentity{Generation: 2}}}
	if err := conn.SetPaneOutputAction(ctx, paneOther, PaneOutputPause); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on mismatched pane origin, got %v", err)
	}

	//nolint:exhaustruct_v5 // matching origin
	paneSame := Pane{h: handle{id: "%1", origin: conn.identity}}
	if err := conn.SetPaneOutputAction(ctx, paneSame, PaneOutputAction("bad")); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument on invalid action, got %v", err)
	}
}

func TestConnectionWatchFormatWithValidation(t *testing.T) {
	var nilConn *Connection

	ctx := t.Context()

	if err := nilConn.WatchFormatWith(ctx, "sub", TargetSession(), "#{session_name}"); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument on nil connection, got %v", err)
	}

	//nolint:exhaustruct_v5 // test connection
	conn := &Connection{identity: ServerIdentity{Generation: 1}}
	if err := conn.WatchFormatWith(ctx, "sub", nil, "#{session_name}"); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("expected ErrInvalidArgument on nil target, got %v", err)
	}

	//nolint:exhaustruct_v5 // mismatched origin
	paneOther := Pane{h: handle{id: "%1", origin: ServerIdentity{Generation: 2}}}
	if err := conn.WatchFormatWith(ctx, "sub", paneOther, "#{pane_id}"); !errors.Is(err, ErrInvalidHandle) {
		t.Errorf("expected ErrInvalidHandle on mismatched pane origin, got %v", err)
	}
}
